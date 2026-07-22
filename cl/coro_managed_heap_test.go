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
	"bytes"
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl/blocks"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroManagedHeapFixture = `package foo

type Node struct {
	Value uint32
	Next *Node
}

type Empty struct{}

var ObservedWritten int64
var ObservedHandled bool

func Child(value uint32) uint32 { return value }

func Root(value uint32) *Node {
	first := &Node{Value: value}
	observed := Child(first.Value)
	second := &Node{Value: observed}
	first.Next = second
	return first
}

func Zero() *Empty { return &Empty{} }

func CapturedResults(value uint32) (written int64, err error, handled bool, node *Node) {
	defer func() {
		ObservedWritten = written
		_ = err
		ObservedHandled = handled
	}()
	node = &Node{Value: value}
	if value != 0 {
		node = &Node{Value: value + 1}
	}
	for index := uint32(0); index < value; index++ {
		node = &Node{Value: value + index}
	}
	written = int64(value)
	value = Child(value)
	handled = value != 0
	return
}

func Conditional(value uint32, allocate bool) *Node {
	if allocate {
		return &Node{Value: value}
	}
	return nil
}

func Loop(value, count uint32) *Node {
	var last *Node
	for index := uint32(0); index < count; index++ {
		last = &Node{Value: value + index}
	}
	return last
}
`

func TestCoroTerminalReconstructionAllocationSubset(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, coroManagedHeapFixture)
	captured := ssaPkg.Func("CapturedResults")
	selected, err := coroStaticTerminalReconstructionAllocations(captured)
	if err != nil {
		t.Fatal(err)
	}
	heap := coroManagedHeapAllocs(captured)
	if len(selected) != 3 || len(heap) < 6 {
		t.Fatalf("CapturedResults terminal/heap allocations = %d/%d, want 3/at least 6", len(selected), len(heap))
	}
	selectedSet := make(map[*ssa.Alloc]struct{}, len(selected))
	for index, allocation := range selected {
		selectedSet[allocation] = struct{}{}
		if allocation != heap[index] || allocation.Block() == nil || allocation.Block().Index != 0 {
			t.Fatalf("CapturedResults selected allocation %d = %v; want the same-order source-entry named-result heap cell", index, allocation)
		}
	}
	infos := blocks.Infos(captured.Blocks)
	ordinaryEntry, ordinaryBranch, ordinaryLoop := false, false, false
	for _, allocation := range heap {
		if _, selected := selectedSet[allocation]; selected {
			continue
		}
		block := allocation.Block()
		if block == nil {
			t.Fatalf("ordinary CapturedResults heap allocation has no source block: %v", allocation)
		}
		switch {
		case block.Index == 0:
			ordinaryEntry = true
		case block.Index >= 0 && block.Index < len(infos) && infos[block.Index].InLoop:
			ordinaryLoop = true
		default:
			ordinaryBranch = true
		}
	}
	if !ordinaryEntry || !ordinaryBranch || !ordinaryLoop {
		t.Fatalf("CapturedResults ordinary heap coverage: entry=%t branch=%t loop=%t", ordinaryEntry, ordinaryBranch, ordinaryLoop)
	}
	for _, name := range []string{"Root", "Conditional", "Loop"} {
		function := ssaPkg.Func(name)
		allocations := coroManagedHeapAllocs(function)
		if len(allocations) == 0 {
			t.Fatalf("%s fixture has no ordinary heap allocation", name)
		}
		selected, err := coroStaticTerminalReconstructionAllocations(function)
		if err != nil {
			t.Fatalf("%s collector: %v", name, err)
		}
		if len(selected) != 0 {
			t.Fatalf("%s ordinary entry/branch/loop allocations were selected as terminal results: %v", name, selected)
		}
	}
}

func TestCoroManagedHeapAllocationNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, ssaPkg, files, universe, plan := prepareCoroManagedHeapTestPlan(t, test.target)
			defer prog.Dispose()
			root := ssaPkg.Func("Root")
			zero := ssaPkg.Func("Zero")
			captured := ssaPkg.Func("CapturedResults")

			audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, "")
			if err != nil {
				t.Fatal(err)
			}
			audit.allowImplicitNilFault = true
			proof := audit.currentFrameRetentionProof()
			if got := proof.exactRootCapabilityProfile(); got != coroFrameRetentionExactRootProfileV2 {
				t.Fatalf("managed-heap root profile = %q", got)
			}
			if got := proof.exactRootCapabilityDigest(); len(got) != 64 {
				t.Fatalf("managed-heap root digest = %q", got)
			}
			heapAllocs := coroManagedHeapAllocs(root)
			if len(heapAllocs) != 2 || len(proof.managedHeapAllocations) != 2 {
				t.Fatalf("Root managed heap allocations: SSA=%d proof=%d, want 2/2", len(heapAllocs), len(proof.managedHeapAllocations))
			}
			for _, allocation := range heapAllocs {
				fact, managed := proof.managedHeapAllocations[allocation]
				if !managed || fact.zeroSized || fact.helper != "AllocZ" || fact.helperTarget == "" {
					t.Fatalf("managed allocation %q fact = %+v, present=%t", allocation, fact, managed)
				}
				if rootFact, rooted := proof.exactRoots[allocation]; !rooted || rootFact.kind != coroFrameRetentionRootManagedHeapAllocation {
					t.Fatalf("managed allocation %q exact root = %+v, present=%t", allocation, rootFact, rooted)
				}
				if reason := audit.validateAlloc(allocation); reason != "" {
					t.Fatalf("managed allocation %q rejected: %s", allocation, reason)
				}
			}

			pointerStore := false
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					store, ok := instruction.(*ssa.Store)
					if !ok || !coroTypeContainsGCPointer(store.Val.Type(), make(map[types.Type]bool)) {
						continue
					}
					addressRoot, reason := audit.stableAddressAt(store.Addr, store, make(map[ssa.Value]bool))
					if reason != "" || addressRoot != coroPhysicalAddressManagedHeap {
						t.Fatalf("pointer store address root=%d reason=%q; want exact managed heap", addressRoot, reason)
					}
					if reason := audit.validateStore(store); reason != "" {
						t.Fatalf("managed-heap pointer store rejected: %s", reason)
					}
					pointerStore = true
				}
			}
			if !pointerStore {
				t.Fatal("Root fixture has no pointer-containing managed-heap store")
			}

			zeroAudit, err := newCoroPhysicalPureSSAAudit(universe, plan, zero, "")
			if err != nil {
				t.Fatal(err)
			}
			zeroProof := zeroAudit.currentFrameRetentionProof()
			zeroAllocs := coroManagedHeapAllocs(zero)
			if len(zeroAllocs) != 1 {
				t.Fatalf("Zero heap allocations = %d, want 1", len(zeroAllocs))
			}
			if fact, ok := zeroProof.managedHeapAllocations[zeroAllocs[0]]; !ok || !fact.zeroSized || fact.helper != "" {
				t.Fatalf("zero-sized allocation fact = %+v, present=%t", fact, ok)
			}

			capturedAudit, err := newCoroPhysicalPureSSAAudit(universe, plan, captured, "")
			if err != nil {
				t.Fatal(err)
			}
			capturedProof := capturedAudit.currentFrameRetentionProof()
			cleanupPlan, err := prepareCoroStaticCleanupPlan(captured, plan, universe, "", true)
			if err != nil {
				t.Fatal(err)
			}
			if cleanupPlan == nil || len(cleanupPlan.terminalResultAllocations) != 3 ||
				!coroTerminalResultAllocationSetMatches(capturedProof, cleanupPlan.terminalResultAllocations) {
				t.Fatalf("CapturedResults cleanup/proof terminal allocation sets disagree: plan=%v proof=%v",
					cleanupPlan.terminalResultAllocations, capturedProof.terminalResultAllocations)
			}
			withoutTerminal := *capturedProof
			withoutTerminal.terminalResultAllocations = make(map[*ssa.Alloc]struct{})
			withoutDigest := coroFrameRetentionRootDigest(capturedAudit, &withoutTerminal)
			if withoutDigest == "" || withoutDigest == capturedProof.exactRootCapabilityDigest() {
				t.Fatalf("terminal reconstruction subset is absent from frame proof digest: with=%q without=%q",
					capturedProof.exactRootCapabilityDigest(), withoutDigest)
			}

			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroPreemptCompilation(compilation)
			compilation.EnableCoroExplicitStatusPanicABI = true
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			pkg, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify managed-heap coroutine before CoroSplit: %v\n%s", err, module.String())
			}
			rootPhysical := requireCoroPhysicalFunction(t, module, "foo.Root")
			rootIR := rootPhysical.String()
			if got := strings.Count(rootIR, "runtime.AllocZ"); got != 2 {
				t.Fatalf("Root AllocZ calls = %d, want 2 ordinary managed allocations:\n%s", got, rootIR)
			}
			rampEntry := rootPhysical.EntryBasicBlock()
			entryHeapCalls := 0
			for _, block := range rootPhysical.BasicBlocks() {
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					if instruction.InstructionOpcode() != llvm.Call || !strings.HasSuffix(instruction.CalledValue().Name(), "/runtime.AllocZ") {
						continue
					}
					entryHeapCalls++
					if instruction.InstructionParent() == rampEntry {
						t.Fatalf("ordinary Root AllocZ was incorrectly moved to the physical ramp entry:\n%s", rootIR)
					}
				}
			}
			if entryHeapCalls != 2 {
				t.Fatalf("Root ordinary AllocZ calls = %d, want 2:\n%s", entryHeapCalls, rootIR)
			}
			for _, forbidden := range []string{"AllocRoot", "alloca %foo.Node"} {
				if strings.Contains(rootIR, forbidden) {
					t.Fatalf("Root managed allocation incorrectly uses %q:\n%s", forbidden, rootIR)
				}
			}
			if !strings.Contains(rootIR, "foo.Child$coro") {
				t.Fatalf("Root does not suspend through Child after its first allocation:\n%s", rootIR)
			}
			capturedPhysical := requireCoroPhysicalFunction(t, module, "foo.CapturedResults")
			capturedIR := capturedPhysical.String()
			capturedHeapAllocs := coroManagedHeapAllocs(captured)
			if len(capturedHeapAllocs) < 6 {
				t.Fatalf("CapturedResults SSA heap allocations = %d, want three named-result cells plus entry/branch/loop objects", len(capturedHeapAllocs))
			}
			for _, allocation := range cleanupPlan.terminalResultAllocations {
				if allocation.Block() == nil || allocation.Block().Index != 0 {
					t.Fatalf("CapturedResults named-result allocation is outside SSA entry block: %s", allocation)
				}
			}
			if got := strings.Count(capturedIR, "runtime.AllocZ"); got != len(capturedHeapAllocs) {
				t.Fatalf("CapturedResults AllocZ calls = %d, want one per %d SSA heap allocations:\n%s", got, len(capturedHeapAllocs), capturedIR)
			}
			var publishBlock llvm.BasicBlock
			for _, block := range capturedPhysical.BasicBlocks() {
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					if instruction.InstructionOpcode() == llvm.Call && instruction.CalledValue().Name() == coroFramePublishHookV1 {
						publishBlock = instruction.InstructionParent()
					}
				}
			}
			if publishBlock.IsNil() {
				t.Fatalf("CapturedResults has no PhysicalABIV1 frame publication:\n%s", capturedIR)
			}
			hoistedHeapCalls, ordinaryHeapCalls := 0, 0
			for _, block := range capturedPhysical.BasicBlocks() {
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					if instruction.InstructionOpcode() != llvm.Call || !strings.HasSuffix(instruction.CalledValue().Name(), "/runtime.AllocZ") {
						continue
					}
					if instruction.InstructionParent() == publishBlock {
						hoistedHeapCalls++
					} else {
						ordinaryHeapCalls++
					}
				}
			}
			if hoistedHeapCalls != 3 || ordinaryHeapCalls != len(capturedHeapAllocs)-3 {
				t.Fatalf("CapturedResults hoisted/ordinary AllocZ calls = %d/%d, want 3/%d:\n%s",
					hoistedHeapCalls, ordinaryHeapCalls, len(capturedHeapAllocs)-3, capturedIR)
			}
			publish := strings.Index(capturedIR, "call void @"+coroFramePublishHookV1)
			alloc := strings.Index(capturedIR, "runtime.AllocZ")
			initialSuspend := strings.Index(capturedIR, "%coro.suspend = call i8 @llvm.coro.suspend")
			if publish < 0 || alloc < 0 || initialSuspend < 0 || publish >= alloc || alloc >= initialSuspend {
				t.Fatalf("CapturedResults terminal allocations are not ordered publish -> AllocZ -> initial suspend:\n%s", capturedIR)
			}
			if !strings.Contains(capturedIR, "foo.Child$coro") || !strings.Contains(capturedIR, "CapturedResults$1$coro") {
				t.Fatalf("CapturedResults does not suspend through both body and captured cleanup:\n%s", capturedIR)
			}

			runCoroABITestPipeline(t, prog, module)
			post := module.String()
			if strings.Contains(post, "AllocRoot") {
				t.Fatalf("CoroSplit changed managed allocation identity to AllocRoot:\n%s", post)
			}
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() {
				t.Fatal("CoroSplit did not emit foo.Root$coro.resume")
			}
			ramp := module.NamedFunction("foo.Root$coro")
			if ramp.IsNil() {
				t.Fatal("CoroSplit lost foo.Root$coro ramp")
			}
			rampIR := ramp.String()
			resumeIR := resume.String()
			if got := strings.Count(resumeIR, "runtime.AllocZ"); got != 2 ||
				strings.Contains(rampIR, "runtime.AllocZ") ||
				!strings.Contains(resumeIR, "foo.Child$coro") || !strings.Contains(resumeIR, ".reload") ||
				!strings.Contains(resumeIR, "store ptr") {
				t.Fatalf("CoroSplit moved ordinary Root AllocZ calls out of resume (resume AllocZ=%d):\nramp:\n%s\nresume:\n%s",
					got, rampIR, resumeIR)
			}
			capturedRamp := module.NamedFunction("foo.CapturedResults$coro")
			capturedResume := module.NamedFunction("foo.CapturedResults$coro.resume")
			if capturedRamp.IsNil() || capturedResume.IsNil() ||
				strings.Count(capturedRamp.String(), "runtime.AllocZ") != 3 ||
				strings.Count(capturedResume.String(), "runtime.AllocZ") != len(capturedHeapAllocs)-3 ||
				!strings.Contains(capturedResume.String(), ".reload") {
				t.Fatalf("CoroSplit did not keep three result AllocZ calls in the ramp and %d ordinary calls in resume:\nramp:\n%s\nresume:\n%s",
					len(capturedHeapAllocs)-3,
					capturedRamp.String(), capturedResume.String())
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit managed-heap coroutine object: %v\n%s", err, post)
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte("foo.Root$coro")) {
				t.Fatal("managed-heap object lost the Root coroutine symbol")
			}
		})
	}
}

func TestCoroManagedHeapAllocationRejectsPreciseShadowProfile(t *testing.T) {
	prog, ssaPkg, _, universe, plan := prepareCoroManagedHeapTestPlan(t, nil)
	defer prog.Dispose()
	root := ssaPkg.Func("Root")
	old := emitShadowStackInstrumentation
	emitShadowStackInstrumentation = true
	defer func() { emitShadowStackInstrumentation = old }()
	audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, "")
	if err != nil {
		t.Fatal(err)
	}
	proof := audit.currentFrameRetentionProof()
	if proof.exactRootCapabilityProfile() != "" || len(proof.managedHeapAllocations) != 0 || len(proof.exactRetainedRoots()) != 0 {
		t.Fatalf("precise/shadow profile received managed heap roots: profile=%q managed=%d roots=%d",
			proof.exactRootCapabilityProfile(), len(proof.managedHeapAllocations), len(proof.exactRetainedRoots()))
	}
	allocations := coroManagedHeapAllocs(root)
	if len(allocations) == 0 || !strings.Contains(audit.validateAlloc(allocations[0]), "non-moving conservative-or-no-GC") {
		t.Fatalf("precise/shadow managed allocation rejection = %q", audit.validateAlloc(allocations[0]))
	}
}

func prepareCoroManagedHeapTestPlan(t *testing.T, target *llssa.Target) (
	llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coro.SSAPlan,
) {
	t.Helper()
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocZ(size uintptr) unsafe.Pointer {
	if size == 0 { return nil }
	return nil
}
func AllocU(size uintptr) unsafe.Pointer {
	if size == 0 { return nil }
	return nil
}
`)
	fooPkg := testProg.addPackage(t, "foo", coroManagedHeapFixture)
	testProg.ssa.Build()
	ssaPkg := fooPkg.ssa
	files := []*ast.File{fooPkg.file}
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: ssaPkg, Files: files},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	functionIDs.ArchiveReady = true
	root, zero, child, captured := ssaPkg.Func("Root"), ssaPkg.Func("Zero"), ssaPkg.Func("Child"), ssaPkg.Func("CapturedResults")
	var capturedCleanup *ssa.Function
	for _, block := range captured.Blocks {
		for _, instruction := range block.Instrs {
			deferred, ok := instruction.(*ssa.Defer)
			if !ok {
				continue
			}
			closure, _ := deferred.Common().Value.(*ssa.MakeClosure)
			capturedCleanup, _ = closure.Fn.(*ssa.Function)
			break
		}
		if capturedCleanup != nil {
			break
		}
	}
	if capturedCleanup == nil {
		prog.Dispose()
		t.Fatal("CapturedResults fixture has no exact captured cleanup target")
	}
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: root, Demand: coro.AsyncDemand},
		{Function: zero, Demand: coro.AsyncDemand},
		{Function: captured, Demand: coro.AsyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == child || fn == capturedCleanup {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, ssaPkg, files, universe, plan
}

func coroManagedHeapAllocs(fn *ssa.Function) []*ssa.Alloc {
	var allocations []*ssa.Alloc
	if fn == nil {
		return allocations
	}
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if allocation, ok := instruction.(*ssa.Alloc); ok && allocation.Heap {
				allocations = append(allocations, allocation)
			}
		}
	}
	return allocations
}
