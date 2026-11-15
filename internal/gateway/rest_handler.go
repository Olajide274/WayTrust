package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nurtw/escrow/internal/domain"
	"github.com/nurtw/escrow/internal/observability"
	"github.com/nurtw/escrow/internal/repository"
	"github.com/nurtw/escrow/internal/resolver"
	"github.com/nurtw/escrow/internal/security"
	"github.com/nurtw/escrow/internal/service"
	"golang.org/x/crypto/bcrypt"
)

type RESTHandler struct {
	db              *sql.DB
	repo            *repository.EscrowRepository
	escrowService   *service.EscrowService
	nibssResolver   resolver.BankAccountResolver
	smsDispatcher   resolver.SMSDispatcher
	rateLimiter     security.PinRateLimiter
	getWaybillOwner func(context.Context, string) (string, error)
	disbursePayout  func(context.Context, string, string) (*service.DisbursePayoutResult, error)
}

func NewRESTHandler(
	db *sql.DB,
	repo *repository.EscrowRepository,
	svc *service.EscrowService,
	res resolver.BankAccountResolver,
	sms resolver.SMSDispatcher,
	limiter security.PinRateLimiter,
) *RESTHandler {
	return &RESTHandler{
		db:            db,
		repo:          repo,
		escrowService: svc,
		nibssResolver: res,
		smsDispatcher: sms,
		rateLimiter:   limiter,
		getWaybillOwner: func(ctx context.Context, waybillID string) (string, error) {
			waybill, err := repo.GetWaybill(ctx, waybillID)
			if err != nil {
				return "", err
			}
			return waybill.AgentID, nil
		},
		disbursePayout: svc.DisbursePayout,
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ==========================================
// 1. REAL AGENT AUTHENTICATION & ONBOARDING
// ==========================================

// List Motor Parks for Registration Dropdown
type ParkOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	City string `json:"city"`
}

func (h *RESTHandler) HandleListParks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	query := `
		SELECT id, name, city
		FROM parks
		WHERE is_active = TRUE
		ORDER BY state, city, name
	`

	rows, err := h.db.QueryContext(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load parks")
		return
	}
	defer rows.Close()

	parks := make([]ParkOption, 0)
	for rows.Next() {
		var p ParkOption
		if err := rows.Scan(&p.ID, &p.Name, &p.City); err != nil {
			continue
		}
		parks = append(parks, p)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read parks")
		return
	}

	writeJSON(w, http.StatusOK, parks)
}

// Agent Signup
type RegisterAgentRequest struct {
	FullName    string `json:"full_name"`
	PhoneNumber string `json:"phone_number"`
	ParkID      string `json:"park_id"`
	Password    string `json:"password"`
}

func (h *RESTHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req RegisterAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.FullName = strings.TrimSpace(req.FullName)
	req.PhoneNumber = strings.TrimSpace(req.PhoneNumber)
	req.ParkID = strings.TrimSpace(req.ParkID)
	req.Password = strings.TrimSpace(req.Password)

	if req.FullName == "" || req.PhoneNumber == "" || req.ParkID == "" || len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "Full name, valid phone number, park location, and password (min 6 chars) are required")
		return
	}

	// Hash password with bcrypt
	hashedPass, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encrypt password")
		return
	}

	// Insert brand new agent with 0 balance into PostgreSQL
	insertQuery := `
		INSERT INTO agents (full_name, phone_number, park_id, wallet_balance_kobo, is_active, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, 0, TRUE, $4, NOW(), NOW())
		RETURNING id, full_name, phone_number, park_id, wallet_balance_kobo, is_active
	`

	var agent domain.Agent
	err = h.db.QueryRowContext(r.Context(), insertQuery, req.FullName, req.PhoneNumber, req.ParkID, string(hashedPass)).Scan(
		&agent.ID, &agent.FullName, &agent.PhoneNumber, &agent.ParkID, &agent.WalletBalanceKobo, &agent.IsActive,
	)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			writeError(w, http.StatusConflict, "An agent with this phone number is already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("database error: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"message": "Registration successful",
		"agent":   agent,
	})
}

// Agent Login
type LoginAgentRequest struct {
	PhoneNumber string `json:"phone_number"`
	Password    string `json:"password"`
}

func (h *RESTHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req LoginAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.PhoneNumber = strings.TrimSpace(req.PhoneNumber)
	req.Password = strings.TrimSpace(req.Password)

	if req.PhoneNumber == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "Phone number and password are required")
		return
	}

	query := `
		SELECT id, full_name, phone_number, park_id, wallet_balance_kobo, is_active, is_admin, password_hash
		FROM agents
		WHERE phone_number = $1
	`
	var agent domain.Agent
	var storedHash string
	var isAdmin bool

	err := h.db.QueryRowContext(r.Context(), query, req.PhoneNumber).Scan(
		&agent.ID,
		&agent.FullName,
		&agent.PhoneNumber,
		&agent.ParkID,
		&agent.WalletBalanceKobo,
		&agent.IsActive,
		&isAdmin,
		&storedHash,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "Invalid phone number or password")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if !agent.IsActive {
		writeError(w, http.StatusForbidden, "This agent account is deactivated. Contact your NURTW Park Secretary.")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid phone number or password")
		return
	}

	token, err := security.IssueToken(agent.ID, agent.PhoneNumber, isAdmin, 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session token")
		return
	}

	observability.Info("agent_login_success", observability.Fields{
		"agent_id": agent.ID,
		"phone":    agent.PhoneNumber,
		"park_id":  agent.ParkID,
		"is_admin": isAdmin,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "Login successful",
		"token":    token,
		"agent":    agent,
		"is_admin": isAdmin,
	})
}

// Agent Profile & Live Balance
func (h *RESTHandler) HandleAgentProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	agentID := security.AgentIDFromContext(r.Context())
	if agentID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	query := `SELECT id, full_name, phone_number, park_id, wallet_balance_kobo, is_active FROM agents WHERE id = $1`
	var agent domain.Agent
	err := h.db.QueryRowContext(r.Context(), query, agentID).Scan(
		&agent.ID, &agent.FullName, &agent.PhoneNumber, &agent.ParkID, &agent.WalletBalanceKobo, &agent.IsActive,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	agent.IsAdmin = security.IsAdminFromContext(r.Context())
	writeJSON(w, http.StatusOK, agent)
}

type SettlementTotals struct {
	Count                int64 `json:"count"`
	DriverPayoutKobo     int64 `json:"driver_payout_kobo"`
	AgentCommissionKobo  int64 `json:"agent_commission_kobo"`
	UnionDuesKobo        int64 `json:"union_dues_kobo"`
	PlatformTreasuryKobo int64 `json:"platform_treasury_kobo"`
}

type SettlementSummary struct {
	Paid     SettlementTotals `json:"paid"`
	Pending  SettlementTotals `json:"pending"`
	Held     SettlementTotals `json:"held"`
	Refunded SettlementTotals `json:"refunded"`
}

func (h *RESTHandler) HandleSettlementSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !security.IsAdminFromContext(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required")
		return
	}

	fromDate, err := settlementDateQueryParam(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "from must be a date in YYYY-MM-DD format")
		return
	}
	toDate, err := settlementDateQueryParam(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "to must be a date in YYYY-MM-DD format")
		return
	}
	if fromDate != nil && toDate != nil && *fromDate > *toDate {
		writeError(w, http.StatusBadRequest, "from must not be after to")
		return
	}

	const query = `
		SELECT
			COUNT(*) FILTER (WHERE p.disbursed_at IS NOT NULL),
			COALESCE(SUM(p.driver_payout_kobo) FILTER (WHERE p.disbursed_at IS NOT NULL), 0),
			COALESCE(SUM(p.agent_commission_kobo) FILTER (WHERE p.disbursed_at IS NOT NULL), 0),
			COALESCE(SUM(p.union_dues_kobo) FILTER (WHERE p.disbursed_at IS NOT NULL), 0),
			COALESCE(SUM(p.platform_treasury_kobo) FILTER (WHERE p.disbursed_at IS NOT NULL), 0),
			COUNT(*) FILTER (WHERE w.current_state = 'DELIVERED' AND p.disbursed_at IS NULL),
			COALESCE(SUM(p.driver_payout_kobo) FILTER (WHERE w.current_state = 'DELIVERED' AND p.disbursed_at IS NULL), 0),
			COALESCE(SUM(p.agent_commission_kobo) FILTER (WHERE w.current_state = 'DELIVERED' AND p.disbursed_at IS NULL), 0),
			COALESCE(SUM(p.union_dues_kobo) FILTER (WHERE w.current_state = 'DELIVERED' AND p.disbursed_at IS NULL), 0),
			COALESCE(SUM(p.platform_treasury_kobo) FILTER (WHERE w.current_state = 'DELIVERED' AND p.disbursed_at IS NULL), 0),
			COUNT(*) FILTER (WHERE w.current_state IN ('DISPUTE_HOLD', 'UNCLAIMED_HOLD')),
			COALESCE(SUM(p.driver_payout_kobo) FILTER (WHERE w.current_state IN ('DISPUTE_HOLD', 'UNCLAIMED_HOLD')), 0),
			COALESCE(SUM(p.agent_commission_kobo) FILTER (WHERE w.current_state IN ('DISPUTE_HOLD', 'UNCLAIMED_HOLD')), 0),
			COALESCE(SUM(p.union_dues_kobo) FILTER (WHERE w.current_state IN ('DISPUTE_HOLD', 'UNCLAIMED_HOLD')), 0),
			COALESCE(SUM(p.platform_treasury_kobo) FILTER (WHERE w.current_state IN ('DISPUTE_HOLD', 'UNCLAIMED_HOLD')), 0),
			COUNT(*) FILTER (WHERE w.current_state = 'REFUNDED'),
			COALESCE(SUM(p.driver_payout_kobo) FILTER (WHERE w.current_state = 'REFUNDED'), 0),
			COALESCE(SUM(p.agent_commission_kobo) FILTER (WHERE w.current_state = 'REFUNDED'), 0),
			COALESCE(SUM(p.union_dues_kobo) FILTER (WHERE w.current_state = 'REFUNDED'), 0),
			COALESCE(SUM(p.platform_treasury_kobo) FILTER (WHERE w.current_state = 'REFUNDED'), 0)
		FROM waybills w
		JOIN payout_splits p ON p.waybill_id = w.id
		WHERE ($1::date IS NULL OR COALESCE(p.disbursed_at, w.created_at) >= $1::date)
		  AND ($2::date IS NULL OR COALESCE(p.disbursed_at, w.created_at) < $2::date + INTERVAL '1 day')
	`

	var summary SettlementSummary
	err = h.db.QueryRowContext(r.Context(), query, fromDate, toDate).Scan(
		&summary.Paid.Count, &summary.Paid.DriverPayoutKobo, &summary.Paid.AgentCommissionKobo,
		&summary.Paid.UnionDuesKobo, &summary.Paid.PlatformTreasuryKobo,
		&summary.Pending.Count, &summary.Pending.DriverPayoutKobo, &summary.Pending.AgentCommissionKobo,
		&summary.Pending.UnionDuesKobo, &summary.Pending.PlatformTreasuryKobo,
		&summary.Held.Count, &summary.Held.DriverPayoutKobo, &summary.Held.AgentCommissionKobo,
		&summary.Held.UnionDuesKobo, &summary.Held.PlatformTreasuryKobo,
		&summary.Refunded.Count, &summary.Refunded.DriverPayoutKobo, &summary.Refunded.AgentCommissionKobo,
		&summary.Refunded.UnionDuesKobo, &summary.Refunded.PlatformTreasuryKobo,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load settlement summary")
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

func settlementDateQueryParam(r *http.Request, name string) (*string, error) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, err
	}
	formatted := parsed.Format("2006-01-02")
	return &formatted, nil
}

// Fund Agent Wallet (Simulate cash collected at motor park counter or Paystack top-up)
type FundWalletRequest struct {
	AgentID    string `json:"agent_id"`
	AmountKobo int64  `json:"amount_kobo"`
}

func (h *RESTHandler) HandleFundWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if !security.IsAdminFromContext(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required")
		return
	}

	adminID := security.AgentIDFromContext(r.Context())
	if adminID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req FundWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	if req.AmountKobo <= 0 || req.AmountKobo > 100000000 {
		writeError(w, http.StatusBadRequest, "amount_kobo must be between 1 and 100000000")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to fund wallet: %v", err))
		return
	}
	defer tx.Rollback()

	updateQuery := `
		UPDATE agents
		SET wallet_balance_kobo = wallet_balance_kobo + $1, updated_at = NOW()
		WHERE id = $2 AND is_active = TRUE
		RETURNING wallet_balance_kobo
	`
	var newBalance int64
	err = tx.QueryRowContext(r.Context(), updateQuery, req.AmountKobo, req.AgentID).Scan(&newBalance)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "target agent not found or inactive")
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to fund wallet: %v", err))
		return
	}

	const insertCreditQuery = `
		INSERT INTO wallet_credits (
			agent_id, credited_by_admin_id, amount_kobo, balance_after_kobo, reason
		) VALUES ($1, $2, $3, $4, $5)
	`
	if _, err := tx.ExecContext(
		r.Context(),
		insertCreditQuery,
		req.AgentID,
		adminID,
		req.AmountKobo,
		newBalance,
		"Admin wallet credit",
	); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to record wallet credit: %v", err))
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to fund wallet: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":          "Wallet funded successfully",
		"new_balance_kobo": newBalance,
	})
}

// ==========================================
// 2. LIVE NIBSS DRIVER BANK RESOLUTION
// ==========================================

type ResolveAccountRequest struct {
	AccountNumber string `json:"account_number"`
	BankCode      string `json:"bank_code"`
	DeclaredName  string `json:"declared_name"`
}

func (h *RESTHandler) HandleResolveDriver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ResolveAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.AccountNumber) != 10 || req.BankCode == "" || req.DeclaredName == "" {
		writeError(w, http.StatusBadRequest, "account_number (10 digits), bank_code, and declared_name are required")
		return
	}

	result, err := h.nibssResolver.ResolveAndVerify(r.Context(), req.AccountNumber, req.BankCode, req.DeclaredName, 0.70)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"verified":         false,
			"message":          err.Error(),
			"resolved_name":    result.ResolvedAccountName,
			"confidence_score": result.ConfidenceScore,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"verified":         true,
		"resolved_name":    result.ResolvedAccountName,
		"confidence_score": result.ConfidenceScore,
	})
}

// ==========================================
// 3. BOOK WAYBILL (CORRIDOR ESCROW LOCK)
// ==========================================

type CreateWaybillRequest struct {
	AgentID             string `json:"agent_id"`
	SenderPhone         string `json:"sender_phone"`
	ReceiverPhone       string `json:"receiver_phone"`
	DriverPhone         string `json:"driver_phone"`
	VehicleRegNumber    string `json:"vehicle_reg_number"`
	OriginParkID        string `json:"origin_park_id"`
	DestinationParkID   string `json:"destination_park_id"`
	TotalFreightKobo    int64  `json:"total_freight_kobo"`
	DriverAccountNumber string `json:"driver_account_number"`
	DriverBankCode      string `json:"driver_bank_code"`
	DriverDeclaredName  string `json:"driver_declared_name"`
}

func (h *RESTHandler) HandleCreateWaybill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req CreateWaybillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Force agent identity from JWT — never trust body agent_id
	authenticatedAgentID := security.AgentIDFromContext(r.Context())
	if authenticatedAgentID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	// Dynamically query PostgreSQL for active corridor policy
	policy, err := h.repo.GetCorridorPolicy(r.Context(), req.OriginParkID, req.DestinationParkID)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("No active fee policy configured for route: %v", err))
		return
	}

	dto := service.CreateWaybillDTO{
		AgentID:             authenticatedAgentID,
		SenderPhone:         req.SenderPhone,
		ReceiverPhone:       req.ReceiverPhone,
		DriverPhone:         req.DriverPhone,
		VehicleRegNumber:    req.VehicleRegNumber,
		OriginParkID:        req.OriginParkID,
		DestinationParkID:   req.DestinationParkID,
		TotalFreightKobo:    req.TotalFreightKobo,
		DriverAccountNumber: req.DriverAccountNumber,
		DriverBankCode:      req.DriverBankCode,
		DriverDeclaredName:  req.DriverDeclaredName,
		CorridorPolicy:      *policy,
	}

	res, err := h.escrowService.CreateWaybill(r.Context(), dto)
	if err != nil {
		if errors.Is(err, domain.ErrInsufficientAgentBalance) {
			writeError(w, http.StatusPaymentRequired, "Agent wallet balance is insufficient for this booking")
			return
		}
		if errors.Is(err, repository.ErrAgentNotFound) {
			writeError(w, http.StatusNotFound, "Agent account not found or inactive")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	observability.Info("waybill_created", observability.Fields{
		"waybill_id":       res.WaybillID,
		"agent_id":         security.AgentIDFromContext(r.Context()),
		"freight_kobo":     req.TotalFreightKobo,
		"origin_park":      req.OriginParkID,
		"destination_park": req.DestinationParkID,
	})

	go func() {
		_ = h.smsDispatcher.SendWaybillCreatedAlert(
			r.Context(),
			req.ReceiverPhone,
			res.WaybillID,
			res.SecretPin,
			req.TotalFreightKobo,
		)
		_ = h.smsDispatcher.SendDriverDispatchAlert(
			r.Context(),
			req.DriverPhone,
			res.WaybillID,
			req.DestinationParkID,
		)
	}()

	writeJSON(w, http.StatusCreated, res)
}

// ==========================================
// 4. VERIFY PICKUP PIN AT DESTINATION
// ==========================================

type VerifyPinRequest struct {
	WaybillID string `json:"waybill_id"`
	Pin       string `json:"pin"`
}

func (h *RESTHandler) HandleVerifyPin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req VerifyPinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.WaybillID = strings.TrimSpace(req.WaybillID)
	req.Pin = strings.TrimSpace(req.Pin)

	locked, _ := h.rateLimiter.CheckLockout(r.Context(), req.WaybillID)
	if locked {
		writeError(w, http.StatusLocked, "Waybill is locked due to 3 failed PIN attempts. Must be unlocked by Union Executive.")
		return
	}

	err := h.escrowService.VerifyPickup(r.Context(), req.WaybillID, req.Pin)
	if err != nil {
		if errors.Is(err, domain.ErrPinMismatch) {
			attempts, _ := h.rateLimiter.RecordFailedAttempt(r.Context(), req.WaybillID)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"verified":           false,
				"error":              "Incorrect 4-digit PIN",
				"attempts_remaining": 3 - attempts,
			})
			return
		}
		if errors.Is(err, domain.ErrWaybillLocked) {
			writeError(w, http.StatusLocked, "Waybill has been locked due to excessive failed attempts")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_ = h.rateLimiter.ResetAttempts(r.Context(), req.WaybillID)

	writeJSON(w, http.StatusOK, map[string]any{
		"verified":   true,
		"waybill_id": req.WaybillID,
		"state":      "DELIVERED",
		"message":    "PIN verified successfully. Parcel released for payout.",
	})
}

// ==========================================
// 5. DISBURSEMENT
// ==========================================

type DisburseRequest struct {
	WaybillID      string `json:"waybill_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type CancelWaybillRequest struct {
	WaybillID      string `json:"waybill_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type StartTransitRequest struct {
	WaybillID string `json:"waybill_id"`
}

func authorizeWaybillDisbursement(w http.ResponseWriter, r *http.Request, ownerID string) bool {
	if security.IsAdminFromContext(r.Context()) {
		return true
	}
	if agentID := security.AgentIDFromContext(r.Context()); agentID != "" && agentID == ownerID {
		return true
	}

	writeError(w, http.StatusForbidden, repository.ErrNotWaybillOwner.Error())
	return false
}

func (h *RESTHandler) HandleStartTransit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	agentID := security.AgentIDFromContext(r.Context())
	if agentID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req StartTransitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.WaybillID = strings.TrimSpace(req.WaybillID)
	if req.WaybillID == "" {
		writeError(w, http.StatusBadRequest, "waybill_id is required")
		return
	}
	if !strings.HasPrefix(req.WaybillID, "WB-") {
		req.WaybillID = "WB-" + req.WaybillID
	}

	inTransitAt, err := h.escrowService.StartTransit(r.Context(), agentID, req.WaybillID)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrWaybillNotFound):
			writeError(w, http.StatusNotFound, "waybill not found")
		case errors.Is(err, repository.ErrNotWaybillOwner):
			writeError(w, http.StatusForbidden, "waybill does not belong to this agent")
		case errors.Is(err, repository.ErrCannotStartTransit):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to start waybill transit")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"waybill_id":    req.WaybillID,
		"state":         domain.WaybillStateInTransit,
		"in_transit_at": inTransitAt,
	})
}

type ReceiverRefusalRequest struct {
	WaybillID string `json:"waybill_id"`
	Reason    string `json:"reason"`
}

func (h *RESTHandler) HandleReportReceiverRefusal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	agentID := security.AgentIDFromContext(r.Context())
	if agentID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req ReceiverRefusalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.WaybillID = strings.TrimSpace(req.WaybillID)
	if req.WaybillID == "" {
		writeError(w, http.StatusBadRequest, "waybill_id is required")
		return
	}
	if !strings.HasPrefix(req.WaybillID, "WB-") {
		req.WaybillID = "WB-" + req.WaybillID
	}

	reportedAt, err := h.escrowService.ReportReceiverRefusal(
		r.Context(), agentID, req.WaybillID, req.Reason,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidReceiverRefusalReason):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, repository.ErrWaybillNotFound):
			writeError(w, http.StatusNotFound, "waybill not found")
		case errors.Is(err, repository.ErrNotWaybillOwner):
			writeError(w, http.StatusForbidden, "waybill does not belong to this agent")
		case errors.Is(err, repository.ErrCannotReportReceiverRefusal):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to report receiver refusal")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"waybill_id":  req.WaybillID,
		"state":       domain.WaybillStateDisputeHold,
		"reason":      strings.TrimSpace(req.Reason),
		"reported_at": reportedAt,
		"funds_held":  true,
		"message":     "Receiver refusal recorded. Funds are held pending dispute resolution.",
	})
}

type ResolveHeldWaybillRequest struct {
	WaybillID string `json:"waybill_id"`
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason"`
}

func (h *RESTHandler) HandleResolveHeldWaybill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !security.IsAdminFromContext(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required")
		return
	}

	adminID := security.AgentIDFromContext(r.Context())
	if adminID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req ResolveHeldWaybillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.WaybillID = strings.TrimSpace(req.WaybillID)
	if req.WaybillID == "" {
		writeError(w, http.StatusBadRequest, "waybill_id is required")
		return
	}
	if !strings.HasPrefix(req.WaybillID, "WB-") {
		req.WaybillID = "WB-" + req.WaybillID
	}
	req.Outcome = strings.ToUpper(strings.TrimSpace(req.Outcome))

	result, err := h.escrowService.ResolveHeldWaybill(
		r.Context(), adminID, req.WaybillID, req.Outcome, req.Reason,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidResolutionReason):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, repository.ErrInvalidResolutionOutcome):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, repository.ErrWaybillNotFound):
			writeError(w, http.StatusNotFound, "waybill not found")
		case errors.Is(err, repository.ErrWaybillNotHeld),
			errors.Is(err, repository.ErrWaybillAlreadyResolved):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, service.ErrAdminCannotResolveOwnWaybill):
			writeError(w, http.StatusForbidden, "administrators cannot resolve waybills they booked")
		case errors.Is(err, service.ErrPayoutInProgress):
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":  "payout in progress; retry later",
				"reason": "Paystack has not confirmed the transfer as successful",
			})
		case errors.Is(err, service.ErrPayoutTransferFailed):
			var payoutErr *resolver.PayoutError
			_ = errors.As(err, &payoutErr)
			fields := observability.Fields{
				"waybill_id": req.WaybillID,
				"error":      err.Error(),
			}
			if payoutErr != nil {
				fields["paystack_status"] = payoutErr.HTTPStatus
				fields["paystack_message"] = payoutErr.Message
				fields["paystack_transfer_status"] = payoutErr.TransferStatus
			}
			observability.Error("waybill_resolution_payout_failed", fields)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":  "driver payout failed; waybill remains held",
				"reason": "Paystack could not confirm a successful transfer",
			})
		default:
			observability.Error("waybill_resolution_failed", observability.Fields{
				"waybill_id": req.WaybillID,
				"error":      err.Error(),
			})
			writeError(w, http.StatusInternalServerError, "failed to resolve held waybill")
		}
		return
	}

	response := map[string]any{
		"waybill_id":  result.WaybillID,
		"outcome":     result.Outcome,
		"state":       result.State,
		"resolved_at": result.ResolvedAt,
	}
	if result.Outcome == domain.ResolutionRefundToAgent {
		response["wallet_balance_kobo"] = result.WalletBalanceKobo
	} else {
		response["transfer_reference"] = result.TransferReference
		response["driver_payout_kobo"] = result.DriverPayoutKobo
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *RESTHandler) HandleCancelWaybill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	agentID := security.AgentIDFromContext(r.Context())
	if agentID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req CancelWaybillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.WaybillID = strings.TrimSpace(req.WaybillID)
	if req.WaybillID == "" {
		writeError(w, http.StatusBadRequest, "waybill_id is required")
		return
	}

	newBal, err := h.escrowService.CancelWaybill(r.Context(), agentID, req.WaybillID, req.IdempotencyKey)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrWaybillNotFound):
			writeError(w, http.StatusNotFound, "waybill not found")
		case errors.Is(err, repository.ErrNotWaybillOwner):
			writeError(w, http.StatusForbidden, "waybill does not belong to this agent")
		case errors.Is(err, repository.ErrCannotCancel):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to cancel waybill")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":             "Waybill cancelled; freight returned to wallet",
		"waybill_id":          req.WaybillID,
		"state":               "CANCELLED",
		"wallet_balance_kobo": newBal,
	})
}

func (h *RESTHandler) HandleDisburse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req DisburseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.WaybillID == "" || req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "waybill_id and idempotency_key are required")
		return
	}

	agentID := security.AgentIDFromContext(r.Context())
	if agentID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	ownerID := ""
	if !security.IsAdminFromContext(r.Context()) {
		var err error
		ownerID, err = h.getWaybillOwner(r.Context(), req.WaybillID)
		if errors.Is(err, repository.ErrWaybillNotFound) {
			// Keep unknown and other agents' waybills indistinguishable to agents.
			writeError(w, http.StatusForbidden, repository.ErrNotWaybillOwner.Error())
			return
		}
		if err != nil {
			observability.Error("waybill_disburse_owner_lookup_failed", observability.Fields{
				"waybill_id": req.WaybillID,
				"error":      err.Error(),
			})
			writeError(w, http.StatusInternalServerError, "failed to verify waybill ownership")
			return
		}
	}
	if !authorizeWaybillDisbursement(w, r, ownerID) {
		return
	}

	res, err := h.disbursePayout(r.Context(), req.WaybillID, req.IdempotencyKey)
	if err != nil {
		if errors.Is(err, service.ErrPayoutInProgress) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":  service.PayoutInProgressResponse,
				"reason": service.PayoutInProgressReason,
			})
			return
		}
		if errors.Is(err, service.ErrPayoutTransferFailed) {
			observability.Error("waybill_disburse_payout_failed", observability.Fields{
				"waybill_id": req.WaybillID,
				"error":      err.Error(),
			})
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":  service.PayoutFailedResponse,
				"reason": service.PayoutFailedReason,
			})
			return
		}
		observability.Error("waybill_disburse_failed", observability.Fields{
			"waybill_id": req.WaybillID,
			"error":      err.Error(),
		})
		if errors.Is(err, repository.ErrAlreadyDisbursed) {
			writeError(w, http.StatusConflict, "Waybill has already been disbursed")
			return
		}
		if errors.Is(err, repository.ErrWaybillNotFound) {
			writeError(w, http.StatusNotFound, "waybill not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	observability.Info("waybill_disburse_success", observability.Fields{
		"waybill_id": req.WaybillID,
	})

	writeJSON(w, http.StatusOK, res)
}

// ==========================================
// 6. REAL WAYBILL MANIFEST QUERY
// ==========================================

func (h *RESTHandler) HandleListAgentWaybills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	authenticatedAgentID := security.AgentIDFromContext(r.Context())
	if authenticatedAgentID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	agentID := authenticatedAgentID

	query := `
		SELECT 
			w.id, w.agent_id, w.sender_phone, w.receiver_phone, w.driver_phone,
			w.vehicle_reg_number, w.origin_park_id, w.destination_park_id,
			w.total_freight_kobo, w.current_state, w.driver_verified_name, w.created_at,
			p.driver_payout_kobo, p.agent_commission_kobo, p.union_dues_kobo, p.platform_treasury_kobo
		FROM waybills w
		LEFT JOIN payout_splits p ON w.id = p.waybill_id
		WHERE w.agent_id = $1
		ORDER BY w.created_at DESC
		LIMIT 50
	`

	rows, err := h.db.QueryContext(r.Context(), query, agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type ManifestItem struct {
		ID                 string `json:"id"`
		AgentID            string `json:"agent_id"`
		SenderPhone        string `json:"sender_phone"`
		ReceiverPhone      string `json:"receiver_phone"`
		DriverPhone        string `json:"driver_phone"`
		VehicleRegNumber   string `json:"vehicle_reg_number"`
		OriginParkID       string `json:"origin_park_id"`
		DestinationParkID  string `json:"destination_park_id"`
		TotalFreightKobo   int64  `json:"total_freight_kobo"`
		CurrentState       string `json:"current_state"`
		DriverVerifiedName string `json:"driver_verified_name"`
		CreatedAt          string `json:"created_at"`
		DriverPayoutKobo   int64  `json:"driver_payout_kobo"`
		AgentCommission    int64  `json:"agent_commission_kobo"`
		UnionDuesKobo      int64  `json:"union_dues_kobo"`
		PlatformTreasury   int64  `json:"platform_treasury_kobo"`
	}

	items := make([]ManifestItem, 0)
	for rows.Next() {
		var item ManifestItem
		var driverPayout, agentComm, unionDues, platformTreasury sql.NullInt64
		if err := rows.Scan(
			&item.ID, &item.AgentID, &item.SenderPhone, &item.ReceiverPhone, &item.DriverPhone,
			&item.VehicleRegNumber, &item.OriginParkID, &item.DestinationParkID,
			&item.TotalFreightKobo, &item.CurrentState, &item.DriverVerifiedName, &item.CreatedAt,
			&driverPayout, &agentComm, &unionDues, &platformTreasury,
		); err != nil {
			continue
		}
		if driverPayout.Valid {
			item.DriverPayoutKobo = driverPayout.Int64
		}
		if agentComm.Valid {
			item.AgentCommission = agentComm.Int64
		}
		if unionDues.Valid {
			item.UnionDuesKobo = unionDues.Int64
		}
		if platformTreasury.Valid {
			item.PlatformTreasury = platformTreasury.Int64
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, items)
}

// ==========================================
// 7. WAYBILL STATUS (PRODUCTION QUERY)
// ==========================================

// HandleWaybillStatus returns the current state and key details of a waybill
// GET /api/v1/waybills/status?id=WB-xxxxxxxx
func (h *RESTHandler) HandleWaybillStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	waybillID := strings.TrimSpace(r.URL.Query().Get("id"))
	if waybillID == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'id' is required")
		return
	}

	// Normalise short codes (e.g. 104829 → WB-104829)
	if !strings.HasPrefix(waybillID, "WB-") {
		waybillID = "WB-" + waybillID
	}

	wb, err := h.escrowService.GetWaybillStatus(r.Context(), waybillID)
	if err != nil {
		if errors.Is(err, repository.ErrWaybillNotFound) {
			writeError(w, http.StatusNotFound, "waybill not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to retrieve waybill status")
		return
	}

	// Safe public response – never expose pin_hash or internal fields
	writeJSON(w, http.StatusOK, map[string]any{
		"waybill_id":                   wb.ID,
		"current_state":                wb.CurrentState,
		"origin_park_id":               wb.OriginParkID,
		"destination_park_id":          wb.DestinationParkID,
		"total_freight_kobo":           wb.TotalFreightKobo,
		"total_freight_naira":          float64(wb.TotalFreightKobo) / 100.0,
		"driver_phone":                 wb.DriverPhone,
		"receiver_phone":               wb.ReceiverPhone,
		"vehicle_reg_number":           wb.VehicleRegNumber,
		"failed_pin_attempts":          wb.FailedPinAttempts,
		"receiver_refusal_reason":      wb.ReceiverRefusalReason,
		"receiver_refusal_reported_at": wb.ReceiverRefusalReportedAt,
		"created_at":                   wb.CreatedAt,
		"updated_at":                   wb.UpdatedAt,
	})
}

// ==========================================
// 8. PARK REGISTRATION (APP-DRIVEN)
// ==========================================

type CreateParkRequest struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	City  string `json:"city"`
	State string `json:"state"`
}

func (h *RESTHandler) HandleCreatePark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if !security.IsAdminFromContext(r.Context()) {
		writeError(w, http.StatusForbidden, "only administrators can register parks")
		return
	}

	var req CreateParkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	req.City = strings.TrimSpace(req.City)
	req.State = strings.TrimSpace(req.State)

	if req.ID == "" || req.Name == "" || req.City == "" || req.State == "" {
		writeError(w, http.StatusBadRequest, "id, name, city, and state are required")
		return
	}

	insertQuery := `
		INSERT INTO parks (id, name, city, state, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, TRUE, NOW(), NOW())
		RETURNING id, name, city, state, is_active
	`

	var id, name, city, state string
	var isActive bool
	err := h.db.QueryRowContext(r.Context(), insertQuery, req.ID, req.Name, req.City, req.State).Scan(
		&id, &name, &city, &state, &isActive,
	)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			writeError(w, http.StatusConflict, "a park with this id already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create park")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"message": "Park registered successfully",
		"park": map[string]any{
			"id":        id,
			"name":      name,
			"city":      city,
			"state":     state,
			"is_active": isActive,
		},
	})
}
