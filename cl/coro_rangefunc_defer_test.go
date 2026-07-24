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

func TestCoroRangeFuncDefersUseOwnerDynamicCleanup(t *testing.T) {
	const source = `package foo
func seq(yield func(int) bool) {
	for i := 0; i < 4; i++ {
		if !yield(i) { return }
	}
}
func save(int) {}
func Root() {
	defer save(9)
	for i := range seq {
		defer save(i)
		if i == 2 { panic("boom") }
	}
}
`
	testProgram := newEmissionTestProgram()
	testProgram.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePackage := testProgram.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocU(size uintptr) unsafe.Pointer { return nil }
func AllocZ(size uintptr) unsafe.Pointer { return nil }
func FreeDeferNode(pointer unsafe.Pointer) {}
func AssertNilDeref(value bool) {}
func Panic(value any) {}
func strequal(left, right string) bool { return false }
func memequalptr(left, right *byte) bool { return false }
`)
	fooPackage := testProgram.addPackage(t, "foo", source)
	testProgram.ssa.Build()
	ssaPkg := fooPackage.ssa
	files := []*ast.File{fooPackage.file}
	program := newLLSSAProg(t)
	defer program.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(program, nil, []EmissionPackage{
		{SSA: runtimePackage.ssa, Files: []*ast.File{runtimePackage.file}},
		{SSA: ssaPkg, Files: files},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: ssaPkg.Func("Root"), Demand: coro.AsyncDemand},
		{Function: ssaPkg.Func("init"), Demand: coro.SyncDemand},
		{Function: runtimePackage.ssa.Func("Panic"), RawPlainDemand: true},
		{Function: runtimePackage.ssa.Func("strequal"), RawPlainDemand: true},
		{Function: runtimePackage.ssa.Func("memequalptr"), RawPlainDemand: true},
		{Function: runtimePackage.ssa.Func("AssertNilDeref"), RawPlainDemand: true},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    coro.DynamicCHAClosed,
		MaxPlainInstructions: -1,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == runtimePackage.ssa.Func("Panic") ||
				function == runtimePackage.ssa.Func("strequal") ||
				function == runtimePackage.ssa.Func("memequalptr") ||
				function == runtimePackage.ssa.Func("AssertNilDeref") {
				return coro.SSAFunctionPolicy{RawPlainEntry: true}, nil
			}
			if function == ssaPkg.Func("save") {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	compilation.FuncRepABI = coro.FuncRepABIV1
	pkg, _, err := NewPackageExWithEmbedOptions(
		program, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify rangefunc cleanup: %v\n%s", err, module.String())
	}
	root := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	yield := requireCoroPhysicalFunction(t, module, "foo.Root$1").String()
	for _, forbidden := range []string{"Sigsetjmp", "SetThreadDefer", "GetThreadDefer", "runtime.RunDefers"} {
		if strings.Contains(root, forbidden) || strings.Contains(yield, forbidden) {
			t.Fatalf("stackless rangefunc cleanup retained legacy defer machinery %q", forbidden)
		}
	}
	if !strings.Contains(root, "FreeDeferNode") || !strings.Contains(root, "switch i32") ||
		!strings.Contains(root, "foo.save$coro") {
		t.Fatalf("rangefunc owner lacks its shared cleanup drainer:\n%s", root)
	}
	if !strings.Contains(yield, "AllocU") || strings.Contains(yield, "FreeDeferNode") {
		t.Fatalf("rangefunc yield does not publish into the owner stack without draining it:\n%s", yield)
	}
	runCoroABITestPipeline(t, program, module)
	if module.NamedFunction("foo.Root$coro.resume").IsNil() ||
		module.NamedFunction("foo.Root$1$coro.resume").IsNil() {
		t.Fatalf("CoroSplit lost rangefunc owner or yield resume entry:\n%s", module.String())
	}
}
