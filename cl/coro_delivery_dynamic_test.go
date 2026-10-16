//go:build !llgo

package cl

import (
	"go/ast"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroDeliveryDynamicHelpersUseTheExistingEffectGraph(t *testing.T) {
	source := newEmissionTestProgram()
	pkg := source.addPackage(t, "example.com/delivery", `package delivery
func Owner(f func() int) int { return f() }
func Other(f func() int) int { return f() }
func Target() int { return 1 }
func Zero(f func()) { f() }
func Spawn(f func() int) { go f() }
`)
	source.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	u, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	owner := pkg.ssa.Func("Owner")
	builder := coroProgramIRBuilder{canonical: emissionCanonicalIndex{universe: u}}
	callFor := func(name string) ssa.CallInstruction {
		t.Helper()
		for _, block := range pkg.ssa.Func(name).Blocks {
			for _, instruction := range block.Instrs {
				if call, ok := instruction.(ssa.CallInstruction); ok {
					return call
				}
			}
		}
		t.Fatal("fixture has no call")
		return nil
	}
	call := callFor("Owner")
	for _, name := range []string{"delivery.initialize", "delivery.drop"} {
		if err := builder.freezeDeliveryHelperCall(owner, name, call); err != nil {
			t.Fatal(err)
		}
		if err := builder.freezeDeliveryHelperCall(owner, name, call); err != nil {
			t.Fatalf("idempotent conditional helper: %v", err)
		}
	}
	calls, err := u.CoroLoweredCalls(owner)
	if err != nil || len(calls) != 2 || calls[0].LogicalName != "delivery.drop" {
		t.Fatalf("frozen conditional calls=%+v: %v", calls, err)
	}
	for _, record := range calls {
		if !record.ManagedDispatch || record.Target != nil || record.WhenResultDeliveryOf != call {
			t.Fatalf("dynamic helper invented an exact target: %+v", record)
		}
	}
	calls[0].WhenResultDeliveryOf = nil
	if record, ok, err := u.ResolveCoroLoweredCallRecord(owner, "delivery.drop"); err != nil || !ok || record.WhenResultDeliveryOf != call {
		t.Fatalf("frozen helper exposed mutable backing storage: %+v %t %v", record, ok, err)
	}
	if target, exact, err := u.ResolveCoroLoweredCall(owner, "delivery.drop"); err != nil || exact || target != nil {
		t.Fatalf("dynamic helper resolved as an exact target: %v %t %v", target, exact, err)
	}
	universe, err := coro.NewSSAEmissionUniverse(source.ssa, u.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(source.ssa, coro.Roots{{Function: owner, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse: universe, FunctionIDs: u.FunctionIDConfig(), OutcomeMode: coro.OutcomeExplicitStatus,
		ClassifyLocalBody: u.CoroLocalBodyFacts, ClassifyLoweredCalls: u.CoroLoweredCalls,
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
			return coro.UnknownManagedDispatch, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fact, ok := plan.FunctionPlan(owner); !ok || fact.Emission != coro.EmitCoroutine || !fact.Exec.Contains(coro.NeedsCleanupFrame) {
		t.Fatalf("hidden managed calls did not color their real owner: %+v %t", fact, ok)
	}
	if err := u.recordCoroLoweredCall(owner, "delivery.drop", pkg.ssa.Func("Target")); err == nil || !strings.Contains(err.Error(), "mixes exact") {
		t.Fatalf("mixed invocation domain accepted: %v", err)
	}
	for _, name := range []string{"Other", "Zero", "Spawn"} {
		candidateOwner := pkg.ssa.Func(name)
		if name == "Other" {
			candidateOwner = owner
		}
		if err := builder.freezeDeliveryHelperCall(candidateOwner, "invalid."+name, callFor(name)); err == nil {
			t.Fatalf("%s accepted as an exact result-owning occurrence", name)
		}
	}
}
