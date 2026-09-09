package build

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestTraceR4MainModule(t *testing.T) {
	t.Setenv("LLGO_R4_LLVM_TRACE", "")
	if traceR4MainModule("absent", "main", llvm.Module{}) {
		t.Fatal("enabled diagnostics without an output directory")
	}
	dir := t.TempDir()
	t.Setenv("LLGO_R4_LLVM_TRACE", dir)
	if traceR4MainModule("absent", "runtime", llvm.Module{}) {
		t.Fatal("captured a dependency package")
	}
	if !traceR4MainModule("enter", "main", llvm.Module{}) {
		t.Fatal("missing frontend phase")
	}
	mod := parseWasmCallerIR(t, "define void @f() { ret void }")
	if !traceR4MainModule("input", "command-line-arguments", mod) {
		t.Fatal("missing command-line package")
	}
	data, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(os.Getpid()), "input.bc"))
	if err != nil || len(data) < 4 || string(data[:4]) != "BC\xc0\xde" {
		t.Fatalf("missing bitcode input: %v", err)
	}
}
