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
	if major := llvmMajorVersion(); major == 14 {
		if !strings.Contains(ir, `"coroutine.presplit"="0"`) {
			t.Fatalf("LLVM 14 coroutine lacks unprepared frontend presplit state:\n%s", ir)
		}
	} else if !strings.Contains(ir, "presplitcoroutine") {
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

func TestCoroBuilderCoroSplit(t *testing.T) {
	fixture := newCoroTestFixture(t, nil, 32)
	mod := fixture.pkg.Module()
	pipeline := "coro-early,cgscc(coro-split),coro-cleanup"
	if llvmMajorVersion() == 14 {
		// LLVM 14 implicitly treats a pipeline beginning with coro-early as a
		// function pipeline, so every pass-manager level must be explicit.
		pipeline = "function(coro-early),cgscc(coro-split),function(coro-cleanup)"
	}
	runCoroPasses(t, fixture, pipeline)

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

	pipeline := "coro-early,cgscc(coro-split),coro-cleanup"
	if llvmMajorVersion() == 14 {
		pipeline = "function(coro-early),cgscc(coro-split),function(coro-cleanup)"
	}
	runCoroPasses(t, fixture, pipeline)
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

func TestCoroBuilderDefaultPipelineLLVM19(t *testing.T) {
	if llvmMajorVersion() != 19 {
		t.Skipf("production default<O0> smoke is specific to LLVM 19, using %s", llvm.Version)
	}
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

func TestCoroBuilderRejectsMisuse(t *testing.T) {
	fixture := newCoroTestFixture(t, nil, 0)
	mustPanicContains(t, "finished coroutine", func() { fixture.coro.Suspend() })
	mustPanicContains(t, "finished coroutine", func() { fixture.coro.Finish() })
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
