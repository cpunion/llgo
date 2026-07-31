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
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSAExactInterfaceReceiverIsOccurrenceLocal(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "exact_interface.go", `package coroid

type Interface interface { Method() }
type Concrete struct{}

func (Concrete) Method() {}

func local() {
	var value Interface = Concrete{}
	value.Method()
}

func unknown(value Interface) {
	value.Method()
}

func typedNil() {
	var pointer *Concrete
	var value Interface = pointer
	value.Method()
}
`)
	local := packageFunction(t, pkg, "local")
	unknown := packageFunction(t, pkg, "unknown")
	typedNil := packageFunction(t, pkg, "typedNil")
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: local, Demand: AsyncDemand},
		{Function: unknown, Demand: AsyncDemand},
		{Function: typedNil, Demand: AsyncDemand},
	}, SSAConfig{
		DynamicResolution:    DynamicCHAClosed,
		MaxPlainInstructions: -1,
		OutcomeMode:          OutcomeExplicitStatus,
	})
	if err != nil {
		t.Fatal(err)
	}

	findInvoke := func(fn *ssa.Function) *ssa.Call {
		t.Helper()
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if call, ok := instruction.(*ssa.Call); ok &&
					call.Common() != nil && call.Common().IsInvoke() {
					return call
				}
			}
		}
		t.Fatalf("%s has no interface invoke", fn.Name())
		return nil
	}

	localCall := findInvoke(local)
	receiver, target, targetPlan, exact, err :=
		plan.ResolveExactInterfaceCall(localCall)
	if err != nil {
		t.Fatal(err)
	}
	if !exact || receiver == nil || target == nil ||
		target.Signature == nil || target.Signature.Recv() == nil ||
		!types.Identical(receiver.Type(), target.Signature.Recv().Type()) {
		t.Fatalf(
			"local exact interface call = receiver:%v target:%v plan:%+v exact:%t",
			receiver, target, targetPlan, exact,
		)
	}
	if _, pointer := types.Unalias(receiver.Type()).Underlying().(*types.Pointer); pointer {
		t.Fatalf("local concrete receiver = %s, want the value type", receiver.Type())
	}
	if target.Synthetic != "" {
		t.Fatalf("local concrete target = %q, want the declared value method", target.Synthetic)
	}

	unknownCall := findInvoke(unknown)
	if receiver, target, _, exact, err :=
		plan.ResolveExactInterfaceCall(unknownCall); err != nil || exact ||
		receiver != nil || target != nil {
		t.Fatalf(
			"unknown interface parameter was devirtualized: receiver=%v target=%v exact=%t err=%v",
			receiver, target, exact, err,
		)
	}

	typedNilCall := findInvoke(typedNil)
	receiver, target, targetPlan, exact, err =
		plan.ResolveExactInterfaceCall(typedNilCall)
	if err != nil {
		t.Fatal(err)
	}
	if !exact || receiver == nil || target == nil ||
		!strings.Contains(target.Synthetic, "wrapper") ||
		!targetPlan.Exec.Contains(MayUnwind) {
		t.Fatalf(
			"typed-nil interface target = receiver:%v target:%v synthetic:%q plan:%+v exact:%t; want nullable pointer wrapper",
			receiver, target, target.Synthetic, targetPlan, exact,
		)
	}
	if _, pointer := types.Unalias(receiver.Type()).Underlying().(*types.Pointer); !pointer {
		t.Fatalf("typed-nil concrete receiver = %s, want *Concrete", receiver.Type())
	}
}

func TestAnalyzeSSAExactInterfaceReceiverIsResolutionIndependent(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "exact_interface_resolution.go", `package coroid

type Interface interface { Method() }
type Concrete struct{}

func (Concrete) Method() {}

func local() {
	var value Interface = Concrete{}
	value.Method()
}
`)
	local := packageFunction(t, pkg, "local")
	var invoke *ssa.Call
	for _, block := range local.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok &&
				call.Common() != nil && call.Common().IsInvoke() {
				invoke = call
			}
		}
	}
	if invoke == nil {
		t.Fatal("local has no interface invoke")
	}

	for _, resolution := range []DynamicResolution{
		DynamicUnknownOnly,
		DynamicCHAOpen,
		DynamicCHAClosed,
	} {
		plan, err := AnalyzeSSA(
			prog,
			Roots{{Function: local, Demand: AsyncDemand}},
			SSAConfig{
				DynamicResolution:    resolution,
				MaxPlainInstructions: -1,
				OutcomeMode:          OutcomeExplicitStatus,
			},
		)
		if err != nil {
			t.Fatalf("resolution %d: %v", resolution, err)
		}
		receiver, target, _, exact, err := plan.ResolveExactInterfaceCall(invoke)
		if err != nil {
			t.Fatalf("resolution %d: %v", resolution, err)
		}
		callPlan, planned := plan.CallPlan(invoke)
		if !exact || receiver == nil || target == nil || !planned ||
			callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
			t.Fatalf(
				"resolution %d exact call = receiver:%v target:%v exact:%t call-plan:%+v present:%t",
				resolution, receiver, target, exact, callPlan, planned,
			)
		}
	}
}
