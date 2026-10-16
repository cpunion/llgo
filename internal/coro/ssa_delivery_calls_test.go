package coro

import (
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

const deliveryCallFlowSource = `package coroid
func resource() int { return 1 }
func ordinary() int { return 2 }
func resourceOwner(fn func() int) int { return fn() }
func plainOwner(fn func() int) int { return fn() }
func openOwner(fn func() int) int { return fn() }
func mixedOwner(flag bool) int {
    fn := ordinary
    if flag { fn = resource }
    return fn()
}
func nilOwner() int { var fn func() int; return fn() }
func deferOwner(fn func() int) { defer fn() }
func zeroOwner(fn func()) { fn() }
func spawnOwner(fn func() int) { go fn() }
`

func analyzeDeliveryCallFlow(t *testing.T, mode ssa.BuilderMode, withoutHelpers ...bool) (*SSAPlan, map[string]*ssa.Function) {
	t.Helper()
	prog, pkg := buildCoroTestSSAWithMode(t, "delivery_calls.go", deliveryCallFlowSource, mode)
	functions := make(map[string]*ssa.Function)
	var inventory []*ssa.Function
	for _, name := range []string{"resource", "ordinary", "resourceOwner", "plainOwner", "openOwner", "mixedOwner", "nilOwner", "deferOwner", "zeroOwner", "spawnOwner"} {
		functions[name] = packageFunction(t, pkg, name)
		inventory = append(inventory, functions[name])
	}
	universe, err := NewSSAEmissionUniverse(prog, inventory)
	if err != nil {
		t.Fatal(err)
	}
	owners := make(map[*ssa.Function]bool)
	var roots Roots
	for _, name := range []string{"resourceOwner", "plainOwner", "openOwner", "mixedOwner", "nilOwner", "deferOwner"} {
		owners[functions[name]] = true
		roots = append(roots, Root{Function: functions[name], Demand: AsyncDemand, EmissionEntry: true})
	}
	classifications := make(map[*ssa.Function]int)
	config := SSAConfig{
		EmissionUniverse: universe, OutcomeMode: OutcomeExplicitStatus, MaxPlainInstructions: -1,
		FunctionIDs: planDigestSSAConfig().FunctionIDs,
		ClassifyFunction: func(function *ssa.Function) (SSAFunctionPolicy, error) {
			if function == functions["resource"] {
				return SSAFunctionPolicy{ResultDelivery: true, Exec: NeedsCleanupFrame}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
		ClassifyClosedDynamicCall: func(function *ssa.Function, call ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
			switch function {
			case functions["resourceOwner"]:
				return SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{functions["resource"]}}, true, nil
			case functions["plainOwner"]:
				return SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{functions["ordinary"]}}, true, nil
			}
			return SSAClosedDynamicCallCertificate{}, false, nil
		},
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (UnknownTarget, error) { return UnknownManagedDispatch, nil },
		ClassifyLoweredCalls: func(function *ssa.Function) ([]SSALoweredCall, error) {
			classifications[function]++
			if !owners[function] {
				return nil, nil
			}
			call := onlyNonBuiltinCall(t, function)
			return []SSALoweredCall{
				{LogicalName: "delivery.drop", ManagedDispatch: true, WhenResultDeliveryOf: call},
				{LogicalName: "delivery.initialize", ManagedDispatch: true, WhenResultDeliveryOf: call},
			}, nil
		},
	}
	if len(withoutHelpers) != 0 && withoutHelpers[0] {
		config.ClassifyLoweredCalls = nil
	}
	plan, err := AnalyzeSSA(prog, roots, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.functions) != len(inventory) {
		t.Fatal("hidden descriptor calls invented SSA functions")
	}
	for function := range owners {
		if config.ClassifyLoweredCalls != nil && classifications[function] != 1 {
			t.Fatalf("%s hidden calls classified %d times, want one freeze", function, classifications[function])
		}
	}
	return plan, functions
}

func TestAnalyzeSSAConditionalDeliveryCallsReuseFlowBeforeFixedPoint(t *testing.T) {
	plan, functions := analyzeDeliveryCallFlow(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	baseline, baselineFunctions := analyzeDeliveryCallFlow(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics, true)
	for _, name := range []string{"resourceOwner", "plainOwner", "openOwner", "mixedOwner", "nilOwner", "deferOwner"} {
		function := functions[name]
		call := onlyNonBuiltinCall(t, function)
		want := name != "plainOwner" && name != "nilOwner"
		callPlan, found := plan.CallPlan(call)
		if !found || callPlan.ResultDelivery != want {
			t.Fatalf("%s result custody=%t, want %t: %+v", name, callPlan.ResultDelivery, want, callPlan)
		}
		body := functionPlanFor(t, plan, function)
		if body.Exec.Contains(NeedsCleanupFrame) != want {
			t.Fatalf("%s local cleanup=%s, want resource cleanup %t", name, body.Exec, want)
		}
		calls := plan.LoweredCalls(function)
		if !want {
			if len(calls) != 0 || body != functionPlanFor(t, baseline, baselineFunctions[name]) {
				t.Fatalf("closed nonresource %s gained helper effects/storage: %+v / %+v", name, calls, body)
			}
			continue
		}
		if len(calls) != 2 || !body.Effect.Contains(AwaitStructured) || body.Effect&^(AwaitStructured|OutcomeStructured) != 0 || body.Emission != EmitCoroutine {
			t.Fatalf("%s did not color from its frozen managed helpers: %+v / %+v", name, calls, body)
		}
		if target, exact := plan.ResolveLoweredCall(function, "delivery.drop"); exact || target != nil {
			t.Fatal("a dynamic helper was misrepresented as an exact SSA target")
		}
		calls[0].ManagedDispatch = false
		if record, _ := plan.ResolveLoweredCallRecord(function, "delivery.drop"); !record.ManagedDispatch {
			t.Fatal("lowered-call lookup leaked mutable storage")
		}
	}
	if _, err := plan.CoroPlanDigest(validPlanDigestMetadata()); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeSSADeliveryCallDigestIsDebugIndependentAndBindsCondition(t *testing.T) {
	plain, functions := analyzeDeliveryCallFlow(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	debug, _ := analyzeDeliveryCallFlow(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics|ssa.GlobalDebug)
	first, err := plain.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	second, err := debug.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil || first != second {
		t.Fatalf("delivery digest changed with debug references: %s / %s: %v", first, second, err)
	}
	owner := functions["resourceOwner"]
	plain.loweredCalls[owner][0].WhenResultDeliveryOf = onlyNonBuiltinCall(t, functions["plainOwner"])
	if _, err := plain.CoroPlanDigest(validPlanDigestMetadata()); err == nil || !strings.Contains(err.Error(), "owner-local") {
		t.Fatalf("cross-owner helper condition escaped digest validation: %v", err)
	}
}

func TestAnalyzeSSADeliveryCallDigestRejectsLostProducerOrHelpers(t *testing.T) {
	for _, mutation := range []string{"producer", "helpers"} {
		t.Run(mutation, func(t *testing.T) {
			plan, functions := analyzeDeliveryCallFlow(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
			if mutation == "producer" {
				delete(plan.resultDeliveryProducers, functions["resource"])
			} else {
				delete(plan.loweredCalls, functions["resourceOwner"])
			}
			if _, err := plan.CoroPlanDigest(validPlanDigestMetadata()); err == nil {
				t.Fatal("result-delivery custody survived a missing producer/helper contract")
			}
		})
	}
}

func TestAnalyzeSSADeliveryCallConditionRequiresExactInvocation(t *testing.T) {
	_, functions := analyzeDeliveryCallFlow(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	for _, name := range []string{"zeroOwner", "spawnOwner"} {
		if err := validateSSADeliveryCallCondition(functions[name], onlyNonBuiltinCall(t, functions[name])); err == nil {
			t.Fatalf("%s accepted without a result-owning local invocation", name)
		}
	}
	call := onlyNonBuiltinCall(t, functions["deferOwner"]).(*ssa.Defer)
	call.DeferStack = ssa.NewConst(nil, types.Typ[types.UnsafePointer])
	if err := validateSSADeliveryCallCondition(functions["deferOwner"], call); err == nil {
		t.Fatal("unbound external defer owner was accepted")
	}
}

func TestAnalyzeSSAManagedLoweredCallHasNoSyntheticTarget(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "dynamic_helper.go", "package coroid; func owner() {}; func target() {}")
	owner, target := packageFunction(t, pkg, "owner"), packageFunction(t, pkg, "target")
	for _, test := range []struct {
		name   string
		record SSALoweredCall
		valid  bool
	}{
		{"managed", SSALoweredCall{LogicalName: "callback", ManagedDispatch: true}, true},
		{"exact-and-managed", SSALoweredCall{LogicalName: "callback", Target: target, ManagedDispatch: true}, false},
		{"raw", SSALoweredCall{LogicalName: "callback", ManagedDispatch: true, RawPlain: true}, false},
		{"unwind", SSALoweredCall{LogicalName: "callback", ManagedDispatch: true, UnwindOnly: true}, false},
		{"no-unwind", SSALoweredCall{LogicalName: "callback", ManagedDispatch: true, NoUnwind: true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: AsyncDemand}}, SSAConfig{
				FunctionIDs: planDigestSSAConfig().FunctionIDs,
				ClassifyLoweredCalls: func(function *ssa.Function) ([]SSALoweredCall, error) {
					if function == owner {
						return []SSALoweredCall{test.record}, nil
					}
					return nil, nil
				},
			})
			if !test.valid {
				if err == nil {
					t.Fatal("unsupported dynamic helper shortcut was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if body := functionPlanFor(t, plan, owner); body.Effect != AwaitStructured || body.Emission != EmitCoroutine {
				t.Fatalf("managed callback did not auto-color owner: %+v", body)
			}
			if _, err := plan.CoroPlanDigest(validPlanDigestMetadata()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
