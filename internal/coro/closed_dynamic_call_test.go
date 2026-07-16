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
			want:        "ordinary *ssa.Call",
		},
		{
			name:        "defer",
			caller:      packageFunction(t, pkg, "deferDynamic"),
			certificate: SSAClosedDynamicCallCertificate{MayBeNil: true},
			want:        "ordinary *ssa.Call",
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
