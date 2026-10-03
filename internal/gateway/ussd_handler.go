package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nurtw/escrow/internal/domain"
	"github.com/nurtw/escrow/internal/repository"
	"github.com/nurtw/escrow/internal/security"
	"github.com/nurtw/escrow/internal/service"
)

type USSDHandler struct {
	escrowService *service.EscrowService
	rateLimiter   security.PinRateLimiter
}

func NewUSSDHandler(svc *service.EscrowService, limiter security.PinRateLimiter) *USSDHandler {
	return &USSDHandler{
		escrowService: svc,
		rateLimiter:   limiter,
	}
}

// ServeHTTP handles incoming telco USSD POST requests from Africa's Talking or Termii
// Request payload fields: sessionId, serviceCode, phoneNumber, text
func (h *USSDHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	sessionID := r.FormValue("sessionId")
	phoneNumber := r.FormValue("phoneNumber")
	text := strings.TrimSpace(r.FormValue("text"))

	w.Header().Set("Content-Type", "text/plain")

	response := h.processUSSDInput(r.Context(), sessionID, phoneNumber, text)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(response))
}

func (h *USSDHandler) processUSSDInput(ctx context.Context, sessionID, _ string, text string) string {
	// Root prompt if no input entered
	if text == "" {
		return "CON Welcome to NURTW Waybill Escrow\nEnter Waybill ID (e.g. 104829):"
	}

	parts := strings.Split(text, "*")

	// Step 1: User just submitted Waybill ID
	if len(parts) == 1 {
		waybillID := strings.TrimSpace(parts[0])
		if !strings.HasPrefix(waybillID, "WB-") {
			waybillID = "WB-" + waybillID
		}

		w, err := h.escrowService.GetWaybillStatus(ctx, waybillID)
		if err != nil {
			if errors.Is(err, repository.ErrWaybillNotFound) {
				return "END Error: Waybill not found. Please verify the code on your SMS."
			}
			return "END Error: Service temporarily unavailable. Please try again."
		}

		if w.CurrentState == domain.WaybillStateDelivered || w.CurrentState == domain.WaybillStateDisbursed {
			return fmt.Sprintf("END Notice: Waybill %s has already been claimed and released.", waybillID)
		}

		if w.CurrentState == domain.WaybillStateDisputeHold {
			return "END Error: This waybill is on DISPUTE HOLD. Please visit the park union office."
		}

		return fmt.Sprintf("CON Waybill: %s\nFreight: N%.2f\nDestination: %s\nEnter 4-digit Pickup PIN to confirm release:",
			waybillID, float64(w.TotalFreightKobo)/100.0, w.DestinationParkID)
	}

	// Step 2: User submitted both Waybill ID and 4-Digit PIN
	if len(parts) == 2 {
		waybillID := strings.TrimSpace(parts[0])
		if !strings.HasPrefix(waybillID, "WB-") {
			waybillID = "WB-" + waybillID
		}
		pin := strings.TrimSpace(parts[1])

		// 1. Check brute-force lockout
		locked, err := h.rateLimiter.CheckLockout(ctx, waybillID)
		if err == nil && locked {
			return "END Security Alert: Waybill locked due to 3 failed PIN attempts. Contact park supervisor."
		}

		// 2. Validate PIN
		err = h.escrowService.VerifyPickup(ctx, waybillID, pin)
		if err != nil {
			if errors.Is(err, domain.ErrPinMismatch) {
				attempts, _ := h.rateLimiter.RecordFailedAttempt(ctx, waybillID)
				rem := 3 - attempts
				if rem <= 0 {
					return "END Security Alert: 3 incorrect PIN attempts. Escrow locked into DISPUTE_HOLD."
				}
				return fmt.Sprintf("END Incorrect PIN. You have %d attempt(s) remaining.", rem)
			}
			if errors.Is(err, domain.ErrWaybillLocked) {
				return "END Security Alert: Waybill locked due to excessive failed attempts."
			}
			return fmt.Sprintf("END Verification error: %v", err)
		}

		// 3. Clear rate-limiter on success
		_ = h.rateLimiter.ResetAttempts(ctx, waybillID)

		// 4. Trigger automated driver bank payout immediately
		idempotencyKey := fmt.Sprintf("USSD_%s_%s", sessionID, waybillID)
		payoutResult, err := h.escrowService.DisbursePayout(ctx, waybillID, idempotencyKey)
		if err != nil {
			// Delivery is already recorded in DB, payout can be retried by cron if gateway timeout occurs
			return "END Parcel release confirmed! Driver payout is queued for processing."
		}

		return fmt.Sprintf("END Release Successful!\nWaybill %s confirmed.\nDriver transfer: N%.2f (Ref: %s).",
			waybillID, float64(payoutResult.DriverPayoutKobo)/100.0, payoutResult.TransferReference)
	}

	return "END Invalid USSD command format. Dial *347# to begin."
}