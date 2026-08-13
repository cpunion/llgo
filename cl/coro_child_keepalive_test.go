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
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroChildAwaitKeepsPointerDerivedUintptrOwnerThroughCompletion(t *testing.T) {
	const source = `package foo
import "unsafe"
var sink uintptr
func Child(word uintptr) { sink = word }
func Parent(pointer *byte) {
	if pointer != nil {
		Child(uintptr(unsafe.Pointer(pointer)))
	}
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
	parent, child := ssaPkg.Func("Parent"), ssaPkg.Func("Child")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: parent, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
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

	var childCall *ssa.Call
	for _, block := range parent.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common().StaticCallee() == child {
				childCall = call
			}
		}
	}
	if childCall == nil {
		t.Fatal("fixture has no Parent -> Child SSA call")
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, plan, parent, CoroFrameRetentionParkABIV2)
	if err != nil {
		t.Fatal(err)
	}
	roots := audit.currentFrameRetentionProof().exactCallKeepaliveRoots(childCall)
	if len(roots) != 1 || roots[0] != parent.Params[0] {
		t.Fatalf("child await keepalive roots = %v, want exact pointer parameter", rootNames(roots))
	}

	compilation := &Compilation{
		CoroPlan:              plan,
		EmissionUniverse:      universe,
		CoroFrameRetentionABI: CoroFrameRetentionParkABIV2,
	}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify child keepalive before CoroSplit: %v\n%s", err, module.String())
	}

	parentRamp := requireCoroPhysicalFunction(t, module, "foo.Parent")
	consumeCount, ownerUseCount := coroChildAwaitCompletionOwnerUseCounts(parentRamp)
	if consumeCount != 2 || ownerUseCount != 2 {
		t.Fatalf("child completion consume/owner fake-use sites = %d/%d, want 2/2:\n%s",
			consumeCount, ownerUseCount, parentRamp.String())
	}

	runCoroABITestPipeline(t, prog, module)
	resume := module.NamedFunction("foo.Parent$coro.resume")
	consumeCount, ownerUseCount = coroChildAwaitCompletionOwnerUseCounts(resume)
	if resume.IsNil() || consumeCount != 2 || ownerUseCount != 2 {
		t.Fatalf("CoroSplit did not retain both completion-bound pointer owners:\n%s", module.String())
	}
}

func coroChildAwaitCompletionOwnerUseCounts(function llvm.Value) (consumes, ownerUses int) {
	if function.IsNil() {
		return 0, 0
	}
	for _, block := range function.BasicBlocks() {
		awaitingOwnerUse := false
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Call {
				continue
			}
			switch instruction.CalledValue().Name() {
			case coroAwaitConsumeHookV1:
				consumes++
				awaitingOwnerUse = true
			case "llvm.fake.use":
				// The scheduler's scalar run-decision scratch may also need an
				// llvm.fake.use. The pointer owner retained across this exact
				// child await is distinguished by its post-consume frame load.
				if awaitingOwnerUse && instruction.OperandsCount() > 1 &&
					instruction.Operand(0).InstructionOpcode() == llvm.Load {
					ownerUses++
					awaitingOwnerUse = false
				}
			}
		}
	}
	return consumes, ownerUses
}
