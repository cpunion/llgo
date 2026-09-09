//go:build !llgo

package ssa

import (
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestMaterializeWasmNoScanGlobals(t *testing.T) {
	for _, test := range []struct {
		name   string
		target *Target
		roots  bool
		want   string
	}{
		{"wasm-gc", &Target{GOOS: "js", GOARCH: "wasm"}, true, "llgo_gc_noscan"},
		{"wasm-nogc", &Target{GOOS: "js", GOARCH: "wasm"}, false, ""},
		{"native", &Target{GOOS: "linux", GOARCH: "amd64"}, true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog := NewProgram(test.target)
			defer prog.Dispose()
			prog.EnableGCRoots(test.roots)
			pkg := prog.NewPackage("p", "p")
			mod := pkg.Module()
			i32 := mod.Context().Int32Type()

			constant := llvm.AddGlobal(mod, i32, "p.constant")
			constant.SetInitializer(llvm.ConstInt(i32, 0x0100070a, false))
			constant.SetGlobalConstant(true)
			mutable := llvm.AddGlobal(mod, i32, "p.mutable")
			mutable.SetInitializer(llvm.ConstNull(i32))
			explicit := llvm.AddGlobal(mod, i32, "p.explicit")
			explicit.SetInitializer(llvm.ConstInt(i32, 1, false))
			explicit.SetGlobalConstant(true)
			explicit.SetSection("keep_this_section")

			pkg.MaterializePreserveSyms()
			if got := constant.Section(); got != test.want {
				t.Fatalf("constant section = %q, want %q", got, test.want)
			}
			if got := mutable.Section(); got != "" {
				t.Fatalf("mutable global moved to %q", got)
			}
			if got := explicit.Section(); got != "keep_this_section" {
				t.Fatalf("explicit section overwritten with %q", got)
			}
		})
	}
}
