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
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroClosedInterfaceAwaitAdaptsZeroSizedPointerReceiver(t *testing.T) {
	const source = `package foo

var gate chan byte

type Runner interface {
	Run() int
	Close()
}

type Zero struct{}

func (Zero) Run() int {
	<-gate
	return 7
}

func (*Zero) Close() {}

func Keep() Runner { return &Zero{} }

func Root(runner Runner) int { return runner.Run() }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	program := newLLSSAProg(t)
	defer program.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		program, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{CoroProfile: CoroProfileStackless},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	invoke := coroInterfaceDispatchFindInvoke(t, root)
	var declared, wrapper *ssa.Function
	for _, function := range universe.Functions() {
		if function == nil || function.Name() != "Run" || function.Signature == nil || function.Signature.Recv() == nil {
			continue
		}
		_, pointer := types.Unalias(function.Signature.Recv().Type()).Underlying().(*types.Pointer)
		switch {
		case !pointer && function.Synthetic == "":
			declared = function
		case pointer && strings.Contains(function.Synthetic, "wrapper"):
			wrapper = function
		}
	}
	if declared == nil || wrapper == nil {
		t.Fatalf("zero-size pointer-promotion methods: declared=%v wrapper=%v", declared, wrapper)
	}

	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    coro.DynamicCHAClosed,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := resolveCoroInterfaceDispatchPlan(plan, universe, invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch.candidates) != 1 {
		t.Fatalf("zero-size interface candidates = %d, want one: %+v", len(dispatch.candidates), dispatch.candidates)
	}
	candidate := dispatch.candidates[0]
	dynamicPointer, pointer := types.Unalias(candidate.receiver).Underlying().(*types.Pointer)
	declaredReceiver := declared.Signature.Recv().Type()
	if !pointer || !types.Identical(dynamicPointer.Elem(), declaredReceiver) || candidate.function != wrapper ||
		candidate.methodEntry != wrapper || !types.Identical(candidate.targetReceiver, candidate.receiver) {
		t.Fatalf(
			"zero-size receiver adaptation = dynamic:%s target:%s function:%v entry:%v; want the exact *Zero method-set wrapper",
			candidate.receiver, candidate.targetReceiver, candidate.function, candidate.methodEntry,
		)
	}
	if size := program.SizeOf(program.Type(declaredReceiver, llssa.InGo)); size != 0 {
		t.Fatalf("declared receiver %s has size %d, want zero", declaredReceiver, size)
	}
	adaptation := false
	for _, block := range wrapper.Blocks {
		for _, instruction := range block.Instrs {
			load, ok := instruction.(*ssa.UnOp)
			if ok && load.Op == token.MUL && types.Identical(load.Type(), declaredReceiver) {
				adaptation = true
			}
		}
	}
	if !adaptation {
		t.Fatalf("pointer method-set wrapper %v has no exact *Zero -> Zero SSA adaptation", wrapper)
	}

	compilation := coroClosedInterfacePlainCompilation(plan, universe)
	compilation.CoroProfile = CoroProfileStackless
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	compiled, _, err := NewPackageExWithEmbedOptions(
		program, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile zero-size interface await: %v", err)
	}
	module := compiled.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify zero-size interface await before CoroSplit: %v\n%s", err, module.String())
	}
	if ir := module.String(); strings.Contains(ir, "AssertNilDeref") {
		t.Fatalf("zero-size interface module retained a native-stack nil assertion:\n%s", ir)
	}
	rootIR := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	if !strings.Contains(rootIR, "call void @"+coroAwaitPrepareHookV1) ||
		!strings.Contains(rootIR, "call i8 @llvm.coro.suspend") {
		t.Fatalf("zero-size interface dispatch did not use structured child-await lowering:\n%s", rootIR)
	}
	var wrapperCoro llvm.Value
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		if strings.Contains(function.Name(), "$llgo$promoted$") && strings.HasSuffix(function.Name(), "$coro") {
			if !wrapperCoro.IsNil() {
				t.Fatalf("multiple promoted coroutine wrappers: %q and %q", wrapperCoro.Name(), function.Name())
			}
			wrapperCoro = function
		}
	}
	if wrapperCoro.IsNil() {
		t.Fatalf("zero-size value receiver has no promoted coroutine wrapper:\n%s", module.String())
	}
	wrapperIR := wrapperCoro.String()
	if strings.Contains(wrapperIR, "AssertNilDeref") ||
		!strings.Contains(wrapperIR, "call void @"+coroFaultPrepareHookV1) ||
		!strings.Contains(wrapperIR, "call void @"+coroAwaitPrepareHookV1) {
		t.Fatalf("promoted wrapper did not lower pointer adaptation through structured fault/await edges:\n%s", wrapperIR)
	}

	runCoroABITestPipeline(t, program, module)
	resume := module.NamedFunction("foo.Root$coro.resume")
	if resume.IsNil() || strings.Contains(module.String(), "AssertNilDeref") {
		t.Fatalf("post-split zero-size receiver resume is absent or retained AssertNilDeref:\n%s", module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify zero-size interface await after CoroSplit: %v\n%s", err, module.String())
	}
}
