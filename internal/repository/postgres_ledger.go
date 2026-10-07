package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nurtw/escrow/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrWaybillNotFound  = errors.New("waybill record not found")
	ErrAgentNotFound    = errors.New("agent record not found or currently inactive")
	ErrAlreadyDisbursed = errors.New("waybill escrow has already been disbursed")
	ErrInvalidPayout    = errors.New("waybill must be in DELIVERED state prior to disbursement")
	ErrNotWaybillOwner = errors.New("waybill does not belong to this agent")
	ErrCannotCancel    = errors.New("waybill cannot be cancelled in its current state")
)

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

	// 3. Mark waybill as DISBURSED
	updateWaybillQuery := `
		UPDATE waybills
		SET current_state = $1, updated_at = NOW()
		WHERE id = $2
	`
	if _, err := tx.ExecContext(ctx, updateWaybillQuery, domain.WaybillStateDisbursed, waybillID); err != nil {
		return nil, fmt.Errorf("failed to update waybill state to DISBURSED: %w", err)
	}

	// 4. Mark payout_splits record with transfer reference and timestamp
	updateSplitQuery := `
		UPDATE payout_splits
		SET driver_transfer_ref = $1, disbursed_at = NOW()
		WHERE waybill_id = $2
	`
	if _, err := tx.ExecContext(ctx, updateSplitQuery, transferReference, waybillID); err != nil {
		return nil, fmt.Errorf("failed to record payout split disbursement: %w", err)
	}

	// 5. Credit agent pre-funded wallet with earned commission
	var agentBalance int64
	lockAgentQuery := `
		SELECT wallet_balance_kobo 
		FROM agents 
		WHERE id = $1 
		FOR UPDATE
	`
	if err := tx.QueryRowContext(ctx, lockAgentQuery, agentID).Scan(&agentBalance); err != nil {
		return nil, fmt.Errorf("failed to lock agent for commission credit: %w", err)
	}

	newAgentBalance := agentBalance + splits.AgentCommissionKobo
	creditAgentQuery := `
		UPDATE agents 
		SET wallet_balance_kobo = $1, updated_at = NOW() 
		WHERE id = $2
	`
	if _, err := tx.ExecContext(ctx, creditAgentQuery, newAgentBalance, agentID); err != nil {
		return nil, fmt.Errorf("failed to credit agent commission: %w", err)
	}

	// 6. Record immutable audit ledger entry for agent commission
	insertAgentLedger := `
		INSERT INTO escrow_ledgers (
			waybill_id, transaction_type, amount_kobo, balance_after_kobo, description, idempotency_key, created_at
		) VALUES ($1, 'AGENT_COMMISSION', $2, $3, $4, $5, NOW())
	`
	agentLedgerKey := fmt.Sprintf("COMMISSION_%s_%s", waybillID, idempotencyKey)
	_, err = tx.ExecContext(ctx, insertAgentLedger,
		waybillID, splits.AgentCommissionKobo, newAgentBalance, "Agent commission settlement credited to wallet", agentLedgerKey,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to write agent commission ledger: %w", err)
	}

	// 7. Record immutable audit ledger entry for driver payout
	insertDisburseLedger := `
		INSERT INTO escrow_ledgers (
			waybill_id, transaction_type, amount_kobo, balance_after_kobo, description, idempotency_key, created_at
		) VALUES ($1, 'DISBURSEMENT_SPLIT', $2, 0, $3, $4, NOW())
	`
	disburseDesc := fmt.Sprintf("Payout disbursed: Driver=%d, Union=%d, Treasury=%d Ref=%s",
		splits.DriverPayoutKobo, splits.UnionDuesKobo, splits.PlatformTreasuryKobo, transferReference)
	_, err = tx.ExecContext(ctx, insertDisburseLedger,
		waybillID, splits.DriverPayoutKobo, disburseDesc, idempotencyKey,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to write disbursement ledger: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("disbursement commit failed: %w", err)
	}

	return &splits, nil
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

// GetWaybill retrieves the waybill record by ID
func (r *EscrowRepository) GetWaybill(ctx context.Context, waybillID string) (*domain.Waybill, error) {
	query := `
		SELECT 
			id, agent_id, sender_phone, receiver_phone, driver_phone,
			vehicle_reg_number, origin_park_id, destination_park_id,
			total_freight_kobo, failed_pin_attempts, driver_bank_code,
			driver_account_number, driver_verified_name, current_state,
			created_at, updated_at
		FROM waybills
		WHERE id = $1
	`
	var w domain.Waybill
	err := r.db.QueryRowContext(ctx, query, waybillID).Scan(
		&w.ID, &w.AgentID, &w.SenderPhone, &w.ReceiverPhone, &w.DriverPhone,
		&w.VehicleRegNumber, &w.OriginParkID, &w.DestinationParkID,
		&w.TotalFreightKobo, &w.FailedPinAttempts, &w.DriverBankCode,
		&w.DriverAccountNum, &w.DriverVerifiedName, &w.CurrentState,
		&w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWaybillNotFound
		}
		return nil, err
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
			// Fallback: Default to first active corridor policy configured in database
			fallbackQuery := `
				SELECT 
					id, corridor_name, model, threshold_kobo,
					platform_basis_points, platform_floor_kobo,
					agent_basis_points, agent_fixed_kobo,
					union_basis_points, union_fixed_kobo
				FROM corridor_policies
				WHERE is_active = TRUE
				ORDER BY created_at ASC
				LIMIT 1
			`
			err = r.db.QueryRowContext(ctx, fallbackQuery).Scan(
				&p.ID, &p.CorridorName, &modelStr, &p.ThresholdKobo,
				&p.PlatformBasisPoints, &p.PlatformFloorKobo,
				&p.AgentBasisPoints, &p.AgentFixedKobo,
				&p.UnionBasisPoints, &p.UnionFixedKobo,
			)
			if err != nil {
				return nil, fmt.Errorf("no active corridor fee policy found in database: %w", err)
			}
		} else {
			return nil, fmt.Errorf("database error fetching corridor policy: %w", err)
		}
	}

	p.Model = domain.PricingModel(modelStr)
	return &p, nil
}