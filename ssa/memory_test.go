package ssa

import "testing"

func TestAssertNilDerefZeroExprNoPanic(t *testing.T) {
	var b Builder
	b.AssertNilDeref(Expr{})
}

func TestGuardPanicLocationScope(t *testing.T) {
	b := &aBuilder{}
	var outer, inner int
	if previous := b.SetPanicLocation(func() { outer++ }); previous != nil {
		t.Fatal("new builder already has a panic-location emitter")
	}
	b.recordGuardPanicLocation()
	previous := b.SetPanicLocation(func() { inner++ })
	b.recordGuardPanicLocation()
	b.SetPanicLocation(previous)
	b.recordGuardPanicLocation()
	b.SetPanicLocation(nil)
	b.recordGuardPanicLocation()
	if outer != 2 || inner != 1 {
		t.Fatalf("location counts outer=%d, inner=%d, want 2 and 1", outer, inner)
	}
}
