package coro

import "testing"

func TestProgramPredicateRequirementsBindFalseAndTrue(t *testing.T) {
	for _, bit := range []ProgramCapabilities{ExecutionDomainProgramCapability(), GCObservationProgramCapability(), ProgramPredicateMask()} {
		testProgramPredicateRequirementsBindFalseAndTrue(t, bit)
	}
}

func testProgramPredicateRequirementsBindFalseAndTrue(t *testing.T, bit ProgramCapabilities) {
	for _, want := range []ProgramCapabilities{0, bit} {
		requirements := ProgramPredicateRequirements{Mask: bit, Values: want}
		if !requirements.Valid() || !requirements.Compatible(want) || requirements.Compatible(want^bit) {
			t.Fatalf("predicate requirements lost exact truth value: %+v", requirements)
		}
		if !requirements.Compatible(want | NewProgramCapabilities(true, true)) {
			t.Fatal("unconsumed worker/fault capability changed specialization compatibility")
		}
		joined, err := (ProgramPredicateRequirements{}).Join(requirements)
		if err != nil || joined != requirements {
			t.Fatalf("identity join = %+v, %v", joined, err)
		}
	}
	if _, err := (ProgramPredicateRequirements{Mask: bit}).Join(ProgramPredicateRequirements{Mask: bit, Values: bit}); err == nil {
		t.Fatal("contradictory producer predicates were accepted")
	}
	if !(ProgramPredicateRequirements{}).Compatible(bit) {
		t.Fatal("unspecialized entry cannot compose with a domain-enabled consumer")
	}
}

func TestProgramPredicateRequirementsJoinIndependentServices(t *testing.T) {
	for _, executionDomain := range []ProgramCapabilities{0, ExecutionDomainProgramCapability()} {
		for _, observation := range []ProgramCapabilities{0, GCObservationProgramCapability()} {
			left := ProgramPredicateRequirements{Mask: ExecutionDomainProgramCapability(), Values: executionDomain}
			right := ProgramPredicateRequirements{Mask: GCObservationProgramCapability(), Values: observation}
			joined, err := left.Join(right)
			if err != nil || joined.Mask != ProgramPredicateMask() || !joined.Compatible(executionDomain|observation) ||
				joined.Compatible((executionDomain|observation)^GCObservationProgramCapability()) {
				t.Fatalf("independent predicate join = %+v, %v", joined, err)
			}
		}
	}
}

func TestProgramPredicateRequirementsRejectUnknownOrUnconsumedValues(t *testing.T) {
	for _, requirements := range []ProgramPredicateRequirements{
		{Values: ExecutionDomainProgramCapability()},
		{Mask: NewProgramCapabilities(true, false)},
		{Mask: ProgramCapabilities(0x80)},
	} {
		if requirements.Valid() || requirements.Compatible(0) {
			t.Fatalf("invalid requirements accepted: %+v", requirements)
		}
		if _, err := requirements.Join(ProgramPredicateRequirements{}); err == nil {
			t.Fatalf("invalid requirements joined: %+v", requirements)
		}
	}
}
