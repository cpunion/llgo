//go:build !llgo

package ssa_test

import (
	"strings"
	"testing"

	"github.com/goplus/llgo/ssa"
	"github.com/goplus/llgo/ssa/ssatest"
)

func TestWasmImportAttributes(t *testing.T) {
	prog := ssatest.NewProgram(t, nil)
	prog.SetWasmImport("foo.fdRead", "wasi_snapshot_preview1", "fd_read")
	module, name, ok := prog.WasmImport("foo.fdRead")
	if !ok || module != "wasi_snapshot_preview1" || name != "fd_read" {
		t.Fatalf("WasmImport = (%q, %q, %v)", module, name, ok)
	}
	if _, _, ok := prog.WasmImport("foo.missing"); ok {
		t.Fatal("missing function has a wasm import")
	}

	pkg := prog.NewPackage("foo", "foo")
	fn := pkg.NewFunc("fdRead", ssa.NoArgsNoRet, ssa.InGo)
	fn.SetWasmImport("wasi_snapshot_preview1", "fd_read")

	ir := pkg.Module().String()
	for _, want := range []string{
		`"wasm-import-module"="wasi_snapshot_preview1"`,
		`"wasm-import-name"="fd_read"`,
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("missing %s in wasm import IR:\n%s", want, ir)
		}
	}
}
