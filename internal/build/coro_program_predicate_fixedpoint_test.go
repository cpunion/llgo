package build

import (
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
)

func TestCoroProgramPredicatesMonotoneFixedPoint(t *testing.T) {
	mask := coro.ProgramPredicateMask()
	current := coro.ProgramPredicateRequirements{Mask: mask}
	for i, discovered := range []coro.ProgramCapabilities{0, coro.ExecutionDomainProgramCapability(), mask, mask} {
		next, changed, err := advanceCoroProgramPredicates(current, discovered)
		if err != nil || next.Values != discovered || changed != (i == 1 || i == 2) {
			t.Fatalf("iteration %d: next=%+v changed=%v error=%v", i, next, changed, err)
		}
		current = next
	}
	for _, discovered := range []coro.ProgramCapabilities{0, coro.GCObservationProgramCapability(), coro.ExecutionDomainProgramCapability()} {
		if _, _, err := advanceCoroProgramPredicates(current, discovered); err == nil {
			t.Fatalf("accepted non-monotone demand %#x after %#x", discovered, mask)
		}
	}
}

func TestCoroProgramPredicatesRejectIncompleteDiscovery(t *testing.T) {
	for _, current := range []coro.ProgramPredicateRequirements{
		{}, {Mask: coro.GCObservationProgramCapability()},
		{Mask: coro.ExecutionDomainProgramCapability(), Values: coro.GCObservationProgramCapability()},
	} {
		if _, _, err := advanceCoroProgramPredicates(current, 0); err == nil {
			t.Fatalf("accepted incomplete predicate binding %+v", current)
		}
	}
	if _, _, err := advanceCoroProgramPredicates(coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask()}, coro.NativeDefaultFaultBoundaryProgramCapability()); err == nil {
		t.Fatal("accepted a physical capability as a program predicate")
	}
}
