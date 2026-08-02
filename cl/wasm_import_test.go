//go:build !llgo

package cl_test

import (
	"strings"
	"testing"

	"github.com/goplus/llgo/cl/cltest"
	llssa "github.com/goplus/llgo/ssa"
)

func TestWasmImportDirective(t *testing.T) {
	const src = `package foo

//go:wasmimport wasi_snapshot_preview1 fd_read
func fdRead(fd int32, buf *byte, size uint32) uint32

func read(buf *byte) uint32 {
	return fdRead(0, buf, 1)
}
`
	ir := cltest.CompileIREx(t, src, "foo.go", false, func(prog llssa.Program) {
		prog.Target().GOOS = "wasip1"
		prog.Target().GOARCH = "wasm"
	})
	for _, want := range []string{
		`"wasm-import-module"="wasi_snapshot_preview1"`,
		`"wasm-import-name"="fd_read"`,
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("missing %s in wasm import IR:\n%s", want, ir)
		}
	}
}

func TestWasmImportDirectiveIgnoredForDefinition(t *testing.T) {
	const src = `package foo

//go:wasmimport gojs runtime.nanotime1
func nanotime1() int64 {
	return 1
}
`
	ir := cltest.CompileIREx(t, src, "foo.go", false, func(prog llssa.Program) {
		prog.Target().GOOS = "js"
		prog.Target().GOARCH = "wasm"
	})
	for _, unwanted := range []string{
		`"wasm-import-module"="gojs"`,
		`"wasm-import-name"="runtime.nanotime1"`,
	} {
		if strings.Contains(ir, unwanted) {
			t.Fatalf("unexpected %s on WebAssembly function definition:\n%s", unwanted, ir)
		}
	}
}
