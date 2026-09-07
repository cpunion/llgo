package ffi

import (
	"go/build"
	"testing"
)

func TestWasmBackendSelection(t *testing.T) {
	for _, test := range []struct {
		name string
		goos string
		want string
	}{
		{name: "Go-compatible JavaScript", goos: "js", want: "link_flags_wasm_emscripten.go"},
		{name: "Go-compatible WASI", goos: "wasip1", want: "link_flags_wasm.go"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = test.goos
			ctx.GOARCH = "wasm"
			pkg, err := ctx.ImportDir(".", 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range pkg.GoFiles {
				if name == test.want {
					return
				}
			}
			t.Fatalf("GoFiles = %v, want %s", pkg.GoFiles, test.want)
		})
	}
}
