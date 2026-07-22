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

package build

import (
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroPlanInputCertifiesOnlyOrdinaryManagedDescriptorCalls(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/manageddispatch", `package manageddispatch
type I interface { M() }
func call(fn func(int) int) int { return fn(1) }
func invoke(value I) { value.M() }
func invokeDeferred(value I) { defer value.M() }
func invokeSpawned(value I) { go value.M() }
func deferred(fn func()) { defer fn() }
func spawned(fn func()) { go fn() }
`, nil)

	input := CoroPlanInput{Program: ssaPkg.Prog, enableManagedDispatch: true}
	plan, err := input.Analyze(coro.Roots{
		{Function: ssaPkg.Func("call"), Demand: coro.AsyncDemand},
		{Function: ssaPkg.Func("invoke"), Demand: coro.AsyncDemand},
		{Function: ssaPkg.Func("invokeDeferred"), Demand: coro.AsyncDemand},
		{Function: ssaPkg.Func("invokeSpawned"), Demand: coro.AsyncDemand},
		{Function: ssaPkg.Func("deferred"), Demand: coro.AsyncDemand},
		{Function: ssaPkg.Func("spawned"), Demand: coro.AsyncDemand},
	}, coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"call", "invoke", "invokeDeferred", "invokeSpawned", "deferred", "spawned"} {
		fn := ssaPkg.Func(name)
		var dynamic ssa.CallInstruction
		for _, candidate := range coroPlanTestCalls(fn) {
			if candidate.Common().StaticCallee() == nil {
				dynamic = candidate
				break
			}
		}
		if dynamic == nil {
			t.Fatalf("%s: missing dynamic call", name)
		}
		callPlan, ok := plan.CallPlan(dynamic)
		if !ok {
			t.Fatalf("%s: missing CallPlan", name)
		}
		want := coro.UnknownManaged
		if name == "call" {
			want = coro.UnknownManagedDispatch
		} else if name == "invoke" {
			want = coro.UnknownManagedInterfaceDispatch
		}
		if callPlan.Unresolved != want {
			t.Fatalf("%s unresolved = %v, want %v (plan=%+v)", name, callPlan.Unresolved, want, callPlan)
		}
	}

	callPlan, _ := plan.FunctionPlan(ssaPkg.Func("call"))
	if !callPlan.LocalEffect.Contains(coro.AwaitStructured) || callPlan.LocalEffect.IsOpaque() || callPlan.LocalExec.IsOpaque() {
		t.Fatalf("managed descriptor caller = %+v", callPlan)
	}
	invokePlan, _ := plan.FunctionPlan(ssaPkg.Func("invoke"))
	if !invokePlan.LocalEffect.Contains(coro.AwaitStructured) || invokePlan.LocalEffect.IsOpaque() || invokePlan.LocalExec.IsOpaque() {
		t.Fatalf("managed interface descriptor caller = %+v", invokePlan)
	}
}

func TestCoroPlanInputManagedDescriptorClassificationPreservesForeignAndRejectsBuilderCertification(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/manageddispatchpolicy", `package manageddispatchpolicy
func call(fn func()) { fn() }
`, nil)
	root := ssaPkg.Func("call")
	input := CoroPlanInput{Program: ssaPkg.Prog, enableManagedDispatch: true}

	plan, err := input.Analyze(coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
			return coro.UnknownForeign, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dynamic := coroPlanTestCalls(root)[0]
	got, _ := plan.CallPlan(dynamic)
	if got.Unresolved != coro.UnknownForeign || got.Kind != coro.CallForeign {
		t.Fatalf("foreign builder classification = %+v", got)
	}

	_, err = input.Analyze(coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
			return coro.UnknownManagedDispatch, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "builder cannot certify managed descriptor dispatch") {
		t.Fatalf("builder descriptor certification error = %v", err)
	}

	_, err = input.Analyze(coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
			return coro.UnknownManagedInterfaceDispatch, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "builder cannot certify managed descriptor dispatch") {
		t.Fatalf("builder interface descriptor certification error = %v", err)
	}
}
