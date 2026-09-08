package ssa

import (
	"go/types"
	"testing"
)

func TestWasmMemProfileConfigAndClone(t *testing.T) {
	for _, arch := range []string{"wasm", "amd64"} {
		for _, roots := range []bool{false, true} {
			for _, enabled := range []bool{false, true} {
				os := "js"
				if arch == "amd64" {
					os = "linux"
				}
				p := NewProgram(&Target{GOOS: os, GOARCH: arch})
				p.EnableGCRoots(roots)
				p.EnableWasmMemoryProfiling(enabled)
				want := arch == "wasm" && roots && enabled
				if p.WasmMemoryProfilingEnabled() != want {
					t.Fatal("incorrect profile mode")
				}
				backend := p.NewBackendProgram()
				if backend.WasmMemoryProfilingEnabled() != want {
					t.Fatal("backend lost profile mode")
				}
				pkg := backend.NewPackage("runtime", PkgRuntime)
				g := pkg.NewVar("mode", types.NewPointer(types.Typ[types.Bool]), InGo)
				backend.InitWasmMemProfileEnabled(g)
				if !g.impl.IsGlobalConstant() || (g.impl.Initializer().ZExtValue() != 0) != want {
					t.Fatal("incorrect immutable profile flag")
				}
				backend.Dispose()
				p.Dispose()
			}
		}
	}
}
