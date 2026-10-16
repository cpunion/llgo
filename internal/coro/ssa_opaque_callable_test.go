//go:build !llgo

package coro

import (
	"testing"

	"golang.org/x/tools/go/ssa"
)

// A matching source literal does not enumerate descriptors manufactured by
// reflection/unsafe or supplied by an external producer. CHA must retain the
// open invocation domain, including when the value reaches a parameter first.
func TestSSAClosedCHARequiresCompleteCallableDomain(t *testing.T) {
	for _, producer := range []struct{ name, body string }{
		{"assertion", "return value.(func() *byte)"},
		{"comma-ok-assertion", "fn, _ := value.(func() *byte); return fn"},
		{"struct-assertion", "box := value.(struct{ Callback func() *byte }); return box.Callback"},
		{"slice-assertion", "return value.([]func() *byte)[0]"},
		{"map-assertion", "return value.(map[int]func() *byte)[0]"},
		{"channel-assertion", "return <-value.(chan func() *byte)"},
		{"external-result", "return external()"},
		{"unsafe-load", "return *(*func() *byte)(pointer)"},
	} {
		t.Run(producer.name, func(t *testing.T) {
			prog, pkg := buildCoroTestSSA(t, "opaque.go", `package coroid
import "unsafe"
var pointer unsafe.Pointer
var published = func() *byte { return nil }
func other() *byte { return nil }
func external() func() *byte
func produce(value any) func() *byte { `+producer.body+` }
func apply(fn func() *byte) *byte { return fn() }
func root(value any) *byte { return apply(produce(value)) }
func exact(second bool) *byte {
 fn := other; if second { fn = func() *byte { return nil } }; return fn()
}
`)
			apply := packageFunction(t, pkg, "apply")
			exact := packageFunction(t, pkg, "exact")
			plan, err := AnalyzeSSA(prog, Roots{
				{Function: packageFunction(t, pkg, "root"), Demand: AsyncDemand},
				{Function: exact, Demand: AsyncDemand},
			}, SSAConfig{
				DynamicResolution: DynamicCHAClosed, OutcomeMode: OutcomeExplicitStatus,
				MaxPlainInstructions: -1,
				ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (UnknownTarget, error) {
					if call.Common().StaticCallee() == nil {
						return UnknownManagedDispatch, nil
					}
					return UnknownManaged, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			call := onlyNonBuiltinCall(t, apply)
			fact, found := plan.CallPlan(call)
			if !found || !fact.Open || fact.Unresolved != UnknownManagedDispatch || len(fact.Targets) == 0 {
				t.Fatalf("opaque runtime descriptor was closed by source-only CHA: %+v present=%t", fact, found)
			}
			if owner := functionPlanFor(t, plan, apply); !owner.Effect.Contains(AwaitStructured) || owner.StaticOutcome {
				t.Fatalf("opaque descriptor lost its universal coroutine path: %+v", owner)
			}
			exactCall := onlyNonBuiltinCall(t, exact)
			if exactFact, found := plan.CallPlan(exactCall); !found || exactFact.Open || len(exactFact.Targets) != 2 {
				t.Fatalf("opaque sibling destroyed exact local scalar-flow closure: %+v present=%t", exactFact, found)
			}
		})
	}
}
