package service

import (
	"errors"
	"testing"

	"github.com/nurtw/escrow/internal/resolver"
)

func TestAdminResolutionTransferDecision(t *testing.T) {
	tests := []struct {
		name          string
		transfer      resolver.PayoutTransfer
		wantReference string
		wantError     error
	}{
		{
			name:          "success",
			transfer:      resolver.PayoutTransfer{Reference: "PAY-1", Status: "success"},
			wantReference: "PAY-1",
		},
		{
			name:          "success without returned reference uses deterministic reference",
			transfer:      resolver.PayoutTransfer{Status: "success"},
			wantReference: "ADMIN_RESOLVE_WB-1",
		},
		{
			name:      "pending",
			transfer:  resolver.PayoutTransfer{Status: "pending"},
			wantError: ErrPayoutInProgress,
		},
		{
			name:      "processing",
			transfer:  resolver.PayoutTransfer{Status: "processing"},
			wantError: ErrPayoutInProgress,
		},
		{
			name:      "otp",
			transfer:  resolver.PayoutTransfer{Status: "otp"},
			wantError: ErrPayoutInProgress,
		},
		{
			name:      "failed",
			transfer:  resolver.PayoutTransfer{Status: "failed", HTTPStatus: 200, Message: "Bank rejected transfer"},
			wantError: ErrPayoutTransferFailed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reference, err := adminResolutionTransferReference(tc.transfer, "ADMIN_RESOLVE_WB-1")
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("adminResolutionTransferReference() error = %v, want %v", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("adminResolutionTransferReference() error = %v", err)
			}
			if reference != tc.wantReference {
				t.Errorf("adminResolutionTransferReference() = %q, want %q", reference, tc.wantReference)
			}
		})
	}
}

func TestAdminResolutionTransferDecisionPreservesPayoutError(t *testing.T) {
	payoutErr := &resolver.PayoutError{
		HTTPStatus: 400,
		Message:    "Duplicate transfer reference",
		Err:        resolver.ErrTransferFailed,
	}
	_, err := adminResolutionTransferReference(resolver.PayoutTransfer{}, "ADMIN_RESOLVE_WB-1")
	if !errors.Is(err, ErrPayoutTransferFailed) {
		t.Fatalf("adminResolutionTransferReference() error = %v, want payout failure", err)
	}

	_, err = adminResolutionTransferReference(resolver.PayoutTransfer{
		Status:     "failed",
		HTTPStatus: payoutErr.HTTPStatus,
		Message:    payoutErr.Message,
	}, "ADMIN_RESOLVE_WB-1")
	var gotPayoutErr *resolver.PayoutError
	if !errors.As(err, &gotPayoutErr) {
		t.Fatalf("adminResolutionTransferReference() error = %v, want Paystack error details", err)
	}
	if gotPayoutErr.HTTPStatus != 400 || gotPayoutErr.Message != "Duplicate transfer reference" {
		t.Fatalf("payout error details = %+v", gotPayoutErr)
	}
}
