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
	"fmt"
	"go/ast"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestCoroFuncAddrUsesExactRawAddressConsumer(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/coro/funcaddr", `package funcaddr
import "unsafe"
//llgo:link Func llgo.funcAddr
func Func(any) unsafe.Pointer
func target() {}
func root() unsafe.Pointer { return Func(target) }
`, nil)
	plan, emission, call, err := analyzeCoroFuncAddrTest(t, ssaPkg, files)
	if err != nil {
		t.Fatal(err)
	}
	semantics, intrinsic, err := coroIntrinsicCallSiteSemanticsForTest(emission, call)
	if err != nil || !intrinsic || semantics != cl.CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("funcAddr semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	if !plan.ElidesCall(call) || !plan.RawFunctionAddressArgument(call, 0) {
		t.Fatalf("funcAddr plan elided=%t raw-argument=%t; want both true", plan.ElidesCall(call), plan.RawFunctionAddressArgument(call, 0))
	}
	if _, ok := plan.CallPlan(call); ok {
		t.Fatal("funcAddr intrinsic declaration unexpectedly retained a CallPlan")
	}
	target := ssaPkg.Func("target")
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.FuncRep != coro.DirectPlain || !targetPlan.RawPlainEntry || !plan.HasRawPlainVariant(target) {
		t.Fatalf("raw-only funcAddr target plan = %+v, %v; want addressable direct-plain RawPlainEntry", targetPlan, ok)
	}
	valuePlan, ok := plan.ValuePlan(target)
	if !ok || len(valuePlan.Funcs) != 1 || valuePlan.Funcs[0].Rep != coro.DirectPlain {
		t.Fatalf("raw-only funcAddr target value plan = %+v, %v; want direct-plain", valuePlan, ok)
	}
}

func TestCoroFuncAddrRawAddressFactIsConsumerScoped(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/coro/funcaddrscoped", `package funcaddrscoped
import "unsafe"
//llgo:link Func llgo.funcAddr
func Func(any) unsafe.Pointer
var published any
func target() {}
func publish() { published = target }
func root() unsafe.Pointer { return Func(target) }
`, nil)
	plan, _, call, err := analyzeCoroFuncAddrTest(t, ssaPkg, files)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.RawFunctionAddressArgument(call, 0) {
		t.Fatal("exact funcAddr consumer lost its raw-address fact")
	}
	targetPlan, ok := plan.FunctionPlan(ssaPkg.Func("target"))
	if !ok || targetPlan.FuncRep != coro.Dispatch {
		t.Fatalf("target with an ordinary interface publication = %+v, %v; want Dispatch", targetPlan, ok)
	}
}

func TestCoroFuncAddrRejectsNonExactSites(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "dynamic any",
			source: `package funcaddrinvalid
import "unsafe"
//llgo:link Func llgo.funcAddr
func Func(any) unsafe.Pointer
func root(value any) unsafe.Pointer { return Func(value) }
`,
			wantErr: "want *ssa.MakeInterface",
		},
		{
			name: "non-function payload",
			source: `package funcaddrinvalid
import "unsafe"
//llgo:link Func llgo.funcAddr
func Func(any) unsafe.Pointer
func root() unsafe.Pointer { return Func(1) }
`,
			wantErr: "requires MakeInterface{X:*ssa.Function}",
		},
		{
			name: "captured closure",
			source: `package funcaddrinvalid
import "unsafe"
//llgo:link Func llgo.funcAddr
func Func(any) unsafe.Pointer
func root(value int) unsafe.Pointer {
	fn := func() { _ = value }
	return Func(fn)
}
`,
			wantErr: "requires MakeInterface{X:*ssa.Function}",
		},
		{
			name: "shared interface consumer",
			source: `package funcaddrinvalid
import "unsafe"
//llgo:link Func llgo.funcAddr
func Func(any) unsafe.Pointer
func consume(any) {}
func target() {}
func root() unsafe.Pointer {
	value := any(target)
	consume(value)
	return Func(value)
}
`,
			wantErr: "exact sole consumer",
		},
		{
			name: "wrong result",
			source: `package funcaddrinvalid
//llgo:link Func llgo.funcAddr
func Func(any) uintptr
func target() {}
func root() uintptr { return Func(target) }
`,
			wantErr: "exact func(any) unsafe.Pointer shape",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/coro/funcaddrinvalid", test.source, nil)
			_, _, _, err := analyzeCoroFuncAddrTest(t, ssaPkg, files)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("funcAddr invalid-site error = %v; want %q", err, test.wantErr)
			}
		})
	}
}

func analyzeCoroFuncAddrTest(t *testing.T, ssaPkg *ssa.Package, files []*ast.File) (*coro.SSAPlan, *cl.EmissionUniverse, ssa.CallInstruction, error) {
	t.Helper()
	prog := llssa.NewProgram(nil)
	t.Cleanup(prog.Dispose)
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: ssaPkg.Pkg.Path(),
	}})
	if err != nil {
		return nil, nil, nil, err
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		return nil, nil, nil, err
	}
	root := ssaPkg.Func("root")
	var intrinsicCall ssa.CallInstruction
	for _, call := range coroPlanTestCalls(root) {
		if callee := call.Common().StaticCallee(); callee != nil && callee.Name() == "Func" {
			intrinsicCall = call
			break
		}
	}
	if intrinsicCall == nil {
		return nil, nil, nil, fmt.Errorf("root has no funcAddr call")
	}
	input := CoroPlanInput{
		Program:                        ssaPkg.Prog,
		EmissionUniverse:               ssaEmission,
		resolveFunction:                emission.Resolve,
		functionBackground:             emission.FunctionBackground,
		callSitePlan:                   emission.CoroCallSitePlan,
		rawFunctionAddressCallArgument: emission.CoroRawFunctionAddressCallArgument,
		staticCodeAddressCallArgument:  emission.CoroStaticCodeAddressCallArgument,
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.EntryResolutionABIV0
	functionIDs.SchedulerABI = coro.SchedulerNoneABIV0
	functionIDs.ArchiveReady = true
	plan, err := input.Analyze(coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		FunctionIDs:          functionIDs,
	})
	return plan, emission, intrinsicCall, err
}
