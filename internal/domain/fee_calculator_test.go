package domain

import "testing"

func TestCalculate_Proportional_KadunaAbujaStyle(t *testing.T) {
	calc := NewFeeCalculator()
	policy := CorridorFeePolicy{
		Model:               PricingModelProportional,
		PlatformBasisPoints: 2000, // 20%
		AgentBasisPoints:    830,  // 8.3%
		UnionBasisPoints:    500,  // 5%
	}

	freight := int64(500000) // ₦5,000
	split, err := calc.Calculate(freight, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if split.PlatformTreasuryKobo != 100000 {
		t.Errorf("platform: got %d want 100000", split.PlatformTreasuryKobo)
	}
	if split.AgentCommissionKobo != 41500 {
		t.Errorf("agent: got %d want 41500", split.AgentCommissionKobo)
	}
	if split.UnionDuesKobo != 25000 {
		t.Errorf("union: got %d want 25000", split.UnionDuesKobo)
	}
	if split.DriverPayoutKobo != 333500 {
		t.Errorf("driver: got %d want 333500", split.DriverPayoutKobo)
	}

	sum := split.DriverPayoutKobo + split.AgentCommissionKobo + split.UnionDuesKobo + split.PlatformTreasuryKobo
	if sum != freight {
		t.Errorf("split sum %d != freight %d", sum, freight)
	}
}

func TestCalculate_RejectsZeroFreight(t *testing.T) {
	calc := NewFeeCalculator()
	_, err := calc.Calculate(0, CorridorFeePolicy{Model: PricingModelProportional})
	if err == nil {
		t.Fatal("expected error for zero freight")
	}
}

func TestCalculate_RejectsDeductionsExceedingFreight(t *testing.T) {
	calc := NewFeeCalculator()
	policy := CorridorFeePolicy{
		Model:               PricingModelProportional,
		PlatformBasisPoints: 5000,
		AgentBasisPoints:    3000,
		UnionBasisPoints:    3000, // 110% total
	}
	_, err := calc.Calculate(100000, policy)
	if err == nil {
		t.Fatal("expected ErrInvalidFeePolicy")
	}
}