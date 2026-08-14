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

package ssa

import (
	"fmt"
	"go/token"
	"go/types"
	"regexp"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

type coroTestFixture struct {
	prog Program
	pkg  Package
	fn   Function
	coro *CoroBuilder
}

func TestCoroBuilderPresplitShape(t *testing.T) {
	fixture := newCoroTestFixture(t, nil, 32)
	mod := fixture.pkg.Module()
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify presplit coroutine: %v\n%s", err, mod.String())
	}

	ir := mod.String()
	if !strings.Contains(ir, "presplitcoroutine") {
		t.Fatalf("coroutine lacks enum presplit attribute:\n%s", ir)
	}
	if !strings.Contains(ir, "@llvm.coro.id(i32 32") {
		t.Fatalf("coro.id lacks allocation alignment guarantee:\n%s", ir)
	}
	width := fixture.prog.PointerSize() * 8
	for _, intrinsic := range []string{"size", "align"} {
		want := fmt.Sprintf("@llvm.coro.%s.i%d", intrinsic, width)
		if !strings.Contains(ir, want) {
			t.Fatalf("missing target-width %s intrinsic %q:\n%s", intrinsic, want, ir)
		}
	}
	if got := strings.Count(ir, "call i8 @llvm.coro.suspend"); got != 3 {
		t.Fatalf("coro.suspend calls = %d, want initial + ordinary + final:\n%s", got, ir)
	}
	if !strings.Contains(ir, "@llvm.coro.suspend(token none, i1 true)") {
		t.Fatalf("missing final suspend:\n%s", ir)
	}
	if got := countCoroEndCalls(ir); got != 1 {
		t.Fatalf("coro.end calls = %d, want one shared end block:\n%s", got, ir)
	}
	assertCoroSuspendDefaults(t, fixture)

	if !strings.Contains(ir, "icmp ne") || !strings.Contains(ir, "coro.frame") || !strings.Contains(ir, "br i1") {
		t.Fatalf("coro.free result is not guarded by a non-null branch:\n%s", ir)
	}
	if !strings.Contains(ir, "call void @coro_frame_free") {
		t.Fatalf("missing injected frame free callback:\n%s", ir)
	}
	for _, forbidden := range []string{"@malloc", "@free(", "runtime/internal/runtime", "CoroEnter", "CoroReschedule"} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("structured builder introduced forbidden runtime coupling %q:\n%s", forbidden, ir)
		}
	}
}

func TestCoroBuilderSuspendCurrentBlockPreservesLogicalCFG(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("corologicalblock", "coro/logical/block")
	defer pkg.Module().Dispose()

	fn := pkg.NewFunc("coro_logical_block", coroHandleSignature(), InGo)
	b := fn.MakeBody(1)
	defer b.Dispose()
	coro := b.BeginCoro(CoroOptions{Frame: CoroFrameOps{
		Alloc: func(Builder, Expr, Expr) Expr { return prog.Nil(prog.VoidPtr()) },
		Free:  func(Builder, Expr, Expr, Expr) {},
	}})

	logical := fn.MakeBlock()
	join := fn.MakeBlock()
	b.Jump(logical)
	b.SetBlock(logical)
	first := logical.first
	originalLast := logical.last

	if got := coro.SuspendCurrentBlock(); got != logical {
		t.Fatalf("first suspend returned block %p, want logical block %p", got, logical)
	}
	firstResume := logical.last
	if firstResume.C == originalLast.C {
		t.Fatal("first suspend did not advance the logical block's physical tail")
	}
	if logical.first.C != first.C || b.blk != logical {
		t.Fatal("first suspend did not preserve the current logical block")
	}

	if got := coro.SuspendCurrentBlock(); got != logical {
		t.Fatalf("second suspend returned block %p, want logical block %p", got, logical)
	}
	secondResume := logical.last
	if secondResume.C == firstResume.C {
		t.Fatal("second suspend did not advance the logical block's physical tail")
	}
	if logical.first.C != first.C || b.blk != logical {
		t.Fatal("second suspend did not preserve the current logical block")
	}
	savedLogical := b.blk
	b.blk = nil
	mustPanicContains(t, "active logical block", func() { coro.SuspendCurrentBlock() })
	b.blk = savedLogical

	b.Jump(join)
	b.SetBlock(join)
	phi := b.Phi(prog.Byte())
	phi.AddIncoming(b, []BasicBlock{logical}, func(int, BasicBlock) Expr {
		return prog.IntVal(1, prog.Byte())
	})
	coro.Finish()
	b.EndBuild()

	if got := phi.impl.IncomingBlock(0); got.C != secondResume.C {
		t.Fatal("phi predecessor does not use the logical block's post-suspend physical tail")
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify coroutine with logical-block suspends: %v\n%s", err, pkg.Module().String())
	}
}

func TestCoroBuilderConditionalSuspendPreservesLogicalCFG(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("coroconditionalblock", "coro/conditional/block")
	defer pkg.Module().Dispose()

	fn := pkg.NewFunc("coro_conditional_block", coroHandleSignature(), InGo)
	b := fn.MakeBody(1)
	defer b.Dispose()
	var resumeCallbackBlocks []BasicBlock
	coro := b.BeginCoro(CoroOptions{
		Frame: CoroFrameOps{
			Alloc: func(Builder, Expr, Expr) Expr { return prog.Nil(prog.VoidPtr()) },
			Free:  func(Builder, Expr, Expr, Expr) {},
		},
		AfterResume: func(b Builder) {
			resumeCallbackBlocks = append(resumeCallbackBlocks, b.blk)
			b.Call(pkg.NewFunc("take_resume_decision", functionSignature(nil, nil), InC).Expr)
		},
	})
	if len(resumeCallbackBlocks) != 1 {
		t.Fatalf("initial resume callbacks = %d, want 1", len(resumeCallbackBlocks))
	}
	logical := fn.MakeBlock()
	join := fn.MakeBlock()
	b.Jump(logical)
	b.SetBlock(logical)
	first := logical.first
	mustPanicContains(t, "boolean condition", func() {
		coro.SuspendCurrentBlockIf(prog.IntVal(1, prog.Byte()), nil)
	})
	callbackCalls := 0
	var suspendCallbackBlock BasicBlock
	if got := coro.SuspendCurrentBlockIf(prog.BoolVal(true), func(b Builder) {
		callbackCalls++
		suspendCallbackBlock = b.blk
		b.Call(pkg.NewFunc("publish_yield", functionSignature(nil, nil), InC).Expr)
	}); got != logical {
		t.Fatalf("conditional suspend returned block %p, want %p", got, logical)
	}
	if callbackCalls != 1 || len(resumeCallbackBlocks) != 2 || logical.first.C != first.C || b.blk != logical {
		t.Fatal("conditional suspend did not preserve its logical block or publication callback")
	}
	resumeCallbackBlock := resumeCallbackBlocks[1]
	continuation := logical.last
	if suspendCallbackBlock == nil || resumeCallbackBlock == nil ||
		suspendCallbackBlock.last.C == resumeCallbackBlock.last.C ||
		resumeCallbackBlock.last.C == continuation.C {
		t.Fatal("conditional resume callback is not isolated from the suspend and false-edge continuation blocks")
	}
	b.Jump(join)
	b.SetBlock(join)
	phi := b.Phi(prog.Byte())
	phi.AddIncoming(b, []BasicBlock{logical}, func(int, BasicBlock) Expr {
		return prog.IntVal(1, prog.Byte())
	})
	coro.Finish()
	b.EndBuild()

	if got := phi.impl.IncomingBlock(0); got.C != continuation.C {
		t.Fatal("conditional suspend phi predecessor does not use the joined physical continuation")
	}
	ir := pkg.Module().String()
	if !strings.Contains(ir, "br i1 true") || !strings.Contains(ir, "call void @publish_yield") ||
		strings.Count(ir, "call void @take_resume_decision") != 2 {
		t.Fatalf("conditional suspend lacks poll branch/publication path:\n%s", ir)
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify conditional coroutine suspend: %v\n%s", err, ir)
	}
}

func TestCoroBuilderPerSuspendAfterResumeOverride(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("corooverride", "coro/resume/override")
	defer pkg.Module().Dispose()

	fn := pkg.NewFunc("coro_resume_override", coroHandleSignature(), InGo)
	b := fn.MakeBody(1)
	defer b.Dispose()
	defaultCalls := 0
	overrideCalls := 0
	coro := b.BeginCoro(CoroOptions{
		Frame: CoroFrameOps{
			Alloc: func(Builder, Expr, Expr) Expr { return prog.Nil(prog.VoidPtr()) },
			Free:  func(Builder, Expr, Expr, Expr) {},
		},
		AfterResume: func(b Builder) {
			defaultCalls++
			b.Call(pkg.NewFunc("default_resume_gate", functionSignature(nil, nil), InC).Expr)
		},
	})
	logical := fn.MakeBlock()
	b.Jump(logical)
	b.SetBlock(logical)
	if got := coro.SuspendCurrentBlock(); got != logical {
		t.Fatal("default suspend did not preserve its logical block")
	}
	mustPanicContains(t, "requires a callback", func() {
		coro.SuspendCurrentBlockWithAfterResume(nil)
	})
	if got := coro.SuspendCurrentBlockWithAfterResume(func(b Builder) {
		overrideCalls++
		b.Call(pkg.NewFunc("exact_resume_gate", functionSignature(nil, nil), InC).Expr)
	}); got != logical {
		t.Fatal("override suspend did not preserve its logical block")
	}
	if defaultCalls != 2 || overrideCalls != 1 {
		t.Fatalf("resume callbacks before final suspend = default:%d override:%d, want 2/1", defaultCalls, overrideCalls)
	}
	coro.Finish()
	b.EndBuild()
	if defaultCalls != 2 || overrideCalls != 1 {
		t.Fatalf("final suspend invoked a resume callback: default:%d override:%d", defaultCalls, overrideCalls)
	}
	ir := pkg.Module().String()
	if strings.Count(ir, "call void @default_resume_gate") != 2 || strings.Count(ir, "call void @exact_resume_gate") != 1 {
		t.Fatalf("default and per-suspend override were not mutually exclusive:\n%s", ir)
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify per-suspend resume override: %v\n%s", err, ir)
	}
}

func TestCoroBuilderCoroSplit(t *testing.T) {
	fixture := newCoroTestFixture(t, nil, 32)
	mod := fixture.pkg.Module()
	runCoroPasses(t, fixture, "coro-early,cgscc(coro-split),coro-cleanup")

	post := mod.String()
	for _, suffix := range []string{".resume", ".destroy"} {
		if mod.NamedFunction("coro_test" + suffix).IsNil() {
			t.Fatalf("CoroSplit did not create coro_test%s:\n%s", suffix, post)
		}
	}
	for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend"} {
		if strings.Contains(post, "call ") && regexp.MustCompile(`call [^\n]*@`+regexp.QuoteMeta(intrinsic)+`\b`).MatchString(post) {
			t.Fatalf("post-split module still calls %s:\n%s", intrinsic, post)
		}
	}
	// The byte local is explicitly 64-byte aligned and live across an ordinary
	// suspend. CoroSplit must therefore replace coro.align with a requirement of
	// at least 64 in the injected allocation path. The exact select folding is
	// intentionally left to later optimization passes.
	if strings.Contains(post, "call i64 @llvm.coro.align") || strings.Contains(post, "call i32 @llvm.coro.align") {
		t.Fatalf("CoroSplit did not lower coro.align:\n%s", post)
	}
	allocCall := frameAllocCallLine(post)
	if !strings.Contains(allocCall, "%coro.alloc.align") {
		t.Fatalf("frame allocator does not receive the normalized frame alignment:\n%s", post)
	}
	width := fixture.prog.PointerSize() * 8
	maxAlign := regexp.MustCompile(fmt.Sprintf(
		`(?m)%%coro\.alloc\.align = select i1 %%[^,]+, i%d 32, i%d 64$`, width, width,
	))
	if !maxAlign.MatchString(post) {
		t.Fatalf("post-split frame alignment is not max(coro.align=64, allocation guarantee=32):\n%s", post)
	}
}

func TestCoroHandleIntrinsicsBeforeAndAfterCoroSplit(t *testing.T) {
	fixture := newCoroTestFixture(t, nil, 32)
	prog := fixture.prog
	promiseType := prog.Struct(prog.Byte(), prog.Uint64())
	control := fixture.pkg.NewFunc("coro_control", functionSignature(
		[]types.Type{types.Typ[types.UnsafePointer]},
		[]types.Type{types.Typ[types.Bool]},
	), InC)
	b := control.MakeBody(1)
	handle := control.Param(0)
	promise := b.CoroPromise(handle, promiseType)
	if promise.kind != vkPtr ||
		!types.Identical(promise.RawType(), types.NewPointer(promiseType.RawType())) {
		t.Fatalf("CoroPromise type = %v, want pointer to %v", promise.RawType(), promiseType.RawType())
	}
	done := b.CoroDone(handle)
	if done.kind != vkBool || !types.Identical(done.RawType(), types.Typ[types.Bool]) {
		t.Fatalf("CoroDone type = %v, want bool", done.RawType())
	}
	b.CoroResume(handle)
	b.CoroDestroy(handle)
	b.Return(done)
	b.EndBuild()
	b.Dispose()

	mod := fixture.pkg.Module()
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify coroutine handle intrinsics: %v\n%s", err, mod.String())
	}
	pre := mod.String()
	for _, intrinsic := range []string{
		"llvm.coro.promise", "llvm.coro.done", "llvm.coro.resume", "llvm.coro.destroy",
	} {
		if !hasCoroIntrinsicCall(pre, intrinsic) {
			t.Fatalf("presplit module lacks %s call:\n%s", intrinsic, pre)
		}
	}
	wantAlign := prog.td.ABITypeAlignment(promiseType.ll)
	promiseCall := regexp.MustCompile(fmt.Sprintf(
		`(?m)call ptr @llvm\.coro\.promise\(ptr [^,]+, i32 %d, i1 false\)`, wantAlign,
	))
	if !promiseCall.MatchString(pre) {
		t.Fatalf("llvm.coro.promise does not use payload ABI alignment %d and from=false:\n%s", wantAlign, pre)
	}

	runCoroPasses(t, fixture, "coro-early,cgscc(coro-split),coro-cleanup")
	post := mod.String()
	for _, intrinsic := range []string{
		"llvm.coro.promise", "llvm.coro.done", "llvm.coro.resume", "llvm.coro.destroy",
	} {
		if hasCoroIntrinsicCall(post, intrinsic) {
			t.Fatalf("post-split module still calls %s:\n%s", intrinsic, post)
		}
	}
	for _, suffix := range []string{".resume", ".destroy"} {
		if mod.NamedFunction("coro_test" + suffix).IsNil() {
			t.Fatalf("CoroSplit did not create coro_test%s:\n%s", suffix, post)
		}
	}
}

func TestCoroElideStaticChildFrameContract(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	pkg := prog.NewPackage("coroelide", "coro/elide")
	t.Cleanup(func() {
		pkg.Module().Dispose()
		prog.Dispose()
	})

	childAlloc := pkg.NewFunc("coro_elide_child_alloc_probe", functionSignature(
		[]types.Type{types.Typ[types.Uintptr], types.Typ[types.Uintptr]},
		[]types.Type{types.Typ[types.UnsafePointer]},
	), InC)
	childFree := pkg.NewFunc("coro_elide_child_free_probe", functionSignature(
		[]types.Type{types.Typ[types.UnsafePointer], types.Typ[types.Uintptr], types.Typ[types.Uintptr]},
		nil,
	), InC)
	childPublish := pkg.NewFunc("coro_elide_child_publish_probe", functionSignature(
		[]types.Type{types.Typ[types.UnsafePointer], types.Typ[types.UnsafePointer]},
		nil,
	), InC)
	bodyProbe := pkg.NewFunc("coro_elide_body_probe", functionSignature(nil, nil), InC)
	doneProbe := pkg.NewFunc("coro_elide_done_probe", functionSignature(
		[]types.Type{types.Typ[types.Bool]}, nil,
	), InC)

	child := pkg.NewFunc("coro_elide_child", coroHandleSignature(), InGo)
	childBuilder := child.MakeBody(1)
	childCoro := childBuilder.BeginCoro(CoroOptions{
		AllocationAlign: 32,
		Frame: CoroFrameOps{
			Alloc: func(b Builder, size, align Expr) Expr {
				return b.Call(childAlloc.Expr, size, align)
			},
			Free: func(b Builder, frame, size, align Expr) {
				b.Call(childFree.Expr, frame, size, align)
			},
		},
		BeforeInitialSuspend: func(b Builder, handle, storage Expr) {
			b.Call(childPublish.Expr, handle, storage)
		},
	})
	childBuilder.Call(bodyProbe.Expr)
	childCoro.Finish()
	childBuilder.EndBuild()
	childBuilder.Dispose()

	parentAlloc := pkg.NewFunc("coro_elide_parent_alloc_probe", functionSignature(
		[]types.Type{types.Typ[types.Uintptr], types.Typ[types.Uintptr]},
		[]types.Type{types.Typ[types.UnsafePointer]},
	), InC)
	parentFree := pkg.NewFunc("coro_elide_parent_free_probe", functionSignature(
		[]types.Type{types.Typ[types.UnsafePointer], types.Typ[types.Uintptr], types.Typ[types.Uintptr]},
		nil,
	), InC)
	parent := pkg.NewFunc("coro_elide_parent", coroHandleSignature(), InGo)
	parentBuilder := parent.MakeBody(1)
	parentCoro := parentBuilder.BeginCoro(CoroOptions{
		AllocationAlign: 32,
		Frame: CoroFrameOps{
			Alloc: func(b Builder, size, align Expr) Expr {
				return b.Call(parentAlloc.Expr, size, align)
			},
			Free: func(b Builder, frame, size, align Expr) {
				b.Call(parentFree.Expr, frame, size, align)
			},
		},
	})
	handle := parentBuilder.Call(child.Expr)
	if !parentBuilder.MarkCoroElideSafe(handle) {
		t.Fatal("LLVM 22 rejected an exact coroutine elision proof")
	}
	parentBuilder.CoroResume(handle)
	done := parentBuilder.CoroDone(handle)
	parentBuilder.CoroDestroy(handle)
	parentBuilder.Call(doneProbe.Expr, done)
	parentCoro.Finish()
	parentBuilder.EndBuild()
	parentBuilder.Dispose()

	fixture := &coroTestFixture{prog: prog, pkg: pkg, fn: parent, coro: parentCoro}
	runCoroPasses(t, fixture, "lto<O3>")
	post := pkg.Module().String()
	parentResume := pkg.Module().NamedFunction("coro_elide_parent.resume").String()
	if !strings.Contains(post, "@coro_elide_child.noalloc") {
		t.Fatalf("CoroSplit did not synthesize the attributed no-allocation ramp:\n%s", post)
	}
	if strings.Contains(parentResume, "call ptr @coro_elide_child()") ||
		strings.Contains(parentResume, "@coro_elide_child_alloc_probe") ||
		strings.Contains(parentResume, "@coro_elide_child_free_probe") {
		t.Fatalf("static child retained its dynamic allocation path in the parent resume:\n%s", parentResume)
	}
	if !regexp.MustCompile(
		`call void @coro_elide_child_publish_probe\(ptr [^,]+, ptr null\)`,
	).MatchString(parentResume) {
		t.Fatalf("elided child publication did not expose a null dynamic-storage operand:\n%s", parentResume)
	}
}

func TestCoroBuilderDefaultPipelineLLVM22(t *testing.T) {
	fixture := newCoroTestFixture(t, nil, 0)
	runCoroPasses(t, fixture, "default<O0>")
	post := fixture.pkg.String()
	if fixture.pkg.Module().NamedFunction("coro_test.resume").IsNil() ||
		fixture.pkg.Module().NamedFunction("coro_test.destroy").IsNil() {
		t.Fatalf("default<O0> did not split coroutine:\n%s", post)
	}
}

func TestCoroBuilderTargetUintptrIntrinsics(t *testing.T) {
	Initialize(InitAll)
	fixture := newCoroTestFixture(t, &Target{GOOS: "wasip1", GOARCH: "wasm"}, 0)
	if got := fixture.prog.PointerSize(); got != 4 {
		t.Fatalf("wasm pointer size = %d, want 4", got)
	}
	ir := fixture.pkg.String()
	for _, intrinsic := range []string{"size", "align"} {
		if !strings.Contains(ir, "@llvm.coro."+intrinsic+".i32") {
			t.Fatalf("wasm coroutine uses non-i32 %s intrinsic:\n%s", intrinsic, ir)
		}
	}
	// AllocationAlign=0 means llvm.coro.id keeps LLVM's 2*pointer guarantee;
	// the callback still receives an effective alignment of at least 8.
	if !strings.Contains(ir, "@llvm.coro.id(i32 0") || !strings.Contains(ir, "i32 8") {
		t.Fatalf("wasm default allocation alignment is not 2*pointer:\n%s", ir)
	}
}

func TestCoroPromiseUsesWasm32ABIAlignment(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
	pkg := prog.NewPackage("coropromise", "coro/promise")
	t.Cleanup(func() {
		pkg.Module().Dispose()
		prog.Dispose()
	})

	fn := pkg.NewFunc("coro_promise", functionSignature(
		[]types.Type{types.Typ[types.UnsafePointer]},
		[]types.Type{types.Typ[types.Bool]},
	), InC)
	b := fn.MakeBody(1)
	promiseType := prog.Uint64()
	promise := b.CoroPromise(fn.Param(0), promiseType)
	if promise.kind != vkPtr ||
		!types.Identical(promise.RawType(), types.NewPointer(promiseType.RawType())) {
		t.Fatalf("CoroPromise type = %v, want *uint64", promise.RawType())
	}
	b.Return(b.CoroDone(fn.Param(0)))
	b.EndBuild()
	b.Dispose()

	if got := prog.PointerSize(); got != 4 {
		t.Fatalf("wasm pointer size = %d, want 4", got)
	}
	align := prog.td.ABITypeAlignment(promiseType.ll)
	if align != 8 {
		t.Fatalf("wasm uint64 ABI alignment = %d, want 8", align)
	}
	ir := pkg.String()
	want := regexp.MustCompile(
		`call ptr @llvm\.coro\.promise\(ptr [^,]+, i32 8, i1 false\)`,
	)
	if !want.MatchString(ir) {
		t.Fatalf("wasm llvm.coro.promise lacks i32 ABI alignment and from=false:\n%s", ir)
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify wasm coroutine promise accessor: %v\n%s", err, ir)
	}
}

func TestCoroRootFactoryDescriptorTargetLayout(t *testing.T) {
	Initialize(InitAll)
	tests := []struct {
		name              string
		target            *Target
		pointerSize       int
		startupSize       uint64
		startupAlign      uint64
		resultSize        uint64
		resultAlign       uint64
		descriptorSize    uint64
		startupSizeOffset uint64
		resultAlignOffset uint64
	}{
		{
			name:              "native",
			pointerSize:       8,
			startupSize:       16,
			startupAlign:      8,
			resultSize:        8,
			resultAlign:       8,
			descriptorSize:    64,
			startupSizeOffset: 32,
			resultAlignOffset: 56,
		},
		{
			name:              "wasm32",
			target:            &Target{GOOS: "wasip1", GOARCH: "wasm"},
			pointerSize:       4,
			startupSize:       8,
			startupAlign:      4,
			resultSize:        4,
			resultAlign:       4,
			descriptorSize:    48,
			startupSizeOffset: 28,
			resultAlignOffset: 40,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := NewProgram(test.target)
			pkg := prog.NewPackage("cororoot", "coro/root")
			t.Cleanup(func() {
				pkg.Module().Dispose()
				prog.Dispose()
			})

			factory := pkg.NewFunc("coro_root_factory", coroRootFactoryTestSignature(), InC)
			startup := prog.Struct(prog.VoidPtr(), prog.VoidPtr())
			result := prog.VoidPtr()
			hash := [16]byte{
				0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
				0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
			}
			descriptor := pkg.NewCoroRootFactoryDescriptor(
				"coro_root_descriptor",
				CoroRootFactoryDescriptorOptions{
					Version: 7,
					ABIHash: hash,
					Flags:   0xa5,
					Factory: factory.Expr,
					Startup: startup,
					Result:  result,
				},
			)

			if got := prog.PointerSize(); got != test.pointerSize {
				t.Fatalf("pointer size = %d, want %d", got, test.pointerSize)
			}
			if descriptor.kind != vkPtr {
				t.Fatalf("descriptor kind = %d, want pointer", descriptor.kind)
			}
			if !descriptor.impl.IsGlobalConstant() {
				t.Fatal("root factory descriptor is not a constant global")
			}
			if got := descriptor.impl.Linkage(); got != llvm.LinkOnceODRLinkage {
				t.Fatalf("descriptor linkage = %v, want linkonce_odr", got)
			}

			descriptorType := prog.Elem(descriptor.Type)
			if got := prog.SizeOf(descriptorType); got != test.descriptorSize {
				t.Fatalf("descriptor size = %d, want %d", got, test.descriptorSize)
			}
			if got := prog.OffsetOf(descriptorType, 5); got != test.startupSizeOffset {
				t.Fatalf("startupSize offset = %d, want %d", got, test.startupSizeOffset)
			}
			if got := prog.OffsetOf(descriptorType, 8); got != test.resultAlignOffset {
				t.Fatalf("resultAlign offset = %d, want %d", got, test.resultAlignOffset)
			}
			if got, want := descriptor.impl.Alignment(),
				prog.td.ABITypeAlignment(descriptorType.ll); got != want {
				t.Fatalf("descriptor alignment = %d, want target ABI alignment %d", got, want)
			}

			initializer := descriptor.impl.Initializer()
			if initializer.IsAConstantStruct().IsNil() {
				t.Fatalf("descriptor initializer is not a constant struct: %v", initializer)
			}
			if got := initializer.OperandsCount(); got != 9 {
				t.Fatalf("descriptor fields = %d, want 9", got)
			}
			wantFixed := []uint64{
				7,
				0xa5,
				0x0102030405060708,
				0x090a0b0c0d0e0f10,
			}
			for i, want := range wantFixed {
				if got := initializer.Operand(i).ZExtValue(); got != want {
					t.Fatalf("descriptor field %d = %#x, want %#x", i, got, want)
				}
			}
			factoryField := initializer.Operand(4)
			if factoryField.Type().TypeKind() != llvm.PointerTypeKind ||
				!factoryField.IsAConstantPointerNull().IsNil() {
				t.Fatalf("factory field is not a non-null constant pointer: %v", factoryField)
			}
			wantPayload := []uint64{
				test.startupSize,
				test.startupAlign,
				test.resultSize,
				test.resultAlign,
			}
			for i, want := range wantPayload {
				field := initializer.Operand(i + 5)
				if got := field.Type().IntTypeWidth(); got != test.pointerSize*8 {
					t.Fatalf("descriptor uintptr field %d width = %d, want %d", i+5, got, test.pointerSize*8)
				}
				if got := field.ZExtValue(); got != want {
					t.Fatalf("descriptor field %d = %d, want %d", i+5, got, want)
				}
			}

			ir := pkg.String()
			if !strings.Contains(ir,
				"@coro_root_descriptor = linkonce_odr unnamed_addr constant") {
				t.Fatalf("descriptor is not unnamed_addr linkonce_odr constant:\n%s", ir)
			}
			if !strings.Contains(ir, "@coro_root_factory") {
				t.Fatalf("descriptor does not reference the root factory:\n%s", ir)
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify root factory descriptor: %v\n%s", err, ir)
			}
		})
	}
}

func TestCoroRootFactoryDescriptorRejectsMisuse(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("badcororoot", "bad/coro/root")
	defer pkg.Module().Dispose()
	factory := pkg.NewFunc("coro_root_factory", coroRootFactoryTestSignature(), InC)
	startup := prog.Struct(prog.VoidPtr(), prog.VoidPtr())
	result := prog.VoidPtr()
	valid := CoroRootFactoryDescriptorOptions{
		Factory: factory.Expr,
		Startup: startup,
		Result:  result,
	}

	mustPanicContains(t, "requires a name", func() {
		pkg.NewCoroRootFactoryDescriptor("", valid)
	})
	mustPanicContains(t, "constant function factory", func() {
		bad := valid
		bad.Factory = Nil
		pkg.NewCoroRootFactoryDescriptor("missing_factory", bad)
	})
	mustPanicContains(t, "constant function factory", func() {
		bad := valid
		bad.Factory = prog.IntVal(1, prog.Uintptr())
		pkg.NewCoroRootFactoryDescriptor("integer_factory", bad)
	})
	mustPanicContains(t, "constant function factory", func() {
		bad := valid
		bad.Factory = prog.Nil(prog.rawType(coroHandleSignature()))
		pkg.NewCoroRootFactoryDescriptor("null_factory", bad)
	})
	mustPanicContains(t, "factory signature", func() {
		bad := valid
		bad.Factory = pkg.NewFunc("wrong_arity_factory", coroHandleSignature(), InC).Expr
		pkg.NewCoroRootFactoryDescriptor("wrong_arity_descriptor", bad)
	})
	mustPanicContains(t, "factory signature", func() {
		bad := valid
		bad.Factory = pkg.NewFunc("wrong_return_factory", functionSignature(
			[]types.Type{
				types.Typ[types.UnsafePointer],
				types.Typ[types.UnsafePointer],
				types.Typ[types.UnsafePointer],
			},
			[]types.Type{types.Typ[types.Bool]},
		), InC).Expr
		pkg.NewCoroRootFactoryDescriptor("wrong_return_descriptor", bad)
	})
	foreignPkg := prog.NewPackage("foreigncororoot", "foreign/coro/root")
	defer foreignPkg.Module().Dispose()
	mustPanicContains(t, "same package module", func() {
		bad := valid
		bad.Factory = foreignPkg.NewFunc("foreign_factory", coroRootFactoryTestSignature(), InC).Expr
		pkg.NewCoroRootFactoryDescriptor("foreign_factory_descriptor", bad)
	})
	mustPanicContains(t, "concrete startup type", func() {
		bad := valid
		bad.Startup = nil
		pkg.NewCoroRootFactoryDescriptor("missing_startup", bad)
	})
	mustPanicContains(t, "concrete startup type", func() {
		bad := valid
		bad.Startup = prog.Void()
		pkg.NewCoroRootFactoryDescriptor("void_startup", bad)
	})
	mustPanicContains(t, "concrete result type", func() {
		bad := valid
		bad.Result = nil
		pkg.NewCoroRootFactoryDescriptor("missing_result", bad)
	})
	mustPanicContains(t, "concrete result type", func() {
		bad := valid
		bad.Result = prog.Void()
		pkg.NewCoroRootFactoryDescriptor("void_result", bad)
	})
	foreignProg := NewProgram(nil)
	defer foreignProg.Dispose()
	mustPanicContains(t, "startup type belongs to another program", func() {
		bad := valid
		bad.Startup = foreignProg.Struct(foreignProg.VoidPtr())
		pkg.NewCoroRootFactoryDescriptor("foreign_startup_descriptor", bad)
	})
	mustPanicContains(t, "result type belongs to another program", func() {
		bad := valid
		bad.Result = foreignProg.VoidPtr()
		pkg.NewCoroRootFactoryDescriptor("foreign_result_descriptor", bad)
	})
}

func TestCoroRootPackageAnchorTargetLayout(t *testing.T) {
	Initialize(InitAll)
	tests := []struct {
		name          string
		target        *Target
		descriptors   int
		pointerSize   int
		anchorSize    uint64
		entriesOffset uint64
	}{
		{name: "native_one", descriptors: 1, pointerSize: 8, anchorSize: 40, entriesOffset: 32},
		{name: "native_many", descriptors: 3, pointerSize: 8, anchorSize: 40, entriesOffset: 32},
		{
			name:          "wasm32_one",
			target:        &Target{GOOS: "wasip1", GOARCH: "wasm"},
			descriptors:   1,
			pointerSize:   4,
			anchorSize:    32,
			entriesOffset: 28,
		},
		{
			name:          "wasm32_many",
			target:        &Target{GOOS: "wasip1", GOARCH: "wasm"},
			descriptors:   3,
			pointerSize:   4,
			anchorSize:    32,
			entriesOffset: 28,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := NewProgram(test.target)
			pkg := prog.NewPackage("coroanchor", "coro/anchor/"+test.name)
			t.Cleanup(func() {
				pkg.Module().Dispose()
				prog.Dispose()
			})

			if got := pkg.CoroRootPackageAnchor(); got != "" {
				t.Fatalf("anchor before emission = %q, want empty", got)
			}
			descriptors := make([]Expr, test.descriptors)
			for i := range descriptors {
				descriptors[i] = newCoroRootDescriptorForAnchor(pkg, fmt.Sprintf("root%d", i))
			}
			hash := [16]byte{
				0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
				0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27,
			}
			const anchorName = "__llgo_coro_root_anchor_test"
			anchor := pkg.NewCoroRootPackageAnchor(
				anchorName,
				CoroRootPackageAnchorOptions{
					Version:     11,
					ABIHash:     hash,
					Descriptors: descriptors,
				},
			)
			if got := pkg.CoroRootPackageAnchor(); got != anchorName {
				t.Fatalf("anchor symbol = %q, want %q", got, anchorName)
			}
			if !anchor.impl.IsGlobalConstant() {
				t.Fatal("package root anchor is not a constant global")
			}
			if got := anchor.impl.Linkage(); got != llvm.ExternalLinkage {
				t.Fatalf("anchor linkage = %v, want external", got)
			}
			if got := anchor.impl.Visibility(); got != llvm.HiddenVisibility {
				t.Fatalf("anchor visibility = %v, want hidden", got)
			}

			anchorType := prog.Elem(anchor.Type)
			if got := prog.SizeOf(anchorType); got != test.anchorSize {
				t.Fatalf("anchor size = %d, want %d", got, test.anchorSize)
			}
			if got := prog.OffsetOf(anchorType, 5); got != test.entriesOffset {
				t.Fatalf("anchor entries offset = %d, want %d", got, test.entriesOffset)
			}
			initializer := anchor.impl.Initializer()
			if initializer.IsAConstantStruct().IsNil() || initializer.OperandsCount() != 6 {
				t.Fatalf("anchor initializer is not a six-field constant struct: %v", initializer)
			}
			wantFixed := []uint64{
				11,
				0,
				0x1011121314151617,
				0x2021222324252627,
				uint64(test.descriptors),
			}
			for i, want := range wantFixed {
				if got := initializer.Operand(i).ZExtValue(); got != want {
					t.Fatalf("anchor field %d = %#x, want %#x", i, got, want)
				}
			}
			if got := initializer.Operand(4).Type().IntTypeWidth(); got != test.pointerSize*8 {
				t.Fatalf("anchor count width = %d, want %d", got, test.pointerSize*8)
			}

			entries := pkg.Module().NamedGlobal(anchorName + ".entries")
			if entries.IsNil() {
				t.Fatal("package root anchor lacks its entries array")
			}
			if !entries.IsGlobalConstant() || entries.Linkage() != llvm.InternalLinkage {
				t.Fatalf("entries array is not an internal constant: %v", entries)
			}
			entriesPointer := stripCoroAnchorConstantPointer(initializer.Operand(5))
			if entriesPointer.C != entries.C {
				t.Fatalf("anchor entries pointer = %v, want %v", entriesPointer, entries)
			}
			entriesInitializer := entries.Initializer()
			if entriesInitializer.IsAConstantArray().IsNil() ||
				entriesInitializer.OperandsCount() != test.descriptors {
				t.Fatalf("entries initializer has %d entries, want %d: %v",
					entriesInitializer.OperandsCount(), test.descriptors, entriesInitializer)
			}
			for i, descriptor := range descriptors {
				got := stripCoroAnchorConstantPointer(entriesInitializer.Operand(i))
				if got.C != descriptor.impl.C {
					t.Fatalf("entries[%d] = %v, want %v", i, got, descriptor.impl)
				}
			}

			pkg.MaterializePreserveSyms()
			used := pkg.Module().NamedGlobal("llvm.used")
			if used.IsNil() {
				t.Fatal("package root anchor was not retained in llvm.used")
			}
			retained := false
			for i := 0; i < used.Initializer().OperandsCount(); i++ {
				if stripCoroAnchorConstantPointer(used.Initializer().Operand(i)).C == anchor.impl.C {
					retained = true
					break
				}
			}
			if !retained {
				t.Fatalf("llvm.used does not retain package root anchor:\n%s", pkg.String())
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify package root anchor: %v\n%s", err, pkg.String())
			}
		})
	}
}

func TestCoroRootPackageAnchorRejectsMisuse(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("badcoroanchor", "bad/coro/anchor")
	defer pkg.Module().Dispose()
	descriptor := newCoroRootDescriptorForAnchor(pkg, "valid")
	valid := CoroRootPackageAnchorOptions{Descriptors: []Expr{descriptor}}

	mustPanicContains(t, "requires a name", func() {
		pkg.NewCoroRootPackageAnchor("", valid)
	})
	mustPanicContains(t, "at least one descriptor", func() {
		pkg.NewCoroRootPackageAnchor("empty", CoroRootPackageAnchorOptions{})
	})
	mustPanicContains(t, "not a constant global", func() {
		bad := valid
		bad.Descriptors = []Expr{Nil}
		pkg.NewCoroRootPackageAnchor("nil_descriptor", bad)
	})
	mustPanicContains(t, "not a constant global", func() {
		bad := valid
		bad.Descriptors = []Expr{prog.IntVal(1, prog.Uintptr())}
		pkg.NewCoroRootPackageAnchor("integer_descriptor", bad)
	})
	mutable := pkg.NewVarEx("mutable_descriptor", prog.Pointer(prog.Uint32()))
	mutable.Init(prog.IntVal(0, prog.Uint32()))
	mustPanicContains(t, "not a constant global", func() {
		bad := valid
		bad.Descriptors = []Expr{mutable.Expr}
		pkg.NewCoroRootPackageAnchor("mutable_descriptor_anchor", bad)
	})
	foreignPkg := prog.NewPackage("foreigncoroanchor", "foreign/coro/anchor")
	defer foreignPkg.Module().Dispose()
	foreign := newCoroRootDescriptorForAnchor(foreignPkg, "foreign")
	mustPanicContains(t, "another package module", func() {
		bad := valid
		bad.Descriptors = []Expr{foreign}
		pkg.NewCoroRootPackageAnchor("foreign_descriptor", bad)
	})
	mustPanicContains(t, "duplicate descriptor", func() {
		bad := valid
		bad.Descriptors = []Expr{descriptor, descriptor}
		pkg.NewCoroRootPackageAnchor("duplicate_descriptor", bad)
	})
	pkg.NewVarEx("occupied_anchor", prog.Pointer(prog.Uint32()))
	mustPanicContains(t, "symbol \"occupied_anchor\" already exists", func() {
		pkg.NewCoroRootPackageAnchor("occupied_anchor", valid)
	})
	pkg.NewVarEx("occupied_entries.entries", prog.Pointer(prog.Uint32()))
	mustPanicContains(t, "symbol \"occupied_entries.entries\" already exists", func() {
		pkg.NewCoroRootPackageAnchor("occupied_entries", valid)
	})
	if got := pkg.CoroRootPackageAnchor(); got != "" {
		t.Fatalf("rejected anchor attempts recorded symbol %q", got)
	}

	pkg.NewCoroRootPackageAnchor("valid_anchor", valid)
	mustPanicContains(t, "already defined as \"valid_anchor\"", func() {
		pkg.NewCoroRootPackageAnchor("second_anchor", valid)
	})
}

func TestCoroProgramManifestTargetLayout(t *testing.T) {
	Initialize(InitAll)
	tests := []struct {
		name            string
		target          *Target
		anchors         int
		bootstrap       string
		pointerSize     int
		manifestSize    uint64
		packagesOffset  uint64
		bootstrapOffset uint64
	}{
		{
			name:            "native_empty",
			pointerSize:     8,
			manifestSize:    48,
			packagesOffset:  32,
			bootstrapOffset: 40,
		},
		{
			name:            "native_one",
			anchors:         1,
			bootstrap:       "function",
			pointerSize:     8,
			manifestSize:    48,
			packagesOffset:  32,
			bootstrapOffset: 40,
		},
		{
			name:            "native_many",
			anchors:         3,
			bootstrap:       "global",
			pointerSize:     8,
			manifestSize:    48,
			packagesOffset:  32,
			bootstrapOffset: 40,
		},
		{
			name:            "wasm32_empty",
			target:          &Target{GOOS: "wasip1", GOARCH: "wasm"},
			pointerSize:     4,
			manifestSize:    40,
			packagesOffset:  28,
			bootstrapOffset: 32,
		},
		{
			name:            "wasm32_one",
			target:          &Target{GOOS: "wasip1", GOARCH: "wasm"},
			anchors:         1,
			bootstrap:       "function",
			pointerSize:     4,
			manifestSize:    40,
			packagesOffset:  28,
			bootstrapOffset: 32,
		},
		{
			name:            "wasm32_many",
			target:          &Target{GOOS: "wasip1", GOARCH: "wasm"},
			anchors:         3,
			bootstrap:       "global",
			pointerSize:     4,
			manifestSize:    40,
			packagesOffset:  28,
			bootstrapOffset: 32,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := NewProgram(test.target)
			pkg := prog.NewPackage("coromanifest", "coro/manifest/"+test.name)
			t.Cleanup(func() {
				pkg.Module().Dispose()
				prog.Dispose()
			})

			if got := pkg.CoroProgramManifest(); got != "" {
				t.Fatalf("manifest before emission = %q, want empty", got)
			}
			anchors := make([]Expr, test.anchors)
			for i := range anchors {
				// Exercise both constant definitions and external declarations.
				anchors[i] = newCoroProgramPackageAnchor(pkg, fmt.Sprintf("package_anchor_%d", i), i%2 == 0)
			}
			var bootstrap Expr
			switch test.bootstrap {
			case "function":
				bootstrap = pkg.NewFunc("manifest_bootstrap", functionSignature(nil, nil), InC).Expr
			case "global":
				global := pkg.NewVarEx("manifest_bootstrap", prog.Pointer(prog.Uintptr()))
				global.Init(prog.IntVal(0, prog.Uintptr()))
				bootstrap = global.Expr
			}
			hash := [16]byte{
				0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37,
				0x40, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47,
			}
			const manifestName = "__llgo_coro_program_manifest_test"
			manifest := pkg.NewCoroProgramManifest(
				manifestName,
				CoroProgramManifestOptions{
					Version:        12,
					ABIHash:        hash,
					PackageAnchors: anchors,
					Bootstrap:      bootstrap,
				},
			)
			if got := pkg.CoroProgramManifest(); got != manifestName {
				t.Fatalf("manifest symbol = %q, want %q", got, manifestName)
			}
			if !manifest.impl.IsGlobalConstant() {
				t.Fatal("program manifest is not a constant global")
			}
			if got := manifest.impl.Linkage(); got != llvm.ExternalLinkage {
				t.Fatalf("manifest linkage = %v, want external", got)
			}
			if got := manifest.impl.Visibility(); got != llvm.HiddenVisibility {
				t.Fatalf("manifest visibility = %v, want hidden", got)
			}

			manifestType := prog.Elem(manifest.Type)
			if got := prog.SizeOf(manifestType); got != test.manifestSize {
				t.Fatalf("manifest size = %d, want %d", got, test.manifestSize)
			}
			if got := prog.OffsetOf(manifestType, 5); got != test.packagesOffset {
				t.Fatalf("manifest packages offset = %d, want %d", got, test.packagesOffset)
			}
			if got := prog.OffsetOf(manifestType, 6); got != test.bootstrapOffset {
				t.Fatalf("manifest bootstrap offset = %d, want %d", got, test.bootstrapOffset)
			}
			initializer := manifest.impl.Initializer()
			if initializer.IsAConstantStruct().IsNil() || initializer.OperandsCount() != 7 {
				t.Fatalf("manifest initializer is not a seven-field constant struct: %v", initializer)
			}
			wantFixed := []uint64{
				12,
				0,
				0x3031323334353637,
				0x4041424344454647,
				uint64(test.anchors),
			}
			for i, want := range wantFixed {
				if got := initializer.Operand(i).ZExtValue(); got != want {
					t.Fatalf("manifest field %d = %#x, want %#x", i, got, want)
				}
			}
			if got := initializer.Operand(4).Type().IntTypeWidth(); got != test.pointerSize*8 {
				t.Fatalf("manifest package count width = %d, want %d", got, test.pointerSize*8)
			}

			packages := pkg.Module().NamedGlobal(manifestName + ".packages")
			if test.anchors == 0 {
				if !packages.IsNil() {
					t.Fatalf("empty catalog unexpectedly emitted packages array: %v", packages)
				}
				if initializer.Operand(5).IsAConstantPointerNull().IsNil() {
					t.Fatalf("empty catalog packages pointer is not null: %v", initializer.Operand(5))
				}
			} else {
				if packages.IsNil() {
					t.Fatal("non-empty catalog lacks its packages array")
				}
				if !packages.IsGlobalConstant() || packages.Linkage() != llvm.InternalLinkage {
					t.Fatalf("packages array is not an internal constant: %v", packages)
				}
				if got := stripCoroAnchorConstantPointer(initializer.Operand(5)); got.C != packages.C {
					t.Fatalf("manifest packages pointer = %v, want %v", got, packages)
				}
				array := packages.Initializer()
				if array.IsAConstantArray().IsNil() || array.OperandsCount() != test.anchors {
					t.Fatalf("packages initializer has %d entries, want %d: %v",
						array.OperandsCount(), test.anchors, array)
				}
				for i, anchor := range anchors {
					if got := stripCoroAnchorConstantPointer(array.Operand(i)); got.C != anchor.impl.C {
						t.Fatalf("packages[%d] = %v, want %v", i, got, anchor.impl)
					}
					if !anchor.impl.IsGlobalConstant() {
						t.Fatalf("package anchor %d was not emitted/normalized as constant", i)
					}
				}
			}
			if bootstrap.IsNil() {
				if initializer.Operand(6).IsAConstantPointerNull().IsNil() {
					t.Fatalf("nil bootstrap was not encoded as null: %v", initializer.Operand(6))
				}
			} else if got := stripCoroAnchorConstantPointer(initializer.Operand(6)); got.C != bootstrap.impl.C {
				t.Fatalf("manifest bootstrap = %v, want %v", got, bootstrap.impl)
			}

			pkg.MaterializePreserveSyms()
			used := pkg.Module().NamedGlobal("llvm.used")
			if used.IsNil() {
				t.Fatal("program manifest was not retained in llvm.used")
			}
			retained := false
			for i := 0; i < used.Initializer().OperandsCount(); i++ {
				if stripCoroAnchorConstantPointer(used.Initializer().Operand(i)).C == manifest.impl.C {
					retained = true
					break
				}
			}
			if !retained {
				t.Fatalf("llvm.used does not retain program manifest:\n%s", pkg.String())
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify coroutine program manifest: %v\n%s", err, pkg.String())
			}
		})
	}
}

func TestCoroProgramManifestRejectsMisuse(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("badcoromanifest", "bad/coro/manifest")
	defer pkg.Module().Dispose()
	anchor := newCoroProgramPackageAnchor(pkg, "valid_package_anchor", true)
	valid := CoroProgramManifestOptions{PackageAnchors: []Expr{anchor}}

	mustPanicContains(t, "requires a name", func() {
		pkg.NewCoroProgramManifest("", valid)
	})
	mustPanicContains(t, "not a non-null constant global", func() {
		bad := valid
		bad.PackageAnchors = []Expr{Nil}
		pkg.NewCoroProgramManifest("nil_anchor", bad)
	})
	mustPanicContains(t, "not a non-null constant global", func() {
		bad := valid
		bad.PackageAnchors = []Expr{prog.IntVal(1, prog.Uintptr())}
		pkg.NewCoroProgramManifest("integer_anchor", bad)
	})
	mustPanicContains(t, "not a non-null constant global", func() {
		bad := valid
		bad.PackageAnchors = []Expr{prog.Nil(prog.VoidPtr())}
		pkg.NewCoroProgramManifest("null_anchor", bad)
	})
	mutable := pkg.NewVarEx("mutable_package_anchor", prog.Pointer(prog.Uint32()))
	mutable.Init(prog.IntVal(0, prog.Uint32()))
	mustPanicContains(t, "not a constant global", func() {
		bad := valid
		bad.PackageAnchors = []Expr{mutable.Expr}
		pkg.NewCoroProgramManifest("mutable_anchor", bad)
	})
	foreignPkg := prog.NewPackage("foreigncoromanifest", "foreign/coro/manifest")
	defer foreignPkg.Module().Dispose()
	foreignAnchor := newCoroProgramPackageAnchor(foreignPkg, "foreign_package_anchor", true)
	mustPanicContains(t, "another entry module", func() {
		bad := valid
		bad.PackageAnchors = []Expr{foreignAnchor}
		pkg.NewCoroProgramManifest("foreign_anchor", bad)
	})
	mustPanicContains(t, "duplicate package anchor", func() {
		bad := valid
		bad.PackageAnchors = []Expr{anchor, anchor}
		pkg.NewCoroProgramManifest("duplicate_anchor", bad)
	})
	mustPanicContains(t, "not a non-null constant function/global pointer", func() {
		bad := valid
		bad.Bootstrap = prog.IntVal(1, prog.Uintptr())
		pkg.NewCoroProgramManifest("integer_bootstrap", bad)
	})
	mustPanicContains(t, "not a non-null constant function/global pointer", func() {
		bad := valid
		bad.Bootstrap = prog.Nil(prog.VoidPtr())
		pkg.NewCoroProgramManifest("null_bootstrap", bad)
	})
	foreignBootstrap := foreignPkg.NewFunc("foreign_bootstrap", functionSignature(nil, nil), InC)
	mustPanicContains(t, "another entry module", func() {
		bad := valid
		bad.Bootstrap = foreignBootstrap.Expr
		pkg.NewCoroProgramManifest("foreign_bootstrap", bad)
	})
	pkg.NewVarEx("occupied_manifest", prog.Pointer(prog.Uint32()))
	mustPanicContains(t, "symbol \"occupied_manifest\" already exists", func() {
		pkg.NewCoroProgramManifest("occupied_manifest", valid)
	})
	pkg.NewVarEx("occupied_packages.packages", prog.Pointer(prog.Uint32()))
	mustPanicContains(t, "symbol \"occupied_packages.packages\" already exists", func() {
		pkg.NewCoroProgramManifest("occupied_packages", valid)
	})
	if got := pkg.CoroProgramManifest(); got != "" {
		t.Fatalf("rejected manifest attempts recorded symbol %q", got)
	}

	pkg.NewCoroProgramManifest("valid_manifest", valid)
	mustPanicContains(t, "already defined as \"valid_manifest\"", func() {
		pkg.NewCoroProgramManifest("second_manifest", CoroProgramManifestOptions{})
	})
}

func TestCoroProgramBootstrapTargetLayout(t *testing.T) {
	Initialize(InitAll)
	tests := []struct {
		name          string
		target        *Target
		factory       bool
		pointerSize   int
		stepSize      uint64
		targetOffset  uint64
		auxOffset     uint64
		bootstrapSize uint64
		stepsOffset   uint64
		factoryOffset uint64
	}{
		{
			name:          "native64_steps",
			pointerSize:   8,
			stepSize:      24,
			targetOffset:  8,
			auxOffset:     16,
			bootstrapSize: 48,
			stepsOffset:   32,
			factoryOffset: 40,
		},
		{
			name:          "wasm32_steps_and_factory",
			target:        &Target{GOOS: "wasip1", GOARCH: "wasm"},
			factory:       true,
			pointerSize:   4,
			stepSize:      16,
			targetOffset:  8,
			auxOffset:     12,
			bootstrapSize: 40,
			stepsOffset:   28,
			factoryOffset: 32,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := NewProgram(test.target)
			pkg := prog.NewPackage("corobootstrap", "coro/bootstrap/"+test.name)
			t.Cleanup(func() {
				pkg.Module().Dispose()
				prog.Dispose()
			})

			if got := pkg.CoroProgramBootstrap(); got != "" {
				t.Fatalf("bootstrap before emission = %q, want empty", got)
			}
			plain := pkg.NewFunc("bootstrap_init", functionSignature(nil, nil), InC)
			anchor := newCoroProgramPackageAnchor(pkg, "bootstrap_root_anchor", false)
			steps := []CoroProgramStep{
				{Kind: CoroProgramStepDirectPlain, Flags: CoroProgramStepInit, Target: plain.Expr},
				{Kind: CoroProgramStepCoroRoot, Flags: CoroProgramStepMain, Target: anchor, Aux: 3},
			}
			var factory Expr
			if test.factory {
				factory = pkg.NewFunc("bootstrap_factory", coroRootFactoryTestSignature(), InC).Expr
			}
			hash := [16]byte{
				0x50, 0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57,
				0x60, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67,
			}
			const bootstrapName = "__llgo_coro_program_bootstrap_test"
			bootstrap := pkg.NewCoroProgramBootstrap(
				bootstrapName,
				CoroProgramBootstrapOptions{
					Version: 1,
					ABIHash: hash,
					Steps:   steps,
					Factory: factory,
				},
			)
			if got := pkg.CoroProgramBootstrap(); got != bootstrapName {
				t.Fatalf("bootstrap symbol = %q, want %q", got, bootstrapName)
			}
			if got := prog.PointerSize(); got != test.pointerSize {
				t.Fatalf("pointer size = %d, want %d", got, test.pointerSize)
			}
			if !bootstrap.impl.IsGlobalConstant() {
				t.Fatal("program bootstrap is not a constant global")
			}
			if got := bootstrap.impl.Linkage(); got != llvm.ExternalLinkage {
				t.Fatalf("bootstrap linkage = %v, want external", got)
			}
			if got := bootstrap.impl.Visibility(); got != llvm.HiddenVisibility {
				t.Fatalf("bootstrap visibility = %v, want hidden", got)
			}

			bootstrapType := prog.Elem(bootstrap.Type)
			if got := prog.SizeOf(bootstrapType); got != test.bootstrapSize {
				t.Fatalf("bootstrap size = %d, want %d", got, test.bootstrapSize)
			}
			if got := prog.OffsetOf(bootstrapType, 5); got != test.stepsOffset {
				t.Fatalf("steps offset = %d, want %d", got, test.stepsOffset)
			}
			if got := prog.OffsetOf(bootstrapType, 6); got != test.factoryOffset {
				t.Fatalf("factory offset = %d, want %d", got, test.factoryOffset)
			}
			stepType := prog.Struct(prog.Uint32(), prog.Uint32(), prog.VoidPtr(), prog.Uintptr())
			if got := prog.SizeOf(stepType); got != test.stepSize {
				t.Fatalf("step size = %d, want %d", got, test.stepSize)
			}
			if got := prog.OffsetOf(stepType, 2); got != test.targetOffset {
				t.Fatalf("step target offset = %d, want %d", got, test.targetOffset)
			}
			if got := prog.OffsetOf(stepType, 3); got != test.auxOffset {
				t.Fatalf("step aux offset = %d, want %d", got, test.auxOffset)
			}

			initializer := bootstrap.impl.Initializer()
			if initializer.IsAConstantStruct().IsNil() || initializer.OperandsCount() != 7 {
				t.Fatalf("bootstrap initializer is not a seven-field constant struct: %v", initializer)
			}
			wantFixed := []uint64{
				1,
				0,
				0x5051525354555657,
				0x6061626364656667,
				uint64(len(steps)),
			}
			for i, want := range wantFixed {
				if got := initializer.Operand(i).ZExtValue(); got != want {
					t.Fatalf("bootstrap field %d = %#x, want %#x", i, got, want)
				}
			}
			if got := initializer.Operand(4).Type().IntTypeWidth(); got != test.pointerSize*8 {
				t.Fatalf("bootstrap step count width = %d, want %d", got, test.pointerSize*8)
			}

			stepsGlobal := pkg.Module().NamedGlobal(bootstrapName + ".steps")
			if stepsGlobal.IsNil() {
				t.Fatal("bootstrap lacks its steps array")
			}
			if !stepsGlobal.IsGlobalConstant() || stepsGlobal.Linkage() != llvm.InternalLinkage {
				t.Fatalf("steps array is not an internal constant: %v", stepsGlobal)
			}
			if got := stripCoroAnchorConstantPointer(initializer.Operand(5)); got.C != stepsGlobal.C {
				t.Fatalf("bootstrap steps pointer = %v, want %v", got, stepsGlobal)
			}
			array := stepsGlobal.Initializer()
			if array.IsAConstantArray().IsNil() || array.OperandsCount() != 2 {
				t.Fatalf("steps initializer has %d entries, want 2: %v", array.OperandsCount(), array)
			}
			wantTargets := []llvm.Value{plain.impl, anchor.impl}
			wantKinds := []uint64{uint64(CoroProgramStepDirectPlain), uint64(CoroProgramStepCoroRoot)}
			wantRoles := []uint64{uint64(CoroProgramStepInit), uint64(CoroProgramStepMain)}
			wantAux := []uint64{0, 3}
			for i := 0; i < 2; i++ {
				step := array.Operand(i)
				if step.IsAConstantStruct().IsNil() || step.OperandsCount() != 4 {
					t.Fatalf("step %d is not a four-field constant struct: %v", i, step)
				}
				if got := step.Operand(0).ZExtValue(); got != wantKinds[i] {
					t.Fatalf("step %d kind = %d, want %d", i, got, wantKinds[i])
				}
				if got := step.Operand(1).ZExtValue(); got != wantRoles[i] {
					t.Fatalf("step %d flags = %d, want role %d", i, got, wantRoles[i])
				}
				if got := stripCoroAnchorConstantPointer(step.Operand(2)); got.C != wantTargets[i].C {
					t.Fatalf("step %d target = %v, want %v", i, got, wantTargets[i])
				}
				if got := step.Operand(3).Type().IntTypeWidth(); got != test.pointerSize*8 {
					t.Fatalf("step %d aux width = %d, want %d", i, got, test.pointerSize*8)
				}
				if got := step.Operand(3).ZExtValue(); got != wantAux[i] {
					t.Fatalf("step %d aux = %d, want %d", i, got, wantAux[i])
				}
			}
			if !anchor.impl.IsGlobalConstant() {
				t.Fatal("coro-root anchor declaration was not normalized to constant")
			}
			if factory.IsNil() {
				if initializer.Operand(6).IsAConstantPointerNull().IsNil() {
					t.Fatalf("nil factory was not encoded as null: %v", initializer.Operand(6))
				}
			} else if got := stripCoroAnchorConstantPointer(initializer.Operand(6)); got.C != factory.impl.C {
				t.Fatalf("bootstrap factory = %v, want %v", got, factory.impl)
			}

			pkg.MaterializePreserveSyms()
			used := pkg.Module().NamedGlobal("llvm.used")
			if used.IsNil() {
				t.Fatal("program bootstrap was not retained in llvm.used")
			}
			retained := false
			for i := 0; i < used.Initializer().OperandsCount(); i++ {
				if stripCoroAnchorConstantPointer(used.Initializer().Operand(i)).C == bootstrap.impl.C {
					retained = true
					break
				}
			}
			if !retained {
				t.Fatalf("llvm.used does not retain program bootstrap:\n%s", pkg.String())
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify coroutine program bootstrap: %v\n%s", err, pkg.String())
			}
		})
	}
}

func TestCoroProgramBootstrapRejectsMisuse(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("badcorobootstrap", "bad/coro/bootstrap")
	defer pkg.Module().Dispose()
	plain := pkg.NewFunc("valid_plain", functionSignature(nil, nil), InC)
	anchor := newCoroProgramPackageAnchor(pkg, "valid_root_anchor", false)
	valid := CoroProgramBootstrapOptions{
		Version: 1,
		Steps: []CoroProgramStep{
			{Kind: CoroProgramStepDirectPlain, Flags: CoroProgramStepInit, Target: plain.Expr},
			{Kind: CoroProgramStepCoroRoot, Flags: CoroProgramStepMain, Target: anchor},
		},
	}
	withStep := func(index int, step CoroProgramStep) CoroProgramBootstrapOptions {
		bad := valid
		bad.Steps = append([]CoroProgramStep(nil), valid.Steps...)
		bad.Steps[index] = step
		return bad
	}

	mustPanicContains(t, "requires a name", func() {
		pkg.NewCoroProgramBootstrap("", valid)
	})
	mustPanicContains(t, "flags must be zero", func() {
		bad := valid
		bad.Flags = 1
		pkg.NewCoroProgramBootstrap("bootstrap_flags", bad)
	})
	mustPanicContains(t, "unsupported version 0", func() {
		bad := valid
		bad.Version = 0
		pkg.NewCoroProgramBootstrap("bootstrap_version_zero", bad)
	})
	mustPanicContains(t, "unsupported version 3", func() {
		bad := valid
		bad.Version = 3
		pkg.NewCoroProgramBootstrap("bootstrap_version_unknown", bad)
	})
	for name, steps := range map[string][]CoroProgramStep{
		"zero":  nil,
		"one":   valid.Steps[:1],
		"three": append(append([]CoroProgramStep(nil), valid.Steps...), valid.Steps[1]),
	} {
		t.Run(name+"_step_count", func(t *testing.T) {
			bad := valid
			bad.Steps = steps
			mustPanicContains(t, "requires exactly two steps", func() {
				pkg.NewCoroProgramBootstrap(name+"_step_count", bad)
			})
		})
	}
	for name, flags := range map[string]uint32{
		"zero":    0,
		"both":    CoroProgramStepInit | CoroProgramStepMain,
		"unknown": 1 << 8,
		"main":    CoroProgramStepMain,
	} {
		t.Run(name+"_init_role", func(t *testing.T) {
			bad := withStep(0, CoroProgramStep{
				Kind: CoroProgramStepDirectPlain, Flags: flags, Target: plain.Expr,
			})
			mustPanicContains(t, "step 0 flags", func() {
				pkg.NewCoroProgramBootstrap(name+"_init_role", bad)
			})
		})
	}
	mustPanicContains(t, "step 1 flags", func() {
		bad := valid
		bad.Steps = append([]CoroProgramStep(nil), valid.Steps...)
		bad.Steps[1].Flags = CoroProgramStepInit
		pkg.NewCoroProgramBootstrap("wrong_main_role", bad)
	})
	for name, target := range map[string]Expr{
		"nil":     Nil,
		"integer": prog.IntVal(1, prog.Uintptr()),
		"null":    prog.Nil(prog.VoidPtr()),
	} {
		t.Run(name+"_target", func(t *testing.T) {
			bad := withStep(0, CoroProgramStep{
				Kind: CoroProgramStepDirectPlain, Flags: CoroProgramStepInit, Target: target,
			})
			mustPanicContains(t, "not a non-null constant pointer", func() {
				pkg.NewCoroProgramBootstrap(name+"_target", bad)
			})
		})
	}
	mustPanicContains(t, "invalid kind 0", func() {
		bad := withStep(0, CoroProgramStep{Flags: CoroProgramStepInit, Target: plain.Expr})
		pkg.NewCoroProgramBootstrap("invalid_zero_kind", bad)
	})
	mustPanicContains(t, "invalid kind 3", func() {
		bad := withStep(0, CoroProgramStep{Kind: 3, Flags: CoroProgramStepInit, Target: plain.Expr})
		pkg.NewCoroProgramBootstrap("invalid_high_kind", bad)
	})
	mustPanicContains(t, "aux must be zero", func() {
		bad := withStep(0, CoroProgramStep{
			Kind: CoroProgramStepDirectPlain, Flags: CoroProgramStepInit, Target: plain.Expr, Aux: 1,
		})
		pkg.NewCoroProgramBootstrap("plain_aux", bad)
	})
	mustPanicContains(t, "not a constant function", func() {
		bad := withStep(0, CoroProgramStep{
			Kind: CoroProgramStepDirectPlain, Flags: CoroProgramStepInit, Target: anchor,
		})
		pkg.NewCoroProgramBootstrap("plain_global", bad)
	})
	wrongPlain := pkg.NewFunc("wrong_plain", functionSignature(
		[]types.Type{types.Typ[types.UnsafePointer]}, nil,
	), InC)
	mustPanicContains(t, "requires target signature ()", func() {
		bad := withStep(0, CoroProgramStep{
			Kind: CoroProgramStepDirectPlain, Flags: CoroProgramStepInit, Target: wrongPlain.Expr,
		})
		pkg.NewCoroProgramBootstrap("wrong_plain_signature", bad)
	})

	mutable := pkg.NewVarEx("mutable_root_anchor", prog.Pointer(prog.Uint32()))
	mutable.Init(prog.IntVal(0, prog.Uint32()))
	mustPanicContains(t, "not a constant global", func() {
		bad := withStep(1, CoroProgramStep{
			Kind: CoroProgramStepCoroRoot, Flags: CoroProgramStepMain, Target: mutable.Expr,
		})
		pkg.NewCoroProgramBootstrap("mutable_root", bad)
	})
	mustPanicContains(t, "not a constant global", func() {
		bad := withStep(1, CoroProgramStep{
			Kind: CoroProgramStepCoroRoot, Flags: CoroProgramStepMain, Target: plain.Expr,
		})
		pkg.NewCoroProgramBootstrap("root_function", bad)
	})

	foreignPkg := prog.NewPackage("foreigncorobootstrap", "foreign/coro/bootstrap")
	defer foreignPkg.Module().Dispose()
	foreignPlain := foreignPkg.NewFunc("foreign_plain", functionSignature(nil, nil), InC)
	mustPanicContains(t, "another entry module", func() {
		bad := withStep(0, CoroProgramStep{
			Kind: CoroProgramStepDirectPlain, Flags: CoroProgramStepInit, Target: foreignPlain.Expr,
		})
		pkg.NewCoroProgramBootstrap("foreign_plain", bad)
	})
	foreignAnchor := newCoroProgramPackageAnchor(foreignPkg, "foreign_root_anchor", true)
	mustPanicContains(t, "another entry module", func() {
		bad := withStep(1, CoroProgramStep{
			Kind: CoroProgramStepCoroRoot, Flags: CoroProgramStepMain, Target: foreignAnchor,
		})
		pkg.NewCoroProgramBootstrap("foreign_root", bad)
	})

	lateInvalidAnchor := newCoroProgramPackageAnchor(pkg, "late_invalid_anchor", false)
	mustPanicContains(t, "invalid kind", func() {
		bad := valid
		bad.Steps = []CoroProgramStep{
			{Kind: CoroProgramStepCoroRoot, Flags: CoroProgramStepInit, Target: lateInvalidAnchor},
			{Kind: 99, Flags: CoroProgramStepMain, Target: plain.Expr},
		}
		pkg.NewCoroProgramBootstrap("atomic_validation", bad)
	})
	if lateInvalidAnchor.impl.IsGlobalConstant() {
		t.Fatal("failed bootstrap emission mutated an earlier anchor declaration")
	}

	mustPanicContains(t, "factory is not a non-null constant function", func() {
		bad := valid
		bad.Factory = prog.IntVal(1, prog.Uintptr())
		pkg.NewCoroProgramBootstrap("integer_factory", bad)
	})
	mustPanicContains(t, "factory is not a non-null constant function", func() {
		bad := valid
		bad.Factory = prog.Nil(prog.rawType(coroRootFactoryTestSignature()))
		pkg.NewCoroProgramBootstrap("null_factory", bad)
	})
	mustPanicContains(t, "requires factory signature", func() {
		bad := valid
		bad.Factory = plain.Expr
		pkg.NewCoroProgramBootstrap("wrong_factory_signature", bad)
	})
	foreignFactory := foreignPkg.NewFunc("foreign_factory", coroRootFactoryTestSignature(), InC)
	mustPanicContains(t, "another entry module", func() {
		bad := valid
		bad.Factory = foreignFactory.Expr
		pkg.NewCoroProgramBootstrap("foreign_factory", bad)
	})

	wasmProg := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer wasmProg.Dispose()
	wasmPkg := wasmProg.NewPackage("wasmcorobootstrap", "wasm/coro/bootstrap")
	defer wasmPkg.Module().Dispose()
	wasmPlain := wasmPkg.NewFunc("wasm_plain", functionSignature(nil, nil), InC)
	wasmAnchor := newCoroProgramPackageAnchor(wasmPkg, "wasm_root_anchor", false)
	mustPanicContains(t, "aux overflows target uintptr", func() {
		wasmPkg.NewCoroProgramBootstrap("wasm_aux_overflow", CoroProgramBootstrapOptions{
			Version: 1,
			Steps: []CoroProgramStep{
				{Kind: CoroProgramStepDirectPlain, Flags: CoroProgramStepInit, Target: wasmPlain.Expr},
				{
					Kind: CoroProgramStepCoroRoot, Flags: CoroProgramStepMain,
					Target: wasmAnchor, Aux: uint64(1) << 32,
				},
			},
		})
	})

	pkg.NewVarEx("occupied_bootstrap", prog.Pointer(prog.Uint32()))
	mustPanicContains(t, "symbol \"occupied_bootstrap\" already exists", func() {
		pkg.NewCoroProgramBootstrap("occupied_bootstrap", valid)
	})
	pkg.NewVarEx("occupied_steps.steps", prog.Pointer(prog.Uint32()))
	mustPanicContains(t, "symbol \"occupied_steps.steps\" already exists", func() {
		pkg.NewCoroProgramBootstrap("occupied_steps", valid)
	})
	if got := pkg.CoroProgramBootstrap(); got != "" {
		t.Fatalf("rejected bootstrap attempts recorded symbol %q", got)
	}

	pkg.NewCoroProgramBootstrap("valid_bootstrap", valid)
	mustPanicContains(t, "already defined as \"valid_bootstrap\"", func() {
		pkg.NewCoroProgramBootstrap("second_bootstrap", CoroProgramBootstrapOptions{})
	})
}

func TestCoroProgramBootstrapV2MixedStartupTable(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("corobootstrapv2", "coro/bootstrap/v2")
	defer pkg.Module().Dispose()

	plains := [4]Function{
		pkg.NewFunc("internal_runtime_init", functionSignature(nil, nil), InC),
		pkg.NewFunc("public_runtime_init", functionSignature(nil, nil), InC),
		pkg.NewFunc("dependency_plain_init", functionSignature(nil, nil), InC),
		pkg.NewFunc("main", functionSignature(nil, nil), InC),
	}
	anchors := [3]Expr{
		newCoroProgramPackageAnchor(pkg, "compiler_abi_init_anchor", false),
		newCoroProgramPackageAnchor(pkg, "dependency_init_anchor", false),
		newCoroProgramPackageAnchor(pkg, "main_package_init_anchor", false),
	}
	factory := pkg.NewFunc("bootstrap_factory_v2", coroRootFactoryTestSignature(), InC)
	roles := [7]uint32{
		CoroProgramStepInternalRuntimeInitV2,
		CoroProgramStepCompilerABIInitV2,
		CoroProgramStepPublicRuntimeInitV2,
		CoroProgramStepPackageInitV2,
		CoroProgramStepPackageInitV2,
		CoroProgramStepPackageInitV2,
		CoroProgramStepMainV2,
	}
	steps := []CoroProgramStep{
		{Kind: CoroProgramStepDirectPlain, Flags: roles[0], Target: plains[0].Expr},
		{Kind: CoroProgramStepCoroRoot, Flags: roles[1], Target: anchors[0], Aux: 2},
		{Kind: CoroProgramStepDirectPlain, Flags: roles[2], Target: plains[1].Expr},
		{Kind: CoroProgramStepDirectPlain, Flags: roles[3], Target: plains[2].Expr},
		{Kind: CoroProgramStepCoroRoot, Flags: roles[4], Target: anchors[1], Aux: 5},
		{Kind: CoroProgramStepCoroRoot, Flags: roles[5], Target: anchors[2], Aux: 7},
		{Kind: CoroProgramStepDirectPlain, Flags: roles[6], Target: plains[3].Expr},
	}
	bootstrap := pkg.NewCoroProgramBootstrap("__llgo_coro_program_bootstrap_v2", CoroProgramBootstrapOptions{
		Version: 2,
		Flags:   CoroProgramBootstrapFlagWorkerV2,
		ABIHash: [16]byte{
			0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
			0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27,
		},
		Steps:   steps,
		Factory: factory.Expr,
	})

	initializer := bootstrap.impl.Initializer()
	if got := initializer.Operand(0).ZExtValue(); got != 2 {
		t.Fatalf("bootstrap version = %d, want 2", got)
	}
	if got := initializer.Operand(1).ZExtValue(); got != uint64(CoroProgramBootstrapFlagWorkerV2) {
		t.Fatalf("bootstrap flags = %#x, want worker capability", got)
	}
	if got := initializer.Operand(4).ZExtValue(); got != uint64(len(steps)) {
		t.Fatalf("bootstrap step count = %d, want %d", got, len(steps))
	}
	stepsGlobal := pkg.Module().NamedGlobal("__llgo_coro_program_bootstrap_v2.steps")
	if stepsGlobal.IsNil() || !stepsGlobal.IsGlobalConstant() {
		t.Fatal("v2 bootstrap lacks its constant steps table")
	}
	array := stepsGlobal.Initializer()
	if got := array.OperandsCount(); got != len(steps) {
		t.Fatalf("v2 steps count = %d, want %d", got, len(steps))
	}
	wantKinds := [7]CoroProgramStepKind{
		CoroProgramStepDirectPlain,
		CoroProgramStepCoroRoot,
		CoroProgramStepDirectPlain,
		CoroProgramStepDirectPlain,
		CoroProgramStepCoroRoot,
		CoroProgramStepCoroRoot,
		CoroProgramStepDirectPlain,
	}
	wantAux := [7]uint64{0, 2, 0, 0, 5, 7, 0}
	for index := range steps {
		step := array.Operand(index)
		if got := step.Operand(0).ZExtValue(); got != uint64(wantKinds[index]) {
			t.Errorf("step %d kind = %d, want %d", index, got, wantKinds[index])
		}
		if got := step.Operand(1).ZExtValue(); got != uint64(roles[index]) {
			t.Errorf("step %d role = %#x, want %#x", index, got, roles[index])
		}
		if got := step.Operand(3).ZExtValue(); got != wantAux[index] {
			t.Errorf("step %d aux = %d, want %d", index, got, wantAux[index])
		}
	}
	if !anchors[0].impl.IsGlobalConstant() || !anchors[1].impl.IsGlobalConstant() ||
		!anchors[2].impl.IsGlobalConstant() {
		t.Fatal("v2 coro-root anchor declarations were not normalized to constants")
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify v2 mixed bootstrap: %v\n%s", err, pkg.String())
	}
}

func TestCoroProgramBootstrapV2RejectsShapeAndRoles(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("badcorobootstrapv2", "bad/coro/bootstrap/v2")
	defer pkg.Module().Dispose()
	plains := [5]Function{
		pkg.NewFunc("v2_step_0", functionSignature(nil, nil), InC),
		pkg.NewFunc("v2_step_1", functionSignature(nil, nil), InC),
		pkg.NewFunc("v2_step_2", functionSignature(nil, nil), InC),
		pkg.NewFunc("v2_step_3", functionSignature(nil, nil), InC),
		pkg.NewFunc("v2_step_4", functionSignature(nil, nil), InC),
	}
	roles := [5]uint32{
		CoroProgramStepInternalRuntimeInitV2,
		CoroProgramStepCompilerABIInitV2,
		CoroProgramStepPublicRuntimeInitV2,
		CoroProgramStepPackageInitV2,
		CoroProgramStepMainV2,
	}
	valid := CoroProgramBootstrapOptions{Version: 2, Steps: make([]CoroProgramStep, 5)}
	for index := range valid.Steps {
		valid.Steps[index] = CoroProgramStep{
			Kind: CoroProgramStepDirectPlain, Flags: roles[index], Target: plains[index].Expr,
		}
	}
	badFlags := valid
	badFlags.Flags = CoroProgramBootstrapFlagWorkerV2 << 1
	mustPanicContains(t, "unknown capability flags", func() {
		pkg.NewCoroProgramBootstrap("v2_bad_capability_flags", badFlags)
	})
	for _, count := range []int{0, 1, 2, 3, 4} {
		bad := valid
		bad.Steps = append([]CoroProgramStep(nil), valid.Steps...)
		bad.Steps = bad.Steps[:count]
		mustPanicContains(t, "version 2 requires at least 5 steps", func() {
			pkg.NewCoroProgramBootstrap(fmt.Sprintf("v2_bad_count_%d", count), bad)
		})
	}
	for index := range roles {
		for name, role := range map[string]uint32{
			"zero":     0,
			"next":     roles[(index+1)%len(roles)],
			"multiple": roles[index] | roles[(index+1)%len(roles)],
			"unknown":  1 << 12,
		} {
			bad := valid
			bad.Steps = append([]CoroProgramStep(nil), valid.Steps...)
			bad.Steps[index].Flags = role
			mustPanicContains(t, fmt.Sprintf("step %d flags", index), func() {
				pkg.NewCoroProgramBootstrap(fmt.Sprintf("v2_bad_role_%d_%s", index, name), bad)
			})
		}
	}
}

func TestCoroBuilderRejectsMisuse(t *testing.T) {
	fixture := newCoroTestFixture(t, nil, 0)
	mustPanicContains(t, "finished coroutine", func() { fixture.coro.Suspend() })
	mustPanicContains(t, "finished coroutine", func() { fixture.coro.SuspendCurrentBlock() })
	mustPanicContains(t, "finished coroutine", func() {
		fixture.coro.SuspendCurrentBlockWithAfterResume(func(Builder) {})
	})
	mustPanicContains(t, "finished coroutine", func() {
		fixture.coro.SuspendCurrentBlockWithResumeDispatch(func(b Builder, normal BasicBlock) {
			b.Jump(normal)
		})
	})
	mustPanicContains(t, "finished coroutine", func() { fixture.coro.SuspendCurrentBlockIf(fixture.prog.BoolVal(true), nil) })
	mustPanicContains(t, "finished coroutine", func() { fixture.coro.Finish() })
	mustPanicContains(t, "nil coroutine builder", func() { (*CoroBuilder)(nil).SuspendCurrentBlock() })
	mustPanicContains(t, "nil coroutine builder", func() {
		(*CoroBuilder)(nil).SuspendCurrentBlockWithAfterResume(func(Builder) {})
	})
	mustPanicContains(t, "nil coroutine builder", func() {
		(*CoroBuilder)(nil).SuspendCurrentBlockWithResumeDispatch(func(Builder, BasicBlock) {})
	})
	mustPanicContains(t, "nil coroutine builder", func() { (*CoroBuilder)(nil).SuspendCurrentBlockIf(Nil, nil) })
	if (*CoroBuilder)(nil).Handle() != Nil {
		t.Fatal("nil coroutine builder returned a non-nil handle")
	}
	if (*CoroBuilder)(nil).InitialResumeBlock() != nil {
		t.Fatal("nil coroutine builder returned a non-nil initial resume block")
	}

	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("badcoro", "bad/coro")
	defer pkg.Module().Dispose()
	fn := pkg.NewFunc("bad_alignment", coroHandleSignature(), InC)
	mustPanicContains(t, "physical parameter index", func() { fn.PhysicalParam(0) })
	mustPanicContains(t, "requires a name", func() {
		pkg.NewCoroFrameDescriptor("", CoroFrameDescriptorOptions{Result: prog.Byte()})
	})
	mustPanicContains(t, "requires a result type", func() {
		pkg.NewCoroFrameDescriptor("bad_descriptor", CoroFrameDescriptorOptions{})
	})
	mustPanicContains(t, "requires a logical function name", func() {
		pkg.NewCoroFrameDescriptor(
			"bad_trace_descriptor",
			CoroFrameDescriptorOptions{Result: prog.Byte()},
		)
	})
	b := fn.MakeBody(1)
	defer b.Dispose()
	invalidHandles := []struct {
		name   string
		handle Expr
	}{
		{"nil expression", Nil},
		{"integer", prog.IntVal(1, prog.Uintptr())},
		{"function", fn.Expr},
	}
	for _, test := range invalidHandles {
		t.Run("reject handle "+test.name, func(t *testing.T) {
			operations := []struct {
				name string
				call func()
			}{
				{"promise", func() { b.CoroPromise(test.handle, prog.Byte()) }},
				{"done", func() { b.CoroDone(test.handle) }},
				{"resume", func() { b.CoroResume(test.handle) }},
				{"destroy", func() { b.CoroDestroy(test.handle) }},
			}
			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					mustPanicContains(t, "handle must be a pointer", operation.call)
				})
			}
		})
	}
	validHandle := prog.Nil(prog.VoidPtr())
	mustPanicContains(t, "concrete payload type", func() {
		b.CoroPromise(validHandle, nil)
	})
	mustPanicContains(t, "concrete payload type", func() {
		b.CoroPromise(validHandle, prog.Void())
	})
	var nilBuilder Builder
	mustPanicContains(t, "without an active function block", func() {
		nilBuilder.CoroDone(validHandle)
	})
	mustPanicContains(t, "alignment", func() {
		b.BeginCoro(CoroOptions{
			AllocationAlign: 3,
			Frame: CoroFrameOps{
				Alloc: func(Builder, Expr, Expr) Expr { return prog.Nil(prog.VoidPtr()) },
				Free:  func(Builder, Expr, Expr, Expr) {},
			},
		})
	})
}

func TestCoroBuilderRejectsCallbackControlFlow(t *testing.T) {
	t.Run("allocator changes LLVM insertion block", func(t *testing.T) {
		prog, b := newCoroCallbackTestBuilder(t)
		mustPanicContains(t, "allocator callback changed insertion block", func() {
			b.BeginCoro(CoroOptions{Frame: CoroFrameOps{
				Alloc: func(b Builder, _, _ Expr) Expr {
					b.SetBlockEx(b.Func.MakeBlock(), AtEnd, false)
					return prog.Nil(prog.VoidPtr())
				},
				Free: func(Builder, Expr, Expr, Expr) {},
			}})
		})
	})

	t.Run("allocator inserts before append point", func(t *testing.T) {
		prog, b := newCoroCallbackTestBuilder(t)
		mustPanicContains(t, "allocator callback modified instructions before append point", func() {
			b.BeginCoro(CoroOptions{Frame: CoroFrameOps{
				Alloc: func(b Builder, _, _ Expr) Expr {
					b.SetBlockEx(b.blk, AtStart, false)
					b.Unreachable()
					return prog.Nil(prog.VoidPtr())
				},
				Free: func(Builder, Expr, Expr, Expr) {},
			}})
		})
	})

	t.Run("free terminates block", func(t *testing.T) {
		prog, b := newCoroCallbackTestBuilder(t)
		coro := b.BeginCoro(CoroOptions{Frame: CoroFrameOps{
			Alloc: func(Builder, Expr, Expr) Expr {
				return prog.Nil(prog.VoidPtr())
			},
			Free: func(b Builder, _, _, _ Expr) {
				b.Unreachable()
			},
		}})
		mustPanicContains(t, "free callback terminated insertion block", coro.Finish)
	})

	t.Run("before initial suspend terminates block", func(t *testing.T) {
		prog, b := newCoroCallbackTestBuilder(t)
		mustPanicContains(t, "before-initial-suspend callback terminated insertion block", func() {
			b.BeginCoro(CoroOptions{
				Frame: CoroFrameOps{
					Alloc: func(Builder, Expr, Expr) Expr {
						return prog.Nil(prog.VoidPtr())
					},
					Free: func(Builder, Expr, Expr, Expr) {},
				},
				BeforeInitialSuspend: func(b Builder, _, _ Expr) {
					b.Unreachable()
				},
			})
		})
	})

	t.Run("after resume terminates block", func(t *testing.T) {
		prog, b := newCoroCallbackTestBuilder(t)
		mustPanicContains(t, "after-resume callback terminated insertion block", func() {
			b.BeginCoro(CoroOptions{
				Frame: CoroFrameOps{
					Alloc: func(Builder, Expr, Expr) Expr {
						return prog.Nil(prog.VoidPtr())
					},
					Free: func(Builder, Expr, Expr, Expr) {},
				},
				AfterResume: func(b Builder) {
					b.Unreachable()
				},
			})
		})
	})

	t.Run("after resume override terminates block", func(t *testing.T) {
		prog, b := newCoroCallbackTestBuilder(t)
		coro := b.BeginCoro(CoroOptions{
			Frame: CoroFrameOps{
				Alloc: func(Builder, Expr, Expr) Expr {
					return prog.Nil(prog.VoidPtr())
				},
				Free: func(Builder, Expr, Expr, Expr) {},
			},
		})
		mustPanicContains(t, "after-resume callback terminated insertion block", func() {
			coro.SuspendCurrentBlockWithAfterResume(func(b Builder) {
				b.Unreachable()
			})
		})
	})
}

func newCoroCallbackTestBuilder(t *testing.T) (Program, Builder) {
	t.Helper()
	prog := NewProgram(nil)
	pkg := prog.NewPackage("badcorocallback", "bad/coro/callback")
	fn := pkg.NewFunc("bad_callback", coroHandleSignature(), InC)
	b := fn.MakeBody(1)
	t.Cleanup(func() {
		b.Dispose()
		pkg.Module().Dispose()
		prog.Dispose()
	})
	return prog, b
}

func newCoroTestFixture(t *testing.T, target *Target, allocationAlign uint32) *coroTestFixture {
	t.Helper()
	Initialize(InitAll)
	prog := NewProgram(target)
	pkg := prog.NewPackage("corotest", "coro/test")
	t.Cleanup(func() {
		pkg.Module().Dispose()
		prog.Dispose()
	})

	alloc := pkg.NewFunc("coro_frame_alloc", functionSignature(
		[]types.Type{types.Typ[types.Uintptr], types.Typ[types.Uintptr]},
		[]types.Type{types.Typ[types.UnsafePointer]},
	), InC)
	free := pkg.NewFunc("coro_frame_free", functionSignature(
		[]types.Type{types.Typ[types.UnsafePointer], types.Typ[types.Uintptr], types.Typ[types.Uintptr]},
		nil,
	), InC)
	sink := pkg.NewFunc("coro_value_sink", functionSignature([]types.Type{types.Typ[types.Uint8]}, nil), InC)

	fn := pkg.NewFunc("coro_test", coroHandleSignature(), InGo)
	b := fn.MakeBody(1)
	promise := b.AllocaT(prog.Byte())
	// Keep promise alignment distinct from AllocationAlign so the test guards
	// llvm.coro.id's allocator-guarantee semantics rather than conflating them.
	promise.impl.SetAlignment(16)
	coro := b.BeginCoro(CoroOptions{
		Promise:         promise,
		AllocationAlign: allocationAlign,
		Frame: CoroFrameOps{
			Alloc: func(b Builder, size, align Expr) Expr {
				return b.Call(alloc.Expr, size, align)
			},
			Free: func(b Builder, frame, size, align Expr) {
				b.Call(free.Expr, frame, size, align)
			},
		},
		BeforeInitialSuspend: func(b Builder, handle, _ Expr) {
			if handle.IsNil() {
				t.Fatal("before-initial-suspend callback received a nil handle")
			}
			b.Store(promise, prog.IntVal(1, prog.Byte()))
		},
	})

	live := b.AllocaT(prog.Byte())
	live.impl.SetAlignment(64)
	b.Store(live, prog.IntVal(7, prog.Byte()))
	coro.Suspend()
	b.Call(sink.Expr, b.Load(live))
	coro.Finish()
	b.EndBuild()
	b.Dispose()

	return &coroTestFixture{prog: prog, pkg: pkg, fn: fn, coro: coro}
}

func functionSignature(params, results []types.Type) *types.Signature {
	makeTuple := func(values []types.Type) *types.Tuple {
		vars := make([]*types.Var, len(values))
		for i, value := range values {
			vars[i] = types.NewVar(token.NoPos, nil, "", value)
		}
		return types.NewTuple(vars...)
	}
	return types.NewSignatureType(nil, nil, nil, makeTuple(params), makeTuple(results), false)
}

func coroHandleSignature() *types.Signature {
	return functionSignature(nil, []types.Type{types.Typ[types.UnsafePointer]})
}

func coroRootFactoryTestSignature() *types.Signature {
	return functionSignature(
		[]types.Type{
			types.Typ[types.UnsafePointer],
			types.Typ[types.UnsafePointer],
			types.Typ[types.UnsafePointer],
		},
		[]types.Type{types.Typ[types.UnsafePointer]},
	)
}

func newCoroRootDescriptorForAnchor(pkg Package, name string) Expr {
	prog := pkg.Prog
	factory := pkg.NewFunc(name+".factory", coroRootFactoryTestSignature(), InC)
	return pkg.NewCoroRootFactoryDescriptor(
		name+".descriptor",
		CoroRootFactoryDescriptorOptions{
			Version: 1,
			Factory: factory.Expr,
			Startup: prog.VoidPtr(),
			Result:  prog.VoidPtr(),
		},
	)
}

func newCoroProgramPackageAnchor(pkg Package, name string, definition bool) Expr {
	prog := pkg.Prog
	global := pkg.NewVarEx(name, prog.Pointer(prog.Uint32()))
	if definition {
		global.Init(prog.IntVal(0, prog.Uint32()))
		global.impl.SetGlobalConstant(true)
	}
	return global.Expr
}

func stripCoroAnchorConstantPointer(value llvm.Value) llvm.Value {
	for !value.IsAConstantExpr().IsNil() && value.OperandsCount() == 1 {
		value = value.Operand(0)
	}
	return value
}

func runCoroPasses(t *testing.T, fixture *coroTestFixture, pipeline string) {
	t.Helper()
	mod := fixture.pkg.Module()
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify before %s: %v\n%s", pipeline, err, mod.String())
	}
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := mod.RunPasses(pipeline, fixture.prog.TargetMachine(), options); err != nil {
		t.Fatalf("run %s: %v\n%s", pipeline, err, mod.String())
	}
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify after %s: %v\n%s", pipeline, err, mod.String())
	}
}

func assertCoroSuspendDefaults(t *testing.T, fixture *coroTestFixture) {
	t.Helper()
	suspendID := llvm.LookupIntrinsicID("llvm.coro.suspend")
	count := 0
	for _, block := range fixture.fn.impl.BasicBlocks() {
		terminator := block.LastInstruction()
		if terminator.IsNil() || terminator.IsASwitchInst().IsNil() {
			continue
		}
		condition := terminator.Operand(0)
		if condition.IsACallInst().IsNil() || condition.CalledValue().IntrinsicID() != suspendID {
			continue
		}
		count++
		defaultBlock := terminator.Operand(1).AsBasicBlock()
		if defaultBlock.C != fixture.coro.suspendBlk.first.C {
			t.Fatalf("coro.suspend switch %d has a non-shared default block", count)
		}
	}
	if count != 3 {
		t.Fatalf("structured coro.suspend switches = %d, want 3", count)
	}
	cleanupTerminator := fixture.coro.cleanupBlk.last.LastInstruction()
	if cleanupTerminator.IsNil() || cleanupTerminator.InstructionOpcode() != llvm.Br {
		t.Fatal("coroutine cleanup block lacks its guarded-free branch")
	}
}

func countCoroEndCalls(ir string) int {
	return strings.Count(ir, "call i1 @llvm.coro.end") + strings.Count(ir, "call void @llvm.coro.end")
}

func hasCoroIntrinsicCall(ir, intrinsic string) bool {
	return regexp.MustCompile(`call [^\n]*@` + regexp.QuoteMeta(intrinsic) + `\b`).MatchString(ir)
}

func frameAllocCallLine(ir string) string {
	for _, line := range strings.Split(ir, "\n") {
		if strings.Contains(line, "call") && strings.Contains(line, "@coro_frame_alloc") {
			return line
		}
	}
	return ""
}

func mustPanicContains(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		got := recover()
		if got == nil {
			t.Fatalf("operation did not panic with %q", want)
		}
		if text := fmt.Sprint(got); !strings.Contains(text, want) {
			t.Fatalf("panic = %q, want substring %q", text, want)
		}
	}()
	fn()
}
