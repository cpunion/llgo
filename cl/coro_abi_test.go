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
	assertCoroV0HeaderStateZero(t, leafIR)
	if !regexp.MustCompile(`define ptr @"?foo\.Leaf\$coro"?\(ptr [^,]+, ptr [^,]+, i32 `).MatchString(leafIR) {
		t.Fatalf("coroutine leaf does not use (g, out, args...) -> handle ABI:\n%s", leafIR)
	}
	if got := strings.Count(leafIR, "call i8 @llvm.coro.suspend"); got != 2 {
		t.Fatalf("coro.suspend calls = %d, want initial + final:\n%s", got, leafIR)
	}
	begin := strings.Index(leafIR, "call ptr @llvm.coro.begin")
	firstStore := strings.Index(leafIR, "store ")
	initialSuspend := strings.Index(leafIR, "call i8 @llvm.coro.suspend")
	if begin < 0 || firstStore < 0 || initialSuspend < 0 || !(begin < firstStore && firstStore < initialSuspend) {
		t.Fatalf("promise/header was not published after coro.begin and before initial suspend:\n%s", leafIR)
	}
	if !strings.Contains(leafIR, "store i32") {
		t.Fatalf("coroutine result was not copied to the external result slot:\n%s", leafIR)
	}
	for _, symbol := range []string{coroFrameAllocHook, coroFrameFreeHook, coroDescriptorPrefix} {
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
	if !strings.Contains(module.String(), "!dbg") {
		t.Fatalf("debug coroutine omitted function/parameter metadata:\n%s", module.String())
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
	if !regexp.MustCompile(`@__llgo_coro_frame_descriptor_v0\.[0-9a-f]+ = linkonce_odr unnamed_addr constant \{ i32, i32, i64, i64, i32, i32 \}`).MatchString(ir) {
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
	for _, forbidden := range []string{"llvm.coro.resume", "llvm.coro.done", "llvm.coro.destroy"} {
		if hasLLVMCall(parentIR, forbidden) {
			t.Fatalf("Parent directly owns forbidden %s operation:\n%s", forbidden, parentIR)
		}
	}

	for _, hook := range []string{
		coroFrameAllocHookV1,
		coroFramePublishHookV1,
		coroAwaitPrepareHookV1,
		coroCompletePrepareHookV1,
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
	if got := strings.Count(ir, "call void @"+coroFramePublishHookV1); got != 2 {
		t.Fatalf("v1 frame publications = %d, want Parent + Child:\n%s", got, ir)
	}
	if got := strings.Count(ir, "call void @"+coroAwaitPrepareHookV1); got != 1 {
		t.Fatalf("v1 await preparations = %d, want one Parent->Child handoff:\n%s", got, ir)
	}
	if got := strings.Count(ir, "call void @"+coroCompletePrepareHookV1); got != 2 {
		t.Fatalf("v1 completion preparations = %d, want Parent + Child:\n%s", got, ir)
	}
	if got := strings.Count(ir, "call void @"+coroFrameFreeHookV1); got != 2 {
		t.Fatalf("task-aware v1 frame frees = %d, want Parent + Child:\n%s", got, ir)
	}
	for name, body := range map[string]string{"Parent": parentIR, "Child": childIR} {
		assertCoroV1TaskAwareFrameCalls(t, name, body, prog.PointerSize()*8)
		assertCoroV1InitialPublish(t, name, body)
		assertCoroV1Completion(t, name, body)
	}
	assertCoroStaticChildAwait(t, parentIR)

	descriptor := regexp.MustCompile(
		`@__llgo_coro_frame_descriptor_v1\.[0-9a-f]+ = linkonce_odr unnamed_addr constant \{ [^}]+ \} \{ i32 1,`,
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
	parentResume := module.NamedFunction("foo.Parent$coro.resume").String()
	if !regexp.MustCompile(`call ptr @"?foo\.Child\$coro"?\(`).MatchString(parentResume) {
		t.Fatalf("Parent resume entry lost the static child ramp call:\n%s", parentResume)
	}
	for _, hook := range []string{coroAwaitPrepareHookV1, coroCompletePrepareHookV1} {
		if !strings.Contains(parentResume, "call void @"+hook) {
			t.Fatalf("Parent resume entry lost %s:\n%s", hook, parentResume)
		}
	}
	for _, forbidden := range []string{"llvm.coro.resume", "llvm.coro.done", "llvm.coro.destroy"} {
		if hasLLVMCall(parentResume, forbidden) {
			t.Fatalf("post-split Parent directly calls forbidden %s:\n%s", forbidden, parentResume)
		}
	}
	for _, function := range []string{"foo.Parent$coro", "foo.Child$coro"} {
		ramp := module.NamedFunction(function).String()
		if !strings.Contains(ramp, "call void @"+coroFramePublishHookV1) {
			t.Fatalf("%s ramp lost frame publication:\n%s", function, ramp)
		}
		destroy := module.NamedFunction(function + ".destroy").String()
		if !strings.Contains(destroy, "call void @"+coroFrameFreeHookV1) {
			t.Fatalf("%s destroy entry lost task-aware frame free:\n%s", function, destroy)
		}
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
		`@__llgo_coro_frame_descriptor_v1\.[0-9a-f]+ = linkonce_odr unnamed_addr constant \{ i32, i32, i64, i64, i32, i32 \} \{ i32 1, i32 [^,]+, i64 [^,]+, i64 [^,]+, i32 [^,]+, i32 [^}]+ \}`,
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
}

func TestCoroChildAwaitPhysicalABIV1FailsClosed(t *testing.T) {
	prog, ssaPkg, files, universe, plan := prepareCoroChildAwaitPhysicalABI(t, nil)
	defer prog.Dispose()

	base := func() *Compilation {
		return &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe,
		}
	}
	for _, test := range []struct {
		name string
		edit func(*Compilation)
		want string
	}{
		{
			name: "child await without physical ABI",
			edit: func(c *Compilation) {
				c.EnableCoroEntryResolution = true
				c.EnableCoroChildAwait = true
			},
			want: "requires coroutine physical ABI",
		},
		{
			name: "physical ABI without entry resolution",
			edit: func(c *Compilation) {
				c.EnableCoroPhysicalABI = true
				c.EnableCoroChildAwait = true
			},
			want: "requires coroutine entry resolution",
		},
		{
			name: "static coroutine call without child await capability",
			edit: func(c *Compilation) {
				c.EnableCoroEntryResolution = true
				c.EnableCoroPhysicalABI = true
			},
			// PhysicalABIV0 retains its original leaf-only validation order and
			// diagnostic rather than adopting any v1 acceptance behavior.
			want: "requires an explicit, isolated yield-only effect",
		},
		{
			name: "v0 physical identity",
			edit: func(c *Compilation) {
				enableCoroChildAwaitCompilation(c)
				c.CoroABI = coro.PhysicalABIV0
			},
			want: `coroutine compilation coroutine ABI "llgo.coro.physical.v0" does not match "llgo.coro.physical.v1"`,
		},
		{
			name: "scheduler-none identity",
			edit: func(c *Compilation) {
				enableCoroChildAwaitCompilation(c)
				c.SchedulerABI = coro.SchedulerNoneABIV0
			},
			want: `coroutine compilation scheduler ABI "llgo.coro.scheduler.none.v0" does not match "llgo.coro.scheduler.child-await.v0"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			compilation := base()
			test.edit(compilation)
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
				t.Fatal("child-await preflight failure returned a partial package")
			}
			if observerCalls != 0 {
				t.Fatalf("observer calls = %d, want pre-codegen rejection", observerCalls)
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
			want:      "requires explicit async-only demand, got sync",
		},
		{
			name:   "both-demand explicit coroutine root",
			source: childAwaitSource,
			roots: []coroRootFactoryTestRoot{
				{name: "Parent", demand: coro.SyncDemand},
				{name: "Parent", demand: coro.AsyncDemand},
			},
			yieldOnly: []string{"Child"},
			want:      "requires explicit async-only demand, got both",
		},
		{
			name:   "plain explicit async root",
			source: `package foo; func Plain(first uint8, second uint32) uint32 { return uint32(first) + second }`,
			roots:  []coroRootFactoryTestRoot{{name: "Plain", demand: coro.AsyncDemand}},
			want:   "requires an async-only defined direct coroutine",
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

func TestCoroLeafPhysicalABIPreflightRejectsUnsupported(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "pointer parameter",
			source: `package foo; func Leaf(value *int) {}`,
			want:   "parameter 0 has unsupported type *int",
		},
		{
			name: "control flow",
			source: `package foo
func Leaf(value uint32) uint32 {
	if value == 0 { return 1 }
	return value
}`,
			want: "requires exactly one basic block",
		},
		{
			name: "call",
			source: `package foo
func Plain(value uint32) uint32 { return value }
func Leaf(value uint32) uint32 { return Plain(value) }`,
			want: "outside the ABI-only leaf allowlist",
		},
		{
			name: "spawn consumer",
			source: `package foo
func Leaf(value uint32) uint32 { return value + 1 }
func Launch() { go Leaf(1) }`,
			want: "goroutine spawn requires scheduler root lowering",
		},
		{
			name: "channel operation",
			source: `package foo
func Leaf(channel chan uint32) uint32 { return <-channel }`,
			want: "requires an explicit, isolated yield-only effect",
		},
		{
			name: "foreign ABI directive",
			source: `package foo
//export Leaf
func Leaf(value uint32) uint32 { return value + 1 }`,
			want: "ABI directive",
		},
		{
			name: "multiple results",
			source: `package foo
func Leaf(value uint32) (uint32, uint32) { return value, value }`,
			want: "supports at most one result",
		},
		{
			name: "shift requires hidden panic check",
			source: `package foo
func Leaf(value uint32, shift int) uint32 { return value << shift }`,
			want: "potentially panicking or non-scalar binary operation",
		},
		{
			name: "nested function literal",
			source: `package foo
func Leaf(value uint32) uint32 {
	_ = func() {}
	return value + 1
}`,
			want: "nested function literals require closure body lowering",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			ssaPkg, _, files := buildGoSSAPkg(t, test.source)
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
			if err != nil {
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
				t.Fatal(err)
			}
			observerCalls := 0
			got, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
				Compilation: &Compilation{
					CoroPlan:                  plan,
					EmissionUniverse:          universe,
					CoroPlanObserver:          func(*ssa.Package, *coro.SSAPlan) { observerCalls++ },
					EnableCoroEntryResolution: true,
					EnableCoroPhysicalABI:     true,
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preflight result = %v, %v; want error containing %q", got, err, test.want)
			}
			if got != nil {
				t.Fatal("preflight failure returned a partial package")
			}
			if observerCalls != 0 {
				t.Fatalf("observer calls = %d, want pre-codegen rejection", observerCalls)
			}
		})
	}
}

func TestCoroPhysicalABIRequiresEntryResolution(t *testing.T) {
	err := (&Compilation{EnableCoroPhysicalABI: true}).preflightCoroPlan()
	if err == nil || !strings.Contains(err.Error(), "requires coroutine entry resolution") {
		t.Fatalf("preflight error = %v, want entry-resolution requirement", err)
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
		universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
		if err != nil {
			t.Fatal(err)
		}
		ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		functionIDs := universe.FunctionIDConfig()
		functionIDs.CoroABI = coro.PhysicalABIV0
		functionIDs.SchedulerABI = coro.SchedulerNoneABIV0
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
		pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
			Compilation: &Compilation{
				CoroPlan:                  plan,
				CoroPlanObserver:          func(*ssa.Package, *coro.SSAPlan) { observerCalls++ },
				EnableCoroEntryResolution: true,
				EnableCoroPhysicalABI:     true,
				CoroPlanDigest:            strings.Repeat("0", 64),
				CoroABI:                   coro.PhysicalABIV0,
				SchedulerABI:              coro.SchedulerNoneABIV0,
				PanicABI:                  coro.PanicLegacyABIV0,
				FuncRepABI:                coro.FuncRepABIV0,
				EmissionUniverse:          universe,
			},
			CacheHit: cacheHit,
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
	for _, required := range []string{"$coro", "llvm.coro.", coroFrameAllocHook, coroFrameFreeHook, coroDescriptorPrefix} {
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
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
			CoroPlan:                  plan,
			EmissionUniverse:          universe,
			EnableCoroEntryResolution: true,
			EnableCoroPhysicalABI:     true,
		},
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
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
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

func enableCoroChildAwaitCompilation(compilation *Compilation) {
	compilation.EnableCoroEntryResolution = true
	compilation.EnableCoroPhysicalABI = true
	compilation.EnableCoroChildAwait = true
	compilation.CoroABI = coro.PhysicalABIV1
	compilation.SchedulerABI = coro.SchedulerChildAwaitABIV0
	compilation.PanicABI = coro.PanicLegacyABIV0
	compilation.FuncRepABI = coro.FuncRepABIV0
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
			`(?m)^\s*(%[-a-zA-Z$._0-9]+) = getelementptr[^\n{]* \{ ptr, ptr, ptr, ptr, ptr, i16, i16, i32, i32 \}, ptr [^,]+, i32 0, i32 `+strconv.Itoa(field.index)+`\s*$`,
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
	publish := strings.Index(body, "call void @"+coroFramePublishHookV1)
	suspend := strings.Index(body, "call i8 @llvm.coro.suspend")
	if begin < 0 || publish < 0 || suspend < 0 || !(begin < publish && publish < suspend) {
		t.Fatalf("%s does not publish its v1 frame after coro.begin and before initial suspend:\n%s", name, body)
	}
	call := regexp.MustCompile(
		`call void @` + regexp.QuoteMeta(coroFramePublishHookV1) + `\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^)]+\)`,
	)
	if !call.MatchString(body) {
		t.Fatalf("%s frame publication lacks (task, handle, header, storage):\n%s", name, body)
	}
}

func assertCoroV1Completion(t *testing.T, name, body string) {
	t.Helper()
	complete := strings.Index(body, "call void @"+coroCompletePrepareHookV1)
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
	publish := strings.Index(parent, "call void @"+coroFramePublishHookV1)
	initialSuspend := strings.Index(parent, "call i8 @llvm.coro.suspend")
	await := strings.Index(parent, "call void @"+coroAwaitPrepareHookV1)
	if childCall == nil || publish < 0 || initialSuspend < 0 || await < 0 ||
		!(publish < initialSuspend && initialSuspend < childCall[0] && childCall[0] < await) {
		t.Fatalf("Parent hook order is not frame_publish -> initial suspend -> Child -> await_prepare:\n%s", parent)
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
	awaitSuspend := strings.Index(parent[await:], "call i8 @llvm.coro.suspend")
	if awaitSuspend < 0 {
		t.Fatalf("Parent does not suspend after await_prepare:\n%s", parent)
	}
	awaitSuspend += await
	complete := strings.Index(parent[awaitSuspend:], "call void @"+coroCompletePrepareHookV1)
	if complete < 0 {
		t.Fatalf("Parent does not complete after its await resume:\n%s", parent)
	}
	complete += awaitSuspend
	completionState := regexp.MustCompile(`(?s)store i16 2,.*store i16 4,.*store i32 2,`)
	if !completionState.MatchString(parent[awaitSuspend:complete]) {
		t.Fatalf("Parent does not publish FrameComplete/FinalSuspended/stateID=2 after await:\n%s", parent[awaitSuspend:complete])
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
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
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
		MaxPlainInstructions: -1,
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
			` = linkonce_odr unnamed_addr constant \{ [^}]+ \} \{ i32 1, i32 0, i64 ([^,]+), i64 ([^,]+),`,
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
