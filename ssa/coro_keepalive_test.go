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
	"bytes"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

// TestCoroKeepAliveRetainsLocalAddress exercises the exact worker-owner
// shape: a typed local address is produced before an ordinary suspend and has
// no resumed use other than KeepAlive. The fake use must remain on the resumed
// side of the cut and make CoroSplit place the local in the coroutine frame.
func TestCoroKeepAliveRetainsLocalAddress(t *testing.T) {
	if major := llvmMajorVersion(); major < 19 || major > 22 {
		t.Skipf("coroutine keepalive regression requires supported LLVM 19-22, using %s", llvm.Version)
	}

	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("corokeepalive", "coro/keepalive")
	defer pkg.Module().Dispose()

	alloc := pkg.NewFunc("keepalive_frame_alloc", functionSignature(
		[]types.Type{types.Typ[types.Uintptr], types.Typ[types.Uintptr]},
		[]types.Type{types.Typ[types.UnsafePointer]},
	), InC)
	free := pkg.NewFunc("keepalive_frame_free", functionSignature(
		[]types.Type{types.Typ[types.UnsafePointer], types.Typ[types.Uintptr], types.Typ[types.Uintptr]},
		nil,
	), InC)

	fn := pkg.NewFunc("coro_keepalive", coroHandleSignature(), InGo)
	b := fn.MakeBody(1)
	coro := b.BeginCoro(CoroOptions{Frame: CoroFrameOps{
		Alloc: func(b Builder, size, align Expr) Expr {
			return b.Call(alloc.Expr, size, align)
		},
		Free: func(b Builder, frame, size, align Expr) {
			b.Call(free.Expr, frame, size, align)
		},
	}})
	owner := b.AllocaT(prog.Uint64()).SetName("worker.owner.local")
	b.Store(owner, prog.IntVal(42, prog.Uint64()))
	coro.Suspend()
	b.KeepAlive(owner)
	coro.Finish()
	b.EndBuild()
	b.Dispose()

	fixture := &coroTestFixture{prog: prog, pkg: pkg, fn: fn, coro: coro}
	pre := pkg.Module().String()
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify presplit keepalive coroutine: %v\n%s", err, pre)
	}
	fakeUse := strings.Index(pre, "call void (...) @llvm.fake.use(ptr %worker.owner.local)")
	if fakeUse < 0 {
		t.Fatalf("presplit coroutine lacks typed owner fake use:\n%s", pre)
	}
	ordinarySuspend := strings.Index(pre, "call i8 @llvm.coro.suspend(token none, i1 false)")
	if ordinarySuspend < 0 || ordinarySuspend > fakeUse {
		t.Fatalf("owner fake use is not after an ordinary suspend:\n%s", pre)
	}

	runCoroPasses(t, fixture, "coro-early,cgscc(coro-split),coro-cleanup")
	post := pkg.Module().String()
	resume := pkg.Module().NamedFunction("coro_keepalive.resume")
	if resume.IsNil() {
		t.Fatalf("CoroSplit did not create coro_keepalive.resume:\n%s", post)
	}
	resumeIR := resume.String()
	if !strings.Contains(resumeIR, "call void (...) @llvm.fake.use(ptr") {
		t.Fatalf("CoroSplit dropped the resumed owner fake use:\n%s", resumeIR)
	}
	if !strings.Contains(resumeIR,
		"%worker.owner.local.reload.addr = getelementptr inbounds %coro_keepalive.Frame") ||
		!strings.Contains(resumeIR,
			"call void (...) @llvm.fake.use(ptr %worker.owner.local.reload.addr)") {
		t.Fatalf("owner address used after resume is not derived from the coroutine frame:\n%s", resumeIR)
	}
	if removed := RemoveKeepAliveCallsAfterCoroSplit(pkg.Module()); removed == 0 {
		t.Fatal("post-CoroSplit keepalive cleanup removed no fake uses")
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify coroutine after keepalive cleanup: %v\n%s", err, pkg.Module().String())
	}
	if strings.Contains(pkg.Module().String(), "call void (...) @llvm.fake.use") {
		t.Fatalf("post-CoroSplit keepalive cleanup left a fake-use call:\n%s", pkg.Module().String())
	}
	object, err := prog.TargetMachine().EmitToMemoryBuffer(pkg.Module(), llvm.ObjectFile)
	if err != nil {
		t.Fatalf("emit coroutine after keepalive cleanup: %v", err)
	}
	defer object.Dispose()
	if bytes.Contains(object.Bytes(), []byte("llvm.fake.use")) {
		t.Fatal("post-CoroSplit object retained an llvm.fake.use reference")
	}
}
