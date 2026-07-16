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
				BeforeInitialSuspend: func(b Builder, _ Expr) {
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
		BeforeInitialSuspend: func(b Builder, handle Expr) {
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
