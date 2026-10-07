package domain

import "testing"

func TestCanDisburse_OnlyDelivered(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{WaybillStateCreated, false},
		{WaybillStateEscrowLocked, false},
		{WaybillStateInTransit, false},
		{WaybillStateDelivered, true},
		{WaybillStateDisbursed, false},
		{WaybillStateDisputeHold, false},
		{WaybillStateUnclaimedHold, false},
		{WaybillStateCancelled, false},
	}
	for _, tc := range cases {
		if got := CanDisburse(tc.state); got != tc.want {
			t.Errorf("CanDisburse(%q) = %v, want %v", tc.state, got, tc.want)
		}
	}
}

func TestAssertCanDisburse(t *testing.T) {
	if err := AssertCanDisburse(WaybillStateDelivered); err != nil {
		t.Fatalf("DELIVERED should allow disburse: %v", err)
	}
	if err := AssertCanDisburse(WaybillStateEscrowLocked); err == nil {
		t.Fatal("ESCROW_LOCKED should reject disburse")
	}
}

func TestCanCancel_OnlyEscrowLocked(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{WaybillStateCreated, false},
		{WaybillStateEscrowLocked, true},
		{WaybillStateInTransit, false},
		{WaybillStateDelivered, false},
		{WaybillStateDisbursed, false},
		{WaybillStateDisputeHold, false},
		{WaybillStateUnclaimedHold, false},
		{WaybillStateCancelled, false},
	}
	for _, tc := range cases {
		if got := CanCancel(tc.state); got != tc.want {
			t.Errorf("CanCancel(%q) = %v, want %v", tc.state, got, tc.want)
		}
	}
}

func TestAssertCanCancel(t *testing.T) {
	if err := AssertCanCancel(WaybillStateEscrowLocked); err != nil {
		t.Fatalf("ESCROW_LOCKED should allow cancel: %v", err)
	}
	if err := AssertCanCancel(WaybillStateDelivered); err == nil {
		t.Fatal("DELIVERED should reject cancel")
	}
}