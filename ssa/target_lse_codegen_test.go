//go:build !llgo

package ssa

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

// Exercise backend selection as well as TargetSpec. The same sequentially
// consistent IR must retain its ordering while choosing the platform's legal
// atomic instructions; a feature string alone does not prove that happened.
func TestMacOSARM64LSEAtomicCodegen(t *testing.T) {
	const source = `
define i32 @add32(ptr %p, i32 %v) {
  %old = atomicrmw add ptr %p, i32 %v seq_cst, align 4
  ret i32 %old
}
define i64 @add64(ptr %p, i64 %v) {
  %old = atomicrmw add ptr %p, i64 %v seq_cst, align 8
  ret i64 %old
}
define { i64, i1 } @cas64(ptr %p, i64 %old, i64 %new) {
  %pair = cmpxchg ptr %p, i64 %old, i64 %new seq_cst seq_cst, align 8
  ret { i64, i1 } %pair
}
`
	for _, test := range []struct {
		name   string
		target Target
		lse    bool
	}{
		{"macOS_default", Target{GOOS: "darwin", GOARCH: "arm64"}, true},
		{"macOS_v8_0", Target{GOOS: "darwin", GOARCH: "arm64", GOARM64: "v8.0"}, true},
		{"linux_v8_0", Target{GOOS: "linux", GOARCH: "arm64", GOARM64: "v8.0"}, false},
		{"windows_v8_0", Target{GOOS: "windows", GOARCH: "arm64", GOARM64: "v8.0"}, false},
		{"linux_explicit_lse", Target{GOOS: "linux", GOARCH: "arm64", GOARM64: "v8.0,lse"}, true},
		{"named_macOS", Target{GOOS: "darwin", GOARCH: "arm64", Target: "custom"}, false},
		{"resolved_macOS", Target{GOOS: "darwin", GOARCH: "arm64", Resolved: &TargetSpec{Triple: "arm64-apple-macosx", CPU: "generic", Features: "+neon"}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog := NewProgram(&test.target)
			defer prog.Dispose()
			path := filepath.Join(t.TempDir(), "atomics.ll")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			buffer, err := llvm.NewMemoryBufferFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			module, err := prog.ctx.ParseIR(buffer) // ParseIR consumes buffer.
			if err != nil {
				t.Fatal(err)
			}
			defer module.Dispose()
			module.SetTarget(prog.TargetSpec().Triple)
			module.SetDataLayout(prog.DataLayout())
			output, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.AssemblyFile)
			if err != nil {
				t.Fatal(err)
			}
			defer output.Dispose()
			assembly := strings.ToLower(string(output.Bytes()))
			for _, instruction := range []string{"ldaddal", "casal"} {
				if strings.Contains(assembly, instruction) != test.lse {
					t.Fatalf("%s presence does not match LSE=%v:\n%s", instruction, test.lse, assembly)
				}
			}
			for _, instruction := range []string{"ldaxr", "stlxr"} {
				if strings.Contains(assembly, instruction) == test.lse {
					t.Fatalf("%s presence does not match LSE=%v:\n%s", instruction, test.lse, assembly)
				}
			}
		})
	}
}
