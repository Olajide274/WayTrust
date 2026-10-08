package service

import (
	"context"
	"errors"
	"testing"

	"github.com/nurtw/escrow/internal/domain"
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
			reference, err := payoutTransferReference(tc.transfer, "ADMIN_RESOLVE_WB-1")
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("payoutTransferReference() error = %v, want %v", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("payoutTransferReference() error = %v", err)
			}
			if reference != tc.wantReference {
				t.Errorf("payoutTransferReference() = %q, want %q", reference, tc.wantReference)
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
	_, err := payoutTransferReference(resolver.PayoutTransfer{}, "ADMIN_RESOLVE_WB-1")
	if !errors.Is(err, ErrPayoutTransferFailed) {
		t.Fatalf("payoutTransferReference() error = %v, want payout failure", err)
	}

	_, err = payoutTransferReference(resolver.PayoutTransfer{
		Status:     "failed",
		HTTPStatus: payoutErr.HTTPStatus,
		Message:    payoutErr.Message,
	}, "ADMIN_RESOLVE_WB-1")
	var gotPayoutErr *resolver.PayoutError
	if !errors.As(err, &gotPayoutErr) {
		t.Fatalf("payoutTransferReference() error = %v, want Paystack error details", err)
	}
	if gotPayoutErr.HTTPStatus != 400 || gotPayoutErr.Message != "Duplicate transfer reference" {
		t.Fatalf("payout error details = %+v", gotPayoutErr)
	}
}

type fakePayoutClient struct {
	initiate func(context.Context, string, string, string, int64, string) (resolver.PayoutTransfer, error)
	verify   func(context.Context, string) (resolver.PayoutTransfer, error)
}

func (f *fakePayoutClient) InitiateDriverPayout(
	ctx context.Context, accountName, accountNumber, bankCode string, amountKobo int64, reference string,
) (resolver.PayoutTransfer, error) {
	return f.initiate(ctx, accountName, accountNumber, bankCode, amountKobo, reference)
}

func (f *fakePayoutClient) VerifyTransfer(ctx context.Context, reference string) (resolver.PayoutTransfer, error) {
	return f.verify(ctx, reference)
}

func TestDisburseAndSettleRequiresSuccessfulTransfer(t *testing.T) {
	tests := []struct {
		name      string
		transfer  resolver.PayoutTransfer
		wantError error
	}{
		{name: "success", transfer: resolver.PayoutTransfer{Reference: "DELIVERY-WB-1", Status: "success"}},
		{name: "pending", transfer: resolver.PayoutTransfer{Reference: "DELIVERY-WB-1", Status: "pending"}, wantError: ErrPayoutInProgress},
		{name: "otp", transfer: resolver.PayoutTransfer{Reference: "DELIVERY-WB-1", Status: "otp"}, wantError: ErrPayoutInProgress},
		{name: "failed", transfer: resolver.PayoutTransfer{Reference: "DELIVERY-WB-1", Status: "failed"}, wantError: ErrPayoutTransferFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakePayoutClient{
				initiate: func(context.Context, string, string, string, int64, string) (resolver.PayoutTransfer, error) {
					return tc.transfer, nil
				},
			}
			settlements := 0
			reference, _, err := disburseAndSettle(
				context.Background(), client, "Driver", "0123456789", "001", 1000, "DELIVERY-WB-1",
				func(gotReference string) (*domain.SplitBreakdown, error) {
					settlements++
					if gotReference != "DELIVERY-WB-1" {
						t.Errorf("settlement reference = %q, want deterministic reference", gotReference)
					}
					return &domain.SplitBreakdown{DriverPayoutKobo: 1000}, nil
				},
			)
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("disburseAndSettle() error = %v, want %v", err, tc.wantError)
				}
				if settlements != 0 {
					t.Fatalf("settlements = %d, want 0 for %s transfer", settlements, tc.transfer.Status)
				}
				return
			}
			if err != nil {
				t.Fatalf("disburseAndSettle() error = %v", err)
			}
			if reference != "DELIVERY-WB-1" || settlements != 1 {
				t.Fatalf("reference = %q, settlements = %d; want DELIVERY-WB-1 and 1", reference, settlements)
			}
		})
	}
}

func TestDeliveryPayoutRetryVerifiesSameReferenceBeforeSettlement(t *testing.T) {
	const reference = "DELIVERY-WB-1"
	initiateCalls, verifyCalls, createdTransfers := 0, 0, 0
	var client *fakePayoutClient
	client = &fakePayoutClient{
		initiate: func(
			ctx context.Context, _, _, _ string, _ int64, gotReference string,
		) (resolver.PayoutTransfer, error) {
			initiateCalls++
			if gotReference != reference {
				t.Errorf("retry reference = %q, want %q", gotReference, reference)
			}
			if initiateCalls == 1 {
				createdTransfers++
				return resolver.PayoutTransfer{Reference: reference, Status: "pending"}, nil
			}
			return client.VerifyTransfer(ctx, gotReference)
		},
		verify: func(_ context.Context, gotReference string) (resolver.PayoutTransfer, error) {
			verifyCalls++
			if gotReference != reference {
				t.Errorf("verification reference = %q, want %q", gotReference, reference)
			}
			return resolver.PayoutTransfer{Reference: reference, Status: "success"}, nil
		},
	}

	settlements := 0
	settle := func(string) (*domain.SplitBreakdown, error) {
		settlements++
		return &domain.SplitBreakdown{DriverPayoutKobo: 1000}, nil
	}
	_, _, err := disburseAndSettle(
		context.Background(), client, "Driver", "0123456789", "001", 1000, reference, settle,
	)
	if !errors.Is(err, ErrPayoutInProgress) {
		t.Fatalf("first disburseAndSettle() error = %v, want payout in progress", err)
	}
	if settlements != 0 {
		t.Fatalf("settlements after pending response = %d, want 0", settlements)
	}

	retryReference, _, err := disburseAndSettle(
		context.Background(), client, "Driver", "0123456789", "001", 1000, reference, settle,
	)
	if err != nil {
		t.Fatalf("retry disburseAndSettle() error = %v", err)
	}
	if retryReference != reference || initiateCalls != 2 || verifyCalls != 1 || createdTransfers != 1 || settlements != 1 {
		t.Fatalf(
			"retry reference=%q initiate calls=%d verify calls=%d created transfers=%d settlements=%d",
			retryReference, initiateCalls, verifyCalls, createdTransfers, settlements,
		)
	}
}
