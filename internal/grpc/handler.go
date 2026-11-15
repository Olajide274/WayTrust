package grpc

import (
	"context"
	"errors"

	"github.com/nurtw/escrow/internal/domain"
	"github.com/nurtw/escrow/internal/repository"
	"github.com/nurtw/escrow/internal/service"
	waybillv1 "github.com/nurtw/escrow/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// WaybillGRPCHandler implements the waybillv1.WaybillServiceServer interface
type WaybillGRPCHandler struct {
	waybillv1.UnimplementedWaybillServiceServer
	repo          *repository.EscrowRepository
	escrowService *service.EscrowService
}

// NewWaybillGRPCHandler constructs the gRPC handler with dependencies
func NewWaybillGRPCHandler(repo *repository.EscrowRepository, svc *service.EscrowService) *WaybillGRPCHandler {
	return &WaybillGRPCHandler{
		repo:          repo,
		escrowService: svc,
	}
}

// CreateWaybill handles incoming gRPC booking requests from the Park Agent
func (h *WaybillGRPCHandler) CreateWaybill(
	ctx context.Context,
	req *waybillv1.CreateWaybillRequest,
) (*waybillv1.CreateWaybillResponse, error) {
	if req.AgentId == "" || req.SenderPhone == "" || req.ReceiverPhone == "" || req.DriverPhone == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required participant contact details")
	}

	if req.TotalFreightKobo <= 0 {
		return nil, status.Error(codes.InvalidArgument, "total_freight_kobo must be greater than zero")
	}

	if req.DriverAccountNumber == "" || req.DriverBankCode == "" || req.DriverDeclaredName == "" {
		return nil, status.Error(codes.InvalidArgument, "missing driver bank account verification details")
	}

	// Dynamically query PostgreSQL for corridor fee policy matching origin and destination parks
	policy, err := h.repo.GetCorridorPolicy(ctx, req.OriginParkId, req.DestinationParkId)
	if err != nil {
		if errors.Is(err, repository.ErrNoCorridorPolicy) {
			return nil, status.Error(codes.NotFound, repository.ErrNoCorridorPolicy.Error())
		}
		return nil, status.Errorf(codes.Internal, "failed to fetch corridor policy: %v", err)
	}

	dto := service.CreateWaybillDTO{
		AgentID:             req.AgentId,
		SenderPhone:         req.SenderPhone,
		ReceiverPhone:       req.ReceiverPhone,
		DriverPhone:         req.DriverPhone,
		VehicleRegNumber:    req.VehicleRegNumber,
		OriginParkID:        req.OriginParkId,
		DestinationParkID:   req.DestinationParkId,
		TotalFreightKobo:    req.TotalFreightKobo,
		DriverAccountNumber: req.DriverAccountNumber,
		DriverBankCode:      req.DriverBankCode,
		DriverDeclaredName:  req.DriverDeclaredName,
		CorridorPolicy:      *policy,
	}

	res, err := h.escrowService.CreateWaybill(ctx, dto)
	if err != nil {
		if errors.Is(err, domain.ErrInsufficientAgentBalance) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		if errors.Is(err, repository.ErrAgentNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "failed to create waybill: %v", err)
	}

	return &waybillv1.CreateWaybillResponse{
		WaybillId:                 res.WaybillID,
		SecretPin:                 res.SecretPin,
		State:                     waybillv1.WaybillState_WAYBILL_STATE_ESCROW_LOCKED,
		AgentRemainingBalanceKobo: res.AgentRemainingBalance,
		ResolvedDriverAccountName: res.ResolvedAccountName,
		NameMatchConfidence:       res.NameMatchConfidence,
	}, nil
}

// ValidatePin verifies the 4-digit pickup PIN submitted via USSD or PWA
func (h *WaybillGRPCHandler) ValidatePin(
	ctx context.Context,
	req *waybillv1.ValidatePinRequest,
) (*waybillv1.ValidatePinResponse, error) {
	if req.WaybillId == "" || req.Pin == "" {
		return nil, status.Error(codes.InvalidArgument, "waybill_id and pin are required")
	}

	err := h.escrowService.VerifyPickup(ctx, req.WaybillId, req.Pin)
	if err != nil {
		if errors.Is(err, domain.ErrPinMismatch) {
			return &waybillv1.ValidatePinResponse{
				Success:  false,
				Message:  "INCORRECT_PIN: Authentication failed",
				NewState: waybillv1.WaybillState_WAYBILL_STATE_ESCROW_LOCKED,
			}, nil
		}
		if errors.Is(err, domain.ErrWaybillLocked) {
			return &waybillv1.ValidatePinResponse{
				Success:  false,
				Message:  "ACCOUNT_LOCKED: 3 failed PIN attempts, transitioned to DISPUTE_HOLD",
				NewState: waybillv1.WaybillState_WAYBILL_STATE_DISPUTE_HOLD,
			}, nil
		}
		if errors.Is(err, repository.ErrWaybillNotFound) {
			return nil, status.Error(codes.NotFound, "waybill not found")
		}
		return nil, status.Errorf(codes.Internal, "verification failed: %v", err)
	}

	return &waybillv1.ValidatePinResponse{
		Success:  true,
		Message:  "SUCCESS: Parcel delivery verified",
		NewState: waybillv1.WaybillState_WAYBILL_STATE_DELIVERED,
	}, nil
}

// DisbursePayout processes the multi-party transfer and state settlement
func (h *WaybillGRPCHandler) DisbursePayout(
	ctx context.Context,
	req *waybillv1.DisbursePayoutRequest,
) (*waybillv1.DisbursePayoutResponse, error) {
	if req.WaybillId == "" || req.IdempotencyKey == "" {
		return nil, status.Error(codes.InvalidArgument, "waybill_id and idempotency_key are required")
	}

	res, err := h.escrowService.DisbursePayout(ctx, req.WaybillId, req.IdempotencyKey)
	if err != nil {
		if errors.Is(err, repository.ErrAlreadyDisbursed) {
			return nil, status.Error(codes.AlreadyExists, "waybill has already been disbursed")
		}
		if errors.Is(err, service.ErrWaybillNotDelivered) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		if errors.Is(err, repository.ErrWaybillNotFound) {
			return nil, status.Error(codes.NotFound, "waybill not found")
		}
		if errors.Is(err, service.ErrPayoutInProgress) {
			return nil, status.Error(
				codes.Aborted,
				service.PayoutInProgressResponse+": "+service.PayoutInProgressReason,
			)
		}
		if errors.Is(err, service.ErrPayoutTransferFailed) {
			return nil, status.Error(
				codes.Unavailable,
				service.PayoutFailedResponse+": "+service.PayoutFailedReason,
			)
		}
		return nil, status.Errorf(codes.Internal, "disbursement failed: %v", err)
	}

	return &waybillv1.DisbursePayoutResponse{
		Success:              true,
		TransactionReference: res.TransferReference,
		DriverAmountKobo:     res.DriverPayoutKobo,
		AgentAmountKobo:      res.AgentCommissionKobo,
		UnionAmountKobo:      res.UnionDuesKobo,
		TreasuryAmountKobo:   res.PlatformTreasuryKobo,
	}, nil
}
