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
	"encoding/binary"
	"encoding/hex"
	"go/ast"
	"go/types"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroLeafPhysicalABIPresplit(t *testing.T) {
	prog, pkg := compileCoroLeafPhysicalABI(t, nil)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify coroutine leaf: %v\n%s", err, module.String())
	}
	ir := module.String()
	if module.NamedFunction("foo.Leaf").IsNil() == false {
		t.Fatalf("physical coroutine retained legacy source ABI symbol:\n%s", ir)
	}
	leaf := module.NamedFunction("foo.Leaf$coro")
	if leaf.IsNil() {
		t.Fatalf("physical coroutine symbol is absent:\n%s", ir)
	}
	leafIR := leaf.String()
	assertCoroV1TaskAwareFrameCalls(t, "Leaf", leafIR, prog.PointerSize()*8)
	if !regexp.MustCompile(`define ptr @"?foo\.Leaf\$coro"?\(ptr [^,]+, ptr [^,]+, i32 `).MatchString(leafIR) {
		t.Fatalf("coroutine leaf does not use (g, out, args...) -> handle ABI:\n%s", leafIR)
	}
	if got := strings.Count(leafIR, "call i8 @llvm.coro.suspend"); got != 2 {
		t.Fatalf("coro.suspend calls = %d, want initial + final:\n%s", got, leafIR)
	}
	begin := strings.Index(leafIR, "call ptr @llvm.coro.begin")
	publish := strings.Index(leafIR, "call void @"+coroFramePublishHookV3)
	initialSuspend := strings.Index(leafIR, "call i8 @llvm.coro.suspend")
	if begin < 0 || publish < 0 || initialSuspend < 0 || !(begin < publish && publish < initialSuspend) {
		t.Fatalf("promise/header was not published after coro.begin and before initial suspend:\n%s", leafIR)
	}
	if !strings.Contains(leafIR, "store i32") {
		t.Fatalf("coroutine result was not copied to the external result slot:\n%s", leafIR)
	}
	for _, symbol := range []string{coroFrameAllocHookV1, coroFrameFreeHookV1, coroDescriptorPrefixV1} {
		if !strings.Contains(ir, symbol) {
			t.Fatalf("coroutine module is missing versioned ABI symbol %q:\n%s", symbol, ir)
		}
	}
	for _, forbidden := range []string{"@malloc", "@free(", "stacksave", "stackrestore", "pthread"} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("coroutine leaf introduced forbidden stack/runtime coupling %q:\n%s", forbidden, ir)
		}
	}
}

func TestCoroLeafPhysicalABIZeroResult(t *testing.T) {
	prog, pkg := compileCoroLeafPhysicalABISource(t, nil, `package foo
func Leaf() {}
`)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify zero-result coroutine leaf: %v\n%s", err, module.String())
	}
	leaf := module.NamedFunction("foo.Leaf$coro")
	if leaf.IsNil() || !regexp.MustCompile(`define ptr @"?foo\.Leaf\$coro"?\(ptr [^,]+, ptr [^)]+\)`).MatchString(leaf.String()) {
		t.Fatalf("zero-result coroutine has the wrong physical ABI:\n%s", module.String())
	}
}

func TestCoroLeafPhysicalABICoroSplit(t *testing.T) {
	prog, pkg := compileCoroLeafPhysicalABI(t, nil)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify before CoroSplit: %v\n%s", err, module.String())
	}
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	const pipeline = "coro-early,cgscc(coro-split),coro-cleanup"
	if err := module.RunPasses(pipeline, prog.TargetMachine(), options); err != nil {
		t.Fatalf("run %s: %v\n%s", pipeline, err, module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify after CoroSplit: %v\n%s", err, module.String())
	}
	ir := module.String()
	for _, suffix := range []string{".resume", ".destroy"} {
		if module.NamedFunction("foo.Leaf$coro" + suffix).IsNil() {
			t.Fatalf("CoroSplit did not create coroutine %s entry:\n%s", suffix, ir)
		}
	}
	for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend", "llvm.coro.end"} {
		if regexp.MustCompile(`call [^\n]*@` + regexp.QuoteMeta(intrinsic) + `\b`).MatchString(ir) {
			t.Fatalf("post-split module still calls %s:\n%s", intrinsic, ir)
		}
	}
	if !strings.Contains(module.NamedFunction("foo.Leaf$coro.resume").String(), "store i32") {
		t.Fatalf("result-slot store did not move to the resume function:\n%s", ir)
	}
}

func TestCoroLeafPhysicalABIGlobalDebug(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkgWithMode(t, `package foo
func Leaf(value uint32) uint32 {
	next := value + 1
	return next
}
`, ssa.SanityCheckFunctions|ssa.InstantiateGenerics|ssa.GlobalDebug)
	leafSSA := ssaPkg.Func("Leaf")
	foundDebugRef := false
	for _, block := range leafSSA.Blocks {
		for _, instruction := range block.Instrs {
			if _, ok := instruction.(*ssa.DebugRef); ok {
				foundDebugRef = true
			}
		}
	}
	if !foundDebugRef {
		t.Fatal("GlobalDebug SSA did not contain a DebugRef")
	}

	oldDebug, oldDebugSyms := enableDbg, enableDbgSyms
	EnableDebug(true)
	EnableDbgSyms(true)
	defer func() {
		EnableDebug(oldDebug)
		EnableDbgSyms(oldDebugSyms)
	}()
	prog, pkg := compileCoroLeafPhysicalABIPackage(t, nil, ssaPkg, files)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify debug coroutine before CoroSplit: %v\n%s", err, module.String())
	}
	ir := module.String()
	if !strings.Contains(ir, "!dbg") {
		t.Fatalf("debug coroutine omitted function/parameter metadata:\n%s", ir)
	}
	parameter := regexp.MustCompile(`(?m)^(!\d+) = !DILocalVariable\(name: "value", arg: 1,`).FindStringSubmatch(ir)
	if len(parameter) != 2 {
		t.Fatalf("debug coroutine omitted source parameter metadata:\n%s", ir)
	}
	location := regexp.MustCompile(
		`(?m)(?:#dbg_(?:value|declare)|@llvm\.dbg\.(?:value|declare))\([^\n]*` +
			regexp.QuoteMeta(parameter[1]) + `(?:,|\))`,
	)
	if !location.MatchString(ir) {
		t.Fatalf("debug coroutine parameter metadata has no location record:\n%s", ir)
	}
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("coro-early,cgscc(coro-split),coro-cleanup", prog.TargetMachine(), options); err != nil {
		t.Fatalf("CoroSplit debug coroutine: %v\n%s", err, module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify debug coroutine after CoroSplit: %v\n%s", err, module.String())
	}
}

func TestCoroLeafPhysicalABIUsesTargetPointerWidth(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog, pkg := compileCoroLeafPhysicalABI(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if got := prog.PointerSize(); got != 4 {
		t.Fatalf("wasm pointer size = %d, want 4", got)
	}
	ir := module.String()
	for _, intrinsic := range []string{"size", "align"} {
		if !strings.Contains(ir, "@llvm.coro."+intrinsic+".i32") {
			t.Fatalf("wasm coroutine uses non-i32 %s intrinsic:\n%s", intrinsic, ir)
		}
	}
	if !regexp.MustCompile(
		`@__llgo_coro_frame_descriptor_v1\.[0-9a-f]+ = linkonce_odr unnamed_addr constant ` +
			`\{ i32, i32, i64, i64, i32, i32, \{ ptr, i32 \}, \{ ptr, i32 \} \}`,
	).MatchString(ir) {
		t.Fatalf("wasm descriptor does not use target-width size/alignment fields:\n%s", ir)
	}
}

func TestCoroChildAwaitPhysicalABIV1Presplit(t *testing.T) {
	prog, pkg := compileCoroChildAwaitPhysicalABI(t, nil)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify child-await coroutines: %v\n%s", err, module.String())
	}
	ir := module.String()
	parent := requireCoroPhysicalFunction(t, module, "foo.Parent")
	child := requireCoroPhysicalFunction(t, module, "foo.Child")
	parentIR, childIR := parent.String(), child.String()

	if got := strings.Count(parentIR, "call i8 @llvm.coro.suspend"); got != 3 {
		t.Fatalf("Parent coro.suspend calls = %d, want initial + await + final:\n%s", got, parentIR)
	}
	if got := strings.Count(childIR, "call i8 @llvm.coro.suspend"); got != 2 {
		t.Fatalf("Child coro.suspend calls = %d, want initial + final:\n%s", got, childIR)
	}
	for _, operation := range []string{"llvm.coro.resume", "llvm.coro.done", "llvm.coro.destroy"} {
		if got := len(regexp.MustCompile(`call [^\n]*@`+regexp.QuoteMeta(operation)+`\b`).FindAllString(parentIR, -1)); got != 1 {
			t.Fatalf("Parent direct static-inline %s calls = %d, want 1:\n%s", operation, got, parentIR)
		}
	}

	for _, hook := range []string{
		coroFrameAllocHookV1,
		coroFramePublishHookV3,
		coroAwaitPrepareHookV1,
		coroAwaitInlineBeginHookV2,
		coroAwaitInlineFinishHookV2,
		coroFrameDestroyCommitHookV2,
		coroAwaitInlineDestroyCommitHookV2,
		coroAwaitConsumeHookV1,
		coroPreemptPollHookV1,
		coroRunDecisionTakeZeroHookV1,
		coroCompletePrepareHookV2,
		coroFrameFreeHookV1,
	} {
		if !strings.Contains(ir, hook) {
			t.Fatalf("child-await module is missing PhysicalABIV1 hook %q:\n%s", hook, ir)
		}
	}
	for _, forbidden := range []string{coroFrameAllocHook, coroFrameFreeHook, coroDescriptorPrefix} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("PhysicalABIV1 module leaked v0 ABI symbol %q:\n%s", forbidden, ir)
		}
	}
	if got := strings.Count(ir, "call ptr @"+coroFrameAllocHookV1); got != 2 {
		t.Fatalf("task-aware v1 frame allocations = %d, want Parent + Child:\n%s", got, ir)
	}
	if got := strings.Count(ir, "call void @"+coroFramePublishHookV3); got != 2 {
		t.Fatalf("v1 frame publications = %d, want Parent + Child:\n%s", got, ir)
	}
	if got := strings.Count(ir, "call void @"+coroAwaitPrepareHookV1); got != 1 {
		t.Fatalf("v1 await preparations = %d, want one Parent->Child handoff:\n%s", got, ir)
	}
	if got := strings.Count(ir, "call i1 @"+coroAwaitInlineBeginHookV2); got != 1 {
		t.Fatalf("v1 inline await attempts = %d, want one Parent->Child fast path:\n%s", got, ir)
	}
	if got := strings.Count(ir, "call i1 @"+coroAwaitInlineFinishHookV2); got != 1 {
		t.Fatalf("v2 inline await finishes = %d, want one Parent->Child ownership settlement:\n%s", got, ir)
	}
	for _, hook := range []string{coroFrameDestroyCommitHookV2, coroAwaitInlineDestroyCommitHookV2} {
		if got := strings.Count(ir, "call void @"+hook); got != 1 {
			t.Fatalf("v2 inline await %s calls = %d, want one:\n%s", hook, got, ir)
		}
	}
	if got := strings.Count(ir, "call i32 @"+coroAwaitConsumeHookV1); got != 2 {
		t.Fatalf("v1 await outcome consume sites = %d, want normal/cancellation reconciliation:\n%s", got, ir)
	}
	if got := strings.Count(ir, "call void @"+coroCompletePrepareHookV2); got != 2 {
		t.Fatalf("v1 completion preparations = %d, want Parent + Child:\n%s", got, ir)
	}
	if got := strings.Count(ir, "call void @"+coroFrameFreeHookV1); got != 2 {
		t.Fatalf("task-aware v1 frame frees = %d, want Parent + Child:\n%s", got, ir)
	}
	for name, body := range map[string]string{"Parent": parentIR, "Child": childIR} {
		assertCoroV1TaskAwareFrameCalls(t, name, body, prog.PointerSize()*8)
		assertCoroV1InitialPublish(t, name, body)
		wantRunDecisions := 1
		if name == "Parent" {
			wantRunDecisions = 2
		}
		assertCoroScalarRunDecisionCalls(t, name, body, wantRunDecisions)
		assertCoroV1InitialRunDecision(t, name, body)
		assertCoroV1Completion(t, name, body)
	}
	assertCoroStaticChildAwait(t, parentIR)

	descriptor := regexp.MustCompile(
		`(?m)^@__llgo_coro_frame_descriptor_v1\.[0-9a-f]+ = linkonce_odr unnamed_addr constant `,
	)
	if got := len(descriptor.FindAllString(ir, -1)); got != 2 {
		t.Fatalf("PhysicalABIV1 descriptors = %d, want Parent + Child:\n%s", got, ir)
	}
	for _, forbidden := range []string{"@malloc", "@free(", "stacksave", "stackrestore", "pthread"} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("child-await lowering introduced forbidden stack/runtime coupling %q:\n%s", forbidden, ir)
		}
	}
}

func TestCoroNamedFunctionTypeDirectAwait(t *testing.T) {
	const source = `package foo
type Task func(uint32) uint32
func Child(value uint32) uint32 { return value + 1 }
func Parent(value uint32) uint32 {
	var task Task = Child
	return task(value)
}
`
	prog, ssaPkg, files, universe, plan := prepareCoroChildAwaitPhysicalABISource(t, nil, source)
	defer prog.Dispose()
	parent := ssaPkg.Func("Parent")
	parentPlan, ok := plan.FunctionPlan(parent)
	if !ok {
		t.Fatal("named-function caller has no FunctionPlan")
	}
	var retag *ssa.ChangeType
	for _, block := range parent.Blocks {
		for _, instruction := range block.Instrs {
			if change, ok := instruction.(*ssa.ChangeType); ok {
				retag = change
				break
			}
		}
	}
	if retag == nil {
		t.Fatal("named function conversion did not produce ChangeType")
	}
	audit := &coroPhysicalPureSSAAudit{
		plan: plan, universe: universe, fn: parent,
	}
	target, err := resolveCoroCompilerElidedStaticAwaitRetag(audit, parentPlan, retag)
	if err != nil || target != ssaPkg.Func("Child") {
		t.Fatalf("named function retag proof = %v, %v; want exact Child", target, err)
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
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
		t.Fatalf("verify named-function direct await: %v\n%s", err, module.String())
	}
	parentIR := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
	if !strings.Contains(parentIR, `@"foo.Child$coro"`) {
		t.Fatalf("named function call did not lower to its exact child await:\n%s", parentIR)
	}
	assertCoroStaticChildAwait(t, parentIR)
	runCoroABITestPipeline(t, prog, module)
}

func TestCoroChildAwaitPhysicalABIV1CoroSplit(t *testing.T) {
	prog, pkg := compileCoroChildAwaitPhysicalABI(t, nil)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	runCoroABITestPipeline(t, prog, module)
	ir := module.String()
	for _, function := range []string{"foo.Parent$coro", "foo.Child$coro"} {
		if module.NamedFunction(function).IsNil() {
			t.Fatalf("post-split module lost ramp %q:\n%s", function, ir)
		}
		for _, suffix := range []string{".resume", ".destroy"} {
			if module.NamedFunction(function + suffix).IsNil() {
				t.Fatalf("CoroSplit did not create %s%s:\n%s", function, suffix, ir)
			}
		}
	}
	for _, intrinsic := range []string{
		"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend", "llvm.coro.end",
		"llvm.coro.resume", "llvm.coro.done", "llvm.coro.destroy",
	} {
		if hasLLVMCall(ir, intrinsic) {
			t.Fatalf("post-split module still calls %s:\n%s", intrinsic, ir)
		}
	}
	assertCoroRunDecisionResumeOnly(t, module, "foo.Parent$coro", 2)
	assertCoroRunDecisionResumeOnly(t, module, "foo.Child$coro", 1)
	parentResume := module.NamedFunction("foo.Parent$coro.resume").String()
	if !regexp.MustCompile(`call ptr @"?foo\.Child\$coro"?\(`).MatchString(parentResume) {
		t.Fatalf("Parent resume entry lost the static child ramp call:\n%s", parentResume)
	}
	for _, hook := range []string{coroAwaitPrepareHookV1, coroCompletePrepareHookV2} {
		if !strings.Contains(parentResume, "call void @"+hook) {
			t.Fatalf("Parent resume entry lost %s:\n%s", hook, parentResume)
		}
	}
	if !strings.Contains(parentResume, "call i32 @"+coroAwaitConsumeHookV1) {
		t.Fatalf("Parent resume entry lost %s:\n%s", coroAwaitConsumeHookV1, parentResume)
	}
	for _, forbidden := range []string{"llvm.coro.resume", "llvm.coro.done", "llvm.coro.destroy"} {
		if hasLLVMCall(parentResume, forbidden) {
			t.Fatalf("post-split Parent directly calls forbidden %s:\n%s", forbidden, parentResume)
		}
	}
	for _, function := range []string{"foo.Parent$coro", "foo.Child$coro"} {
		ramp := module.NamedFunction(function).String()
		if !strings.Contains(ramp, "call void @"+coroFramePublishHookV3) {
			t.Fatalf("%s ramp lost frame publication:\n%s", function, ramp)
		}
		destroy := module.NamedFunction(function + ".destroy").String()
		if !strings.Contains(destroy, "call void @"+coroFrameFreeHookV1) {
			t.Fatalf("%s destroy entry lost task-aware frame free:\n%s", function, destroy)
		}
	}
}

func TestCoroScalarRunDecisionDoesNotGrowFrameNativeAndWasm(t *testing.T) {
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := compileCoroDecisionFrameProbe(t, test.target, false)
			withScalarGate := compileCoroDecisionFrameProbe(t, test.target, true)
			if withScalarGate != baseline {
				t.Fatalf("scalar run-decision frame size = %d, want gate-off baseline %d", withScalarGate, baseline)
			}
		})
	}
}

func TestCoroPreemptCountdownDoesNotGrowFrameNativeAndWasm(t *testing.T) {
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ungated := compileCoroPreemptCountdownFrameProbe(t, test.target, false)
			gated := compileCoroPreemptCountdownFrameProbe(t, test.target, true)
			if gated != ungated {
				t.Fatalf("checkpoint countdown frame size = %d, want ungated poll baseline %d", gated, ungated)
			}
		})
	}
}

func TestCoroPreemptiveLoopPhysicalABIV1(t *testing.T) {
	const source = `package foo
func Loop(limit uint32) uint32 {
	var value uint32
	for value < limit {
		value++
	}
	return value
}
`
	prog, ssaPkg, files, universe, plan := prepareCoroPreemptTestPlan(
		t,
		source,
		[]coroRootFactoryTestRoot{{name: "Loop", demand: coro.AsyncDemand}},
		nil,
		-1,
	)
	defer prog.Dispose()
	loop := ssaPkg.Func("Loop")
	loopPlan, ok := plan.FunctionPlan(loop)
	if !ok || loopPlan.Emission != coro.EmitCoroutine || loopPlan.FuncRep != coro.DirectCoro ||
		!loopPlan.Exec.Contains(coro.NeedsPreempt) || !loopPlan.Effect.Contains(coro.YieldOnly) {
		t.Fatalf("Loop plan = %+v, present=%t; want direct needs-preempt coroutine", loopPlan, ok)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	body := requireCoroPhysicalFunction(t, module, "foo.Loop").String()
	if !strings.Contains(body, "call i1 @"+coroPreemptPollHookV1) {
		t.Fatalf("Loop lacks compiler-inserted preemption poll:\n%s", body)
	}
	if !strings.Contains(body, "call void @"+coroYieldPrepareHookV1) {
		t.Fatalf("Loop lacks compiler-inserted scheduler yield handoff:\n%s", body)
	}
	if !regexp.MustCompile(`(?s)store i16 3,.*store i16 3,.*call void @` + regexp.QuoteMeta(coroYieldPrepareHookV1)).MatchString(body) {
		t.Fatalf("Loop does not publish Yield/Suspended before its handoff:\n%s", body)
	}
	poll := strings.Index(body, "call i1 @"+coroPreemptPollHookV1)
	handoff := strings.Index(body, "call void @"+coroYieldPrepareHookV1)
	if poll < 0 || handoff < 0 || poll >= handoff || !strings.Contains(body[poll:handoff], "br i1") {
		t.Fatalf("Loop yield handoff is not guarded by its preemption poll:\n%s", body)
	}
	if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got < 3 {
		t.Fatalf("Loop coroutine suspends = %d, want initial + yield + final:\n%s", got, body)
	}
	polls := strings.Count(body, "call i1 @"+coroPreemptPollHookV1)
	if polls != 2 {
		t.Fatalf(
			"Loop preemption polls = %d, want block-zero chain boundary plus one cycle feedback cut:\n%s",
			polls, body,
		)
	}
	if gates := strings.Count(body, "icmp ule i32"); gates != polls {
		t.Fatalf("Loop checkpoint gates = %d, want one private countdown gate per poll site (%d):\n%s", gates, polls, body)
	}
	reset := "store i32 " + strconv.FormatUint(coroPreemptCheckpointStride, 10)
	if resets := strings.Count(body, reset); resets < polls+1 {
		t.Fatalf("Loop checkpoint resets = %d, want initial activation plus at least one reset per poll site (%d):\n%s", resets, polls+1, body)
	}
	assertCoroScalarRunDecisionCalls(t, "Loop", body, polls+1)
	initialDecision := strings.Index(body, "call i32 @"+coroRunDecisionTakeZeroHookV1)
	yieldSuspend := strings.Index(body[handoff:], "call i8 @llvm.coro.suspend")
	if yieldSuspend < 0 {
		t.Fatalf("Loop yield handoff has no suspend:\n%s", body)
	}
	yieldSuspend += handoff
	yieldDecision := strings.Index(body[yieldSuspend:], "call i32 @"+coroRunDecisionTakeZeroHookV1)
	if yieldDecision < 0 {
		t.Fatalf("Loop resumed yield edge has no decision gate:\n%s", body)
	}
	yieldDecision += yieldSuspend
	if initialDecision < 0 || yieldDecision <= yieldSuspend {
		t.Fatalf("Loop decision gates are not on initial/resumed paths:\n%s", body)
	}
	runCoroABITestPipeline(t, prog, module)
	post := module.String()
	for _, suffix := range []string{".resume", ".destroy"} {
		if fn := module.NamedFunction("foo.Loop$coro" + suffix); fn.IsNil() {
			t.Fatalf("CoroSplit did not create Loop%s:\n%s", suffix, post)
		}
	}
	assertCoroRunDecisionResumeOnly(t, module, "foo.Loop$coro", polls+1)
}

func TestCoroPreemptiveIrreducibleCFGPhysicalABIV1(t *testing.T) {
	const source = `package foo
func Irreducible(turn, loop bool) uint32 {
	var value uint32
	if turn {
		goto left
	}
right:
	value++
	if loop {
		goto left
	}
	return value
left:
	value++
	if turn {
		turn = false
		goto right
	}
	if loop {
		goto left
	}
	return value
}
`
	prog, ssaPkg, files, universe, plan := prepareCoroPreemptTestPlan(
		t,
		source,
		[]coroRootFactoryTestRoot{{name: "Irreducible", demand: coro.AsyncDemand}},
		nil,
		-1,
	)
	defer prog.Dispose()
	function := ssaPkg.Func("Irreducible")
	functionPlan, ok := plan.FunctionPlan(function)
	if !ok || functionPlan.Emission != coro.EmitCoroutine ||
		!functionPlan.Exec.Contains(coro.NeedsPreempt) {
		t.Fatalf("Irreducible plan = %+v, present=%t; want preemptible coroutine", functionPlan, ok)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	body := requireCoroPhysicalFunction(t, module, "foo.Irreducible").String()
	polls := strings.Count(body, "call i1 @"+coroPreemptPollHookV1)
	if polls < 2 {
		t.Fatalf("irreducible CFG preemption polls = %d, want entry plus cycle cut:\n%s", polls, body)
	}
	if polls >= len(function.Blocks) {
		t.Fatalf(
			"irreducible CFG preemption polls = %d for %d blocks; graph plan did not reduce block polling:\n%s",
			polls, len(function.Blocks), body,
		)
	}
	runCoroABITestPipeline(t, prog, module)
	assertCoroRunDecisionResumeOnly(t, module, "foo.Irreducible$coro", polls+1)
}

func TestCoroProgramInitPhysicalABIV2(t *testing.T) {
	const source = `package foo
import (
	"embed"
	_ "unsafe"
)

var State uint32
var Files embed.FS

func Plain() { State = 1 }
func Yield() { State = 2 }
func init() {
	Plain()
	Yield()
}
`
	prog, ssaPkg, files, universe, plan := prepareCoroProgramInitTestPlan(t, source)
	defer prog.Dispose()
	packageInit := ssaPkg.Func("init")
	initPlan, ok := plan.FunctionPlan(packageInit)
	if !ok || initPlan.Emission != coro.EmitCoroutine || initPlan.FuncRep != coro.DirectCoro || initPlan.Demand != coro.AsyncDemand {
		t.Fatalf("package init plan = %+v, present=%t; want async-only direct coroutine", initPlan, ok)
	}
	foundElidedUnsafeInit := false
	for _, block := range packageInit.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Call.StaticCallee() == nil || call.Call.StaticCallee().Pkg == nil ||
				call.Call.StaticCallee().Pkg.Pkg.Path() != "unsafe" || call.Call.StaticCallee().Name() != "init" {
				continue
			}
			foundElidedUnsafeInit = plan.ElidesCall(call)
			if _, planned := plan.CallPlan(call); planned {
				t.Fatal("frontend-elided unsafe.init unexpectedly has a CallPlan")
			}
		}
	}
	if !foundElidedUnsafeInit {
		t.Fatal("package init fixture has no exact frontend-elided unsafe.init call")
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	embedMap := goembed.VarMap{
		"Files": {Files: []goembed.FileData{{Name: "asset.txt", Data: []byte("payload")}}},
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, embedMap,
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify coroutine package init: %v\n%s", err, module.String())
	}

	packageInitIR := requireCoroPhysicalFunction(t, module, "foo.init").String()
	if !strings.Contains(packageInitIR, `load i1, ptr @"foo.init$guard"`) ||
		!strings.Contains(packageInitIR, `store i1 true, ptr @"foo.init$guard"`) {
		t.Fatalf("package init coroutine lost its canonical guard load/store:\n%s", packageInitIR)
	}
	if !strings.Contains(module.String(), "asset.txt") || !strings.Contains(module.String(), "payload") {
		t.Fatalf("package init coroutine did not apply compiler-generated embed initialization:\n%s", packageInitIR)
	}
	if !regexp.MustCompile(`call void @"?embed\.init"?\(`).MatchString(packageInitIR) {
		t.Fatalf("package init lost its exact known-external no-suspend call:\n%s", packageInitIR)
	}
	declaredInit := requireCoroPhysicalFunction(t, module, "foo.init#1").String()
	if !regexp.MustCompile(`call void @"?foo\.Plain"?\(`).MatchString(declaredInit) {
		t.Fatalf("declared init lost its exact direct plain call:\n%s", declaredInit)
	}
	if !regexp.MustCompile(`call ptr @"?foo\.Yield\$coro"?\(`).MatchString(declaredInit) ||
		!strings.Contains(declaredInit, "call void @"+coroAwaitPrepareHookV1) {
		t.Fatalf("declared init lost its static child await:\n%s", declaredInit)
	}
	runCoroABITestPipeline(t, prog, module)
}

func TestCoroProgramManagedEntryRejectsMethodNamedInit(t *testing.T) {
	const source = `package foo

type Ring struct{}

func (*Ring) init() {}
func init() {}
`
	ssaPkg, _, _ := buildGoSSAPkg(t, source)
	ring := ssaPkg.Pkg.Scope().Lookup("Ring")
	if ring == nil {
		t.Fatal("fixture Ring type is absent")
	}
	selection := ssaPkg.Prog.MethodSets.MethodSet(types.NewPointer(ring.Type())).Lookup(ssaPkg.Pkg, "init")
	if selection == nil {
		t.Fatal("fixture init method selection is absent")
	}
	method := ssaPkg.Prog.MethodValue(selection)
	if method == nil || method.Signature == nil || method.Signature.Recv() == nil {
		t.Fatalf("fixture init method has unexpected SSA shape: %v", method)
	}
	if isCoroProgramManagedEntry(method) {
		t.Fatalf("receiver method %q was classified as a program bootstrap entry", method.String())
	}
	for _, name := range []string{"init", "init#1"} {
		function := ssaPkg.Func(name)
		if function == nil {
			t.Fatalf("fixture top-level %s is absent", name)
		}
		if !isCoroProgramManagedEntry(function) {
			t.Fatalf("top-level %q lost its program bootstrap classification", function.String())
		}
	}
}

func TestCoroPhysicalValueTransportABIV1NativeAndWasm(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name        string
		target      *llssa.Target
		pointerBits int
		uintptrIR   string
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, pointerBits: 32, uintptrIR: "i32"},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, ssaPkg, files, universe, plan := prepareCoroPhysicalValueTransportABI(t, test.target)
			defer prog.Dispose()
			pointerBits := prog.PointerSize() * 8
			if test.pointerBits != 0 && pointerBits != test.pointerBits {
				t.Fatalf("pointer width = %d, want %d", pointerBits, test.pointerBits)
			}
			uintptrIR := test.uintptrIR
			if uintptrIR == "" {
				uintptrIR = "i" + strconv.Itoa(pointerBits)
			}

			child := ssaPkg.Func("Child")
			callbackPlan, ok := plan.ValuePlan(child.Params[0])
			if !ok || len(callbackPlan.Funcs) != 1 || len(callbackPlan.Funcs[0].Path) != 0 ||
				callbackPlan.Funcs[0].Rep != coro.Dispatch {
				t.Fatalf("Child callback ValuePlan = %+v, present=%t; want one canonical scalar Dispatch leaf", callbackPlan, ok)
			}
			parent := ssaPkg.Func("Parent")
			var childCall *ssa.Call
			for _, block := range parent.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(*ssa.Call)
					if ok && call.Call.StaticCallee() == child {
						childCall = call
					}
				}
			}
			if childCall == nil {
				t.Fatal("Parent has no static Child call")
			}
			nilCallbackPlan, ok := plan.ValuePlan(childCall.Call.Args[0])
			if !ok || len(nilCallbackPlan.Funcs) != 1 || len(nilCallbackPlan.Funcs[0].Path) != 0 ||
				nilCallbackPlan.Funcs[0].Rep != coro.Dispatch || !nilCallbackPlan.Funcs[0].MayBeNil ||
				len(nilCallbackPlan.Funcs[0].Targets) != 0 {
				t.Fatalf("nil callback ValuePlan = %+v, present=%t; want closed nil canonical Dispatch leaf", nilCallbackPlan, ok)
			}
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroChildAwaitCompilation(compilation)
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
				t.Fatalf("verify physical value transport before CoroSplit: %v\n%s", err, module.String())
			}

			childIR := requireCoroPhysicalFunction(t, module, "foo.Child").String()
			parentIR := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
			pairIR := requireCoroPhysicalFunction(t, module, "foo.Pair").String()
			if !regexp.MustCompile(`define ptr @"?foo\.Child\$coro"?\(ptr [^,]+, ptr [^,]+, \{ ptr, ptr \} [^,]+, ptr `).MatchString(childIR) {
				t.Fatalf("Child callback/pointer parameters do not use LLGo's canonical two-pointer closure layout:\n%s", childIR)
			}
			if !regexp.MustCompile(`call ptr @"?foo\.Child\$coro"?\([^\n]*\{ ptr, ptr \} zeroinitializer, ptr `).MatchString(parentIR) {
				t.Fatalf("Parent did not transport the nil callback through the typed canonical closure argument:\n%s", parentIR)
			}
			assertCoroResultSlotFields(t, "Pair before CoroSplit", pairIR, uintptrIR)
			if !regexp.MustCompile(`store %foo\.Payload [^,]+, ptr `).MatchString(childIR) {
				t.Fatalf("Child did not copy the complete named struct result into its typed result slot:\n%s", childIR)
			}

			runCoroABITestPipeline(t, prog, module)
			post := module.String()
			for _, function := range []string{"foo.Child$coro", "foo.Parent$coro", "foo.Pair$coro"} {
				for _, suffix := range []string{".resume", ".destroy"} {
					if module.NamedFunction(function + suffix).IsNil() {
						t.Fatalf("CoroSplit did not create %s%s:\n%s", function, suffix, post)
					}
				}
			}
			assertCoroResultSlotFields(t, "Pair after CoroSplit", module.NamedFunction("foo.Pair$coro.resume").String(), uintptrIR)
			if !regexp.MustCompile(`store %foo\.Payload [^,]+, ptr `).MatchString(module.NamedFunction("foo.Child$coro.resume").String()) {
				t.Fatalf("Child struct result store did not survive CoroSplit:\n%s", module.NamedFunction("foo.Child$coro.resume").String())
			}
		})
	}
}

func TestCoroAwaitResultReconstruction(t *testing.T) {
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg := prog.NewPackage("awaitresult", "await/result")
	module := pkg.Module()
	defer module.Dispose()
	physical := &context{prog: prog}
	pointer := types.NewPointer(types.Typ[types.Uint32])
	empty := types.NewStruct(nil, nil)
	for _, test := range []struct {
		name    string
		results *types.Tuple
		loads   int
		inserts int
	}{
		{name: "zero", results: types.NewTuple()},
		{name: "one", results: types.NewTuple(types.NewVar(0, nil, "ptr", pointer)), loads: 1},
		{name: "one_empty", results: types.NewTuple(types.NewVar(0, nil, "empty", empty))},
		{name: "many", results: types.NewTuple(
			types.NewVar(0, nil, "ptr", pointer),
			types.NewVar(0, nil, "count", types.Typ[types.Uintptr]),
		), loads: 2, inserts: 2},
	} {
		resultCount := 0
		if test.results != nil {
			resultCount = test.results.Len()
		}
		fields := make([]*types.Var, resultCount)
		for i := range fields {
			fields[i] = types.NewField(0, nil, test.results.At(i).Name(), test.results.At(i).Type(), false)
		}
		name := "await_" + test.name
		fn := pkg.NewFunc(name, llssa.NoArgsNoRet, llssa.InGo)
		b := fn.MakeBody(1)
		slot := b.AllocaT(prog.Type(types.NewStruct(fields, nil), llssa.InGo))
		got := physical.loadCoroAwaitResult(b, slot, test.results)
		switch resultCount {
		case 0:
			if !got.IsNil() {
				t.Fatalf("zero-result await value type = %v, want llssa.Nil", got.RawType())
			}
		case 1:
			want := test.results.At(0).Type()
			if got.IsNil() || !types.Identical(got.RawType(), prog.Type(want, llssa.InGo).RawType()) {
				t.Fatalf("one-result await value type = %v, want field type %v", got.RawType(), want)
			}
		default:
			if got.IsNil() || !types.Identical(got.RawType(), prog.Type(test.results, llssa.InGo).RawType()) {
				t.Fatalf("multi-result await value type = %v, want source tuple %v", got.RawType(), test.results)
			}
		}
		b.Return()
		b.EndBuild()
		b.Dispose()
		body := module.NamedFunction(name).String()
		if got := strings.Count(body, "load "); got != test.loads {
			t.Fatalf("%s await result loads = %d, want %d:\n%s", test.name, got, test.loads, body)
		}
		if got := strings.Count(body, "insertvalue "); got != test.inserts {
			t.Fatalf("%s await result tuple inserts = %d, want %d:\n%s", test.name, got, test.inserts, body)
		}
		if strings.Contains(body, "AssertNilDeref") {
			t.Fatalf("%s compiler-owned result slot retained a nil-dereference helper:\n%s", test.name, body)
		}
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify await result reconstruction: %v\n%s", err, module.String())
	}
}

func assertCoroResultSlotFields(t *testing.T, name, body, uintptrIR string) {
	t.Helper()
	resultType := regexp.QuoteMeta("{ ptr, " + uintptrIR + " }")
	for index, storeType := range []string{"ptr", uintptrIR} {
		field := regexp.MustCompile(
			`(?m)^\s*(%[-a-zA-Z$._0-9]+) = getelementptr inbounds(?: (?:nuw|nusw))* ` + resultType +
				`, ptr [^,]+, i32 0, i32 ` + strconv.Itoa(index) + `\s*$`,
		).FindStringSubmatch(body)
		if len(field) != 2 || !regexp.MustCompile(`(?m)^\s*store `+storeType+` [^,]+, ptr `+regexp.QuoteMeta(field[1])+`(?:,|\s*$)`).MatchString(body) {
			t.Fatalf("%s has no typed store for result field %d (%s):\n%s", name, index, storeType, body)
		}
	}
}

func TestCoroStaticPlainCallExecutionConstraints(t *testing.T) {
	for _, test := range []struct {
		name    string
		exec    coro.ExecFlags
		wantErr string
	}{
		{name: "thread affine rejected", exec: coro.ThreadAffine, wantErr: "thread-affine"},
		{name: "IRQ unsafe allowed on ordinary G", exec: coro.IRQUnsafe},
	} {
		t.Run(test.name, func(t *testing.T) {
			const source = `package foo
func Plain() {}
func Root() { Plain() }
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
			root, plain := ssaPkg.Func("Root"), ssaPkg.Func("Plain")
			functionIDs := universe.FunctionIDConfig()
			functionIDs.CoroABI = coro.PhysicalABIV1
			functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
			functionIDs.ArchiveReady = true
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse:     ssaUniverse,
				FunctionIDs:          functionIDs,
				MaxPlainInstructions: -1,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					switch fn {
					case root:
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					case plain:
						return coro.SSAFunctionPolicy{Exec: test.exec}, nil
					default:
						return coro.SSAFunctionPolicy{}, nil
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			var plainCall *ssa.Call
			for _, instruction := range root.Blocks[0].Instrs {
				call, ok := instruction.(*ssa.Call)
				if ok && call.Call.StaticCallee() == plain {
					plainCall = call
					break
				}
			}
			if plainCall == nil {
				t.Fatal("Root has no static Plain call")
			}
			_, _, resolveErr := resolveCoroStaticPlainCall(plan, plainCall)
			if test.wantErr == "" && resolveErr != nil {
				t.Fatalf("ordinary-G direct plain target rejected: %v", resolveErr)
			}
			if test.wantErr != "" && (resolveErr == nil || !strings.Contains(resolveErr.Error(), test.wantErr)) {
				t.Fatalf("direct plain target error = %v, want %q", resolveErr, test.wantErr)
			}
			if test.wantErr == "" {
				rootPlan, ok := plan.FunctionPlan(root)
				if !ok {
					t.Fatal("Root has no function plan")
				}
				if err := validateCoroPhysicalABI(root, rootPlan, plan, true, true); err != nil {
					t.Fatalf("ordinary-G IRQ-unsafe CFG preflight rejected: %v", err)
				}
			}
		})
	}
}

func TestCoroStaticPlainCallAcceptsOnlyExactTrustedInlineForeignEdge(t *testing.T) {
	const source = `package foo
import _ "unsafe"
//llgo:coro contract foreign.v1 progress=unknown affinity=unknown reentry=unknown memory=unknown inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none inline-memory=borrow-until-return
//go:linkname Foreign C.trusted_inline_physical_probe
func Foreign(int) int
//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
func Root(value int) int { return Foreign(value) }
func Outer(value int) int { return Root(value) + 1 }
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
	root, outer, foreign := ssaPkg.Func("Root"), ssaPkg.Func("Outer"), ssaPkg.Func("Foreign")
	var foreignCall *ssa.Call
	for _, instruction := range root.Blocks[0].Instrs {
		call, ok := instruction.(*ssa.Call)
		if ok && call.Call.StaticCallee() == foreign {
			foreignCall = call
			break
		}
	}
	if foreignCall == nil {
		t.Fatal("Root has no static Foreign call")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	foreignCertificate, certified, err := universe.CoroCallableContractCertificate(foreign)
	if err != nil || !certified || !foreignCertificate.HasTrustedInlineContract {
		t.Fatalf("Foreign callable certificate = %+v, %t, %v", foreignCertificate, certified, err)
	}
	defaultForeignExec := coro.CallableContractExecConstraints(foreignCertificate.Contract)
	if defaultForeignExec != coro.ThreadAffine|coro.OpaqueExec ||
		coro.CallableContractExecConstraints(foreignCertificate.TrustedInlineContract) != 0 {
		t.Fatalf("Foreign contract projections = default:%s selected:%s", defaultForeignExec, coro.CallableContractExecConstraints(foreignCertificate.TrustedInlineContract))
	}
	rootCertificate, certified, err := universe.CoroCallableContractCertificate(root)
	if err != nil || !certified || rootCertificate.Scope != coro.CallableContractScopeWrapper {
		t.Fatalf("Root callable certificate = %+v, %t, %v", rootCertificate, certified, err)
	}
	config := coro.SSAConfig{
		EmissionUniverse: ssaUniverse, FunctionIDs: functionIDs, MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == foreign {
				return coro.SSAFunctionPolicy{
					IgnoreBody: true, External: coro.ExternalUnknownForeign, OverrideExternal: true,
					Exec: coro.BlockForeign | coro.IRQUnsafe | defaultForeignExec, CallableContractCertificate: foreignCertificate,
				}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	}
	auto, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveCoroStaticPlainCall(auto, foreignCall); err == nil {
		t.Fatal("ordinary Auto edge to unknown blocking foreign target was accepted inline")
	}
	config.ClassifyFunction = func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		switch fn {
		case foreign:
			return coro.SSAFunctionPolicy{
				IgnoreBody: true, External: coro.ExternalUnknownForeign, OverrideExternal: true,
				Exec: coro.BlockForeign | coro.IRQUnsafe | defaultForeignExec, CallableContractCertificate: foreignCertificate,
			}, nil
		case root:
			return coro.SSAFunctionPolicy{CallableContractCertificate: rootCertificate}, nil
		case outer:
			return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
		default:
			return coro.SSAFunctionPolicy{}, nil
		}
	}
	config.ClassifyTrustedInlineCall = universe.CoroTrustedInlineCallCertificate
	trusted, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: outer, Demand: coro.AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	target, targetPlan, err := resolveCoroStaticPlainCall(trusted, foreignCall)
	if err != nil {
		t.Fatalf("exact TrustedInline edge rejected: %v", err)
	}
	if target != foreign || targetPlan.External != coro.ExternalUnknownForeign ||
		targetPlan.Exec != coro.BlockForeign|coro.IRQUnsafe|coro.ThreadAffine|coro.OpaqueExec {
		t.Fatalf("trusted target = %v, %+v", target, targetPlan)
	}
	outerPlan, ok := trusted.FunctionPlan(outer)
	if !ok {
		t.Fatal("trusted Outer has no function plan")
	}
	if err := validateCoroPhysicalABI(outer, outerPlan, trusted, true, true); err != nil {
		t.Fatalf("trusted-inline physical preflight rejected: %v", err)
	}
	compilation := &Compilation{CoroPlan: trusted, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
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
		t.Fatalf("verify trusted-inline coroutine: %v\n%s", err, module.String())
	}
	rootBody := module.NamedFunction("foo.Root")
	if rootBody.IsNil() || !strings.Contains(rootBody.String(), "@trusted_inline_physical_probe") {
		t.Fatalf("trusted-inline wrapper does not directly call its exact target:\n%s", module.String())
	}
	body := requireCoroPhysicalFunction(t, module, "foo.Outer").String()
	if !strings.Contains(body, "@foo.Root") {
		t.Fatalf("coroutine caller does not use the bounded plain wrapper:\n%s", body)
	}
	if strings.Contains(rootBody.String(), "@"+coroWorkerParkHookV1) || strings.Contains(body, "@"+coroWorkerParkHookV1) {
		t.Fatalf("trusted-inline path unexpectedly uses worker lowering:\n%s\n%s", rootBody.String(), body)
	}
	runCoroABITestPipeline(t, prog, module)
}

func TestCoroPreemptiveStraightLineBudgetPhysicalABIV1(t *testing.T) {
	source := "package foo\nfunc Heavy(value uint32) uint32 {\n" +
		strings.Repeat("value++\n", 150) +
		"return value\n}\n"
	prog, ssaPkg, files, universe, plan := prepareCoroPreemptTestPlan(
		t,
		source,
		[]coroRootFactoryTestRoot{{name: "Heavy", demand: coro.AsyncDemand}},
		nil,
		16,
	)
	defer prog.Dispose()
	heavy := ssaPkg.Func("Heavy")
	heavyPlan, ok := plan.FunctionPlan(heavy)
	if !ok || !heavyPlan.Exec.Contains(coro.NeedsPreempt) || !heavyPlan.Effect.Contains(coro.YieldOnly) {
		t.Fatalf("Heavy plan = %+v, present=%t; want instruction-budget preemption", heavyPlan, ok)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	body := requireCoroPhysicalFunction(t, module, "foo.Heavy").String()
	if got := strings.Count(body, "call void @"+coroYieldPrepareHookV1); got < 2 {
		t.Fatalf("Heavy compiler yield handoffs = %d, want at least two periodic cuts:\n%s", got, body)
	}
	runCoroABITestPipeline(t, prog, module)
}

func TestCoroPreemptiveInstructionBudgetBoundary(t *testing.T) {
	source := "package foo\nfunc AtLimit(value uint32) uint32 {\n" +
		strings.Repeat("value++\n", coroPreemptInstructionBudget-1) +
		"return value\n}\nfunc OverLimit(value uint32) uint32 {\n" +
		strings.Repeat("value++\n", coroPreemptInstructionBudget) +
		"return value\n}\n"
	prog, ssaPkg, files, universe, plan := prepareCoroPreemptTestPlan(
		t,
		source,
		[]coroRootFactoryTestRoot{
			{name: "AtLimit", demand: coro.AsyncDemand},
			{name: "OverLimit", demand: coro.AsyncDemand},
		},
		nil,
		16,
	)
	defer prog.Dispose()
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if got := strings.Count(requireCoroPhysicalFunction(t, module, "foo.AtLimit").String(), "call i1 @"+coroPreemptPollHookV1); got != 1 {
		t.Fatalf("AtLimit preemption polls = %d, want block-zero chain-boundary poll only", got)
	}
	if got := strings.Count(requireCoroPhysicalFunction(t, module, "foo.OverLimit").String(), "call i1 @"+coroPreemptPollHookV1); got != 2 {
		t.Fatalf("OverLimit preemption polls = %d, want block-zero plus one instruction-budget poll", got)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify instruction-budget boundary coroutines: %v\n%s", err, module.String())
	}
}

func TestCoroChildAwaitPhysicalABIV1Wasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog, pkg := compileCoroChildAwaitPhysicalABI(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if got := prog.PointerSize(); got != 4 {
		t.Fatalf("wasm pointer size = %d, want 4", got)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify wasm child-await coroutines: %v\n%s", err, module.String())
	}
	ir := module.String()
	for _, intrinsic := range []string{"size", "align"} {
		if !strings.Contains(ir, "@llvm.coro."+intrinsic+".i32") {
			t.Fatalf("wasm child-await coroutine uses non-i32 %s intrinsic:\n%s", intrinsic, ir)
		}
	}
	if !regexp.MustCompile(
		`@__llgo_coro_frame_descriptor_v1\.[0-9a-f]+ = linkonce_odr unnamed_addr constant ` +
			`\{ i32, i32, i64, i64, i32, i32, \{ ptr, i32 \}, \{ ptr, i32 \} \}`,
	).MatchString(ir) {
		t.Fatalf("wasm PhysicalABIV1 descriptor does not use i32 size/alignment fields:\n%s", ir)
	}
	parentIR := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
	assertCoroV1TaskAwareFrameCalls(t, "wasm Parent", parentIR, 32)
	if !regexp.MustCompile(`call ptr @llvm\.coro\.promise\(ptr [^,]+, i32 4, i1 false\)`).MatchString(parentIR) {
		t.Fatalf("wasm child header lookup does not use wasm32 ABI alignment and from=false:\n%s", parentIR)
	}
	assertCoroStaticChildAwait(t, parentIR)
	runCoroABITestPipeline(t, prog, module)
	post := module.String()
	for _, function := range []string{"foo.Parent$coro", "foo.Child$coro"} {
		for _, suffix := range []string{".resume", ".destroy"} {
			if module.NamedFunction(function + suffix).IsNil() {
				t.Fatalf("wasm CoroSplit did not create %s%s:\n%s", function, suffix, post)
			}
		}
	}
	assertCoroRunDecisionResumeOnly(t, module, "foo.Parent$coro", 2)
	assertCoroRunDecisionResumeOnly(t, module, "foo.Child$coro", 1)
}

func TestCoroTargetCapabilitiesFailClosed(t *testing.T) {
	for _, test := range []struct {
		name        string
		compilation *Compilation
		want        string
	}{
		{
			name: "native fleet without worker",
			compilation: &Compilation{
				CoroTargetCapabilities: coro.TargetCapabilities(2),
			},
			want: "invalid coroutine target capability set",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.compilation.preflightCoroPlan()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("target capability validation error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCoroExplicitAsyncRootFactoryV1Presplit(t *testing.T) {
	prog, pkg := compileCoroChildAwaitPhysicalABI(t, nil)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify explicit async root factory: %v\n%s", err, module.String())
	}
	ir := module.String()
	parent := requireCoroPhysicalFunction(t, module, "foo.Parent")
	child := requireCoroPhysicalFunction(t, module, "foo.Child")
	hash, factory := requireSingleCoroRootFactoryV1(t, module)
	parentHash := requireCoroFrameDescriptorHash(t, "Parent", parent.String())
	childHash := requireCoroFrameDescriptorHash(t, "Child", child.String())
	if hash != parentHash {
		t.Fatalf("root factory hash = %s, want explicit Parent frame hash %s", hash, parentHash)
	}
	if childHash == parentHash {
		t.Fatalf("propagated Child and explicit Parent unexpectedly share ABI hash %s", childHash)
	}
	if !module.NamedFunction(coroRootFactoryPrefix+childHash).IsNil() ||
		strings.Contains(ir, coroRootFactoryDescriptorPrefix+childHash) {
		t.Fatalf("propagated AsyncDemand Child incorrectly received a root factory/descriptor:\n%s", ir)
	}
	assertCoroRootFactoryV1Body(t, factory.String())
	assertCoroRootFactoryV1Descriptor(t, ir, hash, parentHash, prog.PointerSize()*8)
	assertCoroRootDescriptorLLVMUsed(t, module, hash)
	if got := len(regexp.MustCompile(`define ptr @"?`+regexp.QuoteMeta(coroRootFactoryPrefix)+`[0-9a-f]{32}"?\(`).FindAllString(ir, -1)); got != 1 {
		t.Fatalf("root factory definitions = %d, want only explicit Parent:\n%s", got, ir)
	}
	if got := len(regexp.MustCompile(`@`+regexp.QuoteMeta(coroRootFactoryDescriptorPrefix)+`[0-9a-f]{32} =`).FindAllString(ir, -1)); got != 1 {
		t.Fatalf("root factory descriptors = %d, want only explicit Parent:\n%s", got, ir)
	}
}

func TestCoroRootPackageAnchorV1CanonicalRegistry(t *testing.T) {
	const source = `package foo
func AlphaChild(value uint32) uint32 { return value + 1 }
func Alpha(value uint32) uint32 { return AlphaChild(value) + 1 }
func ZebraChild(value uint32) uint32 { return value + 2 }
func Zebra(value uint32) uint32 { return ZebraChild(value) + 1 }
`
	prog, ssaPkg, files, universe, plan := prepareCoroRootFactoryTestPlan(
		t, source,
		[]coroRootFactoryTestRoot{
			{name: "Zebra", demand: coro.AsyncDemand},
			{name: "Alpha", demand: coro.AsyncDemand},
		},
		[]string{"AlphaChild", "ZebraChild"},
	)
	defer prog.Dispose()
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
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
		t.Fatalf("verify root package anchor: %v\n%s", err, module.String())
	}

	anchor := requireSingleCoroRootPackageAnchorV1(t, module)
	if got := pkg.CoroRootPackageAnchor(); got != anchor.Name() {
		t.Fatalf("package anchor = %q, want %q", got, anchor.Name())
	}
	initializer := anchor.Initializer()
	if initializer.IsAConstantStruct().IsNil() || initializer.OperandsCount() != 6 {
		t.Fatalf("anchor initializer is not a six-field constant struct: %v", initializer)
	}
	if got := initializer.Operand(0).ZExtValue(); got != uint64(coroRootPackageAnchorVersionV1) {
		t.Fatalf("anchor version = %d, want %d", got, coroRootPackageAnchorVersionV1)
	}
	if got := initializer.Operand(4).ZExtValue(); got != 2 {
		t.Fatalf("anchor descriptor count = %d, want 2", got)
	}
	suffix := strings.TrimPrefix(anchor.Name(), coroRootPackageAnchorPrefix)
	decoded, err := hex.DecodeString(suffix)
	if err != nil || len(decoded) != 16 {
		t.Fatalf("anchor suffix %q is not a 128-bit hex ABI hash: %v", suffix, err)
	}
	if got, want := initializer.Operand(2).ZExtValue(), binary.BigEndian.Uint64(decoded[:8]); got != want {
		t.Fatalf("anchor hashLo = %#x, want symbol hash %#x", got, want)
	}
	if got, want := initializer.Operand(3).ZExtValue(), binary.BigEndian.Uint64(decoded[8:]); got != want {
		t.Fatalf("anchor hashHi = %#x, want symbol hash %#x", got, want)
	}

	entries := module.NamedGlobal(anchor.Name() + ".entries")
	if entries.IsNil() || entries.Initializer().IsAConstantArray().IsNil() {
		t.Fatalf("anchor entries array is absent or non-constant:\n%s", module.String())
	}
	entryValues := entries.Initializer()
	rootPlans := plan.Roots()
	if len(rootPlans) != 2 || entryValues.OperandsCount() != len(rootPlans) {
		t.Fatalf("root plans/entries = %d/%d, want 2/2", len(rootPlans), entryValues.OperandsCount())
	}
	for i, root := range rootPlans {
		function := module.NamedFunction("foo." + root.Function.Name() + coroPrimarySuffix)
		if function.IsNil() {
			t.Fatalf("root coroutine %q is absent:\n%s", root.ID, module.String())
		}
		hash := requireCoroFrameDescriptorHash(t, root.Function.Name(), function.String())
		want := coroRootFactoryDescriptorPrefix + hash
		if got := stripCoroRootPackageConstantPointer(entryValues.Operand(i)).Name(); got != want {
			t.Fatalf("anchor entries[%d] = %q, want FunctionID-ordered root %q descriptor %q", i, got, root.ID, want)
		}
	}
	for _, name := range []string{"AlphaChild", "ZebraChild"} {
		child := module.NamedFunction("foo." + name + coroPrimarySuffix)
		if child.IsNil() {
			t.Fatalf("propagated coroutine %q is absent", name)
		}
		hash := requireCoroFrameDescriptorHash(t, name, child.String())
		if !module.NamedGlobal(coroRootFactoryDescriptorPrefix+hash).IsNil() ||
			!module.NamedFunction(coroRootFactoryPrefix+hash).IsNil() {
			t.Fatalf("propagated async function %q received a root factory/descriptor:\n%s", name, module.String())
		}
	}
	assertCoroRootPackageAnchorLLVMUsed(t, module, anchor)
}

func TestCoroRootPackageAnchorV1AbsentWithoutExplicitRoots(t *testing.T) {
	prog, ssaPkg, files, universe, plan := prepareCoroRootFactoryTestPlan(
		t, `package foo; func Plain(value uint32) uint32 { return value + 1 }`, nil, nil,
	)
	defer prog.Dispose()
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if got := pkg.CoroRootPackageAnchor(); got != "" {
		t.Fatalf("rootless package anchor = %q, want none", got)
	}
	if anchors := coroRootPackageAnchorsV1(module); len(anchors) != 0 {
		t.Fatalf("rootless package emitted %d anchor(s):\n%s", len(anchors), module.String())
	}
	if strings.Contains(module.String(), coroRootPackageAnchorPrefix) {
		t.Fatalf("rootless package IR contains a root anchor marker:\n%s", module.String())
	}
}

func TestCoroRootPackageAnchorV1StableAcrossCacheRegistration(t *testing.T) {
	compile := func(cacheHit bool, planDigest, source string) string {
		t.Helper()
		prog, ssaPkg, files, universe, plan := prepareCoroRootFactoryTestPlan(
			t, source,
			[]coroRootFactoryTestRoot{{name: "Root", demand: coro.AsyncDemand}},
			[]string{"Root"},
		)
		compilation := &Compilation{
			CoroPlan:         plan,
			CoroPlanDigest:   planDigest,
			EmissionUniverse: universe,
		}
		enableCoroChildAwaitCompilation(compilation)
		installCoroLoweringFactsForTest(t, compilation)
		pkg, _, err := NewPackageExWithEmbedOptions(
			prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
			PackageOptions{Compilation: compilation, CacheHit: cacheHit},
		)
		if err != nil {
			prog.Dispose()
			t.Fatal(err)
		}
		module := pkg.Module()
		name := requireSingleCoroRootPackageAnchorV1(t, module).Name()
		module.Dispose()
		prog.Dispose()
		return name
	}

	const rootUint32 = `package foo; func Root(value uint32) uint32 { return value + 1 }`
	const digest = "0000000000000000000000000000000000000000000000000000000000000000"
	sourceAnchor := compile(false, digest, rootUint32)
	cached := compile(true, digest, rootUint32)
	if cached != sourceAnchor {
		t.Fatalf("cache registration anchor = %q, source anchor = %q", cached, sourceAnchor)
	}
	fallbackA := compile(false, "", rootUint32)
	fallbackB := compile(false, "", rootUint32)
	if fallbackA != fallbackB {
		t.Fatalf("digest-free direct compilation anchors are unstable: %q != %q", fallbackA, fallbackB)
	}
	const otherDigest = "1111111111111111111111111111111111111111111111111111111111111111"
	if other := compile(false, otherDigest, rootUint32); other == sourceAnchor {
		t.Fatalf("anchor %q did not include the canonical plan digest", other)
	}
	const rootUint64 = `package foo; func Root(value uint64) uint64 { return value + 1 }`
	if changedABI := compile(false, digest, rootUint64); changedABI == sourceAnchor {
		t.Fatalf("anchor %q did not include the root factory descriptor ABI hash", changedABI)
	}
}

func TestCoroExplicitAsyncRootFactoryV1CoroSplit(t *testing.T) {
	prog, pkg := compileCoroChildAwaitPhysicalABI(t, nil)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	hash, _ := requireSingleCoroRootFactoryV1(t, module)
	runCoroABITestPipeline(t, prog, module)
	ir := module.String()
	factoryName := coroRootFactoryPrefix + hash
	factory := module.NamedFunction(factoryName)
	if factory.IsNil() {
		t.Fatalf("CoroSplit lost explicit root factory %q:\n%s", factoryName, ir)
	}
	assertCoroRootFactoryV1Body(t, factory.String())
	if !module.NamedFunction(factoryName+".resume").IsNil() ||
		!module.NamedFunction(factoryName+".destroy").IsNil() {
		t.Fatalf("non-coroutine root factory was cloned into resume/destroy entries:\n%s", ir)
	}
	for _, function := range []string{"foo.Parent$coro", "foo.Child$coro"} {
		for _, suffix := range []string{".resume", ".destroy"} {
			if module.NamedFunction(function + suffix).IsNil() {
				t.Fatalf("CoroSplit did not create %s%s:\n%s", function, suffix, ir)
			}
		}
	}
	if !strings.Contains(ir, coroRootFactoryDescriptorPrefix+hash) {
		t.Fatalf("CoroSplit lost explicit root descriptor %q:\n%s", coroRootFactoryDescriptorPrefix+hash, ir)
	}
	assertCoroRootDescriptorLLVMUsed(t, module, hash)
	runCoroABIGlobalDCE(t, prog, module)
	if module.NamedGlobal(coroRootFactoryDescriptorPrefix+hash).IsNil() ||
		module.NamedFunction(factoryName).IsNil() {
		t.Fatalf("GlobalDCE lost linker-retained root descriptor/factory:\n%s", module.String())
	}
	assertCoroRootDescriptorLLVMUsed(t, module, hash)
	assertCoroRootObjectRetained(t, prog, module, hash)
}

func TestCoroExplicitAsyncRootFactoryV1Wasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog, pkg := compileCoroChildAwaitPhysicalABI(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if got := prog.PointerSize(); got != 4 {
		t.Fatalf("wasm pointer size = %d, want 4", got)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify wasm explicit async root factory: %v\n%s", err, module.String())
	}
	ir := module.String()
	hash, factory := requireSingleCoroRootFactoryV1(t, module)
	parentHash := requireCoroFrameDescriptorHash(t, "wasm Parent", requireCoroPhysicalFunction(t, module, "foo.Parent").String())
	childHash := requireCoroFrameDescriptorHash(t, "wasm Child", requireCoroPhysicalFunction(t, module, "foo.Child").String())
	if hash != parentHash || childHash == hash {
		t.Fatalf("wasm root hashes: factory=%s Parent=%s Child=%s", hash, parentHash, childHash)
	}
	assertCoroRootFactoryV1Body(t, factory.String())
	assertCoroRootFactoryV1Descriptor(t, ir, hash, parentHash, 32)
	assertCoroRootDescriptorLLVMUsed(t, module, hash)
	if !module.NamedFunction(coroRootFactoryPrefix+childHash).IsNil() ||
		strings.Contains(ir, coroRootFactoryDescriptorPrefix+childHash) {
		t.Fatalf("wasm propagated Child incorrectly received a root factory/descriptor:\n%s", ir)
	}

	runCoroABITestPipeline(t, prog, module)
	post := module.String()
	factoryName := coroRootFactoryPrefix + hash
	postFactory := module.NamedFunction(factoryName)
	if postFactory.IsNil() {
		t.Fatalf("wasm CoroSplit lost root factory %q:\n%s", factoryName, post)
	}
	assertCoroRootFactoryV1Body(t, postFactory.String())
	if !module.NamedFunction(factoryName+".resume").IsNil() ||
		!module.NamedFunction(factoryName+".destroy").IsNil() {
		t.Fatalf("wasm root factory was incorrectly coroutine-split:\n%s", post)
	}
	assertCoroRootDescriptorLLVMUsed(t, module, hash)
	for _, function := range []string{"foo.Parent$coro", "foo.Child$coro"} {
		for _, suffix := range []string{".resume", ".destroy"} {
			if module.NamedFunction(function + suffix).IsNil() {
				t.Fatalf("wasm CoroSplit did not create %s%s:\n%s", function, suffix, post)
			}
		}
	}
}

func TestCoroExplicitRootFactoryV1FailsClosed(t *testing.T) {
	const childAwaitSource = `package foo
func Child(first uint8, second uint32) uint32 { return uint32(first) + second }
func Parent(first uint8, second uint32) uint32 { return Child(first, second) + 1 }
`
	for _, test := range []struct {
		name      string
		source    string
		roots     []coroRootFactoryTestRoot
		yieldOnly []string
		want      string
	}{
		{
			name:      "sync explicit coroutine root",
			source:    childAwaitSource,
			roots:     []coroRootFactoryTestRoot{{name: "Parent", demand: coro.SyncDemand}},
			yieldOnly: []string{"Child"},
			want:      "has synchronous demand without a planned raw plain entry, got root=sync total=sync",
		},
		{
			name:   "both-demand explicit coroutine root",
			source: childAwaitSource,
			roots: []coroRootFactoryTestRoot{
				{name: "Parent", demand: coro.SyncDemand},
				{name: "Parent", demand: coro.AsyncDemand},
			},
			yieldOnly: []string{"Child"},
			want:      "has synchronous demand without a planned raw plain entry, got root=both total=both",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, ssaPkg, files, universe, plan := prepareCoroRootFactoryTestPlan(
				t, test.source, test.roots, test.yieldOnly,
			)
			defer prog.Dispose()
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroChildAwaitCompilation(compilation)
			observerCalls := 0
			compilation.CoroPlanObserver = func(*ssa.Package, *coro.SSAPlan) { observerCalls++ }
			got, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preflight result = %v, %v; want error containing %q", got, err, test.want)
			}
			if got != nil {
				t.Fatal("root-factory preflight failure returned a partial package")
			}
			if observerCalls != 0 {
				t.Fatalf("observer calls = %d, want pre-codegen rejection", observerCalls)
			}
		})
	}
}

func TestCoroExplicitPlainRootKeepsSinglePlainBody(t *testing.T) {
	const source = `package foo; func Plain(first uint8, second uint32) uint32 { return uint32(first) + second }`
	for _, demand := range []coro.Demand{coro.SyncDemand, coro.AsyncDemand, coro.BothDemand} {
		t.Run(demand.String(), func(t *testing.T) {
			roots := []coroRootFactoryTestRoot{{name: "Plain", demand: demand}}
			if demand == coro.BothDemand {
				roots = []coroRootFactoryTestRoot{
					{name: "Plain", demand: coro.SyncDemand},
					{name: "Plain", demand: coro.AsyncDemand},
				}
			}
			prog, ssaPkg, files, universe, plan := prepareCoroRootFactoryTestPlan(t, source, roots, nil)
			defer prog.Dispose()
			plain := ssaPkg.Func("Plain")
			function, ok := plan.FunctionPlan(plain)
			if !ok || function.External != coro.Defined || function.Emission != coro.EmitPlain ||
				function.FuncRep != coro.DirectPlain || function.Demand != demand {
				t.Fatalf("plain root plan = %+v, present=%t", function, ok)
			}
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroChildAwaitCompilation(compilation)
			pkg, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			if module.NamedFunction("foo.Plain").IsNil() {
				t.Fatalf("plain root body is absent:\n%s", module.String())
			}
			if !module.NamedFunction("foo.Plain" + coroPrimarySuffix).IsNil() {
				t.Fatalf("plain root incorrectly gained a coroutine body:\n%s", module.String())
			}
			if got := pkg.CoroRootPackageAnchor(); got != "" {
				t.Fatalf("plain root package anchor = %q, want none", got)
			}
			if strings.Contains(module.String(), coroRootFactoryPrefix) ||
				strings.Contains(module.String(), coroRootFactoryDescriptorPrefix) {
				t.Fatalf("plain root incorrectly gained a root factory or descriptor:\n%s", module.String())
			}
		})
	}
}

func TestCoroExplicitPlainRootMayUseDescriptorRepresentation(t *testing.T) {
	const source = `package foo
var Saved func(uint32) uint32
func Plain(value uint32) uint32 {
	Saved = Plain
	return value + 1
}
`
	prog, ssaPkg, files, universe, plan := prepareCoroRootFactoryTestPlan(
		t, source, []coroRootFactoryTestRoot{{name: "Plain", demand: coro.SyncDemand}}, nil,
	)
	defer prog.Dispose()
	plain := ssaPkg.Func("Plain")
	function, ok := plan.FunctionPlan(plain)
	if !ok || function.Emission != coro.EmitPlain || function.Primary != coro.PrimaryPlain || function.FuncRep != coro.Dispatch {
		t.Fatalf("descriptor-backed plain root plan = %+v, present=%t", function, ok)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.FuncRepABI = coro.FuncRepABIV1
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if module.NamedFunction("foo.Plain").IsNil() {
		t.Fatalf("descriptor-backed plain root body is absent:\n%s", module.String())
	}
	if strings.Contains(module.String(), coroRootFactoryPrefix) || strings.Contains(module.String(), coroRootFactoryDescriptorPrefix) {
		t.Fatalf("descriptor-backed plain root incorrectly gained a coroutine root factory:\n%s", module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify descriptor-backed plain root: %v\n%s", err, module.String())
	}
}

func TestCoroExplicitPlainAsyncRootAcceptsPropagatedSyncDemand(t *testing.T) {
	const source = `package foo
func Plain(value uint32) uint32 { return value + 1 }
func Caller() uint32 { return Plain(41) }
`
	prog, ssaPkg, files, universe, plan := prepareCoroRootFactoryTestPlan(
		t, source,
		[]coroRootFactoryTestRoot{
			{name: "Plain", demand: coro.AsyncDemand},
			{name: "Caller", demand: coro.SyncDemand},
		},
		nil,
	)
	defer prog.Dispose()
	plain := ssaPkg.Func("Plain")
	function, ok := plan.FunctionPlan(plain)
	if !ok || function.Demand != coro.BothDemand || function.Emission != coro.EmitPlain ||
		function.FuncRep != coro.DirectPlain {
		t.Fatalf("propagated-demand plain root plan = %+v, present=%t", function, ok)
	}
	roots := plan.Roots()
	foundExplicitAsync := false
	for _, root := range roots {
		if root.Function == plain {
			foundExplicitAsync = root.Demand == coro.AsyncDemand
		}
	}
	if !foundExplicitAsync {
		t.Fatalf("plain explicit root set = %+v, want async-only root with total both demand", roots)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Module().NamedFunction("foo.Plain").IsNil() ||
		!pkg.Module().NamedFunction("foo.Plain"+coroPrimarySuffix).IsNil() {
		t.Fatalf("propagated-demand plain root did not keep one plain body:\n%s", pkg.Module().String())
	}
}

func TestCoroPhysicalConsumersAcceptBuiltinInPlainBody(t *testing.T) {
	const source = `package foo
func Helper() {}
func Plain(values []int) int { return len(values) }
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
	plain := ssaPkg.Func("Plain")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: plain, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          universe.FunctionIDConfig(),
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var builtinCall ssa.CallInstruction
	for _, block := range plain.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil {
				continue
			}
			builtin, ok := call.Common().Value.(*ssa.Builtin)
			if ok && builtin.Name() == "len" {
				builtinCall = call
			}
		}
	}
	if builtinCall == nil {
		t.Fatal("Plain has no SSA len builtin call")
	}
	if _, found := plan.CallPlan(builtinCall); found {
		t.Fatal("AnalyzeSSA unexpectedly created a CallPlan for len")
	}
	pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
		Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe},
	})
	if err != nil {
		t.Fatalf("compile active physical ABI plain builtin: %v", err)
	}
	if pkg.Module().NamedFunction("foo.Plain").IsNil() {
		t.Fatalf("plain builtin body was not emitted:\n%s", pkg.String())
	}

	// The exemption is exact: a non-builtin CallInstruction introduced after
	// analysis still has no CallPlan and must remain fail-closed.
	helper := ssaPkg.Func("Helper")
	plain.Blocks[0].Instrs = append(plain.Blocks[0].Instrs, &ssa.Call{
		Call: ssa.CallCommon{Value: helper},
	})
	err = validateCoroPhysicalConsumers(plan, false)
	if err == nil || !strings.Contains(err.Error(), "call has no compilation CallPlan") {
		t.Fatalf("non-builtin call without CallPlan error = %v", err)
	}
}

func TestCoroPhysicalABICacheRegistrationPreservesPhysicalMetadata(t *testing.T) {
	const source = `package foo
func Leaf(value uint32) uint32 { return value + 1 }
`
	compile := func(cacheHit bool) (string, int) {
		t.Helper()
		ssaPkg, _, files := buildGoSSAPkg(t, source)
		prog := newLLSSAProg(t)
		defer prog.Dispose()
		prog.EnableFuncInfoMetadata(true)
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
		leaf := ssaPkg.Func("Leaf")
		plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: leaf, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == leaf {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		observerCalls := 0
		compilation := &Compilation{
			CoroPlan:         plan,
			CoroPlanObserver: func(*ssa.Package, *coro.SSAPlan) { observerCalls++ },

			CoroPlanDigest:   strings.Repeat("0", 64),
			CoroABI:          coro.PhysicalABIV1,
			SchedulerABI:     coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
			PanicABI:         coro.PanicExplicitStatusABIV0,
			FuncRepABI:       coro.FuncRepABIV1,
			EmissionUniverse: universe}
		installCoroLoweringFactsForTest(t, compilation)
		pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
			Compilation: compilation,
			CacheHit:    cacheHit,
		})
		if err != nil {
			t.Fatal(err)
		}
		return pkg.String(), observerCalls
	}

	sourceIR, sourceObserverCalls := compile(false)
	if sourceObserverCalls != 1 {
		t.Fatalf("source observer calls = %d, want 1", sourceObserverCalls)
	}
	cachedIR, cachedObserverCalls := compile(true)
	if cachedObserverCalls != 0 {
		t.Fatalf("cache registration observer calls = %d, want 0", cachedObserverCalls)
	}
	if cachedIR != sourceIR {
		t.Fatalf("cache registration changed plan-aware frontend metadata:\nsource:\n%s\ncached:\n%s", sourceIR, cachedIR)
	}
	for _, required := range []string{"$coro", "llvm.coro.", coroFrameAllocHookV1, coroFrameFreeHookV1, coroDescriptorPrefixV1} {
		if !strings.Contains(cachedIR, required) {
			t.Fatalf("cache registration is missing physical coroutine marker %q:\n%s", required, cachedIR)
		}
	}
	if !strings.Contains(cachedIR, `!"foo.Leaf$coro"`) {
		t.Fatalf("cache registration funcinfo does not name the archived coroutine symbol:\n%s", cachedIR)
	}
}

func compileCoroLeafPhysicalABI(t *testing.T, target *llssa.Target) (llssa.Program, llssa.Package) {
	t.Helper()
	return compileCoroLeafPhysicalABISource(t, target, `package foo
func Leaf(value uint32) uint32 { return value + 1 }
`)
}

func compileCoroLeafPhysicalABISource(t *testing.T, target *llssa.Target, source string) (llssa.Program, llssa.Package) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	return compileCoroLeafPhysicalABIPackage(t, target, ssaPkg, files)
}

func compileCoroLeafPhysicalABIPackage(t *testing.T, target *llssa.Target, ssaPkg *ssa.Package, files []*ast.File) (llssa.Program, llssa.Package) {
	t.Helper()
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	leaf := ssaPkg.Func("Leaf")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: leaf, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          universe.FunctionIDConfig(),
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == leaf {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
		Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg
}

func compileCoroChildAwaitPhysicalABI(t *testing.T, target *llssa.Target) (llssa.Program, llssa.Package) {
	t.Helper()
	prog, ssaPkg, files, universe, plan := prepareCoroChildAwaitPhysicalABI(t, target)
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,
	}
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg
}

func prepareCoroChildAwaitPhysicalABI(t *testing.T, target *llssa.Target) (
	llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coro.SSAPlan,
) {
	t.Helper()
	const source = `package foo
func Child(first uint8, second uint32) uint32 { return uint32(first) + second }
func Parent(first uint8, second uint32) uint32 { return Child(first, second) + 1 }
`
	return prepareCoroChildAwaitPhysicalABISource(t, target, source)
}

func prepareCoroChildAwaitPhysicalABISource(
	t *testing.T, target *llssa.Target, source string,
) (llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coro.SSAPlan) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
		prog.Dispose()
		t.Fatal(err)
	}
	for name, fn := range map[string]*ssa.Function{"Parent": parent, "Child": child} {
		function, ok := plan.FunctionPlan(fn)
		if !ok || function.Primary != coro.PrimaryCoroutine || function.FuncRep != coro.DirectCoro || function.Demand != coro.AsyncDemand {
			prog.Dispose()
			t.Fatalf("%s child-await plan = %+v, present=%t; want async-only direct coroutine", name, function, ok)
		}
	}
	return prog, ssaPkg, files, universe, plan
}

func prepareCoroPhysicalValueTransportABI(t *testing.T, target *llssa.Target) (
	llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coro.SSAPlan,
) {
	t.Helper()
	const source = `package foo

type Payload struct {
	Ptr   *uint32
	Count uintptr
	Label string
	Bytes []byte
	Slots [2]uintptr
}

func Child(callback func(*uint32), ptr *uint32, value Payload) Payload {
	return value
}

func Parent(ptr *uint32, value Payload) Payload {
	return Child(nil, ptr, value)
}

func Pair(ptr *uint32, count uintptr) (*uint32, uintptr) {
	return ptr, count
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	parent, child, pair := ssaPkg.Func("Parent"), ssaPkg.Func("Child"), ssaPkg.Func("Pair")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: parent, Demand: coro.AsyncDemand},
		{Function: pair, Demand: coro.AsyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == child || fn == pair {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	for name, fn := range map[string]*ssa.Function{"Parent": parent, "Child": child, "Pair": pair} {
		function, ok := plan.FunctionPlan(fn)
		if !ok || function.Primary != coro.PrimaryCoroutine || function.FuncRep != coro.DirectCoro || function.Demand != coro.AsyncDemand {
			prog.Dispose()
			t.Fatalf("%s value-transport plan = %+v, present=%t; want async-only direct coroutine", name, function, ok)
		}
	}
	return prog, ssaPkg, files, universe, plan
}

func enableCoroChildAwaitCompilation(compilation *Compilation) {
	compilation.CoroABI = coro.PhysicalABIV1
	compilation.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	compilation.FuncRepABI = coro.FuncRepABIV1
}

func enableCoroPreemptCompilation(compilation *Compilation) {
	enableCoroChildAwaitCompilation(compilation)
}

func requireCoroPhysicalFunction(t *testing.T, module llvm.Module, sourceName string) llvm.Value {
	t.Helper()
	if legacy := module.NamedFunction(sourceName); !legacy.IsNil() {
		t.Fatalf("coroutine retained legacy source ABI symbol %q:\n%s", sourceName, module.String())
	}
	physical := module.NamedFunction(sourceName + "$coro")
	if physical.IsNil() {
		t.Fatalf("coroutine physical symbol %q is absent:\n%s", sourceName+"$coro", module.String())
	}
	return physical
}

func assertCoroV1TaskAwareFrameCalls(t *testing.T, name, body string, pointerBits int) {
	t.Helper()
	integer := "i" + strconv.Itoa(pointerBits)
	alloc := regexp.MustCompile(
		`call ptr @` + regexp.QuoteMeta(coroFrameAllocHookV1) +
			`\(ptr [^,]+, ` + integer + ` [^,]+, ` + integer + ` [^,]+, ptr @__llgo_coro_frame_descriptor_v1\.[0-9a-f]+\)`,
	)
	if !alloc.MatchString(body) {
		t.Fatalf("%s lacks task-aware v1 frame allocation:\n%s", name, body)
	}
	free := regexp.MustCompile(
		`call void @` + regexp.QuoteMeta(coroFrameFreeHookV1) +
			`\(ptr [^,]+, ptr [^,]+, ` + integer + ` [^,]+, ` + integer + ` [^,]+, ptr @__llgo_coro_frame_descriptor_v1\.[0-9a-f]+\)`,
	)
	if !free.MatchString(body) {
		t.Fatalf("%s lacks task-aware v1 frame free:\n%s", name, body)
	}
}

func assertCoroV0HeaderStateZero(t *testing.T, body string) {
	t.Helper()
	for _, field := range []struct {
		index int
		type_ string
		name  string
	}{
		{index: coroHeaderSuspendReason, type_: "i16", name: "suspend reason"},
		{index: coroHeaderLifecycle, type_: "i16", name: "lifecycle"},
		{index: coroHeaderStateID, type_: "i32", name: "state ID"},
	} {
		addresses := regexp.MustCompile(
			`(?m)^\s*(%[-a-zA-Z$._0-9]+) = getelementptr[^\n{]* \{ ptr, ptr, ptr, ptr, ptr, i16, i16, i32, i32, i32 \}, ptr [^,]+, i32 0, i32 `+strconv.Itoa(field.index)+`\s*$`,
		).FindAllStringSubmatch(body, -1)
		if len(addresses) == 0 {
			t.Fatalf("v0 coroutine has no header %s store:\n%s", field.name, body)
		}
		for _, address := range addresses {
			store := regexp.MustCompile(
				`(?m)^\s*store ` + field.type_ + ` ([^,]+), ptr ` + regexp.QuoteMeta(address[1]) + `(?:,|\s*$)`,
			).FindStringSubmatch(body)
			if len(store) != 2 {
				t.Fatalf("v0 coroutine header %s address %s has no store:\n%s", field.name, address[1], body)
			}
			if store[1] != "0" {
				t.Fatalf("v0 coroutine header %s = %s, want reserved zero state:\n%s", field.name, store[1], body)
			}
		}
	}
}

func assertCoroV1InitialPublish(t *testing.T, name, body string) {
	t.Helper()
	begin := strings.Index(body, "call ptr @llvm.coro.begin")
	publish := strings.Index(body, "call void @"+coroFramePublishHookV3)
	suspend := strings.Index(body, "call i8 @llvm.coro.suspend")
	if begin < 0 || publish < 0 || suspend < 0 || !(begin < publish && publish < suspend) {
		t.Fatalf("%s does not publish its v1 frame after coro.begin and before initial suspend:\n%s", name, body)
	}
	call := regexp.MustCompile(
		`call void @` + regexp.QuoteMeta(coroFramePublishHookV3) +
			`\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^)]+\)`,
	)
	if !call.MatchString(body) {
		t.Fatalf("%s frame publication lacks (task, handle, header, storage, metadata, descriptor, result):\n%s", name, body)
	}
}

func assertCoroScalarRunDecisionCalls(t *testing.T, name, body string, want int) {
	t.Helper()
	callPrefix := "call i32 @" + coroRunDecisionTakeZeroHookV1
	if got := strings.Count(body, callPrefix); got != want {
		t.Fatalf("%s run-decision calls = %d, want %d:\n%s", name, got, want, body)
	}
	dispatch := regexp.MustCompile(
		`(?m)(%[-a-zA-Z$._0-9]+) = call i32 @` + regexp.QuoteMeta(coroRunDecisionTakeZeroHookV1) +
			`\(ptr [^)]+\)\n\s+(%[-a-zA-Z$._0-9]+) = icmp ne i32 (%[-a-zA-Z$._0-9]+), 0`,
	)
	matches := dispatch.FindAllStringSubmatch(body, -1)
	if got := len(matches); got != want {
		t.Fatalf("%s scalar zero-ticket dispatches = %d, want %d:\n%s", name, got, want, body)
	}
	completion := ""
	searchOffset := 0
	for _, match := range matches {
		if match[1] != match[3] {
			t.Fatalf("%s scalar run-decision result does not directly control its branch: %v:\n%s", name, match, body)
		}
		relative := strings.Index(body[searchOffset:], match[0])
		if relative < 0 {
			t.Fatalf("%s scalar run-decision block cannot be located:\n%s", name, body)
		}
		startOfMatch := searchOffset + relative
		searchOffset = startOfMatch + len(match[0])
		rest := body[searchOffset:]
		end := len(rest)
		if next := regexp.MustCompile(`(?m)^[-a-zA-Z$._0-9]+:`).FindStringIndex(rest); next != nil {
			end = next[0]
		}
		block := body[startOfMatch : searchOffset+end]
		branch := regexp.MustCompile(
			`(?m)^\s+br i1 ` + regexp.QuoteMeta(match[2]) +
				`, label %([-a-zA-Z$._0-9]+), label %[-a-zA-Z$._0-9]+` +
				`(?:, !prof ![0-9]+)?\s*$`,
		).FindStringSubmatch(block)
		if len(branch) != 2 {
			t.Fatalf("%s scalar run-decision result does not control its block terminator:\n%s", name, block)
		}
		completionBlock, ok := coroTextCancellationCompletionBlock(body, branch[1])
		if !ok {
			t.Fatalf("%s cancellation target %q does not have a unique unconditional path to completion:\n%s", name, branch[1], body)
		}
		if completion == "" {
			completion = completionBlock
		} else if completionBlock != completion {
			t.Fatalf("%s cancellation gates reach different cleanup entries %s and %s:\n%s",
				name, completion, completionBlock, body)
		}
	}
	if completion == "" {
		t.Fatalf("%s has no cancellation cleanup destination:\n%s", name, body)
	}
}

// coroTextCancellationCompletionBlock follows only unique unconditional
// branches. LLVM 22 may split child-consumption from the shared completion
// block, so comparing only the cancellation target's immediate successor is
// too strict: every gate must instead converge on the same exact completion
// publication block.
func coroTextCancellationCompletionBlock(body, start string) (string, bool) {
	labelPattern := regexp.MustCompile(`(?m)^([-a-zA-Z$._0-9]+):(?:[ \t]*;[^\n]*)?[ \t]*$`)
	matches := labelPattern.FindAllStringSubmatchIndex(body, -1)
	blocks := make(map[string]string, len(matches))
	for index, match := range matches {
		end := len(body)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		blocks[body[match[2]:match[3]]] = body[match[0]:end]
	}
	unconditional := regexp.MustCompile(`(?m)^\s+br label %([-a-zA-Z$._0-9]+)\s*$`)
	visited := make(map[string]bool)
	for label := start; label != "" && !visited[label]; {
		visited[label] = true
		block, ok := blocks[label]
		if !ok {
			return "", false
		}
		if strings.Contains(block, "call void @"+coroCompletePrepareHookV2) {
			return label, true
		}
		branches := unconditional.FindAllStringSubmatch(block, -1)
		if len(branches) != 1 {
			return "", false
		}
		label = branches[0][1]
	}
	return "", false
}

func assertCoroCancellationTerminalStatusPublication(t *testing.T, function llvm.Value) {
	t.Helper()
	if function.IsNil() {
		t.Fatal("cannot inspect cancellation terminal status in a nil function")
	}
	var terminalPointer llvm.Value
	completeCalls := 0
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Call || instruction.CalledValue().Name() != coroCompletePrepareHookV2 {
				continue
			}
			completeCalls++
			if got := instruction.OperandsCount() - 1; got != 4 {
				t.Fatalf("%s completion arguments = %d, want (g,handle,header,status):\n%s",
					function.Name(), got, instruction.String())
			}
			status := instruction.Operand(3)
			if status.InstructionOpcode() != llvm.Load || status.Type().TypeKind() != llvm.IntegerTypeKind ||
				status.Type().IntTypeWidth() != 32 {
				t.Fatalf("%s completion status is not loaded from frame-local storage:\n%s", function.Name(), instruction.String())
			}
			terminalPointer = status.Operand(0)
		}
	}
	if completeCalls != 1 || terminalPointer.IsNil() {
		t.Fatalf("%s completion publication calls = %d, want one frame-local status load:\n%s",
			function.Name(), completeCalls, function.String())
	}
	stores := make(map[uint64]llvm.BasicBlock)
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Store || instruction.Operand(1) != terminalPointer {
				continue
			}
			value := instruction.Operand(0)
			if value.Type().TypeKind() != llvm.IntegerTypeKind || value.Type().IntTypeWidth() != 32 ||
				value.IsAConstantInt().IsNil() {
				continue
			}
			status := value.ZExtValue()
			if status == coroAwaitCompletionAbort || status == coroAwaitCompletionShutdown {
				stores[status] = block
			}
		}
	}
	abort, abortOK := stores[coroAwaitCompletionAbort]
	shutdown, shutdownOK := stores[coroAwaitCompletionShutdown]
	if !abortOK || !shutdownOK || abort == shutdown {
		t.Fatalf("%s lacks distinct frame-local Abort/Shutdown stores:\n%s", function.Name(), function.String())
	}
	for status, block := range stores {
		terminator := block.LastInstruction()
		if terminator.IsNil() || terminator.InstructionOpcode() != llvm.Br || terminator.SuccessorsCount() != 1 ||
			!coroTestBlockCanReachDirectCall(terminator.Successor(0), coroCompletePrepareHookV2) {
			t.Fatalf("%s status %d does not converge on shared cleanup/completion:\n%s",
				function.Name(), status, block.AsValue().String())
		}
	}
}

func coroFrameAllocationSize(t *testing.T, ramp llvm.Value, pointerBits int) uint64 {
	t.Helper()
	if ramp.IsNil() {
		t.Fatal("cannot inspect frame allocation of nil coroutine ramp")
	}
	pattern := regexp.MustCompile(
		`call ptr @` + regexp.QuoteMeta(coroFrameAllocHookV1) +
			`\(ptr [^,]+, i` + strconv.Itoa(pointerBits) + ` ([0-9]+),`,
	)
	match := pattern.FindStringSubmatch(ramp.String())
	if len(match) != 2 {
		t.Fatalf("%s has no constant PhysicalABIV1 frame allocation:\n%s", ramp.Name(), ramp.String())
	}
	got, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		t.Fatalf("parse %s frame size %q: %v", ramp.Name(), match[1], err)
	}
	return got
}

func compileCoroDecisionFrameProbe(t *testing.T, target *llssa.Target, scalarGate bool) uint64 {
	t.Helper()
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	defer prog.Dispose()
	pkg := prog.NewPackage("coro_decision_frame_probe", "llgo/test/coro-decision-frame-probe")
	defer pkg.Module().Dispose()
	ctx := &context{
		prog: prog,
		pkg:  pkg,
		compilation: &Compilation{

			CoroABI:      coro.PhysicalABIV1,
			SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0},
	}
	sourceSignature := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	abi := newCoroPhysicalABI(ctx, plannedFunctionSymbol{
		name: "llgo.test.coro-decision-frame-probe",
		plan: coro.FunctionPlan{ID: "llgo.test.coro-decision-frame-probe"},
	}, sourceSignature)
	if !scalarGate {
		abi.runDecisionTakeZeroHook = ""
	}
	const name = "coro_decision_frame_probe$coro"
	ctx.fn = pkg.NewFunc(name, abi.physicalSig, llssa.InGo)
	b := ctx.fn.MakeBody(1)
	defer b.Dispose()
	body := ctx.beginCoroBody(b, abi, nil)
	body.completion = ctx.fn.MakeBlock()
	body.finalSuspend = ctx.fn.MakeBlock()
	body.bindCancellationCompletion(b)
	b.SetBlock(body.coro.InitialResumeBlock())
	body.activate(b)
	b.Jump(body.completion)
	b.SetBlock(body.completion)
	body.complete(b)
	b.SetBlock(body.finalSuspend)
	body.finish(b)
	b.EndBuild()
	module := pkg.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify target=%v scalar-gate=%t probe before CoroSplit: %v\n%s", target, scalarGate, err, module.String())
	}
	runCoroABITestPipeline(t, prog, module)
	ramp := module.NamedFunction(name)
	if scalarGate {
		assertCoroRunDecisionResumeOnly(t, module, name, 1)
	} else if functionHasReachableDirectCall(module.NamedFunction(name+".resume"), coroRunDecisionTakeZeroHookV1) {
		t.Fatalf("gate-off frame probe retained scalar run-decision call:\n%s", module.String())
	}
	return coroFrameAllocationSize(t, ramp, prog.PointerSize()*8)
}

func TestCoroPhysicalABIRuntimeContextDescriptorProof(t *testing.T) {
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg := prog.NewPackage("coro_context_descriptor_probe", "llgo/test/coro-context-descriptor-probe")
	defer pkg.Module().Dispose()
	ctx := &context{prog: prog, pkg: pkg}
	sig := types.NewSignatureType(nil, nil, nil, nil, nil, false)

	var contextFreeHash [16]byte
	for _, test := range []struct {
		name string
		exec coro.ExecFlags
		want bool
	}{
		{name: "closed managed body", want: true},
		{name: "ambient runtime primitive", exec: coro.NeedsRuntimeContext},
		{name: "open target", exec: coro.OpaqueExec},
		{name: "irq eligibility is orthogonal", exec: coro.IRQUnsafe, want: true},
		{name: "thread affinity is orthogonal", exec: coro.ThreadAffine, want: true},
		{name: "worker blocking is orthogonal", exec: coro.BlockForeign, want: true},
	} {
		abi := newCoroPhysicalABI(ctx, plannedFunctionSymbol{
			name: test.name,
			plan: coro.FunctionPlan{ID: coro.FunctionID("llgo.test.context." + test.name), Exec: test.exec},
		}, sig)
		got := abi.descriptorFlags&coro.FrameDescriptorNoRuntimeContextV1 != 0
		if got != test.want {
			t.Fatalf("%s context-free descriptor = %t, want %t (exec=%s)", test.name, got, test.want, test.exec)
		}
		if test.want {
			contextFreeHash = abi.hash
		} else if abi.hash == contextFreeHash {
			t.Fatalf("%s descriptor hash did not bind runtime-context mode", test.name)
		}
	}
}

func compileCoroPreemptCountdownFrameProbe(t *testing.T, target *llssa.Target, gated bool) uint64 {
	t.Helper()
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	defer prog.Dispose()
	pkg := prog.NewPackage("coro_preempt_frame_probe", "llgo/test/coro-preempt-frame-probe")
	defer pkg.Module().Dispose()
	ctx := &context{
		prog: prog,
		pkg:  pkg,
		compilation: &Compilation{
			CoroABI:      coro.PhysicalABIV1,
			SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		},
	}
	sourceSignature := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	abi := newCoroPhysicalABI(ctx, plannedFunctionSymbol{
		name: "llgo.test.coro-preempt-frame-probe",
		plan: coro.FunctionPlan{ID: "llgo.test.coro-preempt-frame-probe"},
	}, sourceSignature)
	name := "coro_preempt_frame_probe$coro"
	ctx.fn = pkg.NewFunc(name, abi.physicalSig, llssa.InGo)
	b := ctx.fn.MakeBody(1)
	defer b.Dispose()
	body := ctx.beginCoroBody(b, abi, nil)
	body.completion = ctx.fn.MakeBlock()
	body.finalSuspend = ctx.fn.MakeBlock()
	body.bindCancellationCompletion(b)
	b.SetBlock(body.coro.InitialResumeBlock())
	body.activate(b)
	if gated {
		body.pollAndSuspendForPreempt(b)
	} else {
		body.suspendCurrentFrameIfYieldRequested(b, b.Call(body.preemptPoll, body.task))
	}
	b.Jump(body.completion)
	b.SetBlock(body.completion)
	body.complete(b)
	b.SetBlock(body.finalSuspend)
	body.finish(b)
	b.EndBuild()
	module := pkg.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify target=%v gated=%t preemption frame probe before CoroSplit: %v\n%s", target, gated, err, module.String())
	}
	runCoroABITestPipeline(t, prog, module)
	return coroFrameAllocationSize(t, module.NamedFunction(name), prog.PointerSize()*8)
}

func assertCoroRunDecisionResumeOnly(t *testing.T, module llvm.Module, rampName string, want int) {
	t.Helper()
	for _, name := range []string{rampName, rampName + ".destroy"} {
		function := module.NamedFunction(name)
		if function.IsNil() {
			t.Fatalf("post-CoroSplit module has no function %q:\n%s", name, module.String())
		}
		if functionHasReachableDirectCall(function, coroRunDecisionTakeZeroHookV1) {
			t.Fatalf("run-decision gate is reachable outside the resume entry in %s:\n%s", name, function.String())
		}
	}
	resumeName := rampName + ".resume"
	resume := module.NamedFunction(resumeName)
	if resume.IsNil() {
		t.Fatalf("post-CoroSplit module has no function %q:\n%s", resumeName, module.String())
	}
	assertCoroScalarRunDecisionCalls(t, resumeName, resume.String(), want)
}

// functionHasReachableDirectCall follows only executable CFG edges. LLVM's
// coro-split clones case-0 resume blocks into .destroy, then makes those blocks
// dead by replacing llvm.coro.suspend with the constant destroy result 1.
// Frontend test functions are optnone, so simplifycfg intentionally retains
// that textual dead clone; it must not be mistaken for an executable gate.
func functionHasReachableDirectCall(function llvm.Value, callee string) bool {
	entry := function.EntryBasicBlock()
	if entry.IsNil() {
		return false
	}
	type cfgEdge struct {
		block       llvm.BasicBlock
		predecessor llvm.BasicBlock
	}
	type cfgState struct {
		cfgEdge
		constants map[llvm.Value]uint64
	}
	seen := make(map[cfgEdge][]map[llvm.Value]uint64)
	pending := []cfgState{{cfgEdge: cfgEdge{block: entry}, constants: make(map[llvm.Value]uint64)}}
	for len(pending) != 0 {
		state := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		alreadySeen := false
		for _, constants := range seen[state.cfgEdge] {
			if sameCoroCFGConstants(constants, state.constants) {
				alreadySeen = true
				break
			}
		}
		if alreadySeen {
			continue
		}
		seen[state.cfgEdge] = append(seen[state.cfgEdge], state.constants)
		constants := copyCoroCFGConstants(state.constants)
		for instruction := state.block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if !instruction.IsAPHINode().IsNil() {
				value, ok := coroCFGPHIIncomingConstant(instruction, state.predecessor, constants)
				if ok {
					constants[instruction] = value
				} else {
					delete(constants, instruction)
				}
			}
			if (!instruction.IsACallInst().IsNil() || !instruction.IsAInvokeInst().IsNil()) &&
				instruction.CalledValue().Name() == callee {
				return true
			}
		}
		terminator := state.block.LastInstruction()
		for _, successor := range executableTerminatorSuccessors(terminator, constants) {
			pending = append(pending, cfgState{
				cfgEdge:   cfgEdge{block: successor, predecessor: state.block},
				constants: constants,
			})
		}
	}
	return false
}

func executableTerminatorSuccessors(terminator llvm.Value, constants map[llvm.Value]uint64) []llvm.BasicBlock {
	count := terminator.SuccessorsCount()
	if count == 0 {
		return nil
	}
	if terminator.InstructionOpcode() == llvm.Br && count == 2 {
		if condition, ok := coroCFGConstant(terminator.Operand(0), constants); ok {
			if condition != 0 {
				return []llvm.BasicBlock{terminator.Successor(0)}
			}
			return []llvm.BasicBlock{terminator.Successor(1)}
		}
	}
	if terminator.InstructionOpcode() == llvm.Switch {
		if condition, ok := coroCFGConstant(terminator.Operand(0), constants); ok {
			selected := 0
			for successor := 1; successor < count; successor++ {
				if terminator.GetSwitchCaseValue(successor).ZExtValue() == condition {
					selected = successor
					break
				}
			}
			return []llvm.BasicBlock{terminator.Successor(selected)}
		}
	}
	successors := make([]llvm.BasicBlock, count)
	for successor := range successors {
		successors[successor] = terminator.Successor(successor)
	}
	return successors
}

func coroCFGPHIIncomingConstant(
	phi llvm.Value,
	predecessor llvm.BasicBlock,
	constants map[llvm.Value]uint64,
) (uint64, bool) {
	if predecessor.IsNil() {
		return 0, false
	}
	for incoming := 0; incoming < phi.IncomingCount(); incoming++ {
		if phi.IncomingBlock(incoming) == predecessor {
			return coroCFGConstant(phi.IncomingValue(incoming), constants)
		}
	}
	return 0, false
}

func coroCFGConstant(value llvm.Value, constants map[llvm.Value]uint64) (uint64, bool) {
	if !value.IsAConstantInt().IsNil() {
		return value.ZExtValue(), true
	}
	constant, ok := constants[value]
	return constant, ok
}

func copyCoroCFGConstants(constants map[llvm.Value]uint64) map[llvm.Value]uint64 {
	copy := make(map[llvm.Value]uint64, len(constants))
	for value, constant := range constants {
		copy[value] = constant
	}
	return copy
}

func sameCoroCFGConstants(left, right map[llvm.Value]uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for value, constant := range left {
		if other, ok := right[value]; !ok || other != constant {
			return false
		}
	}
	return true
}

func assertCoroV1InitialRunDecision(t *testing.T, name, body string) {
	t.Helper()
	initialSuspend := strings.Index(body, "call i8 @llvm.coro.suspend")
	if initialSuspend < 0 {
		t.Fatalf("%s initial resume has no initial suspend:\n%s", name, body)
	}
	decisionRelative := strings.Index(body[initialSuspend:], "call i32 @"+coroRunDecisionTakeZeroHookV1)
	if decisionRelative < 0 {
		t.Fatalf("%s initial resume has no run-decision gate:\n%s", name, body)
	}
	decision := initialSuspend + decisionRelative
	activate := regexp.MustCompile(`(?s)store i16 0,.*store i16 2,`).FindStringIndex(body[decision:])
	if activate == nil {
		t.Fatalf("%s run-decision gate is not before initial frame activation:\n%s", name, body)
	}
}

func assertCoroV1Completion(t *testing.T, name, body string) {
	t.Helper()
	complete := strings.Index(body, "call void @"+coroCompletePrepareHookV2)
	finalSuspend := strings.Index(body, "@llvm.coro.suspend(token none, i1 true)")
	if complete < 0 || finalSuspend < 0 || complete >= finalSuspend {
		t.Fatalf("%s does not prepare completion before final suspend:\n%s", name, body)
	}
	segment := body[:complete]
	state := regexp.MustCompile(`(?s)store i16 2,.*store i16 4,.*store i32 [1-9][0-9]*,`)
	if !state.MatchString(segment) {
		t.Fatalf("%s does not publish final reason/lifecycle/stateID before completion preparation:\n%s", name, body)
	}
}

func assertCoroStaticChildAwait(t *testing.T, parent string) {
	t.Helper()
	childCall := regexp.MustCompile(`call ptr @"?foo\.Child\$coro"?\(`).FindStringIndex(parent)
	publish := strings.Index(parent, "call void @"+coroFramePublishHookV3)
	initialSuspend := strings.Index(parent, "call i8 @llvm.coro.suspend")
	await := strings.Index(parent, "call void @"+coroAwaitPrepareHookV1)
	inline := strings.Index(parent, "call i1 @"+coroAwaitInlineBeginHookV2)
	if childCall == nil || publish < 0 || initialSuspend < 0 || await < 0 ||
		inline < 0 ||
		!(publish < initialSuspend && initialSuspend < childCall[0] && childCall[0] < await && await < inline) {
		t.Fatalf("Parent hook order is not frame_publish -> initial suspend -> Child -> await_prepare -> await_inline:\n%s", parent)
	}
	prefix := parent[childCall[0]:await]
	promiseResult := regexp.MustCompile(`(%[-a-zA-Z$._0-9]+) = call ptr @llvm\.coro\.promise\(ptr [^,]+, i32 [0-9]+, i1 false\)`).FindStringSubmatch(prefix)
	parentHandle := regexp.MustCompile(`(%[-a-zA-Z$._0-9]+) = call ptr @llvm\.coro\.begin`).FindStringSubmatch(parent)
	if len(promiseResult) != 2 || len(parentHandle) != 2 {
		t.Fatalf("Parent child-await lacks named child promise or parent handle:\n%s", parent)
	}
	parentLink := regexp.MustCompile(
		`(?s)getelementptr [^\n]+, ptr ` + regexp.QuoteMeta(promiseResult[1]) +
			`, i32 0, i32 1\s+store ptr ` + regexp.QuoteMeta(parentHandle[1]) + `,`,
	)
	if !parentLink.MatchString(prefix) {
		t.Fatalf("Parent does not store its handle into child.parent before handoff:\n%s", prefix)
	}
	state := regexp.MustCompile(`(?s)store i16 1,.*store i16 3,.*store i32 1,`)
	if !state.MatchString(prefix) {
		t.Fatalf("Parent does not publish Call/Suspended/stateID=1 before await_prepare:\n%s", prefix)
	}
	awaitSuspend := strings.Index(parent[inline:], "call i8 @llvm.coro.suspend")
	if awaitSuspend < 0 {
		t.Fatalf("Parent does not retain a conditional slow suspend after await_inline:\n%s", parent)
	}
	awaitSuspend += inline
	decisionRelative := strings.Index(parent[awaitSuspend:], "call i32 @"+coroRunDecisionTakeZeroHookV1)
	if decisionRelative < 0 {
		t.Fatalf("Parent slow edge does not take its run decision after await resume:\n%s", parent)
	}
	if !regexp.MustCompile(`(?s)call i1 @` + regexp.QuoteMeta(coroAwaitInlineBeginHookV2) +
		`.*xor i1 .*true.*br i1`).MatchString(parent[inline:]) {
		t.Fatalf("Parent does not branch to the slow suspend on an incomplete inline await:\n%s", parent)
	}
	resume := strings.Index(parent[inline:], "call void @llvm.coro.resume")
	done := strings.Index(parent[inline:], "call i1 @llvm.coro.done")
	finish := strings.Index(parent[inline:], "call i1 @"+coroAwaitInlineFinishHookV2)
	destroy := strings.Index(parent[inline:], "call void @llvm.coro.destroy")
	frameCommit := strings.Index(parent[inline:], "call void @"+coroFrameDestroyCommitHookV2)
	inlineCommit := strings.Index(parent[inline:], "call void @"+coroAwaitInlineDestroyCommitHookV2)
	if resume < 0 || done <= resume || finish <= done || destroy <= finish ||
		frameCommit <= destroy || inlineCommit <= frameCommit {
		t.Fatalf("Parent static-inline ownership order is not resume -> done -> finish -> destroy -> frame commit -> await commit:\n%s", parent)
	}
	if !regexp.MustCompile(`(?s)store i16 0,.*store i16 2,.*call i32 @` +
		regexp.QuoteMeta(coroAwaitConsumeHookV1) + `.*switch i32`).MatchString(parent[inline:]) {
		t.Fatalf("Parent shared fast/resumed continuation does not activate and consume the child outcome:\n%s", parent)
	}
	completionState := regexp.MustCompile(`(?s)store i16 2,.*store i16 4,.*store i32 2,`)
	if !completionState.MatchString(parent[childCall[0]:]) {
		t.Fatalf("Parent does not publish FrameComplete/FinalSuspended/stateID=2 after await:\n%s", parent)
	}
}

func runCoroABITestPipeline(t *testing.T, prog llssa.Program, module llvm.Module) {
	t.Helper()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify before CoroSplit: %v\n%s", err, module.String())
	}
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	const pipeline = "coro-early,cgscc(coro-split),coro-cleanup"
	if err := module.RunPasses(pipeline, prog.TargetMachine(), options); err != nil {
		t.Fatalf("run %s: %v\n%s", pipeline, err, module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify after CoroSplit: %v\n%s", err, module.String())
	}
}

func runCoroABIGlobalDCE(t *testing.T, prog llssa.Program, module llvm.Module) {
	t.Helper()
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("globaldce", prog.TargetMachine(), options); err != nil {
		t.Fatalf("run globaldce: %v\n%s", err, module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify after globaldce: %v\n%s", err, module.String())
	}
}

func TestCoroRawPlainYieldIsSynchronousNoop(t *testing.T) {
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg := prog.NewPackage("coro_raw_yield", "llgo/test/coro-raw-yield")
	defer pkg.Module().Dispose()
	fn := pkg.NewFunc("raw_yield", llssa.NoArgsNoRet, llssa.InGo)
	b := fn.MakeBody(1)
	defer b.Dispose()
	ctx := &context{prog: prog, pkg: pkg, fn: fn, rawPlainBody: true}
	ctx.compileCoroYield(b)
	b.Return()
	b.EndBuild()
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify raw/plain yield no-op: %v\n%s", err, pkg.Module().String())
	}
	ir := pkg.Module().NamedFunction("raw_yield").String()
	for _, forbidden := range []string{coroYieldPrepareHookV1, "llvm.coro.", "llgo.coroYield"} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("raw/plain yield retained managed scheduler operation %q:\n%s", forbidden, ir)
		}
	}
}

type coroRootFactoryTestRoot struct {
	name   string
	demand coro.Demand
}

func prepareCoroRootFactoryTestPlan(
	t *testing.T,
	source string,
	testRoots []coroRootFactoryTestRoot,
	yieldOnly []string,
) (llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coro.SSAPlan) {
	return prepareCoroRootFactoryTestPlanWithMaxPlainInstructions(t, source, testRoots, yieldOnly, -1)
}

func prepareCoroRootFactoryTestPlanWithMaxPlainInstructions(
	t *testing.T,
	source string,
	testRoots []coroRootFactoryTestRoot,
	yieldOnly []string,
	maxPlainInstructions int,
) (llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coro.SSAPlan) {
	return prepareCoroRootFactoryTestPlanWithScheduler(
		t, source, testRoots, yieldOnly, maxPlainInstructions, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
	)
}

func prepareCoroPreemptTestPlan(
	t *testing.T,
	source string,
	testRoots []coroRootFactoryTestRoot,
	yieldOnly []string,
	maxPlainInstructions int,
) (llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coro.SSAPlan) {
	return prepareCoroRootFactoryTestPlanWithScheduler(
		t, source, testRoots, yieldOnly, maxPlainInstructions, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
	)
}

func prepareCoroProgramInitTestPlan(
	t *testing.T, source string,
) (llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coro.SSAPlan) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	packageInit := ssaPkg.Func("init")
	yield := ssaPkg.Func("Yield")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: packageInit, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			switch {
			case fn == yield:
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			case fn.Pkg != nil && fn.Pkg.Pkg.Path() == "embed" && fn.Name() == "init":
				// The fixture does not compile the standard embed package, but its
				// package initializer is an exact frozen no-suspend external edge.
				return coro.SSAFunctionPolicy{
					Effect: coro.NoSuspend, External: coro.ExternalKnown, OverrideExternal: true,
				}, nil
			default:
				return coro.SSAFunctionPolicy{}, nil
			}
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			return callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init", nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, ssaPkg, files, universe, plan
}

func prepareCoroRootFactoryTestPlanWithScheduler(
	t *testing.T,
	source string,
	testRoots []coroRootFactoryTestRoot,
	yieldOnly []string,
	maxPlainInstructions int,
	schedulerABI string,
) (llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coro.SSAPlan) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
	functionIDs.SchedulerABI = schedulerABI
	functionIDs.ArchiveReady = true
	roots := make(coro.Roots, len(testRoots))
	for i, root := range testRoots {
		fn := ssaPkg.Func(root.name)
		if fn == nil {
			prog.Dispose()
			t.Fatalf("test root %q is absent", root.name)
		}
		roots[i] = coro.Root{Function: fn, Demand: root.demand}
	}
	yieldSet := make(map[*ssa.Function]bool, len(yieldOnly))
	for _, name := range yieldOnly {
		fn := ssaPkg.Func(name)
		if fn == nil {
			prog.Dispose()
			t.Fatalf("yield-only function %q is absent", name)
		}
		yieldSet[fn] = true
	}
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, roots, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: maxPlainInstructions,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if yieldSet[fn] {
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

func requireSingleCoroRootFactoryV1(t *testing.T, module llvm.Module) (string, llvm.Value) {
	t.Helper()
	ir := module.String()
	pattern := regexp.MustCompile(
		`(?m)^define ptr @"?` + regexp.QuoteMeta(coroRootFactoryPrefix) + `([0-9a-f]{32})"?\(ptr [^,]+, ptr [^,]+, ptr [^)]+\)`,
	)
	matches := pattern.FindAllStringSubmatch(ir, -1)
	if len(matches) != 1 {
		t.Fatalf("root factory definitions = %d, want exactly one explicit factory:\n%s", len(matches), ir)
	}
	hash := matches[0][1]
	factory := module.NamedFunction(coroRootFactoryPrefix + hash)
	if factory.IsNil() {
		t.Fatalf("root factory %q is absent despite its definition:\n%s", coroRootFactoryPrefix+hash, ir)
	}
	return hash, factory
}

func coroRootPackageAnchorsV1(module llvm.Module) []llvm.Value {
	var anchors []llvm.Value
	for global := module.FirstGlobal(); !global.IsNil(); global = llvm.NextGlobal(global) {
		if strings.HasPrefix(global.Name(), coroRootPackageAnchorPrefix) &&
			!strings.HasSuffix(global.Name(), ".entries") {
			anchors = append(anchors, global)
		}
	}
	return anchors
}

func requireSingleCoroRootPackageAnchorV1(t *testing.T, module llvm.Module) llvm.Value {
	t.Helper()
	anchors := coroRootPackageAnchorsV1(module)
	if len(anchors) != 1 {
		t.Fatalf("root package anchors = %d, want exactly one:\n%s", len(anchors), module.String())
	}
	anchor := anchors[0]
	if !anchor.IsGlobalConstant() || anchor.Linkage() != llvm.ExternalLinkage ||
		anchor.Visibility() != llvm.HiddenVisibility {
		t.Fatalf("root package anchor is not an external hidden constant: %v", anchor)
	}
	return anchor
}

func stripCoroRootPackageConstantPointer(value llvm.Value) llvm.Value {
	for !value.IsAConstantExpr().IsNil() && value.OperandsCount() == 1 {
		value = value.Operand(0)
	}
	return value
}

func assertCoroRootPackageAnchorLLVMUsed(t *testing.T, module llvm.Module, anchor llvm.Value) {
	t.Helper()
	used := module.NamedGlobal("llvm.used")
	if used.IsNil() || used.Initializer().IsNil() {
		t.Fatalf("root package anchor is not protected by llvm.used:\n%s", module.String())
	}
	for i := 0; i < used.Initializer().OperandsCount(); i++ {
		if stripCoroRootPackageConstantPointer(used.Initializer().Operand(i)).C == anchor.C {
			return
		}
	}
	t.Fatalf("llvm.used does not retain root package anchor %q:\n%s", anchor.Name(), module.String())
}

func requireCoroFrameDescriptorHash(t *testing.T, name, body string) string {
	t.Helper()
	matches := regexp.MustCompile(
		`@`+regexp.QuoteMeta(coroDescriptorPrefixV1)+`([0-9a-f]{32})`,
	).FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatalf("%s has no PhysicalABIV1 frame descriptor:\n%s", name, body)
	}
	hash := matches[0][1]
	for _, match := range matches[1:] {
		if match[1] != hash {
			t.Fatalf("%s references multiple frame descriptor hashes %s and %s:\n%s", name, hash, match[1], body)
		}
	}
	return hash
}

func assertCoroRootFactoryV1Body(t *testing.T, body string) {
	t.Helper()
	if !regexp.MustCompile(
		`define ptr @"?` + regexp.QuoteMeta(coroRootFactoryPrefix) + `[0-9a-f]{32}"?\(ptr %0, ptr %1, ptr %2\)`,
	).MatchString(body) {
		t.Fatalf("root factory does not use (g, out, startup) -> handle ABI:\n%s", body)
	}
	loads := regexp.MustCompile(
		`(?s)(%[-a-zA-Z$._0-9]+) = getelementptr inbounds[^\n{]*\{ i8, i32 \}, ptr %2, i32 0, i32 0\s+` +
			`(%[-a-zA-Z$._0-9]+) = load i8, ptr [^,]+, align 1.*?` +
			`(%[-a-zA-Z$._0-9]+) = getelementptr inbounds[^\n{]*\{ i8, i32 \}, ptr %2, i32 0, i32 1\s+` +
			`(%[-a-zA-Z$._0-9]+) = load i32, ptr [^,]+, align 4`,
	).FindStringSubmatch(body)
	if len(loads) != 5 {
		t.Fatalf("root factory does not load typed {uint8,uint32} startup arguments:\n%s", body)
	}
	call := regexp.MustCompile(
		`call ptr @"?foo\.Parent\$coro"?\(ptr %0, ptr %1, i8 ` + regexp.QuoteMeta(loads[2]) +
			`, i32 ` + regexp.QuoteMeta(loads[4]) + `\)`,
	)
	if !call.MatchString(body) {
		t.Fatalf("root factory does not pass (g, out, typed startup args) to Parent$coro exactly:\n%s", body)
	}
	if got := len(regexp.MustCompile(`\bcall\b`).FindAllString(body, -1)); got != 1 {
		t.Fatalf("root factory calls = %d, want only Parent$coro:\n%s", got, body)
	}
	for _, forbidden := range []string{
		"llvm.coro.", "coro.suspend", ".resume", ".destroy", "clone",
		`@"foo.Parent"(`, "@foo.Parent(", `@"foo.Child$coro"(`, "@foo.Child$coro(",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("root factory contains forbidden coroutine/clone/plain-primary marker %q:\n%s", forbidden, body)
		}
	}
}

func assertCoroRootFactoryV1Descriptor(t *testing.T, ir, hash, parentHash string, pointerBits int) {
	t.Helper()
	if hash != parentHash {
		t.Fatalf("root factory hash %s does not match Parent physical ABI hash %s", hash, parentHash)
	}
	uintptrType := "i" + strconv.Itoa(pointerBits)
	rootPattern := regexp.MustCompile(
		`@` + regexp.QuoteMeta(coroRootFactoryDescriptorPrefix+hash) +
			` = linkonce_odr unnamed_addr constant \{ i32, i32, i64, i64, ptr, ` + uintptrType + `, ` + uintptrType + `, ` + uintptrType + `, ` + uintptrType + ` \} ` +
			`\{ i32 1, i32 0, i64 ([^,]+), i64 ([^,]+), ptr @"?` + regexp.QuoteMeta(coroRootFactoryPrefix+hash) + `"?, ` +
			uintptrType + ` 8, ` + uintptrType + ` 4, ` + uintptrType + ` 4, ` + uintptrType + ` 4 \}`,
	)
	root := rootPattern.FindStringSubmatch(ir)
	if len(root) != 3 {
		t.Fatalf("root descriptor lacks v1/hash/factory/startup(8,4)/result(4,4) target layout:\n%s", ir)
	}
	framePattern := regexp.MustCompile(
		`@` + regexp.QuoteMeta(coroDescriptorPrefixV1+parentHash) +
			` = linkonce_odr unnamed_addr constant ` +
			`\{ i32, i32, i64, i64, ` + uintptrType + `, ` + uintptrType +
			`, \{ ptr, ` + uintptrType + ` \}, \{ ptr, ` + uintptrType + ` \} \} ` +
			`\{ i32 1, i32 ` + strconv.FormatUint(uint64(coro.FrameDescriptorNoRuntimeContextV1), 10) +
			`, i64 ([^,]+), i64 ([^,]+),`,
	)
	frame := framePattern.FindStringSubmatch(ir)
	if len(frame) != 3 {
		t.Fatalf("Parent frame descriptor %q is absent:\n%s", coroDescriptorPrefixV1+parentHash, ir)
	}
	if root[1] != frame[1] || root[2] != frame[2] {
		t.Fatalf("root descriptor hash words = (%s,%s), Parent frame hash words = (%s,%s)", root[1], root[2], frame[1], frame[2])
	}
}

func assertCoroRootDescriptorLLVMUsed(t *testing.T, module llvm.Module, hash string) {
	t.Helper()
	used := module.NamedGlobal("llvm.used")
	if used.IsNil() {
		t.Fatalf("root descriptor is not protected from final-link dead stripping by llvm.used:\n%s", module.String())
	}
	if got := used.Linkage(); got != llvm.AppendingLinkage {
		t.Fatalf("llvm.used linkage = %v, want appending", got)
	}
	if got := used.Section(); got != "llvm.metadata" {
		t.Fatalf("llvm.used section = %q, want llvm.metadata", got)
	}
	name := coroRootFactoryDescriptorPrefix + hash
	var usedLine string
	for _, line := range strings.Split(module.String(), "\n") {
		if strings.HasPrefix(line, "@llvm.used =") {
			usedLine = line
			break
		}
	}
	if usedLine == "" || (!strings.Contains(usedLine, "ptr @"+name) &&
		!strings.Contains(usedLine, `ptr @"`+name+`"`)) {
		t.Fatalf("llvm.used does not retain root descriptor %q: %s", name, usedLine)
	}
}

func assertCoroRootObjectRetained(t *testing.T, prog llssa.Program, module llvm.Module, hash string) {
	t.Helper()
	object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		t.Fatalf("emit root-retention object: %v\n%s", err, module.String())
	}
	defer object.Dispose()
	for _, name := range []string{
		coroRootFactoryDescriptorPrefix + hash,
		coroRootFactoryPrefix + hash,
	} {
		if !bytes.Contains(object.Bytes(), []byte(name)) {
			t.Fatalf("object symbol table lost linker-retained root symbol %q", name)
		}
	}
}

func hasLLVMCall(ir, intrinsic string) bool {
	return regexp.MustCompile(`call [^\n]*@` + regexp.QuoteMeta(intrinsic) + `\b`).MatchString(ir)
}
