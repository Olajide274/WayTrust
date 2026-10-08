package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/nurtw/escrow/internal/domain"
	"github.com/nurtw/escrow/internal/repository"
	"github.com/nurtw/escrow/internal/resolver"
)

var (
	ErrInvalidFreightAmount         = errors.New("total_freight_kobo must be greater than zero")
	ErrWaybillNotDelivered          = errors.New("cannot disburse escrow: parcel must be in DELIVERED state")
	ErrInvalidContactInfo           = errors.New("sender, receiver, and driver phone numbers are required")
	ErrInvalidNUBAN                 = errors.New("account number must be exactly 10 numeric digits")
	ErrInvalidReceiverRefusalReason = errors.New("receiver refusal reason must be between 1 and 500 characters")
	ErrInvalidResolutionReason      = errors.New("resolution reason must be between 1 and 500 characters")
	ErrPayoutTransferFailed         = errors.New("upstream payout transfer failed")
)

type EscrowService struct {
	repo            *repository.EscrowRepository
	nibssResolver   resolver.BankAccountResolver
	calculator      domain.FeeCalculator
	payoutDisburser resolver.PayoutDisburser
}

func NewEscrowService(
	repo *repository.EscrowRepository,
	res resolver.BankAccountResolver,
	calc domain.FeeCalculator,
	payout resolver.PayoutDisburser,
) *EscrowService {
	return &EscrowService{
		repo:            repo,
		nibssResolver:   res,
		calculator:      calc,
		payoutDisburser: payout,
	}
}

type CreateWaybillDTO struct {
	AgentID             string
	SenderPhone         string
	ReceiverPhone       string
	DriverPhone         string
	VehicleRegNumber    string
	OriginParkID        string
	DestinationParkID   string
	TotalFreightKobo    int64
	DriverAccountNumber string
	DriverBankCode      string
	DriverDeclaredName  string
	CorridorPolicy      domain.CorridorFeePolicy
}

type CreateWaybillResult struct {
	WaybillID             string
	SecretPin             string
	AgentRemainingBalance int64
	ResolvedAccountName   string
	NameMatchConfidence   float64
}

func generateSecureNumericPIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(9000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d", n.Int64()+1000), nil
}

func (s *EscrowService) CreateWaybill(ctx context.Context, dto CreateWaybillDTO) (*CreateWaybillResult, error) {
	// 1. Fail-fast input validation (runs before calling any external APIs)
	if dto.TotalFreightKobo <= 0 {
		return nil, ErrInvalidFreightAmount
	}
	if dto.SenderPhone == "" || dto.ReceiverPhone == "" || dto.DriverPhone == "" {
		return nil, ErrInvalidContactInfo
	}
	if len(dto.DriverAccountNumber) != 10 {
		return nil, ErrInvalidNUBAN
	}

	// 2. Compute 4-party corridor revenue split
	split, err := s.calculator.Calculate(dto.TotalFreightKobo, dto.CorridorPolicy)
	if err != nil {
		return nil, fmt.Errorf("fee calculation error: %w", err)
	}

	// 3. Resolve and verify driver bank details with upstream NIBSS switch
	verification, err := s.nibssResolver.ResolveAndVerify(
		ctx,
		dto.DriverAccountNumber,
		dto.DriverBankCode,
		dto.DriverDeclaredName,
		0.70, // 70% confidence threshold for phonetic / tokenized matching
	)
	if err != nil {
		return nil, fmt.Errorf("driver bank validation failed: %w", err)
	}

	// 4. Generate high-entropy 4-digit pickup PIN
	rawPin, err := generateSecureNumericPIN()
	if err != nil {
		return nil, fmt.Errorf("failed to generate secure PIN: %w", err)
	}

	waybillID := fmt.Sprintf("WB-%s", uuid.New().String()[:8])

	waybill := domain.Waybill{
		ID:                 waybillID,
		AgentID:            dto.AgentID,
		SenderPhone:        dto.SenderPhone,
		ReceiverPhone:      dto.ReceiverPhone,
		DriverPhone:        dto.DriverPhone,
		VehicleRegNumber:   dto.VehicleRegNumber,
		OriginParkID:       dto.OriginParkID,
		DestinationParkID:  dto.DestinationParkID,
		TotalFreightKobo:   dto.TotalFreightKobo,
		DriverBankCode:     dto.DriverBankCode,
		DriverAccountNum:   dto.DriverAccountNumber,
		DriverVerifiedName: verification.ResolvedAccountName,
		CurrentState:       domain.WaybillStateEscrowLocked,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	// 5. Atomic database execution: Agent row lock + wallet deduction + Bcrypt hash + split ledger
	newAgentBalance, err := s.repo.CreateEscrowTx(ctx, waybill, rawPin, split)
	if err != nil {
		return nil, err
	}

	return &CreateWaybillResult{
		WaybillID:             waybillID,
		SecretPin:             rawPin,
		AgentRemainingBalance: newAgentBalance,
		ResolvedAccountName:   verification.ResolvedAccountName,
		NameMatchConfidence:   verification.ConfidenceScore,
	}, nil
}

func (s *EscrowService) VerifyPickup(ctx context.Context, waybillID, rawPin string) error {
	return s.repo.ValidateAndMarkDeliveredTx(ctx, waybillID, rawPin)
}

type DisbursePayoutResult struct {
	WaybillID            string
	TransferReference    string
	DriverPayoutKobo     int64
	AgentCommissionKobo  int64
	UnionDuesKobo        int64
	PlatformTreasuryKobo int64
	DisbursedAt          time.Time
}

type ResolveHeldWaybillResult struct {
	WaybillID         string
	Outcome           string
	State             string
	ResolvedAt        time.Time
	WalletBalanceKobo int64
	TransferReference string
	DriverPayoutKobo  int64
}

func (s *EscrowService) DisbursePayout(ctx context.Context, waybillID, idempotencyKey string) (*DisbursePayoutResult, error) {
	w, err := s.repo.GetWaybill(ctx, waybillID)
	if err != nil {
		return nil, err
	}

	if w.CurrentState != domain.WaybillStateDelivered {
		return nil, ErrWaybillNotDelivered
	}

	splits, err := s.repo.GetPayoutSplit(ctx, waybillID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve payout split: %w", err)
	}

	transferRef, err := s.payoutDisburser.InitiateDriverPayout(
		ctx,
		w.DriverVerifiedName,
		w.DriverAccountNum,
		w.DriverBankCode,
		splits.DriverPayoutKobo,
		idempotencyKey,
	)
	if err != nil {
		return nil, fmt.Errorf("upstream payout transfer failed: %w", err)
	}

	finalSplits, err := s.repo.DisburseEscrowTx(ctx, waybillID, transferRef, idempotencyKey)
	if err != nil {
		return nil, err
	}

	return &DisbursePayoutResult{
		WaybillID:            waybillID,
		TransferReference:    transferRef,
		DriverPayoutKobo:     finalSplits.DriverPayoutKobo,
		AgentCommissionKobo:  finalSplits.AgentCommissionKobo,
		UnionDuesKobo:        finalSplits.UnionDuesKobo,
		PlatformTreasuryKobo: finalSplits.PlatformTreasuryKobo,
		DisbursedAt:          time.Now(),
	}, nil
}

func (s *EscrowService) ResolveHeldWaybill(
	ctx context.Context,
	adminID string,
	waybillID string,
	outcome string,
	reason string,
) (*ResolveHeldWaybillResult, error) {
	if adminID == "" || waybillID == "" {
		return nil, errors.New("admin_id and waybill_id are required")
	}
	reason = strings.TrimSpace(reason)
	if length := utf8.RuneCountInString(reason); length < 1 || length > 500 {
		return nil, ErrInvalidResolutionReason
	}
	if outcome != domain.ResolutionReleaseToDriver && outcome != domain.ResolutionRefundToAgent {
		return nil, repository.ErrInvalidResolutionOutcome
	}

	payoutReference := fmt.Sprintf("ADMIN_RESOLVE_%s", waybillID)
	var payout repository.HeldWaybillPayout
	if outcome == domain.ResolutionReleaseToDriver {
		payout = func(
			ctx context.Context,
			accountName, accountNo, bankCode string,
			amountKobo int64,
			reference string,
		) (string, error) {
			transferRef, err := s.payoutDisburser.InitiateDriverPayout(
				ctx, accountName, accountNo, bankCode, amountKobo, reference,
			)
			if err != nil {
				return "", fmt.Errorf("%w: %w", ErrPayoutTransferFailed, err)
			}
			return transferRef, nil
		}
	}

	resolution, err := s.repo.ResolveHeldWaybillTx(
		ctx, waybillID, adminID, outcome, reason, payoutReference, payout,
	)
	if err != nil {
		return nil, err
	}

	return &ResolveHeldWaybillResult{
		WaybillID:         waybillID,
		Outcome:           resolution.Outcome,
		State:             resolution.State,
		ResolvedAt:        resolution.ResolvedAt,
		WalletBalanceKobo: resolution.WalletBalanceKobo,
		TransferReference: resolution.TransferReference,
		DriverPayoutKobo:  resolution.Splits.DriverPayoutKobo,
	}, nil
}

func (s *EscrowService) CancelWaybill(ctx context.Context, agentID, waybillID, idempotencyKey string) (int64, error) {
	if agentID == "" || waybillID == "" {
		return 0, fmt.Errorf("agent_id and waybill_id are required")
	}
	if idempotencyKey == "" {
		idempotencyKey = fmt.Sprintf("CANCEL_%s", waybillID)
	}
	return s.repo.CancelEscrowTx(ctx, waybillID, agentID, idempotencyKey)
}

func (s *EscrowService) StartTransit(ctx context.Context, agentID, waybillID string) (time.Time, error) {
	if agentID == "" || waybillID == "" {
		return time.Time{}, fmt.Errorf("agent_id and waybill_id are required")
	}
	return s.repo.StartTransitTx(ctx, waybillID, agentID)
}

func (s *EscrowService) ReportReceiverRefusal(ctx context.Context, agentID, waybillID, reason string) (time.Time, error) {
	if agentID == "" || waybillID == "" {
		return time.Time{}, fmt.Errorf("agent_id and waybill_id are required")
	}
	reason = strings.TrimSpace(reason)
	if length := utf8.RuneCountInString(reason); length < 1 || length > 500 {
		return time.Time{}, ErrInvalidReceiverRefusalReason
	}
	return s.repo.ReportReceiverRefusalTx(ctx, waybillID, agentID, reason)
}

// GetWaybillStatus returns the current waybill record for USSD status checks
func (s *EscrowService) GetWaybillStatus(ctx context.Context, waybillID string) (*domain.Waybill, error) {
	return s.repo.GetWaybill(ctx, waybillID)
}
