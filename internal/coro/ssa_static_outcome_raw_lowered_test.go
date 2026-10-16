//go:build !llgo

package coro

import (
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSAStaticOutcomeConsumesRawLoweredClosure(t *testing.T) {
	for _, test := range []struct {
		name     string
		raw      bool
		external bool
		known    bool
		yield    bool
		local    bool
		want     bool
	}{
		{name: "exact raw occurrence", raw: true, local: true, want: true},
		{name: "ordinary managed occurrence", local: true},
		{name: "unproved external raw body", raw: true, external: true, local: true},
		{name: "imported summary is not an owned raw body", raw: true, external: true, known: true, local: true},
		{name: "owner still yields", raw: true, local: true, yield: true},
		{name: "owner local recipe is not synchronous", raw: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			helperSource := "func helper() int { return 7 }"
			if test.external {
				helperSource = "func helper() int"
			}
			prog, pkg := buildCoroTestSSA(t, "static_raw_lowered.go", `package coroid
func leaf(value *int) int { return *value }
func caller(value *int) int { return leaf(value) }
`+helperSource)
			leaf := packageFunction(t, pkg, "leaf")
			caller := packageFunction(t, pkg, "caller")
			helper := packageFunction(t, pkg, "helper")
			universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{caller, leaf, helper})
			if err != nil {
				t.Fatal(err)
			}
			config := planDigestSSAConfig()
			config.EmissionUniverse = universe
			config.OutcomeMode = OutcomeExplicitStatus
			config.MaxPlainInstructions = 128
			config.ClassifyLocalBody = func(fn *ssa.Function) (SSAFunctionBodyFacts, error) {
				facts := scanSSAFunctionBody(fn)
				facts.StaticOutcomeLocal = test.local
				return facts, nil
			}
			config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn == helper {
					if test.known {
						return SSAFunctionPolicy{External: ExternalKnown}, nil
					}
					// The helper's managed representation remains independently
					// suspending. Only this exact raw occurrence is synchronous.
					return SSAFunctionPolicy{Effect: YieldOnly}, nil
				}
				if fn == leaf && test.yield {
					return SSAFunctionPolicy{Effect: YieldOnly}, nil
				}
				return SSAFunctionPolicy{}, nil
			}
			config.ClassifyLoweredCalls = func(fn *ssa.Function) ([]SSALoweredCall, error) {
				if fn == leaf {
					return []SSALoweredCall{{LogicalName: "compiler.operation", Target: helper, RawPlain: test.raw}}, nil
				}
				return nil, nil
			}
			plan, err := AnalyzeSSA(prog, Roots{{Function: caller, ManagedDemand: AsyncDemand}}, config)
			if err != nil {
				t.Fatal(err)
			}
			got := functionPlanFor(t, plan, leaf)
			if got.StaticOutcome != test.want {
				t.Fatalf("leaf = %+v, want static outcome %t", got, test.want)
			}
			if got.AtomicCostProof != AtomicCostUnproven || got.AtomicCost != 0 || got.AtomicCostCertificate != "" {
				t.Fatalf("raw closure manufactured an atomic cost proof: %+v", got)
			}
			if test.want {
				if got.Emission != EmitOutcomePlain {
					t.Fatalf("exact static leaf kept a redundant coroutine primary: %+v", got)
				}
				call, found := plan.ResolveLoweredCallRecord(leaf, "compiler.operation")
				if !found || !plan.HasRawPlainLoweredTarget(call) {
					t.Fatal("promotion lost the existing raw-closure capability")
				}
				call.RawPlain = false
				if plan.HasRawPlainLoweredTarget(call) {
					t.Fatal("ordinary managed occurrence acquired a raw capability")
				}
			}
		})
	}
}

func TestAnalyzeSSAStaticOutcomeDoesNotAdmitSourceRawCalls(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "static_source_raw.go", `package coroid
func helper() {}
func leaf(value *int) int { helper(); return *value }
func caller(value *int) int { return leaf(value) }
`)
	leaf, helper := packageFunction(t, pkg, "leaf"), packageFunction(t, pkg, "helper")
	call := onlyNonBuiltinCall(t, leaf)
	config := planDigestSSAConfig()
	config.OutcomeMode = OutcomeExplicitStatus
	config.MaxPlainInstructions = -1
	config.ClassifyLocalBody = func(fn *ssa.Function) (SSAFunctionBodyFacts, error) {
		facts := scanSSAFunctionBody(fn)
		facts.StaticOutcomeLocal = true
		return facts, nil
	}
	config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		if fn == helper {
			return SSAFunctionPolicy{TrustedNoPreempt: true, TrustedNoUnwind: true}, nil
		}
		return SSAFunctionPolicy{}, nil
	}
	config.ClassifyRawPlainCall = func(_ *ssa.Function, candidate ssa.CallInstruction) (SSARawPlainCallCertificate, bool, error) {
		if candidate == call {
			return SSARawPlainCallCertificate{ID: "test.source.raw"}, true, nil
		}
		return SSARawPlainCallCertificate{}, false, nil
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: packageFunction(t, pkg, "caller"), ManagedDemand: AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, leaf); got.StaticOutcome || got.Emission != EmitCoroutine {
		t.Fatalf("source raw call was confused with a compiler-owned lowered occurrence: %+v", got)
	}
}
