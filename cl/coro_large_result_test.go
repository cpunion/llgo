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
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroLargeAwaitResultUsesManagedFrameSlot(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocZ(size uintptr) unsafe.Pointer { return nil }
`)
	fooPkg := testProg.addPackage(t, "foo", `package foo
const size = 128*1024 + 1
var result [size]byte
func Child() [size]byte { return result }
func Parent() byte { return Child()[1] }
`)
	testProg.ssa.Build()

	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: fooPkg.ssa, Files: []*ast.File{fooPkg.file}},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(fooPkg.ssa.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	parent, child := fooPkg.ssa.Func("Parent"), fooPkg.ssa.Func("Child")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(fooPkg.ssa.Prog, coro.Roots{{
		Function: parent,
		Demand:   coro.AsyncDemand,
	}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		OutcomeMode:          coro.OutcomeExplicitStatus,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == child {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target, frozen, err := universe.ResolveCoroLoweredCall(parent, coroManagedFrameSlotAllocZCall); err != nil {
		t.Fatal(err)
	} else if !frozen || target != runtimePkg.ssa.Func("AllocZ") {
		t.Fatalf("managed result-slot helper target = %v, frozen=%t; want runtime.AllocZ", target, frozen)
	}
	if _, frozen, err := universe.ResolveCoroPlainLoweredCall(parent, coroManagedFrameSlotAllocZCall); err != nil {
		t.Fatal(err)
	} else if frozen {
		t.Fatal("managed result-slot helper leaked into the plain call recipe")
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, fooPkg.ssa, []*ast.File{fooPkg.file}, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify large-result coroutine before CoroSplit: %v\n%s", err, module.String())
	}
	parentIR := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
	if got := strings.Count(parentIR, `runtime.AllocZ"`); got != 1 {
		t.Fatalf("large child result AllocZ calls = %d, want one managed result slot:\n%s", got, parentIR)
	}
	if strings.Contains(parentIR, "alloca { [131073 x i8] }") {
		t.Fatalf("large child result remained an inline ramp allocation:\n%s", parentIR)
	}
	if strings.Contains(parentIR, "store [131073 x i8]") {
		t.Fatalf("direct indexing copied the complete awaited aggregate:\n%s", parentIR)
	}

	runCoroABITestPipeline(t, prog, module)
	parentRamp := module.NamedFunction("foo.Parent$coro")
	if got := coroFrameAllocationSize(t, parentRamp, prog.PointerSize()*8); got >= 128*1024 {
		t.Fatalf("large child result inflated the split coroutine frame to %d bytes", got)
	}
	for _, line := range strings.Split(module.String(), "\n") {
		if strings.HasPrefix(line, `%"foo.Parent$coro.Frame" = type {`) &&
			strings.Contains(line, "[131073 x i8]") {
			t.Fatalf("large child result remained inline in the split coroutine frame: %s", line)
		}
	}
}
