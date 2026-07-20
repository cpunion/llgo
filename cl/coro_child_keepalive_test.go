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
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
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

	parentIR := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
	consume := "call i32 @" + coroAwaitConsumeHookV1
	fakeUse := "call void (...) @llvm.fake.use(ptr "
	consumeAt, fakeUseAt := allTextIndexes(parentIR, consume), allTextIndexes(parentIR, fakeUse)
	if len(consumeAt) != 2 || len(fakeUseAt) != 2 {
		t.Fatalf("child completion consume/fake-use sites = %d/%d, want 2/2:\n%s", len(consumeAt), len(fakeUseAt), parentIR)
	}
	for index := range consumeAt {
		if fakeUseAt[index] <= consumeAt[index] || index+1 < len(consumeAt) && fakeUseAt[index] >= consumeAt[index+1] {
			t.Fatalf("fake-use %d does not follow its exact completion consume:\n%s", index, parentIR)
		}
	}

	runCoroABITestPipeline(t, prog, module)
	resume := module.NamedFunction("foo.Parent$coro.resume")
	if resume.IsNil() || strings.Count(resume.String(), fakeUse) != 2 {
		t.Fatalf("CoroSplit did not retain both completion-bound pointer owners:\n%s", module.String())
	}
}

func allTextIndexes(text, marker string) []int {
	var indexes []int
	for offset := 0; ; {
		index := strings.Index(text[offset:], marker)
		if index < 0 {
			return indexes
		}
		index += offset
		indexes = append(indexes, index)
		offset = index + len(marker)
	}
}
