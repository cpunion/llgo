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
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestLegacySigjmpIntrinsicsFreezeNativeTypedControl(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func Sigsetjmp(unsafe.Pointer, int32) int32 { return 0 }
func Siglongjmp(unsafe.Pointer, int32) {}
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/sigjmp", `package sigjmp
import "unsafe"
//llgo:link Sigjmpbuf llgo.sigjmpbuf
func Sigjmpbuf() unsafe.Pointer
//llgo:link Sigsetjmp llgo.sigsetjmp
func Sigsetjmp(unsafe.Pointer, int32) int32
//llgo:link Siglongjmp llgo.siglongjmp
func Siglongjmp(unsafe.Pointer, int32)
func Use() int32 {
	buf := Sigjmpbuf()
	value := Sigsetjmp(buf, 0)
	Siglongjmp(buf, 1)
	return value
}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		t.Fatal(err)
	}
	owner := callerPkg.ssa.Func("Use")
	lowered, err := universe.CoroLoweredCalls(owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered) != 0 {
		t.Fatalf("typed sigjmp lowered calls = %+v; want no hidden runtime leaves", lowered)
	}
	want := map[string]CoroControlOperation{
		"Sigjmpbuf":  CoroControlNone,
		"Sigsetjmp":  CoroControlReturnsTwice,
		"Siglongjmp": CoroControlNonlocalJump,
	}
	for _, call := range allocaCStrTestCalls(owner) {
		plan, frozen, err := universe.CoroCallSitePlan(call)
		callee := call.Common().StaticCallee()
		expected, known := want[callee.Name()]
		if err != nil || !frozen || !known || !plan.Intrinsic ||
			plan.IntrinsicSemantics != CoroIntrinsicCallInlineNoSuspend ||
			plan.Elision != CoroCallElidedIntrinsic ||
			plan.ControlOperation != expected {
			t.Fatalf("typed sigjmp call %q plan = %+v, %v, %v; want control %s", call, plan, frozen, err, expected)
		}
		if expected != CoroControlNone && expected.ExecFlags() != coro.IRQUnsafe {
			t.Fatalf("typed sigjmp operation %s exec = %s, want irq-unsafe", expected, expected.ExecFlags())
		}
	}
}

func TestProcessControlIntrinsicsFreezeExactTypedOperations(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/control", `package control
//llgo:link Fork llgo.controlFork
func Fork() int32
//llgo:link Execve llgo.controlExecve
func Execve(*int8, **int8, **int8) int32
//llgo:link Exit llgo.controlExit
func Exit(int32)
//llgo:link Trap llgo.controlTrap
func Trap()
func Use(path *int8, argv **int8, envp **int8, status int32, trap bool) int32 {
	pid := Fork()
	if trap { Trap() }
	if status != 0 { Exit(status) }
	if pid != 0 { return pid }
	return Execve(path, argv, envp)
}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]CoroControlOperation{
		"Fork":   CoroControlProcessFork,
		"Execve": CoroControlProcessExec,
		"Exit":   CoroControlProcessExit,
		"Trap":   CoroControlTrap,
	}
	seen := make(map[string]bool, len(want))
	for _, call := range allocaCStrTestCalls(pkg.ssa.Func("Use")) {
		callee := call.Common().StaticCallee()
		if callee == nil {
			continue
		}
		expected, relevant := want[callee.Name()]
		if !relevant {
			continue
		}
		plan, frozen, err := universe.CoroCallSitePlan(call)
		if err != nil || !frozen || !plan.Intrinsic ||
			plan.IntrinsicSemantics != CoroIntrinsicCallInlineNoSuspend ||
			plan.Elision != CoroCallElidedIntrinsic ||
			plan.ControlOperation != expected ||
			plan.ControlOperation.ExecFlags() != coro.IRQUnsafe {
			t.Fatalf("control call %q plan = %+v, %v, %v; want %s", call, plan, frozen, err, expected)
		}
		seen[callee.Name()] = true
	}
	for name := range want {
		if !seen[name] {
			t.Fatalf("control operation %s was not frozen", name)
		}
	}
}

func TestTypedControlIntrinsicsRejectNonCanonicalShapes(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "returns-twice result",
			source: `package badcontrol
import "unsafe"
//llgo:link Control llgo.sigsetjmp
func Control(unsafe.Pointer, int32) int64
func Use(value unsafe.Pointer) { _ = Control(value, 0) }
`,
			want: "func(unsafe.Pointer, int32) int32",
		},
		{
			name: "fork result",
			source: `package badcontrol
//llgo:link Control llgo.controlFork
func Control() int64
func Use() { _ = Control() }
`,
			want: "func() int32",
		},
		{
			name: "exec pointer",
			source: `package badcontrol
//llgo:link Control llgo.controlExecve
func Control(*byte, **byte, **byte) int32
func Use(path *byte, argv **byte) { _ = Control(path, argv, argv) }
`,
			want: "func(*int8, **int8, **int8) int32",
		},
		{
			name: "exit status",
			source: `package badcontrol
//llgo:link Control llgo.controlExit
func Control(int)
func Use() { Control(2) }
`,
			want: "func(int32)",
		},
		{
			name: "trap argument",
			source: `package badcontrol
//llgo:link Control llgo.controlTrap
func Control(int32)
func Use() { Control(2) }
`,
			want: "func()",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			pkg := testProg.addPackage(
				t,
				"example.com/emission/badcontrol/"+strings.ReplaceAll(test.name, " ", "-"),
				test.source,
			)
			testProg.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
				SSA: pkg.ssa, Files: []*ast.File{pkg.file},
			}})
			if err != nil {
				t.Fatal(err)
			}
			call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
			if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil ||
				!intrinsic || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("bad typed-control semantics = _, %v, %v; want exact %s shape error", intrinsic, err, test.want)
			}
		})
	}
}

func TestNativeSigjmpControlFailsClosedInPhysicalCoroutine(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	pkg := testProg.addPackage(t, "example.com/emission/sigjmpcoroutine", `package sigjmpcoroutine
import "unsafe"
//llgo:link Sigsetjmp llgo.sigsetjmp
func Sigsetjmp(unsafe.Pointer, int32) int32
func Child() {}
func Root(value unsafe.Pointer) { _ = Sigsetjmp(value, 0); Child() }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
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
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{
		Function: root,
		Demand:   coro.AsyncDemand,
	}}, coro.SSAConfig{
		EmissionUniverse: ssaUniverse,
		FunctionIDs:      functionIDs,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == child {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			site, frozen, err := universe.CoroCallSitePlan(call)
			return frozen && site.ElidesCall(), err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rootPlan, planned := plan.FunctionPlan(root)
	if !planned || rootPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("sigjmp Root plan = %+v, present=%t; want physical coroutine", rootPlan, planned)
	}
	err = validateCoroPhysicalABIWithUniverseCapabilities(
		root, rootPlan, plan, universe, true, true, false, false,
	)
	if err == nil || !strings.Contains(err.Error(), "cannot retain a stackless coroutine resume activation") {
		t.Fatalf("physical sigjmp preflight error = %v; want native-activation rejection", err)
	}
}

func TestLegacySigjmpIntrinsicsFailClosedOnWasm(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	pkg := testProg.addPackage(t, "example.com/emission/sigjmpwasm", `package sigjmpwasm
import "unsafe"
//llgo:link Siglongjmp llgo.siglongjmp
func Siglongjmp(unsafe.Pointer, int32)
func Use(value unsafe.Pointer) { Siglongjmp(value, 1) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic || !strings.Contains(err.Error(), "requires a non-legacy coroutine PanicABI") {
		t.Fatalf("wasm legacy siglongjmp semantics = _, %v, %v; want PanicABI fail-closed error", intrinsic, err)
	}
}
