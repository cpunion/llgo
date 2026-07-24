//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package coro

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSAClosedDynamicCallCertificates(t *testing.T) {
	prog, pkg := buildClosedDynamicCallTestSSA(t)
	dynamicPlain := packageFunction(t, pkg, "dynamicPlain")
	nilOnly := packageFunction(t, pkg, "nilOnly")
	dynamicSuspend := packageFunction(t, pkg, "dynamicSuspend")
	plain := packageFunction(t, pkg, "plain")
	suspend := packageFunction(t, pkg, "suspend")

	plan, err := AnalyzeSSA(prog, Roots{
		{Function: dynamicPlain, Demand: AsyncDemand},
		{Function: nilOnly, Demand: AsyncDemand},
		{Function: dynamicSuspend, Demand: AsyncDemand},
	}, SSAConfig{
		ClassifyClosedDynamicCall: func(caller *ssa.Function, _ ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
			switch caller {
			case dynamicPlain:
				return SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{plain}, MayBeNil: true}, true, nil
			case nilOnly:
				return SSAClosedDynamicCallCertificate{MayBeNil: true}, true, nil
			case dynamicSuspend:
				return SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{suspend}, MayBeNil: true}, true, nil
			default:
				return SSAClosedDynamicCallCertificate{}, false, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	plainCall := onlyNonBuiltinCall(t, dynamicPlain)
	assertClosedDynamicCall(t, plan, plainCall, true, plain)
	plainValue, ok := plan.ValuePlan(plainCall.Common().Value)
	if !ok || len(plainValue.Funcs) != 1 || plainValue.Funcs[0].Rep != Dispatch ||
		!plainValue.Funcs[0].MayBeNil || len(plainValue.Funcs[0].Targets) != 1 {
		t.Fatalf("certified plain callee value = %+v, present=%t; want nullable singleton Dispatch", plainValue, ok)
	}
	if got := functionPlanFor(t, plan, dynamicPlain); got.Effect != NoSuspend || got.Effect.IsOpaque() {
		t.Fatalf("certified plain caller effect = %s, want no-suspend", got.Effect)
	}
	if got := functionPlanFor(t, plan, plain); got.FuncRep != Dispatch || got.Primary != PrimaryPlain {
		t.Fatalf("certified plain target plan = %+v, want descriptor-backed plain target", got)
	}

	nilCall := onlyNonBuiltinCall(t, nilOnly)
	assertClosedDynamicCall(t, plan, nilCall, true)
	if got := functionPlanFor(t, plan, nilOnly); got.Effect != NoSuspend || got.Effect.IsOpaque() {
		t.Fatalf("closed nil-only caller effect = %s, want no-suspend", got.Effect)
	}

	suspendCall := onlyNonBuiltinCall(t, dynamicSuspend)
	assertClosedDynamicCall(t, plan, suspendCall, true, suspend)
	if got := functionPlanFor(t, plan, dynamicSuspend); got.Effect.IsOpaque() || !got.Effect.Contains(MayPark) {
		t.Fatalf("certified suspending caller effect = %s, want known MayPark", got.Effect)
	}
	if got := functionPlanFor(t, plan, suspend); got.Demand != AsyncDemand || got.FuncRep != Dispatch || !got.Effect.Contains(MayPark) {
		t.Fatalf("certified suspending target plan = %+v, want demanded descriptor target with MayPark", got)
	}
}

func TestAnalyzeSSAClosedDynamicDeferCertificate(t *testing.T) {
	prog, pkg := buildClosedDynamicCallTestSSA(t)
	owner := packageFunction(t, pkg, "deferDynamic")
	target := packageFunction(t, pkg, "suspend")
	deferred := onlyNonBuiltinCall(t, owner)
	if _, ok := deferred.(*ssa.Defer); !ok {
		t.Fatalf("deferDynamic call = %T, want *ssa.Defer", deferred)
	}

	plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: AsyncDemand}}, SSAConfig{
		MaxPlainInstructions: -1,
		OutcomeMode:          OutcomeExplicitStatus,
		ClassifyUnknownCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (UnknownTarget, error) {
			if candidate == deferred {
				return UnknownManagedDispatch, nil
			}
			return UnknownManaged, nil
		},
		ClassifyClosedDynamicCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
			if candidate != deferred {
				return SSAClosedDynamicCallCertificate{}, false, nil
			}
			return SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{target}}, true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertClosedDynamicCall(t, plan, deferred, false, target)
	callPlan, _ := plan.CallPlan(deferred)
	ownerPlan := functionPlanFor(t, plan, owner)
	targetPlan := functionPlanFor(t, plan, target)
	if callPlan.Kind != CallDefer || callPlan.Transport != ManagedTransport ||
		ownerPlan.Emission != EmitCoroutine || !ownerPlan.Exec.Contains(NeedsCleanupFrame) ||
		!ownerPlan.Effect.Contains(AwaitStructured) || targetPlan.FuncRep != Dispatch || targetPlan.Emission != EmitCoroutine {
		t.Fatalf("closed descriptor defer plans = call:%+v owner:%+v target:%+v", callPlan, ownerPlan, targetPlan)
	}
}

func TestAnalyzeSSAClosedNilOnlyCallDoesNotInventManagedDispatch(t *testing.T) {
	prog, pkg := buildClosedDynamicCallTestSSA(t)
	owner := packageFunction(t, pkg, "nilOnly")
	call := onlyNonBuiltinCall(t, owner)
	classified := 0
	plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: AsyncDemand}}, SSAConfig{
		ClassifyUnknownCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (UnknownTarget, error) {
			if candidate == call {
				classified++
				return UnknownManagedDispatch, nil
			}
			return UnknownManaged, nil
		},
		ClassifyClosedDynamicCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
			if candidate != call {
				return SSAClosedDynamicCallCertificate{}, false, nil
			}
			return SSAClosedDynamicCallCertificate{MayBeNil: true}, true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if classified != 0 {
		t.Fatalf("nil-only closed call requested %d nonexistent managed descriptor classifications", classified)
	}
	callPlan, ok := plan.CallPlan(call)
	ownerPlan := functionPlanFor(t, plan, owner)
	if !ok || callPlan.Open || callPlan.Rep != Dispatch || !callPlan.MayBeNil || len(callPlan.Targets) != 0 ||
		ownerPlan.LocalEffect.Contains(AwaitStructured) || ownerPlan.LocalExec.IsOpaque() || ownerPlan.Exec.IsOpaque() {
		t.Fatalf("nil-only closed plans = call:%+v/%t owner:%+v", callPlan, ok, ownerPlan)
	}
}

func TestAnalyzeSSAClosedSyncDispatchIsExactAndDoesNotColorOwner(t *testing.T) {
	prog, pkg := buildClosedDynamicCallTestSSA(t)
	owner := packageFunction(t, pkg, "dynamicPlain")
	target := packageFunction(t, pkg, "plain")
	call := onlyNonBuiltinCall(t, owner)

	analyze := func(syncDispatch bool) (*SSAPlan, error) {
		return AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{
			MaxPlainInstructions: -1,
			OutcomeMode:          OutcomeExplicitStatus,
			ClassifyUnknownCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (UnknownTarget, error) {
				if candidate == call {
					return UnknownManagedDispatch, nil
				}
				return UnknownManaged, nil
			},
			ClassifyClosedDynamicCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
				if candidate != call {
					return SSAClosedDynamicCallCertificate{}, false, nil
				}
				return SSAClosedDynamicCallCertificate{
					Targets:      []*ssa.Function{target},
					MayBeNil:     true,
					SyncDispatch: syncDispatch,
				}, true, nil
			},
		})
	}

	syncPlan, err := analyze(true)
	if err != nil {
		t.Fatal(err)
	}
	callPlan, ok := syncPlan.CallPlan(call)
	if !ok || !callPlan.SyncDispatch || callPlan.Rep != Dispatch || callPlan.Open ||
		callPlan.Unresolved != UnknownManaged || !callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		t.Fatalf("synchronous descriptor CallPlan = %+v, present=%t", callPlan, ok)
	}
	ownerPlan := functionPlanFor(t, syncPlan, owner)
	if ownerPlan.LocalEffect != NoSuspend || ownerPlan.Effect != NoSuspend || ownerPlan.Demand != SyncDemand ||
		ownerPlan.Primary != PrimaryPlain || ownerPlan.Emission != EmitPlain {
		t.Fatalf("synchronous descriptor owner plan = %+v, want uncolored plain owner", ownerPlan)
	}
	targetPlan := functionPlanFor(t, syncPlan, target)
	if targetPlan.Demand != SyncDemand || targetPlan.Effect != NoSuspend || targetPlan.FuncRep != Dispatch ||
		targetPlan.Primary != PrimaryPlain || targetPlan.Emission != EmitPlain {
		t.Fatalf("synchronous descriptor target plan = %+v, want sync-demanded descriptor-backed plain target", targetPlan)
	}

	managedPlan, err := analyze(false)
	if err != nil {
		t.Fatal(err)
	}
	managedCall, ok := managedPlan.CallPlan(call)
	if !ok || managedCall.SyncDispatch || managedCall.Rep != Dispatch || managedCall.Open {
		t.Fatalf("ordinary managed descriptor CallPlan = %+v, present=%t", managedCall, ok)
	}
	managedOwner := functionPlanFor(t, managedPlan, owner)
	if !managedOwner.LocalEffect.Contains(AwaitStructured) || !managedOwner.LocalEffect.Contains(OutcomeStructured) ||
		managedOwner.Emission != EmitCoroutine {
		t.Fatalf("ordinary managed descriptor owner plan = %+v, want conservative structured await/outcome", managedOwner)
	}
}

func TestAnalyzeSSAClosedSyncDispatchAcceptsExactRawVariantOwners(t *testing.T) {
	prog, pkg := buildClosedDynamicCallTestSSA(t)
	owner := packageFunction(t, pkg, "dynamicPlain")
	target := packageFunction(t, pkg, "plain")
	call := onlyNonBuiltinCall(t, owner)

	analyze := func(managed Demand, ownerEffect Effect) *SSAPlan {
		t.Helper()
		plan, err := AnalyzeSSA(prog, Roots{{
			Function: owner, ManagedDemand: managed, RawPlainDemand: true,
		}}, SSAConfig{
			MaxPlainInstructions: -1,
			OutcomeMode:          OutcomeExplicitStatus,
			ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn == owner {
					return SSAFunctionPolicy{Effect: ownerEffect, RawPlainEntry: true}, nil
				}
				return SSAFunctionPolicy{}, nil
			},
			ClassifyUnknownCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (UnknownTarget, error) {
				if candidate == call {
					return UnknownManagedDispatch, nil
				}
				return UnknownManaged, nil
			},
			ClassifyClosedDynamicCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
				if candidate != call {
					return SSAClosedDynamicCallCertificate{}, false, nil
				}
				return SSAClosedDynamicCallCertificate{
					Targets: []*ssa.Function{target}, MayBeNil: true, SyncDispatch: true,
				}, true, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	rawOnly := analyze(NoDemand, NoSuspend)
	rawOwner := functionPlanFor(t, rawOnly, owner)
	if rawOwner.Emission != EmitRawPlain || !rawOwner.RawPlainOnly || rawOwner.ManagedDemand != NoDemand ||
		!rawOwner.RawPlainDemand || !rawOnly.HasRawPlainVariant(owner) {
		t.Fatalf("raw-only SyncDispatch owner plan = %+v, variant=%t", rawOwner, rawOnly.HasRawPlainVariant(owner))
	}
	rawCall, ok := rawOnly.CallPlan(call)
	if !ok || !rawCall.SyncDispatch || rawCall.Open || rawCall.Rep != Dispatch || len(rawCall.Targets) != 1 {
		t.Fatalf("raw-only SyncDispatch CallPlan = %+v, present=%t", rawCall, ok)
	}
	if got := functionPlanFor(t, rawOnly, target); got.Emission != EmitPlain || got.Effect != NoSuspend ||
		got.FuncRep != Dispatch || got.ManagedDemand != SyncDemand || got.RawPlainDemand {
		t.Fatalf("raw-only SyncDispatch target plan = %+v, want managed-sync EmitPlain descriptor", got)
	}

	mixed := analyze(AsyncDemand, YieldOnly)
	mixedOwner := functionPlanFor(t, mixed, owner)
	if mixedOwner.Emission != EmitCoroutine || mixedOwner.RawPlainOnly || mixedOwner.ManagedDemand != AsyncDemand ||
		!mixedOwner.RawPlainDemand || !mixed.HasRawPlainVariant(owner) {
		t.Fatalf("mixed SyncDispatch owner plan = %+v, variant=%t", mixedOwner, mixed.HasRawPlainVariant(owner))
	}
	if got := functionPlanFor(t, mixed, target); got.Emission != EmitPlain || got.Effect != NoSuspend ||
		got.FuncRep != Dispatch || got.ManagedDemand != SyncDemand || got.RawPlainDemand {
		t.Fatalf("mixed SyncDispatch target plan = %+v, want managed-sync EmitPlain descriptor", got)
	}
}

func TestAnalyzeSSAClosedSyncDispatchRejectsSuspendingTarget(t *testing.T) {
	prog, pkg := buildClosedDynamicCallTestSSA(t)
	owner := packageFunction(t, pkg, "dynamicSuspend")
	target := packageFunction(t, pkg, "suspend")
	call := onlyNonBuiltinCall(t, owner)
	_, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyClosedDynamicCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
			if candidate != call {
				return SSAClosedDynamicCallCertificate{}, false, nil
			}
			return SSAClosedDynamicCallCertificate{
				Targets:      []*ssa.Function{target},
				MayBeNil:     true,
				SyncDispatch: true,
			}, true, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not one defined non-suspending descriptor-backed plain primary") {
		t.Fatalf("suspending synchronous descriptor target error = %v", err)
	}
}

func TestAnalyzeSSAClosedSyncDispatchRejectsCoroutineOwner(t *testing.T) {
	prog, pkg := buildClosedDynamicCallTestSSA(t)
	owner := packageFunction(t, pkg, "dynamicPlain")
	target := packageFunction(t, pkg, "plain")
	call := onlyNonBuiltinCall(t, owner)
	_, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: AsyncDemand}}, SSAConfig{
		MaxPlainInstructions: -1,
		OutcomeMode:          OutcomeExplicitStatus,
		ClassifyClosedDynamicCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
			if candidate != call {
				return SSAClosedDynamicCallCertificate{}, false, nil
			}
			return SSAClosedDynamicCallCertificate{
				Targets:      []*ssa.Function{target},
				MayBeNil:     true,
				SyncDispatch: true,
			}, true, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "synchronous descriptor call owner") ||
		!strings.Contains(err.Error(), "not one defined non-suspending plain primary") {
		t.Fatalf("coroutine synchronous descriptor owner error = %v", err)
	}
}

func TestAnalyzeSSAClosedSyncDispatchPublicationIsExact(t *testing.T) {
	analyze := func(mixed bool) (*SSAPlan, *ssa.Function, error) {
		extra := ""
		if mixed {
			extra = "escaped = plain"
		}
		prog, pkg := buildCoroTestSSA(t, "sync_publication.go", `package coroid
func plain(int) {}
func accept(func(int)) {}
var escaped func(int)
func publish() {
	accept(plain)
	`+extra+`
}
func dynamic(fn func(int)) { fn(1) }
`)
		owner := packageFunction(t, pkg, "dynamic")
		target := packageFunction(t, pkg, "plain")
		publicationOwner := packageFunction(t, pkg, "publish")
		descriptorCall := onlyNonBuiltinCall(t, owner)
		var publicationCall ssa.CallInstruction
		for _, block := range publicationOwner.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if ok && call.Common() != nil && call.Common().StaticCallee() != nil &&
					call.Common().StaticCallee().Name() == "accept" {
					publicationCall = call
				}
			}
		}
		if publicationCall == nil {
			t.Fatalf("%s has no static accept call", publicationOwner)
		}
		plan, err := AnalyzeSSA(prog, Roots{
			{Function: owner, Demand: SyncDemand},
			{Function: publicationOwner, Demand: AsyncDemand},
		}, SSAConfig{
			MaxPlainInstructions: -1,
			OutcomeMode:          OutcomeExplicitStatus,
			ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (UnknownTarget, error) {
				if call == descriptorCall {
					return UnknownManagedDispatch, nil
				}
				return UnknownManaged, nil
			},
			ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
				if call != descriptorCall {
					return SSAClosedDynamicCallCertificate{}, false, nil
				}
				return SSAClosedDynamicCallCertificate{
					Targets:      []*ssa.Function{target},
					MayBeNil:     true,
					SyncDispatch: true,
					SyncOnlyCallArguments: []SSASyncOnlyCallArgument{{
						Call:     publicationCall,
						Argument: 0,
					}},
				}, true, nil
			},
		})
		return plan, target, err
	}

	syncPlan, target, err := analyze(false)
	if err != nil {
		t.Fatal(err)
	}
	targetPlan := functionPlanFor(t, syncPlan, target)
	if targetPlan.Demand != SyncDemand || targetPlan.Effect != NoSuspend || targetPlan.FuncRep != Dispatch ||
		targetPlan.Emission != EmitPlain {
		t.Fatalf("exact synchronous publication target = %+v, want sync-demanded plain descriptor", targetPlan)
	}

	mixedPlan, mixedTarget, err := analyze(true)
	if err != nil {
		t.Fatalf("mixed ordinary publication rejected: %v", err)
	}
	mixedTargetPlan := functionPlanFor(t, mixedPlan, mixedTarget)
	if mixedTargetPlan.ManagedDemand != SyncDemand || mixedTargetPlan.Effect != NoSuspend ||
		mixedTargetPlan.FuncRep != Dispatch || mixedTargetPlan.Emission != EmitPlain {
		t.Fatalf("mixed ordinary publication target = %+v, want exact no-suspend descriptor", mixedTargetPlan)
	}
}

func TestAnalyzeSSAClosedDynamicCallCertificateRejectsInvalidProof(t *testing.T) {
	prog, pkg := buildClosedDynamicCallTestSSA(t)
	dynamicPlain := packageFunction(t, pkg, "dynamicPlain")
	plain := packageFunction(t, pkg, "plain")
	suspend := packageFunction(t, pkg, "suspend")
	wrongSignature := packageFunction(t, pkg, "wrongSignature")
	external := packageFunction(t, pkg, "external")

	tests := []struct {
		name        string
		caller      *ssa.Function
		certificate SSAClosedDynamicCallCertificate
		want        string
	}{
		{
			name:        "static",
			caller:      packageFunction(t, pkg, "staticCall"),
			certificate: SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{plain}},
			want:        "cannot identify a static call",
		},
		{
			name:        "invoke",
			caller:      packageFunction(t, pkg, "interfaceInvoke"),
			certificate: SSAClosedDynamicCallCertificate{MayBeNil: true},
			want:        "cannot identify an interface invoke",
		},
		{
			name:        "go",
			caller:      packageFunction(t, pkg, "goDynamic"),
			certificate: SSAClosedDynamicCallCertificate{MayBeNil: true},
			want:        "ordinary call or defer",
		},
		{
			name:        "synchronous defer",
			caller:      packageFunction(t, pkg, "deferDynamic"),
			certificate: SSAClosedDynamicCallCertificate{MayBeNil: true, SyncDispatch: true},
			want:        "cannot claim synchronous",
		},
		{
			name:        "multiple targets",
			caller:      dynamicPlain,
			certificate: SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{plain, suspend}, MayBeNil: true},
			want:        "has 2 targets",
		},
		{
			name:        "signature mismatch",
			caller:      dynamicPlain,
			certificate: SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{wrongSignature}, MayBeNil: true},
			want:        "has signature",
		},
		{
			name:        "external target",
			caller:      dynamicPlain,
			certificate: SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{external}, MayBeNil: true},
			want:        "not an external target",
		},
		{
			name:        "empty non-nil",
			caller:      dynamicPlain,
			certificate: SSAClosedDynamicCallCertificate{},
			want:        "neither a target nor nil",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			call := onlyNonBuiltinCall(t, test.caller)
			_, err := AnalyzeSSA(prog, Roots{{Function: test.caller, Demand: AsyncDemand}}, SSAConfig{
				ClassifyClosedDynamicCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
					if candidate != call {
						return SSAClosedDynamicCallCertificate{}, false, nil
					}
					return test.certificate, true, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AnalyzeSSA error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAnalyzeSSAClosedDynamicCallCertificateRejectsTargetOutsideUniverse(t *testing.T) {
	prog, pkg := buildClosedDynamicCallTestSSA(t)
	caller := packageFunction(t, pkg, "dynamicPlain")
	target := packageFunction(t, pkg, "plain")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{caller})
	if err != nil {
		t.Fatal(err)
	}
	_, err = AnalyzeSSA(prog, Roots{{Function: caller, Demand: AsyncDemand}}, SSAConfig{
		EmissionUniverse: universe,
		ClassifyClosedDynamicCall: func(_ *ssa.Function, _ ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
			return SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{target}, MayBeNil: true}, true, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "outside the effective emission universe") {
		t.Fatalf("outside-universe certificate error = %v", err)
	}
}

func assertClosedDynamicCall(t *testing.T, plan *SSAPlan, call ssa.CallInstruction, mayBeNil bool, targets ...*ssa.Function) {
	t.Helper()
	got, ok := plan.CallPlan(call)
	if !ok {
		t.Fatalf("certified call %s has no CallPlan", call)
	}
	if got.Rep != Dispatch || got.Open || got.MayBeNil != mayBeNil || len(got.Targets) != len(targets) {
		t.Fatalf("certified call plan = %+v, want closed Dispatch nil=%t targets=%d", got, mayBeNil, len(targets))
	}
	if got.SyncDispatch {
		t.Fatalf("ordinary closed dynamic call unexpectedly has synchronous-dispatch semantics: %+v", got)
	}
	for i, target := range targets {
		id, ok := plan.FunctionID(target)
		if !ok {
			t.Fatalf("certified target %q has no FunctionID", target.Name())
		}
		if got.Targets[i] != id {
			t.Fatalf("certified call target[%d] = %s, want %s", i, got.Targets[i], id)
		}
	}
}

func buildClosedDynamicCallTestSSA(t *testing.T) (*ssa.Program, *ssa.Package) {
	t.Helper()
	return buildCoroTestSSA(t, "closed_dynamic_call.go", `package coroid

var channel chan int

func plain(int) {}
func suspend(int) { <-channel }
func wrongSignature(string) {}
func external(int)

func dynamicPlain(fn func(int)) { fn(1) }
func nilOnly(fn func(int)) { fn(2) }
func dynamicSuspend(fn func(int)) { fn(3) }
func staticCall() { plain(4) }

type Interface interface { Method() }
func interfaceInvoke(value Interface) { value.Method() }
func goDynamic(fn func(int)) { go fn(5) }
func deferDynamic(fn func(int)) { defer fn(6) }
`)
}
