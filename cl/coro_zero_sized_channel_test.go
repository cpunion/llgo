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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroZeroSizedChannelSource = `package foo

func Recv(ch chan struct{}) struct{} {
	return <-ch
}

func Select(first, second chan struct{}) (int, struct{}, bool) {
	select {
	case value, ok := <-first:
		return 0, value, ok
	case <-second:
		return 1, struct{}{}, true
	}
}
`

func TestCoroZeroSizedChannelResultsUseKnownNonNilStorage(t *testing.T) {
	program, pkg := compileCoroZeroSizedChannelFixture(t)
	defer program.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify zero-sized channel lowering before CoroSplit: %v\n%s", err, module.String())
	}
	recv := requireCoroPhysicalFunction(t, module, "foo.Recv").String()
	if !strings.Contains(recv, "github.com/goplus/llgo/runtime/internal/runtime.CoroChanTryRecv") ||
		!strings.Contains(recv, "@"+coroChanRecvParkHookV1) {
		t.Fatalf("direct zero-sized receive did not use coroutine channel lowering:\n%s", recv)
	}
	selected := requireCoroPhysicalFunction(t, module, "foo.Select").String()
	for _, helper := range []string{
		"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectTry",
		"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectPark",
		"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectResume",
	} {
		if !strings.Contains(selected, helper) {
			t.Fatalf("zero-sized select result lacks %q:\n%s", helper, selected)
		}
	}
	if ir := module.String(); strings.Contains(ir, "AssertNilDeref") {
		t.Fatalf("compiler-owned zero-sized channel result storage retained AssertNilDeref:\n%s", ir)
	}

	runCoroABITestPipeline(t, program, module)
	for _, name := range []string{"foo.Recv$coro.resume", "foo.Select$coro.resume"} {
		if function := module.NamedFunction(name); function.IsNil() {
			t.Fatalf("CoroSplit did not create %q:\n%s", name, module.String())
		}
	}
	if ir := module.String(); strings.Contains(ir, "AssertNilDeref") {
		t.Fatalf("post-split zero-sized channel module retained AssertNilDeref:\n%s", ir)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify zero-sized channel lowering after CoroSplit: %v\n%s", err, module.String())
	}
}

func compileCoroZeroSizedChannelFixture(t *testing.T) (llssa.Program, llssa.Package) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroZeroSizedChannelSource)
	program := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		program, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{CoroProfile: CoroProfileStackless},
	)
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	functions := []*ssa.Function{ssaPkg.Func("Recv"), ssaPkg.Func("Select")}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	roots := make(coro.Roots, 0, len(functions))
	for _, function := range functions {
		roots = append(roots, coro.Root{Function: function, Demand: coro.AsyncDemand})
	}
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, roots, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,

		CoroABI:      coro.PhysicalABIV1,
		SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI:     coro.PanicExplicitStatusABIV0,
		FuncRepABI:   coro.FuncRepABIV1, CoroProfile: CoroProfileStackless,
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		program, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	return program, pkg
}
