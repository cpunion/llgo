//go:build !llgo

package ssa

import (
	"github.com/xgo-dev/llvm"
	"strings"
	"testing"
)

func TestCoroBuilderBorrowedFreeCallback(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("borrowedfree", "coro/borrowedfree")
	defer pkg.Module().Dispose()
	fn := pkg.NewFunc("borrowed_free", coroHandleSignature(), InGo)
	b := fn.MakeBody(1)
	defer b.Dispose()
	calls := 0
	coro := b.BeginCoro(CoroOptions{Frame: CoroFrameOps{
		Alloc: func(b Builder, size, align Expr) Expr { return prog.Nil(prog.VoidPtr()) },
		Free:  func(b Builder, frame, size, align Expr) {},
		FreeBorrowed: func(b Builder, frame, size, align Expr) {
			calls++
			if frame.IsNil() || size.Type != prog.Uintptr() || align.Type != prog.Uintptr() {
				t.Fatal("borrowed callback arguments")
			}
			b.KeepAlive(frame)
		},
	}})
	coro.Suspend()
	coro.Finish()
	b.EndBuild()
	if calls != 1 {
		t.Fatalf("callback count=%d", calls)
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pkg.Module().String(), "llvm.coro.free") {
		t.Fatal("missing free intrinsic")
	}
}
