package ssa

import (
	"go/token"
	"go/types"
	"runtime"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestLocationRecordInsertionPoint(t *testing.T) {
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("foo", "foo")
	fn := pkg.NewFunc("foo.f", NoArgsNoRet, InGo)
	b := fn.MakeBody(2)
	defer b.Dispose()
	callee := pkg.NewFunc("record", NoArgsNoRet, InGo)
	emitted := 0
	record := func() {
		b.EmitLocationRecord("panic", "foo.f", "foo.go", 1, func() {
			b.Call(callee.Expr)
			emitted++
		})
	}
	record()
	record()
	if emitted != 1 {
		t.Fatal("tail duplicate was not skipped")
	}
	b.Jump(fn.Block(1))
	b.SetBlock(fn.Block(1))
	record()
	b.Return()
	// Phi lowering and other rewrites can revisit an already terminated block.
	// Do not reuse the remembered record when inserting before its terminator.
	b.SetBlockEx(fn.Block(1), BeforeLast, true)
	record()
	record()
	if emitted != 4 {
		t.Fatalf("emitted %d records, want 4", emitted)
	}
	b.EndBuild()
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
}

func TestLocationRecordDebugPositions(t *testing.T) {
	prog := NewProgram(nil)
	defer prog.Dispose()
	prog.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
	pkg := prog.NewPackage("foo", "foo")
	pkg.InitDebug("foo", "foo", token.NewFileSet())
	fn := pkg.NewFunc("foo.f", NoArgsNoRet, InGo)
	b := fn.MakeBody(1)
	defer b.Dispose()
	pos := token.Position{Filename: "foo.go", Line: 10, Column: 2}
	b.DebugFunction(fn, nil, pos, pos)
	callee := pkg.NewFunc("record", NoArgsNoRet, InGo)
	emitted := 0
	record := func() {
		b.EmitLocationRecord("panic", "foo.f", pos.Filename, pos.Line, func() {
			b.Call(callee.Expr)
			emitted++
		})
	}
	record()
	record()
	loc := b.diLocation
	loc.Col++
	b.setDebugLocation(loc)
	record()
	if emitted != 2 {
		t.Fatalf("emitted %d records, want two distinct debugger positions", emitted)
	}
	b.Return()
	b.EndBuild()
	pkg.FinalizeDebug()
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
}
