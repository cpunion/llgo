package plan9asm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	llvm "github.com/xgo-dev/llvm"
	extplan9asm "github.com/xgo-dev/plan9asm"
)

func TestProveNoSuspendLeafDirectClosure(t *testing.T) {
	translation := parseNoSuspendTestModule(t, `
declare i64 @llvm.ctpop.i64(i64) #0

define i64 @"example.com/asm.helper"(i64 %value) {
entry:
  %result = call i64 @llvm.ctpop.i64(i64 %value)
  ret i64 %result
}

define i64 @"example.com/asm.Leaf"(i64 %value) {
entry:
  %result = call i64 @"example.com/asm.helper"(i64 %value)
  ret i64 %result
}

attributes #0 = { nocallback nofree nosync nounwind speculatable willreturn memory(none) }
`)
	proof, err := ProveNoSuspendLeaf(translation, "example.com/asm.Leaf")
	if err != nil {
		t.Fatal(err)
	}
	if proof.Symbol != "example.com/asm.Leaf" || proof.Signature == "" || len(proof.CallClosure) != 3 || len(proof.ClosureSHA256) != 64 {
		t.Fatalf("proof = %+v; want exact leaf/helper/intrinsic closure and SHA-256", proof)
	}
	for _, name := range []string{"example.com/asm.Leaf", "example.com/asm.helper", "llvm.ctpop.i64"} {
		if !containsString(proof.CallClosure, name) {
			t.Fatalf("proof closure %v lacks %q", proof.CallClosure, name)
		}
	}
}

func TestProveNoSuspendLeafFloatingNegation(t *testing.T) {
	translation := parseNoSuspendTestModule(t, `
define double @"example.com/asm.Leaf"(double %value) {
entry:
  %result = fneg double %value
  ret double %result
}
`)
	if _, err := ProveNoSuspendLeaf(translation, "example.com/asm.Leaf"); err != nil {
		t.Fatal(err)
	}
}

func TestProveNoSuspendLeafFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		ir   string
		want string
	}{
		{
			name: "external call",
			ir: `declare i64 @external(i64)
define i64 @"example.com/asm.Leaf"(i64 %value) {
entry:
  %result = call i64 @external(i64 %value)
  ret i64 %result
}`,
			want: "has no definition",
		},
		{
			name: "indirect call",
			ir: `define i64 @"example.com/asm.Leaf"(ptr %fn, i64 %value) {
entry:
  %result = call i64 %fn(i64 %value)
  ret i64 %result
}`,
			want: "indirect or inline-assembly",
		},
		{
			name: "unproved intrinsic",
			ir: `declare void @llvm.trap()
define i64 @"example.com/asm.Leaf"(i64 %value) {
entry:
  call void @llvm.trap()
  ret i64 %value
}`,
			want: "missing nofree attribute",
		},
		{
			name: "atomic synchronization",
			ir: `define i64 @"example.com/asm.Leaf"(ptr %value) {
entry:
  %result = atomicrmw add ptr %value, i64 1 seq_cst
  ret i64 %result
}`,
			want: "unsupported LLVM opcode",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			translation := parseNoSuspendTestModule(t, test.ir)
			if _, err := ProveNoSuspendLeaf(translation, "example.com/asm.Leaf"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ProveNoSuspendLeaf error = %v; want %q", err, test.want)
			}
		})
	}
}

func parseNoSuspendTestModule(t *testing.T, ir string) *ModuleTranslation {
	t.Helper()
	context := llvm.NewContext()
	path := filepath.Join(t.TempDir(), "nosuspend.ll")
	if err := os.WriteFile(path, []byte(ir), 0o644); err != nil {
		context.Dispose()
		t.Fatal(err)
	}
	buffer, err := llvm.NewMemoryBufferFromFile(path)
	if err != nil {
		context.Dispose()
		t.Fatal(err)
	}
	module, err := context.ParseIR(buffer)
	if err != nil {
		context.Dispose()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		module.Dispose()
		context.Dispose()
	})
	return &ModuleTranslation{
		Module: module,
		Signatures: map[string]extplan9asm.FuncSig{
			"example.com/asm.Leaf": {Name: "example.com/asm.Leaf"},
		},
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
