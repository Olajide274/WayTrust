package domain

import (
	"errors"
	"fmt"
	"time"
)

// Domain Errors
var (
	ErrInsufficientAgentBalance = errors.New("insufficient agent pre-funded wallet balance")
	ErrInvalidStateTransition   = errors.New("invalid escrow state transition")
	ErrPinMismatch              = errors.New("invalid 4-digit secret PIN")
	ErrWaybillLocked            = errors.New("waybill locked due to excessive failed PIN attempts")
	ErrDriverNameMismatch       = errors.New("driver bank account name does not match union record")
	ErrInvalidFeePolicy         = errors.New("invalid fee policy configuration: deductions exceed freight")
)

// Stakeholder financial split structure in Kobo (1 Naira = 100 Kobo)
type SplitBreakdown struct {
	DriverPayoutKobo     int64 `json:"driver_payout_kobo"`
	AgentCommissionKobo  int64 `json:"agent_commission_kobo"`
	UnionDuesKobo        int64 `json:"union_dues_kobo"`
	PlatformTreasuryKobo int64 `json:"platform_treasury_kobo"`
	TotalFreightKobo     int64 `json:"total_freight_kobo"`
}

// PricingType identifies the mathematical model applied to a corridor
type PricingModel string

const (
	PricingModelProportional PricingModel = "PROPORTIONAL"  // Basis points split
	PricingModelDynamicFloor PricingModel = "DYNAMIC_FLOOR" // Percentage with fixed floor
)

// Waybill state machine constants (must match the CHECK constraint in the database)
const (
	WaybillStateCreated       = "CREATED"
	WaybillStateEscrowLocked  = "ESCROW_LOCKED"
	WaybillStateInTransit     = "IN_TRANSIT"
	WaybillStateDelivered     = "DELIVERED"
	WaybillStateDisbursed     = "DISBURSED"
	WaybillStateDisputeHold   = "DISPUTE_HOLD"
	WaybillStateUnclaimedHold = "UNCLAIMED_HOLD"
	WaybillStateCancelled     = "CANCELLED"
	WaybillStateRefunded      = "REFUNDED"
)

const (
	ResolutionReleaseToDriver = "RELEASE_TO_DRIVER"
	ResolutionRefundToAgent   = "REFUND_TO_AGENT"
)

// CorridorFeePolicy defines the dynamic rules for a specific route corridor
type CorridorFeePolicy struct {
	ID                  string       `json:"id"`
	CorridorName        string       `json:"corridor_name"`
	Model               PricingModel `json:"model"`
	ThresholdKobo       int64        `json:"threshold_kobo"`
	PlatformBasisPoints int64        `json:"platform_basis_points"`
	PlatformFloorKobo   int64        `json:"platform_floor_kobo"`
	AgentBasisPoints    int64        `json:"agent_basis_points"`
	AgentFixedKobo      int64        `json:"agent_fixed_kobo"`
	UnionBasisPoints    int64        `json:"union_basis_points"`
	UnionFixedKobo      int64        `json:"union_fixed_kobo"`
}

// FeeCalculator evaluates dynamic corridor rules without hardcoded branches
type FeeCalculator interface {
	Calculate(freightKobo int64, policy CorridorFeePolicy) (SplitBreakdown, error)
}

// DefaultFeeCalculator executes mathematical splits with zero floating point inaccuracies
type DefaultFeeCalculator struct{}

func NewFeeCalculator() *DefaultFeeCalculator {
	return &DefaultFeeCalculator{}
}

// Calculate applies the policy configuration dynamically against any freight value
func (c *DefaultFeeCalculator) Calculate(freightKobo int64, policy CorridorFeePolicy) (SplitBreakdown, error) {
	if freightKobo <= 0 {
		return SplitBreakdown{}, errors.New("freight must be greater than zero")
	}

	var platformFee, agentCommission, unionDues int64

	switch policy.Model {
	case PricingModelProportional:
		platformFee = (freightKobo * policy.PlatformBasisPoints) / 10000
		agentCommission = (freightKobo * policy.AgentBasisPoints) / 10000
		unionDues = (freightKobo * policy.UnionBasisPoints) / 10000

	case PricingModelDynamicFloor:
		calculatedPlatform := (freightKobo * policy.PlatformBasisPoints) / 10000
		if calculatedPlatform > policy.PlatformFloorKobo {
			platformFee = calculatedPlatform
		} else {
			platformFee = policy.PlatformFloorKobo
		}

		if policy.AgentFixedKobo > 0 {
			agentCommission = policy.AgentFixedKobo
		} else {
			agentCommission = (freightKobo * policy.AgentBasisPoints) / 10000
		}

		if policy.UnionFixedKobo > 0 {
			unionDues = policy.UnionFixedKobo
		} else {
			unionDues = (freightKobo * policy.UnionBasisPoints) / 10000
		}

	default:
		return SplitBreakdown{}, fmt.Errorf("unsupported pricing model: %s", policy.Model)
	}

	totalDeductions := platformFee + agentCommission + unionDues
	if totalDeductions >= freightKobo {
		return SplitBreakdown{}, ErrInvalidFeePolicy
	}

	driverPayout := freightKobo - totalDeductions

	return SplitBreakdown{
		DriverPayoutKobo:     driverPayout,
		AgentCommissionKobo:  agentCommission,
		UnionDuesKobo:        unionDues,
		PlatformTreasuryKobo: platformFee,
		TotalFreightKobo:     freightKobo,
	}, nil
}

// Waybill entity matching the PostgreSQL ledger schema
type Waybill struct {
	ID                        string     `json:"id"`
	AgentID                   string     `json:"agent_id"`
	SenderPhone               string     `json:"sender_phone"`
	ReceiverPhone             string     `json:"receiver_phone"`
	DriverPhone               string     `json:"driver_phone"`
	VehicleRegNumber          string     `json:"vehicle_reg_number"`
	OriginParkID              string     `json:"origin_park_id"`
	DestinationParkID         string     `json:"destination_park_id"`
	TotalFreightKobo          int64      `json:"total_freight_kobo"`
	PinHash                   string     `json:"-"`
	FailedPinAttempts         int        `json:"failed_pin_attempts"`
	DriverBankCode            string     `json:"driver_bank_code"`
	DriverAccountNum          string     `json:"driver_account_number"`
	DriverVerifiedName        string     `json:"driver_verified_name"`
	CurrentState              string     `json:"current_state"`
	ReceiverRefusalReason     string     `json:"receiver_refusal_reason,omitempty"`
	ReceiverRefusalReportedAt *time.Time `json:"receiver_refusal_reported_at,omitempty"`
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
}

// Agent entity representing the pre-funded motor park clerk
type Agent struct {
	ID                string    `json:"id"`
	FullName          string    `json:"full_name"`
	PhoneNumber       string    `json:"phone_number"`
	ParkID            string    `json:"park_id"`
	PasswordHash      string    `json:"-"`
	WalletBalanceKobo int64     `json:"wallet_balance_kobo"`
	IsActive          bool      `json:"is_active"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// CanDisburse reports whether escrow payout is allowed for the current state.
func CanDisburse(currentState string) bool {
	return currentState == WaybillStateDelivered
}

// AssertCanDisburse returns ErrInvalidStateTransition unless state is DELIVERED.
func AssertCanDisburse(currentState string) error {
	if !CanDisburse(currentState) {
		return fmt.Errorf("%w: current state is %s", ErrInvalidStateTransition, currentState)
	}
	return nil
}

// CanCancel reports whether the agent may abort escrow and reclaim freight.
func CanCancel(currentState string) bool {
	return currentState == WaybillStateEscrowLocked
}

// AssertCanCancel returns ErrInvalidStateTransition unless state is ESCROW_LOCKED.
func AssertCanCancel(currentState string) error {
	if !CanCancel(currentState) {
		return fmt.Errorf("%w: current state is %s", ErrInvalidStateTransition, currentState)
	}
	return nil
}

// CanStartTransit reports whether a waybill may enter transit.
func CanStartTransit(currentState string) bool {
	return currentState == WaybillStateEscrowLocked
}

// CanReportReceiverRefusal reports whether a receiver refusal may be reported.
func CanReportReceiverRefusal(currentState string) bool {
	return currentState == WaybillStateInTransit
}

// HeldWaybillResolutionState returns the terminal state for a valid held-waybill resolution.
func HeldWaybillResolutionState(currentState, outcome string) (string, error) {
	if currentState != WaybillStateDisputeHold && currentState != WaybillStateUnclaimedHold {
		return "", fmt.Errorf("%w: cannot resolve waybill in %s", ErrInvalidStateTransition, currentState)
	}

	switch outcome {
	case ResolutionReleaseToDriver:
		return WaybillStateDisbursed, nil
	case ResolutionRefundToAgent:
		return WaybillStateRefunded, nil
	default:
		return "", fmt.Errorf("invalid waybill resolution outcome: %s", outcome)
	}
}
