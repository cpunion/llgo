//go:build !llgo
// +build !llgo

package ssa_test

import (
	"strings"
	"testing"

	"github.com/goplus/llgo/ssa"
	"github.com/goplus/llgo/ssa/ssatest"
)

func TestSetjmpLongjmpIRPaths(t *testing.T) {
	prog := ssatest.NewProgram(t, nil)
	pkg := prog.NewPackage("foo", "foo")

	fn := pkg.NewFunc("f", ssa.NoArgsNoRet, ssa.InGo)
	b := fn.MakeBody(1)
	jb := b.AllocaSigjmpBuf()
	zero := prog.IntVal(0, prog.CInt())
	one := prog.IntVal(1, prog.CInt())
	_ = b.Sigsetjmp(jb, zero)
	b.Siglongjmp(jb, one)
	b.Longjmp(jb, one)
	b.Return()
	b.EndBuild()

	ir := pkg.Module().String()
	if !strings.Contains(ir, "setjmp") {
		t.Fatalf("expected setjmp/sigsetjmp symbol in IR, got:\n%s", ir)
	}
	if !strings.Contains(ir, "longjmp") {
		t.Fatalf("expected longjmp/siglongjmp symbol in IR, got:\n%s", ir)
	}
	if strings.Contains(ir, "runtime.Sigsetjmp") || strings.Contains(ir, "runtime.Siglongjmp") {
		t.Fatalf("sigjmp lowering retained hidden runtime leaves:\n%s", ir)
	}
}

func TestSigjmpUsesSetjmpOnExplicitTarget(t *testing.T) {
	prog := ssatest.NewProgram(t, nil)
	prog.Target().Target = "esp32"
	pkg := prog.NewPackage("foo", "foo")

	fn := pkg.NewFunc("f", ssa.NoArgsNoRet, ssa.InGo)
	b := fn.MakeBody(1)
	jb := b.AllocaSigjmpBuf()
	zero := prog.IntVal(0, prog.CInt())
	one := prog.IntVal(1, prog.CInt())
	_ = b.Sigsetjmp(jb, zero)
	b.Siglongjmp(jb, one)
	b.Return()
	b.EndBuild()

	ir := pkg.Module().String()
	if !strings.Contains(ir, "@setjmp") || !strings.Contains(ir, "@longjmp") {
		t.Fatalf("expected setjmp/longjmp fallback on explicit target, got:\n%s", ir)
	}
}

func TestTypedProcessControlIR(t *testing.T) {
	prog := ssatest.NewProgram(t, nil)
	pkg := prog.NewPackage("foo", "foo")

	process := pkg.NewFunc("process", ssa.NoArgsNoRet, ssa.InGo)
	b := process.MakeBody(1)
	pid := b.ControlFork()
	path := prog.Zero(prog.CStr())
	argv := prog.Zero(prog.Pointer(prog.CStr()))
	_ = pid
	_ = b.ControlExecve(path, argv, argv)
	b.Return()
	b.EndBuild()

	exit := pkg.NewFunc("exitNow", ssa.NoArgsNoRet, ssa.InGo)
	b = exit.MakeBody(1)
	b.ControlExit(prog.IntVal(2, prog.CInt()))
	b.Unreachable()
	b.EndBuild()

	trap := pkg.NewFunc("trapNow", ssa.NoArgsNoRet, ssa.InGo)
	b = trap.MakeBody(1)
	b.ControlTrap()
	b.Unreachable()
	b.EndBuild()

	ir := pkg.Module().String()
	for _, symbol := range []string{"@fork", "@execve", "@exit", "@llvm.trap"} {
		if !strings.Contains(ir, symbol) {
			t.Fatalf("typed control IR omitted %s:\n%s", symbol, ir)
		}
	}
	if !strings.Contains(ir, "noreturn") {
		t.Fatalf("typed terminal control IR omitted noreturn attribute:\n%s", ir)
	}
}

func TestDeferInLoopContiguousDrainerGeneration(t *testing.T) {
	prog := ssatest.NewProgram(t, nil)
	pkg := prog.NewPackage("foo", "foo")

	c1 := pkg.NewFunc("c1", ssa.NoArgsNoRet, ssa.InGo)
	b1 := c1.MakeBody(1)
	b1.Return()
	b1.EndBuild()

	c2 := pkg.NewFunc("c2", ssa.NoArgsNoRet, ssa.InGo)
	b2 := c2.MakeBody(1)
	b2.Return()
	b2.EndBuild()

	fn := pkg.NewFunc("main", ssa.NoArgsNoRet, ssa.InGo)
	b := fn.MakeBody(1)
	fn.SetRecover(fn.MakeBlock())
	b.Return()
	b.SetBlockEx(fn.Block(0), ssa.BeforeLast, true)

	// Two contiguous loop defers should share one drain-loop generation pass.
	b.Defer(ssa.DeferInLoop, c1.Expr, ssa.Builder.Call)
	b.Defer(ssa.DeferInLoop, c2.Expr, ssa.Builder.Call)
	// Non-loop defer resets loop drainer state while walking deferred stmts.
	b.Defer(ssa.DeferAlways, c1.Expr, ssa.Builder.Call)
	b.EndBuild()

	ir := pkg.Module().String()
	if !strings.Contains(ir, "FreeDeferNode") {
		t.Fatalf("expected defer node drain/free in IR, got:\n%s", ir)
	}
}
