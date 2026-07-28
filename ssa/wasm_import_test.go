//go:build !llgo

package ssa_test

import (
	"strings"
	"testing"

	"github.com/goplus/llgo/ssa"
	"github.com/goplus/llgo/ssa/ssatest"
	"github.com/xgo-dev/llvm"
)

func TestWasmImportAttributes(t *testing.T) {
	prog := ssatest.NewProgram(t, &ssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	pkg := prog.NewPackage("foo", "foo")
	fn := pkg.NewFunc("fdRead", ssa.NoArgsNoRet, ssa.InGo)
	fn.SetWasmImport("wasi_snapshot_preview1", "fd_read")
	caller := pkg.NewFunc("use", ssa.NoArgsNoRet, ssa.InGo)
	body := caller.MakeBody(1)
	body.Call(fn.Expr)
	body.Return()

	ir := pkg.Module().String()
	for _, want := range []string{
		`"wasm-import-module"="wasi_snapshot_preview1"`,
		`"wasm-import-name"="fd_read"`,
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("missing %s in wasm import IR:\n%s", want, ir)
		}
	}
	object, err := prog.TargetMachine().EmitToMemoryBuffer(pkg.Module(), llvm.ObjectFile)
	if err != nil {
		t.Fatalf("emit wasm import object: %v\n%s", err, ir)
	}
	defer object.Dispose()
	for _, want := range []string{"wasi_snapshot_preview1", "fd_read"} {
		if !strings.Contains(string(object.Bytes()), want) {
			t.Fatalf("wasm import object does not retain %q", want)
		}
	}
}
