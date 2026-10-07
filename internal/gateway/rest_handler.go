package gateway

import (
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
	db            *sql.DB
	repo          *repository.EscrowRepository
	escrowService *service.EscrowService
	nibssResolver resolver.BankAccountResolver
	smsDispatcher resolver.SMSDispatcher
	rateLimiter   security.PinRateLimiter
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

	writeJSON(w, http.StatusOK, agent)
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

	var req FundWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	authenticatedAgentID := security.AgentIDFromContext(r.Context())
	if authenticatedAgentID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	// Agents may only fund their own wallet (body agent_id is ignored)
	req.AgentID = authenticatedAgentID

	if req.AgentID == "" || req.AmountKobo <= 0 {
		writeError(w, http.StatusBadRequest, "Valid agent_id and amount_kobo > 0 are required")
		return
	}

	updateQuery := `
		UPDATE agents
		SET wallet_balance_kobo = wallet_balance_kobo + $1, updated_at = NOW()
		WHERE id = $2 AND is_active = TRUE
		RETURNING wallet_balance_kobo
	`
	var newBalance int64
	err := h.db.QueryRowContext(r.Context(), updateQuery, req.AmountKobo, req.AgentID).Scan(&newBalance)
	if err != nil {
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
	req.AgentID = authenticatedAgentID

	// Dynamically query PostgreSQL for active corridor policy
	policy, err := h.repo.GetCorridorPolicy(r.Context(), req.OriginParkID, req.DestinationParkID)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("No active fee policy configured for route: %v", err))
		return
	}

	dto := service.CreateWaybillDTO{
		AgentID:             req.AgentID,
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
		// map repository errors if you do that elsewhere
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":                "Waybill cancelled; freight returned to wallet",
		"waybill_id":             req.WaybillID,
		"state":                  "CANCELLED",
		"wallet_balance_kobo":    newBal,
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

	res, err := h.escrowService.DisbursePayout(r.Context(), req.WaybillID, req.IdempotencyKey)
	if err != nil {
		observability.Error("waybill_disburse_failed", observability.Fields{
			"waybill_id": req.WaybillID,
			"error":      err.Error(),
		})
		if errors.Is(err, repository.ErrAlreadyDisbursed) {
			writeError(w, http.StatusConflict, "Waybill has already been disbursed")
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
		"waybill_id":          wb.ID,
		"current_state":       wb.CurrentState,
		"origin_park_id":      wb.OriginParkID,
		"destination_park_id": wb.DestinationParkID,
		"total_freight_kobo":  wb.TotalFreightKobo,
		"total_freight_naira": float64(wb.TotalFreightKobo) / 100.0,
		"driver_phone":        wb.DriverPhone,
		"receiver_phone":      wb.ReceiverPhone,
		"vehicle_reg_number":  wb.VehicleRegNumber,
		"failed_pin_attempts": wb.FailedPinAttempts,
		"created_at":          wb.CreatedAt,
		"updated_at":          wb.UpdatedAt,
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
