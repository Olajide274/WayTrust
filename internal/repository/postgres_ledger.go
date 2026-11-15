package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nurtw/escrow/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrWaybillNotFound             = errors.New("waybill record not found")
	ErrAgentNotFound               = errors.New("agent record not found or currently inactive")
	ErrAlreadyDisbursed            = errors.New("waybill escrow has already been disbursed")
	ErrInvalidPayout               = errors.New("waybill must be in DELIVERED state prior to disbursement")
	ErrNotWaybillOwner             = errors.New("waybill does not belong to this agent")
	ErrCannotCancel                = errors.New("waybill cannot be cancelled in its current state")
	ErrCannotStartTransit          = errors.New("waybill cannot enter transit in its current state")
	ErrCannotReportReceiverRefusal = errors.New("receiver refusal cannot be reported in the waybill's current state")
	ErrWaybillNotHeld              = errors.New("waybill is not in a resolvable held state")
	ErrWaybillAlreadyResolved      = errors.New("waybill has already been resolved")
	ErrInvalidResolutionOutcome    = errors.New("invalid waybill resolution outcome")
	ErrNoCorridorPolicy            = errors.New("no corridor policy for this route")
)

type HeldWaybillPayout func(context.Context, string, string, string, int64, string) (string, error)

type HeldWaybillResolution struct {
	Outcome           string
	State             string
	ResolvedAt        time.Time
	WalletBalanceKobo int64
	TransferReference string
	Splits            domain.SplitBreakdown
}

type EscrowRepository struct {
	db *sql.DB
}

func NewEscrowRepository(db *sql.DB) *EscrowRepository {
	return &EscrowRepository{db: db}
}

// CreateEscrowTx executes atomic wallet reservation, PIN hashing, waybill creation, and split pre-allocation
func (r *EscrowRepository) CreateEscrowTx(
	ctx context.Context,
	w domain.Waybill,
	rawPin string,
	split domain.SplitBreakdown,
) (int64, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Pessimistic row-level lock on agent wallet to prevent race-condition overdrafts
	var currentBalance int64
	agentLockQuery := `
		SELECT wallet_balance_kobo 
		FROM agents 
		WHERE id = $1 AND is_active = TRUE 
		FOR UPDATE
	`
	err = tx.QueryRowContext(ctx, agentLockQuery, w.AgentID).Scan(&currentBalance)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrAgentNotFound
		}
		return 0, fmt.Errorf("failed to lock agent record: %w", err)
	}

	if currentBalance < w.TotalFreightKobo {
		return 0, domain.ErrInsufficientAgentBalance
	}

	// 2. Deduct freight amount from agent wallet
	newBalance := currentBalance - w.TotalFreightKobo
	updateAgentQuery := `
		UPDATE agents 
		SET wallet_balance_kobo = $1, updated_at = NOW() 
		WHERE id = $2
	`
	if _, err := tx.ExecContext(ctx, updateAgentQuery, newBalance, w.AgentID); err != nil {
		return 0, fmt.Errorf("failed to deduct agent wallet: %w", err)
	}

	// 3. Cryptographically hash the secret PIN with salted bcrypt
	hashedPin, err := bcrypt.GenerateFromPassword([]byte(rawPin), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf("failed to hash secret PIN: %w", err)
	}

	// 4. Insert waybill in ESCROW_LOCKED state
	insertWaybillQuery := `
		INSERT INTO waybills (
			id, agent_id, sender_phone, receiver_phone, driver_phone,
			vehicle_reg_number, origin_park_id, destination_park_id,
			total_freight_kobo, pin_hash, failed_pin_attempts,
			driver_bank_code, driver_account_number, driver_verified_name,
			current_state, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, 0,
			$11, $12, $13,
			$14, NOW(), NOW()
		)
	`
	_, err = tx.ExecContext(ctx, insertWaybillQuery,
		w.ID, w.AgentID, w.SenderPhone, w.ReceiverPhone, w.DriverPhone,
		w.VehicleRegNumber, w.OriginParkID, w.DestinationParkID,
		w.TotalFreightKobo, string(hashedPin),
		w.DriverBankCode, w.DriverAccountNum, w.DriverVerifiedName,
		domain.WaybillStateEscrowLocked,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert waybill record: %w", err)
	}

	// 5. Create immutable audit ledger record
	insertLedgerQuery := `
		INSERT INTO escrow_ledgers (
			waybill_id, transaction_type, amount_kobo, balance_after_kobo, description, idempotency_key, created_at
		) VALUES ($1, 'AGENT_DEDUCTION', $2, $3, $4, $5, NOW())
	`
	idempotencyKey := fmt.Sprintf("INIT_LOCK_%s", w.ID)
	_, err = tx.ExecContext(ctx, insertLedgerQuery,
		w.ID, w.TotalFreightKobo, newBalance, "Waybill escrow reservation deducted from agent wallet", idempotencyKey,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to record ledger entry: %w", err)
	}

	// 6. Record pre-allocated payout split
	insertSplitQuery := `
		INSERT INTO payout_splits (
			waybill_id, driver_payout_kobo, agent_commission_kobo, union_dues_kobo, platform_treasury_kobo
		) VALUES ($1, $2, $3, $4, $5)
	`
	_, err = tx.ExecContext(ctx, insertSplitQuery,
		w.ID, split.DriverPayoutKobo, split.AgentCommissionKobo, split.UnionDuesKobo, split.PlatformTreasuryKobo,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to record payout split: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("transaction commit failed: %w", err)
	}

	return newBalance, nil
}

// ValidateAndMarkDeliveredTx locks the waybill row, verifies PIN hash, tracks failed attempts, and advances state
func (r *EscrowRepository) ValidateAndMarkDeliveredTx(ctx context.Context, waybillID, rawPin string) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var pinHash string
	var failedAttempts int
	var currentState string

	lockWaybillQuery := `
		SELECT pin_hash, failed_pin_attempts, current_state
		FROM waybills
		WHERE id = $1
		FOR UPDATE
	`
	err = tx.QueryRowContext(ctx, lockWaybillQuery, waybillID).Scan(&pinHash, &failedAttempts, &currentState)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrWaybillNotFound
		}
		return fmt.Errorf("failed to lock waybill: %w", err)
	}

	if failedAttempts >= 3 {
		return domain.ErrWaybillLocked
	}

	if currentState != domain.WaybillStateEscrowLocked && currentState != domain.WaybillStateInTransit {
		return fmt.Errorf("%w: current state is %s", domain.ErrInvalidStateTransition, currentState)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(pinHash), []byte(rawPin)); err != nil {
		newAttempts := failedAttempts + 1
		targetState := currentState
		if newAttempts >= 3 {
			targetState = domain.WaybillStateDisputeHold
		}

		updateFailureQuery := `
			UPDATE waybills
			SET failed_pin_attempts = $1, current_state = $2, updated_at = NOW()
			WHERE id = $3
		`
		_, _ = tx.ExecContext(ctx, updateFailureQuery, newAttempts, targetState, waybillID)
		_ = tx.Commit()

		if newAttempts >= 3 {
			return domain.ErrWaybillLocked
		}
		return domain.ErrPinMismatch
	}

	updateDeliveredQuery := `
		UPDATE waybills
		SET current_state = $1, updated_at = NOW()
		WHERE id = $2
	`
	if _, err := tx.ExecContext(ctx, updateDeliveredQuery, domain.WaybillStateDelivered, waybillID); err != nil {
		return fmt.Errorf("failed to transition waybill to DELIVERED: %w", err)
	}

	return tx.Commit()
}

// DisburseEscrowTx atomically distributes funds, credits agent commission, and flags waybill DISBURSED
func (r *EscrowRepository) DisburseEscrowTx(
	ctx context.Context,
	waybillID string,
	transferReference string,
	idempotencyKey string,
) (*domain.SplitBreakdown, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Lock waybill row
	var currentState string
	var agentID string
	var totalFreight int64

	waybillLockQuery := `
		SELECT current_state, agent_id, total_freight_kobo
		FROM waybills
		WHERE id = $1
		FOR UPDATE
	`
	err = tx.QueryRowContext(ctx, waybillLockQuery, waybillID).Scan(&currentState, &agentID, &totalFreight)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWaybillNotFound
		}
		return nil, fmt.Errorf("failed to lock waybill: %w", err)
	}

	if currentState == domain.WaybillStateDisbursed {
		return nil, ErrAlreadyDisbursed
	}
	if currentState != domain.WaybillStateDelivered {
		return nil, fmt.Errorf("%w: parcel status is %s", ErrInvalidPayout, currentState)
	}

	// 2. Lock payout_splits row
	var splits domain.SplitBreakdown
	var disbursedAt sql.NullTime

	splitLockQuery := `
		SELECT driver_payout_kobo, agent_commission_kobo, union_dues_kobo, platform_treasury_kobo, disbursed_at
		FROM payout_splits
		WHERE waybill_id = $1
		FOR UPDATE
	`
	err = tx.QueryRowContext(ctx, splitLockQuery, waybillID).Scan(
		&splits.DriverPayoutKobo,
		&splits.AgentCommissionKobo,
		&splits.UnionDuesKobo,
		&splits.PlatformTreasuryKobo,
		&disbursedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve payout split: %w", err)
	}

	if disbursedAt.Valid {
		return nil, ErrAlreadyDisbursed
	}
	splits.TotalFreightKobo = totalFreight

	if err := applyPayoutSettlementTx(ctx, tx, waybillID, agentID, transferReference, idempotencyKey, splits); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("disbursement commit failed: %w", err)
	}

	return &splits, nil
}

func applyPayoutSettlementTx(
	ctx context.Context,
	tx *sql.Tx,
	waybillID string,
	agentID string,
	transferReference string,
	idempotencyKey string,
	splits domain.SplitBreakdown,
) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE waybills
		SET current_state = $1, updated_at = NOW()
		WHERE id = $2
	`, domain.WaybillStateDisbursed, waybillID); err != nil {
		return fmt.Errorf("failed to update waybill state to DISBURSED: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE payout_splits
		SET driver_transfer_ref = $1, disbursed_at = NOW()
		WHERE waybill_id = $2
	`, transferReference, waybillID); err != nil {
		return fmt.Errorf("failed to record payout split disbursement: %w", err)
	}

	var agentBalance int64
	if err := tx.QueryRowContext(ctx, `
		SELECT wallet_balance_kobo
		FROM agents
		WHERE id = $1
		FOR UPDATE
	`, agentID).Scan(&agentBalance); err != nil {
		return fmt.Errorf("failed to lock agent for commission credit: %w", err)
	}

	newAgentBalance := agentBalance + splits.AgentCommissionKobo
	if _, err := tx.ExecContext(ctx, `
		UPDATE agents
		SET wallet_balance_kobo = $1, updated_at = NOW()
		WHERE id = $2
	`, newAgentBalance, agentID); err != nil {
		return fmt.Errorf("failed to credit agent commission: %w", err)
	}

	agentLedgerKey := fmt.Sprintf("COMMISSION_%s_%s", waybillID, idempotencyKey)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO escrow_ledgers (
			waybill_id, transaction_type, amount_kobo, balance_after_kobo, description, idempotency_key, created_at
		) VALUES ($1, 'AGENT_COMMISSION', $2, $3, $4, $5, NOW())
	`, waybillID, splits.AgentCommissionKobo, newAgentBalance, "Agent commission settlement credited to wallet", agentLedgerKey); err != nil {
		return fmt.Errorf("failed to write agent commission ledger: %w", err)
	}

	disburseDesc := fmt.Sprintf("Payout disbursed: Driver=%d, Union=%d, Treasury=%d Ref=%s",
		splits.DriverPayoutKobo, splits.UnionDuesKobo, splits.PlatformTreasuryKobo, transferReference)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO escrow_ledgers (
			waybill_id, transaction_type, amount_kobo, balance_after_kobo, description, idempotency_key, created_at
		) VALUES ($1, 'DISBURSEMENT_SPLIT', $2, 0, $3, $4, NOW())
	`, waybillID, splits.DriverPayoutKobo, disburseDesc, idempotencyKey); err != nil {
		return fmt.Errorf("failed to write disbursement ledger: %w", err)
	}

	return nil
}

// ResolveHeldWaybillTx commits a held-waybill resolution and its financial records atomically.
// The payout callback runs while the waybill row is locked so concurrent resolutions cannot pay twice.
func (r *EscrowRepository) ResolveHeldWaybillTx(
	ctx context.Context,
	waybillID string,
	adminID string,
	outcome string,
	reason string,
	payoutReference string,
	payout HeldWaybillPayout,
) (*HeldWaybillResolution, error) {
	if outcome != domain.ResolutionReleaseToDriver && outcome != domain.ResolutionRefundToAgent {
		return nil, ErrInvalidResolutionOutcome
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("failed to begin waybill resolution transaction: %w", err)
	}
	defer tx.Rollback()

	var currentState, agentID, driverName, driverAccount, driverBank string
	var totalFreight int64
	err = tx.QueryRowContext(ctx, `
		SELECT current_state, agent_id::text, total_freight_kobo,
		       driver_verified_name, driver_account_number, driver_bank_code
		FROM waybills
		WHERE id = $1
		FOR UPDATE
	`, waybillID).Scan(&currentState, &agentID, &totalFreight, &driverName, &driverAccount, &driverBank)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWaybillNotFound
		}
		return nil, fmt.Errorf("failed to lock waybill for resolution: %w", err)
	}

	if currentState == domain.WaybillStateDisbursed || currentState == domain.WaybillStateRefunded {
		return nil, ErrWaybillAlreadyResolved
	}
	targetState, err := domain.HeldWaybillResolutionState(currentState, outcome)
	if err != nil {
		return nil, ErrWaybillNotHeld
	}

	result := &HeldWaybillResolution{Outcome: outcome, State: targetState}
	if outcome == domain.ResolutionReleaseToDriver {
		if payout == nil {
			return nil, errors.New("driver payout callback is required for release resolution")
		}

		splits, err := lockPayoutSplitTx(ctx, tx, waybillID, totalFreight)
		if err != nil {
			return nil, err
		}
		transferReference, err := payout(ctx, driverName, driverAccount, driverBank, splits.DriverPayoutKobo, payoutReference)
		if err != nil {
			return nil, err
		}
		if err := applyPayoutSettlementTx(ctx, tx, waybillID, agentID, transferReference, payoutReference, splits); err != nil {
			return nil, err
		}
		result.TransferReference = transferReference
		result.Splits = splits
	} else {
		var agentBalance int64
		if err := tx.QueryRowContext(ctx, `
			SELECT wallet_balance_kobo
			FROM agents
			WHERE id = $1
			FOR UPDATE
		`, agentID).Scan(&agentBalance); err != nil {
			return nil, fmt.Errorf("failed to lock booking agent wallet for refund: %w", err)
		}

		newBalance := agentBalance + totalFreight
		if _, err := tx.ExecContext(ctx, `
			UPDATE agents
			SET wallet_balance_kobo = $1, updated_at = NOW()
			WHERE id = $2
		`, newBalance, agentID); err != nil {
			return nil, fmt.Errorf("failed to credit booking agent wallet: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE waybills
			SET current_state = $1, updated_at = NOW()
			WHERE id = $2
		`, targetState, waybillID); err != nil {
			return nil, fmt.Errorf("failed to mark waybill refunded: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO escrow_ledgers (
				waybill_id, transaction_type, amount_kobo, balance_after_kobo, description, idempotency_key, created_at
			) VALUES ($1, 'REFUND', $2, $3, $4, $5, NOW())
		`, waybillID, totalFreight, newBalance,
			fmt.Sprintf("Held waybill %s resolved; freight returned to booking agent", waybillID),
			fmt.Sprintf("RESOLUTION_REFUND_%s", waybillID)); err != nil {
			return nil, fmt.Errorf("failed to write held-waybill refund ledger: %w", err)
		}
		result.WalletBalanceKobo = newBalance
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO waybill_resolutions (waybill_id, resolved_by, outcome, reason)
		VALUES ($1, $2, $3, $4)
		RETURNING resolved_at
	`, waybillID, adminID, outcome, reason).Scan(&result.ResolvedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to record waybill resolution: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("waybill resolution commit failed: %w", err)
	}
	return result, nil
}

func lockPayoutSplitTx(ctx context.Context, tx *sql.Tx, waybillID string, totalFreight int64) (domain.SplitBreakdown, error) {
	var splits domain.SplitBreakdown
	var disbursedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT driver_payout_kobo, agent_commission_kobo, union_dues_kobo,
		       platform_treasury_kobo, disbursed_at
		FROM payout_splits
		WHERE waybill_id = $1
		FOR UPDATE
	`, waybillID).Scan(
		&splits.DriverPayoutKobo,
		&splits.AgentCommissionKobo,
		&splits.UnionDuesKobo,
		&splits.PlatformTreasuryKobo,
		&disbursedAt,
	)
	if err != nil {
		return domain.SplitBreakdown{}, fmt.Errorf("failed to retrieve payout split: %w", err)
	}
	if disbursedAt.Valid {
		return domain.SplitBreakdown{}, ErrWaybillAlreadyResolved
	}
	splits.TotalFreightKobo = totalFreight
	return splits, nil
}

// CancelEscrowTx returns locked freight to the owning agent and marks the waybill CANCELLED.
// Allowed only from ESCROW_LOCKED. Idempotent retry: if already CANCELLED and same agent, succeeds without double-credit.
func (r *EscrowRepository) CancelEscrowTx(
	ctx context.Context,
	waybillID string,
	agentID string,
	idempotencyKey string,
) (newWalletBalance int64, err error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Lock waybill
	var currentState string
	var ownerID string
	var totalFreight int64
	waybillLockQuery := `
		SELECT current_state, agent_id::text, total_freight_kobo
		FROM waybills
		WHERE id = $1
		FOR UPDATE
	`
	err = tx.QueryRowContext(ctx, waybillLockQuery, waybillID).Scan(&currentState, &ownerID, &totalFreight)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrWaybillNotFound
		}
		return 0, fmt.Errorf("failed to lock waybill: %w", err)
	}

	if ownerID != agentID {
		return 0, ErrNotWaybillOwner
	}

	// Already cancelled: return current wallet (no second credit)
	if currentState == domain.WaybillStateCancelled {
		var bal int64
		_ = tx.QueryRowContext(ctx, `SELECT wallet_balance_kobo FROM agents WHERE id = $1`, agentID).Scan(&bal)
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return bal, nil
	}

	if err := domain.AssertCanCancel(currentState); err != nil {
		return 0, fmt.Errorf("%w: %s", ErrCannotCancel, currentState)
	}

	// 2. Credit full freight back to agent wallet
	var agentBalance int64
	lockAgentQuery := `
		SELECT wallet_balance_kobo
		FROM agents
		WHERE id = $1 AND is_active = TRUE
		FOR UPDATE
	`
	err = tx.QueryRowContext(ctx, lockAgentQuery, agentID).Scan(&agentBalance)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrAgentNotFound
		}
		return 0, fmt.Errorf("failed to lock agent wallet: %w", err)
	}

	newBalance := agentBalance + totalFreight
	creditAgentQuery := `
		UPDATE agents
		SET wallet_balance_kobo = $1, updated_at = NOW()
		WHERE id = $2
	`
	if _, err := tx.ExecContext(ctx, creditAgentQuery, newBalance, agentID); err != nil {
		return 0, fmt.Errorf("failed to credit agent wallet on cancel: %w", err)
	}

	// 3. Transition waybill
	updateWaybillQuery := `
		UPDATE waybills
		SET current_state = $1, updated_at = NOW()
		WHERE id = $2
	`
	if _, err := tx.ExecContext(ctx, updateWaybillQuery, domain.WaybillStateCancelled, waybillID); err != nil {
		return 0, fmt.Errorf("failed to set waybill CANCELLED: %w", err)
	}

	// 4. Ledger (REFUND matches original schema CHECK)
	insertLedgerQuery := `
		INSERT INTO escrow_ledgers (
			waybill_id, transaction_type, amount_kobo, balance_after_kobo, description, idempotency_key, created_at
		) VALUES ($1, 'REFUND', $2, $3, $4, $5, NOW())
	`
	key := idempotencyKey
	if key == "" {
		key = fmt.Sprintf("CANCEL_%s", waybillID)
	}
	desc := fmt.Sprintf("Waybill %s cancelled; freight returned to agent wallet", waybillID)
	_, err = tx.ExecContext(ctx, insertLedgerQuery, waybillID, totalFreight, newBalance, desc, key)
	if err != nil {
		return 0, fmt.Errorf("failed to write cancel ledger: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("cancel commit failed: %w", err)
	}
	return newBalance, nil
}

// StartTransitTx moves an owned, escrow-locked waybill into transit exactly once.
func (r *EscrowRepository) StartTransitTx(ctx context.Context, waybillID, agentID string) (time.Time, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var currentState string
	var ownerID string
	err = tx.QueryRowContext(ctx, `
		SELECT current_state, agent_id::text
		FROM waybills
		WHERE id = $1
		FOR UPDATE
	`, waybillID).Scan(&currentState, &ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, ErrWaybillNotFound
		}
		return time.Time{}, fmt.Errorf("failed to lock waybill: %w", err)
	}
	if ownerID != agentID {
		return time.Time{}, ErrNotWaybillOwner
	}
	if !domain.CanStartTransit(currentState) {
		return time.Time{}, fmt.Errorf("%w: %s", ErrCannotStartTransit, currentState)
	}

	var inTransitAt time.Time
	err = tx.QueryRowContext(ctx, `
		UPDATE waybills
		SET current_state = $1, in_transit_at = NOW(), updated_at = NOW()
		WHERE id = $2
		RETURNING in_transit_at
	`, domain.WaybillStateInTransit, waybillID).Scan(&inTransitAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to start waybill transit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, fmt.Errorf("failed to commit transit start: %w", err)
	}
	return inTransitAt, nil
}

// HoldExpiredTransitWaybills moves unclaimed transit waybills past the timeout into UNCLAIMED_HOLD.
func (r *EscrowRepository) HoldExpiredTransitWaybills(ctx context.Context, timeout time.Duration) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE waybills
		SET current_state = $1, updated_at = NOW()
		WHERE current_state = $2
		  AND in_transit_at <= NOW() - ($3 * INTERVAL '1 second')
	`, domain.WaybillStateUnclaimedHold, domain.WaybillStateInTransit, int64(timeout.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("failed to hold expired transit waybills: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count held waybills: %w", err)
	}
	return rowsAffected, nil
}

// ReportReceiverRefusalTx records the refusal and holds the waybill atomically.
func (r *EscrowRepository) ReportReceiverRefusalTx(ctx context.Context, waybillID, agentID, reason string) (time.Time, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var currentState string
	var ownerID string
	err = tx.QueryRowContext(ctx, `
		SELECT current_state, agent_id::text
		FROM waybills
		WHERE id = $1
		FOR UPDATE
	`, waybillID).Scan(&currentState, &ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, ErrWaybillNotFound
		}
		return time.Time{}, fmt.Errorf("failed to lock waybill: %w", err)
	}
	if ownerID != agentID {
		return time.Time{}, ErrNotWaybillOwner
	}
	if !domain.CanReportReceiverRefusal(currentState) {
		return time.Time{}, fmt.Errorf("%w: %s", ErrCannotReportReceiverRefusal, currentState)
	}

	var reportedAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO receiver_refusal_reports (waybill_id, agent_id, reason)
		VALUES ($1, $2, $3)
		RETURNING created_at
	`, waybillID, agentID, reason).Scan(&reportedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to record receiver refusal: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE waybills
		SET current_state = $1, updated_at = NOW()
		WHERE id = $2
	`, domain.WaybillStateDisputeHold, waybillID); err != nil {
		return time.Time{}, fmt.Errorf("failed to place waybill on dispute hold: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, fmt.Errorf("failed to commit receiver refusal report: %w", err)
	}
	return reportedAt, nil
}

// GetWaybill retrieves the waybill record by ID
func (r *EscrowRepository) GetWaybill(ctx context.Context, waybillID string) (*domain.Waybill, error) {
	query := `
		SELECT 
			w.id, w.agent_id, w.sender_phone, w.receiver_phone, w.driver_phone,
			w.vehicle_reg_number, w.origin_park_id, w.destination_park_id,
			w.total_freight_kobo, w.failed_pin_attempts, w.driver_bank_code,
			w.driver_account_number, w.driver_verified_name, w.current_state,
			w.created_at, w.updated_at, rr.reason, rr.created_at
		FROM waybills w
		LEFT JOIN receiver_refusal_reports rr ON rr.waybill_id = w.id
		WHERE w.id = $1
	`
	var w domain.Waybill
	var refusalReason sql.NullString
	var refusalReportedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, query, waybillID).Scan(
		&w.ID, &w.AgentID, &w.SenderPhone, &w.ReceiverPhone, &w.DriverPhone,
		&w.VehicleRegNumber, &w.OriginParkID, &w.DestinationParkID,
		&w.TotalFreightKobo, &w.FailedPinAttempts, &w.DriverBankCode,
		&w.DriverAccountNum, &w.DriverVerifiedName, &w.CurrentState,
		&w.CreatedAt, &w.UpdatedAt, &refusalReason, &refusalReportedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWaybillNotFound
		}
		return nil, err
	}
	if refusalReason.Valid {
		w.ReceiverRefusalReason = refusalReason.String
	}
	if refusalReportedAt.Valid {
		w.ReceiverRefusalReportedAt = &refusalReportedAt.Time
	}
	return &w, nil
}

// GetPayoutSplit retrieves the pre-allocated payout breakdown
func (r *EscrowRepository) GetPayoutSplit(ctx context.Context, waybillID string) (*domain.SplitBreakdown, error) {
	query := `
		SELECT driver_payout_kobo, agent_commission_kobo, union_dues_kobo, platform_treasury_kobo
		FROM payout_splits
		WHERE waybill_id = $1
	`
	var s domain.SplitBreakdown
	err := r.db.QueryRowContext(ctx, query, waybillID).Scan(
		&s.DriverPayoutKobo,
		&s.AgentCommissionKobo,
		&s.UnionDuesKobo,
		&s.PlatformTreasuryKobo,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetCorridorPolicy retrieves the active pricing and revenue split policy dynamically from PostgreSQL
func (r *EscrowRepository) GetCorridorPolicy(ctx context.Context, originParkID, destParkID string) (*domain.CorridorFeePolicy, error) {
	query := `
		SELECT 
			id, corridor_name, model, threshold_kobo,
			platform_basis_points, platform_floor_kobo,
			agent_basis_points, agent_fixed_kobo,
			union_basis_points, union_fixed_kobo
		FROM corridor_policies
		WHERE origin_park_id = $1 AND destination_park_id = $2 AND is_active = TRUE
		LIMIT 1
	`
	var p domain.CorridorFeePolicy
	var modelStr string

	err := r.db.QueryRowContext(ctx, query, originParkID, destParkID).Scan(
		&p.ID, &p.CorridorName, &modelStr, &p.ThresholdKobo,
		&p.PlatformBasisPoints, &p.PlatformFloorKobo,
		&p.AgentBasisPoints, &p.AgentFixedKobo,
		&p.UnionBasisPoints, &p.UnionFixedKobo,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoCorridorPolicy
		}
		return nil, fmt.Errorf("database error fetching corridor policy: %w", err)
	}

	p.Model = domain.PricingModel(modelStr)
	return &p, nil
}
