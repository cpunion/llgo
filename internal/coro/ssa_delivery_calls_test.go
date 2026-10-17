//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0.
 */

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

func TestAnalyzeSSARangeDeferDeliveryUsesExecutionOwner(t *testing.T) {
	analyze := func(mode ssa.BuilderMode) (*SSAPlan, *ssa.Function, []ssa.CallInstruction) {
		prog, pkg := buildCoroTestSSAWithMode(t, "range_delivery.go", `package coroid
func sequence(yield func(int) bool) { yield(0) }
func Owner(f func() int) {
 for range sequence { defer f() }
 for range sequence { for range sequence { defer f() } }
}
`, mode)
		owner := packageFunction(t, pkg, "Owner")
		var calls []ssa.CallInstruction
		var collect func(*ssa.Function)
		collect = func(fn *ssa.Function) {
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					if call, ok := instruction.(*ssa.Defer); ok {
						if SSAResultDeliveryOwner(call) != owner || call.DeferStack == nil {
							t.Fatalf("range defer lost its structural stack owner: %s", call)
						}
						if err := validateSSADeliveryCallCondition(call.Parent(), call); err == nil {
							t.Fatal("registration closure was accepted as external result owner")
						}
						calls = append(calls, call)
					}
				}
			}
			for _, child := range fn.AnonFuncs {
				collect(child)
			}
		}
		collect(owner)
		if len(calls) != 2 || calls[0].Parent() == calls[1].Parent() {
			t.Fatalf("fixture needs sibling/nested source sites, got %v", calls)
		}
		firstOrdinal, firstErr := SemanticInstructionOrdinal(calls[0])
		secondOrdinal, secondErr := SemanticInstructionOrdinal(calls[1])
		if firstErr != nil || secondErr != nil || calls[0].Block().Index != calls[1].Block().Index || firstOrdinal != secondOrdinal {
			t.Fatalf("fixture must isolate equal block/instruction ordinals across source functions: %d/%d, %v/%v", firstOrdinal, secondOrdinal, firstErr, secondErr)
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: AsyncDemand}}, SSAConfig{
			FunctionIDs: planDigestSSAConfig().FunctionIDs, MaxPlainInstructions: -1,
			OutcomeMode:         OutcomeExplicitStatus,
			ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (UnknownTarget, error) { return UnknownManagedDispatch, nil },
			ClassifyLoweredCalls: func(fn *ssa.Function) ([]SSALoweredCall, error) {
				if fn != owner {
					return nil, nil
				}
				var helpers []SSALoweredCall
				for _, call := range calls {
					helpers = append(helpers, SSALoweredCall{LogicalName: "drop:" + call.Parent().Name(), ManagedDispatch: true, WhenResultDeliveryOf: call})
				}
				return helpers, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if fact := functionPlanFor(t, plan, owner); !fact.LocalExec.Contains(NeedsCleanupFrame) {
			t.Fatalf("outer owner has no result cleanup: %+v", fact)
		}
		for _, call := range calls {
			if fact, ok := plan.CallPlan(call); !ok || !fact.ResultDelivery {
				t.Fatalf("external defer lost its conditional delivery: %+v", fact)
			}
			// The generic SSA scanner already marks source defer registration
			// as cleanup-bearing. It must not acquire the result helper edges;
			// physical guard absence is tested with the production ProgramIR.
			if helpers := plan.LoweredCalls(call.Parent()); len(helpers) != 0 {
				t.Fatalf("registration closure acquired result helpers: %+v", helpers)
			}
		}
		return plan, owner, calls
	}
	plan, owner, calls := analyze(ssa.SanityCheckFunctions | ssa.InstantiateGenerics)
	debug, _, _ := analyze(ssa.SanityCheckFunctions | ssa.InstantiateGenerics | ssa.GlobalDebug)
	digest, err := plan.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	debugDigest, err := debug.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil || digest != debugDigest {
		t.Fatalf("relocated defer digest is debug-dependent: %s / %s, %v", digest, debugDigest, err)
	}
	// Both source calls remain valid conditions for the same physical owner.
	// The digest must nevertheless distinguish their exact source functions.
	plan.loweredCalls[owner][0].WhenResultDeliveryOf = calls[1]
	plan.loweredCalls[owner][1].WhenResultDeliveryOf = calls[0]
	changed, err := plan.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil || changed == digest {
		t.Fatalf("relocated condition lost its source-function identity: %s / %s, %v", digest, changed, err)
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

func TestAnalyzeSSAConditionalDeliveryExactHelperSharesFlowAndDigest(t *testing.T) {
	for _, kind := range []string{"resource", "ordinary", "open"} {
		t.Run(kind, func(t *testing.T) {
			prog, pkg := buildCoroTestSSA(t, "conditional_allocator.go", deliveryCallFlowSource+"\nfunc allocator() {}\n")
			owner, allocator := packageFunction(t, pkg, "openOwner"), packageFunction(t, pkg, "allocator")
			producer, ordinary := packageFunction(t, pkg, "resource"), packageFunction(t, pkg, "ordinary")
			call := onlyNonBuiltinCall(t, owner)
			config := SSAConfig{
				FunctionIDs: planDigestSSAConfig().FunctionIDs, OutcomeMode: OutcomeExplicitStatus,
				ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
					if fn == producer {
						return SSAFunctionPolicy{ResultDelivery: true, Exec: NeedsCleanupFrame}, nil
					}
					if fn == allocator {
						return SSAFunctionPolicy{Effect: WaitForeign}, nil
					}
					return SSAFunctionPolicy{}, nil
				},
				ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (UnknownTarget, error) { return UnknownManagedDispatch, nil },
				ClassifyClosedDynamicCall: func(fn *ssa.Function, site ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
					if fn != owner || site != call || kind == "open" {
						return SSAClosedDynamicCallCertificate{}, false, nil
					}
					target := producer
					if kind == "ordinary" {
						target = ordinary
					}
					return SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{target}}, true, nil
				},
				ClassifyLoweredCalls: func(fn *ssa.Function) ([]SSALoweredCall, error) {
					if fn != owner {
						return nil, nil
					}
					return []SSALoweredCall{
						{LogicalName: "delivery.allocate", Target: allocator, WhenResultDeliveryOf: call},
						{LogicalName: "delivery.initialize", ManagedDispatch: true, WhenResultDeliveryOf: call},
						{LogicalName: "delivery.drop", ManagedDispatch: true, WhenResultDeliveryOf: call},
					}, nil
				},
			}
			plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: AsyncDemand}}, config)
			if err != nil {
				t.Fatal(err)
			}
			want := kind != "ordinary"
			if target, found := plan.ResolveLoweredCall(owner, "delivery.allocate"); found != want || want && target != allocator {
				t.Fatalf("conditional exact helper target=%v present=%t want=%t", target, found, want)
			}
			if fact := functionPlanFor(t, plan, allocator); (fact.Demand != NoDemand) != want {
				t.Fatalf("conditional exact helper retained wrong demand: %+v", fact)
			}
			if body := functionPlanFor(t, plan, owner); body.Effect.Contains(WaitForeign) != want {
				t.Fatalf("exact conditional effect did not follow ordinary managed propagation: %+v", body)
			}
			if _, err := plan.CoroPlanDigest(validPlanDigestMetadata()); err != nil {
				t.Fatal(err)
			}
			if !want {
				return
			}
			// Every forbidden shortcut is rejected again by the immutable
			// digest, even if a caller corrupts the retained classifier result.
			for _, shortcut := range []string{"raw", "no-unwind", "unwind-only", "terminal-elision"} {
				original := plan.loweredCalls[owner][0]
				mutated := original
				switch shortcut {
				case "raw":
					mutated.RawPlain = true
				case "no-unwind":
					mutated.NoUnwind = true
				case "unwind-only":
					mutated.UnwindOnly = true
				case "terminal-elision":
					mutated.UnwindOnly, mutated.ExplicitStatusElided = true, true
				}
				plan.loweredCalls[owner][0] = mutated
				if _, err := plan.CoroPlanDigest(validPlanDigestMetadata()); err == nil {
					t.Fatalf("conditional exact helper accepted %s", shortcut)
				}
				plan.loweredCalls[owner][0] = original
			}
		})
	}
}
