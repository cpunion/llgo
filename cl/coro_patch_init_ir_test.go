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
	"regexp"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/goplus/llgo/internal/typepatch"
	"github.com/goplus/llgo/ssa/abi"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

type patchInitCFGSnapshot struct {
	blocks []*ssa.BasicBlock
	succs  [][]*ssa.BasicBlock
}

func snapshotPatchInitCFG(fn *ssa.Function) patchInitCFGSnapshot {
	snapshot := patchInitCFGSnapshot{
		blocks: append([]*ssa.BasicBlock(nil), fn.Blocks...),
		succs:  make([][]*ssa.BasicBlock, len(fn.Blocks)),
	}
	for index, block := range fn.Blocks {
		snapshot.succs[index] = append([]*ssa.BasicBlock(nil), block.Succs...)
	}
	return snapshot
}

func assertPatchInitCFGUnchanged(t *testing.T, phase, name string, fn *ssa.Function, before patchInitCFGSnapshot) {
	t.Helper()
	if len(fn.Blocks) != len(before.blocks) {
		t.Fatalf("%s %s blocks = %d, want unchanged %d", phase, name, len(fn.Blocks), len(before.blocks))
	}
	for index, block := range fn.Blocks {
		if block != before.blocks[index] {
			t.Fatalf("%s %s block %d identity changed", phase, name, index)
		}
		if len(block.Succs) != len(before.succs[index]) {
			t.Fatalf("%s %s block %d successors = %d, want unchanged %d", phase, name, index, len(block.Succs), len(before.succs[index]))
		}
		for successor, got := range block.Succs {
			if want := before.succs[index][successor]; got != want {
				t.Fatalf("%s %s block %d successor %d = %p, want unchanged %p", phase, name, index, successor, got, want)
			}
		}
	}
}

func patchInitDirectCallCount(body, symbol string) int {
	pattern := regexp.MustCompile(`(?m)^\s*(?:%[-a-zA-Z$._0-9]+\s*=\s*)?(?:musttail\s+|tail\s+)?call\b[^\n]*@"?` + regexp.QuoteMeta(symbol) + `"?\(`)
	return len(pattern.FindAllStringIndex(body, -1))
}

func requirePatchInitDirectCall(t *testing.T, owner, body, target string) {
	t.Helper()
	if count := patchInitDirectCallCount(body, target); count != 1 {
		t.Fatalf("%s direct calls to %q = %d, want exactly one:\n%s", owner, target, count, body)
	}
}

func forbidPatchInitDirectCall(t *testing.T, owner, body, target string) {
	t.Helper()
	if count := patchInitDirectCallCount(body, target); count != 0 {
		t.Fatalf("%s directly calls forbidden target %q %d time(s):\n%s", owner, target, count, body)
	}
}

func TestCoroPatchInitIRUsesPublicThenPrivateSymbolsWithoutMutatingSSA(t *testing.T) {
	const (
		patchedPath  = "example.com/emission/patchir"
		importerPath = "example.com/emission/patchirimporter"
	)
	testProg := newEmissionTestProgram()
	original := testProg.addPackage(t, patchedPath, `package patchir

var Original = originalValue()

func originalValue() int {
	Yield()
	return 1
}

func Yield() {}
`)
	alternate := testProg.addPackage(t, abi.PatchPathPrefix+patchedPath, `package patchir

var Patched = patchedValue()

func patchedValue() int { return 2 }
`)
	importer := testProg.addPackage(t, importerPath, `package patchirimporter

import _ "example.com/emission/patchir"

var Ready = true
`)
	testProg.ssa.Build()

	originalInit := original.ssa.Func("init")
	publicInit := alternate.ssa.Func("init")
	importerInit := importer.ssa.Func("init")
	if originalInit == nil || publicInit == nil || importerInit == nil {
		t.Fatalf("fixture initializers = original %v, public %v, importer %v", originalInit, publicInit, importerInit)
	}
	watched := []struct {
		name     string
		function *ssa.Function
		before   patchInitCFGSnapshot
	}{
		{name: "original init", function: originalInit, before: snapshotPatchInitCFG(originalInit)},
		{name: "public patch init", function: publicInit, before: snapshotPatchInitCFG(publicInit)},
		{name: "importer init", function: importerInit, before: snapshotPatchInitCFG(importerInit)},
	}
	assertUnchanged := func(phase string) {
		t.Helper()
		for _, function := range watched {
			assertPatchInitCFGUnchanged(t, phase, function.name, function.function, function.before)
		}
	}

	patches := Patches{patchedPath: {
		Alt:   alternate.ssa,
		Types: typepatch.Clone(alternate.types),
	}}
	patchedFiles := []*ast.File{original.file, alternate.file}
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, patches, []EmissionPackage{
		{SSA: original.ssa, Files: patchedFiles},
		{SSA: importer.ssa, Files: []*ast.File{importer.file}},
	})
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
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{
		{Function: importerInit, Demand: coro.AsyncDemand},
		// Build orchestration roots every public patch initializer independently:
		// no unpatched source function object denotes that public symbol.
		{Function: publicInit, Demand: coro.AsyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			_, _, redirected, err := universe.CoroPatchInitRedirect(call)
			return redirected, err
		},
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == original.ssa.Func("Yield") {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertUnchanged("after analysis")
	for name, fn := range map[string]*ssa.Function{
		"original init":     originalInit,
		"public patch init": publicInit,
		"importer init":     importerInit,
	} {
		functionPlan, present := plan.FunctionPlan(fn)
		if !present || functionPlan.Emission != coro.EmitCoroutine || functionPlan.FuncRep != coro.DirectCoro || functionPlan.Demand != coro.AsyncDemand {
			t.Fatalf("%s plan = %+v, present=%t; want async-only direct coroutine", name, functionPlan, present)
		}
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	tracking := NewCallerTracking()
	patchedLL, _, err := NewPackageExWithEmbedOptions(
		prog, tracking, patches, nil, original.ssa, patchedFiles, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile patched package: %v", err)
	}
	importerLL, _, err := NewPackageExWithEmbedOptions(
		prog, tracking, patches, nil, importer.ssa, []*ast.File{importer.file}, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile importer package: %v", err)
	}
	assertUnchanged("after compilation")

	patchedModule := patchedLL.Module()
	defer patchedModule.Dispose()
	importerModule := importerLL.Module()
	defer importerModule.Dispose()
	for name, module := range map[string]llvm.Module{"patched": patchedModule, "importer": importerModule} {
		if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
			t.Fatalf("verify %s module: %v\n%s", name, err, module.String())
		}
	}

	publicSymbol := patchedPath + ".init$coro"
	privateSymbol := patchedPath + ".init$hasPatch$coro"
	importerSymbol := importerPath + ".init$coro"
	public := patchedModule.NamedFunction(publicSymbol)
	private := patchedModule.NamedFunction(privateSymbol)
	if public.IsNil() || public.FirstBasicBlock().IsNil() || private.IsNil() || private.FirstBasicBlock().IsNil() {
		t.Fatalf("patch init definitions = public %v, private %v; want bodyful %q and %q\n%s", public, private, publicSymbol, privateSymbol, patchedModule.String())
	}
	importerEntry := importerModule.NamedFunction(importerSymbol)
	if importerEntry.IsNil() || importerEntry.FirstBasicBlock().IsNil() {
		t.Fatalf("importer init definition %q is absent:\n%s", importerSymbol, importerModule.String())
	}

	importerIR := importerEntry.String()
	publicIR := public.String()
	privateIR := private.String()
	requirePatchInitDirectCall(t, "importer init", importerIR, publicSymbol)
	requirePatchInitDirectCall(t, "public patch init", publicIR, privateSymbol)
	for _, target := range []string{importerPath + ".init", importerSymbol, privateSymbol} {
		forbidPatchInitDirectCall(t, "importer init", importerIR, target)
	}
	for _, target := range []string{patchedPath + ".init", publicSymbol} {
		forbidPatchInitDirectCall(t, "public patch init", publicIR, target)
	}
	for _, target := range []string{patchedPath + ".init$hasPatch", privateSymbol, publicSymbol} {
		forbidPatchInitDirectCall(t, "private original init", privateIR, target)
	}
}
