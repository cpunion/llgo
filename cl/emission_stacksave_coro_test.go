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

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestStackSaveIntrinsicIsExactNoSuspendPlainIsland(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	pkg := testProg.addPackage(t, "example.com/emission/stacksaveintrinsic", `package stacksaveintrinsic
import "unsafe"
//llgo:link StackSave llgo.stackSave
func StackSave() unsafe.Pointer
func Use() unsafe.Pointer { return StackSave() }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	if semantics, intrinsic, err := universe.CoroIntrinsicSemantics(pkg.ssa.Func("StackSave")); err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("StackSave function semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	if semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("StackSave call semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
}

func TestStackSaveIntrinsicRejectsNonCanonicalShape(t *testing.T) {
	for _, test := range []struct {
		name        string
		declaration string
		use         string
	}{
		{name: "argument", declaration: "func StackSave(uintptr) unsafe.Pointer", use: "StackSave(0)"},
		{name: "result", declaration: "func StackSave() uintptr", use: "StackSave()"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			pkg := testProg.addPackage(t, "example.com/emission/stacksaveintrinsicbad"+test.name, `package stacksaveintrinsicbad
import "unsafe"
var _ unsafe.Pointer
//llgo:link StackSave llgo.stackSave
`+test.declaration+`
func Use() { _ = `+test.use+` }
`)
			testProg.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
			if err != nil {
				t.Fatal(err)
			}
			call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
			if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic || !strings.Contains(err.Error(), "func() unsafe.Pointer") {
				t.Fatalf("bad StackSave semantics = _, %v, %v; want exact-shape error", intrinsic, err)
			}
		})
	}
}

func TestStackSaveIntrinsicFailsClosedInPhysicalCoroutine(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	pkg := testProg.addPackage(t, "example.com/emission/stacksavecoroutine", `package stacksavecoroutine
import "unsafe"
//llgo:link StackSave llgo.stackSave
func StackSave() unsafe.Pointer
func Child() {}
func Root() { _ = StackSave(); Child() }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	functionIDs.ArchiveReady = true
	root := pkg.ssa.Func("Root")
	child := pkg.ssa.Func("Child")
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse: ssaUniverse,
		FunctionIDs:      functionIDs,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == child {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, site ssa.CallInstruction) (bool, error) {
			if site == nil || site.Common() == nil {
				return false, nil
			}
			callee := site.Common().StaticCallee()
			if _, frozen := universe.Resolve(callee); callee == nil || !frozen {
				return false, nil
			}
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(site)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("Root plan = %+v, present=%t; want physical coroutine", rootPlan, ok)
	}
	err = validateCoroPhysicalABIWithUniverseCapabilities(root, rootPlan, plan, universe, true, true, false, false)
	if err == nil || !strings.Contains(err.Error(), "cannot retain a native resume-stack pointer") {
		t.Fatalf("physical StackSave preflight error = %v; want native resume-stack rejection", err)
	}
}
