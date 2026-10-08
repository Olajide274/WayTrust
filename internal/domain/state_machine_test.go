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

func TestCanStartTransit_OnlyEscrowLocked(t *testing.T) {
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
		if got := CanStartTransit(tc.state); got != tc.want {
			t.Errorf("CanStartTransit(%q) = %v, want %v", tc.state, got, tc.want)
		}
	}
}

func TestCanReportReceiverRefusal_OnlyInTransit(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{WaybillStateCreated, false},
		{WaybillStateEscrowLocked, false},
		{WaybillStateInTransit, true},
		{WaybillStateDelivered, false},
		{WaybillStateDisbursed, false},
		{WaybillStateDisputeHold, false},
		{WaybillStateUnclaimedHold, false},
		{WaybillStateCancelled, false},
	}
	for _, tc := range cases {
		if got := CanReportReceiverRefusal(tc.state); got != tc.want {
			t.Errorf("CanReportReceiverRefusal(%q) = %v, want %v", tc.state, got, tc.want)
		}
	}
}

func TestHeldWaybillResolutionState(t *testing.T) {
	heldStates := []string{WaybillStateDisputeHold, WaybillStateUnclaimedHold}
	outcomes := []struct {
		outcome string
		state   string
	}{
		{ResolutionReleaseToDriver, WaybillStateDisbursed},
		{ResolutionRefundToAgent, WaybillStateRefunded},
	}

	for _, heldState := range heldStates {
		for _, tc := range outcomes {
			got, err := HeldWaybillResolutionState(heldState, tc.outcome)
			if err != nil {
				t.Errorf("HeldWaybillResolutionState(%q, %q) returned error: %v", heldState, tc.outcome, err)
			} else if got != tc.state {
				t.Errorf("HeldWaybillResolutionState(%q, %q) = %q, want %q", heldState, tc.outcome, got, tc.state)
			}
		}
	}

	for _, state := range []string{
		WaybillStateCreated,
		WaybillStateEscrowLocked,
		WaybillStateInTransit,
		WaybillStateDelivered,
		WaybillStateDisbursed,
		WaybillStateRefunded,
		WaybillStateCancelled,
	} {
		for _, outcome := range outcomes {
			if _, err := HeldWaybillResolutionState(state, outcome.outcome); err == nil {
				t.Errorf("HeldWaybillResolutionState(%q, %q) should reject non-held state", state, outcome.outcome)
			}
		}
	}

	if _, err := HeldWaybillResolutionState(WaybillStateDisputeHold, "UNKNOWN"); err == nil {
		t.Fatal("unknown resolution outcome should be rejected")
	}
}
