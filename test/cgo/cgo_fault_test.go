//go:build llgo && !wasm

package cgo

import (
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const cFaultTracebackChild = "LLGO_TEST_C_FAULT_TRACEBACK"

func init() {
	if os.Getenv(cFaultTracebackChild) == "1" {
		cFaultViaGo()
	}
}

//go:noinline
func cFaultViaGo() {
	CauseFault(2)
}

func recoverCFault(captureStack bool) (recovered any, stack string) {
	defer func() {
		recovered = recover()
		if captureStack {
			stack = string(debug.Stack())
		}
	}()
	cFaultViaGo()
	return nil, ""
}

func TestCFaultRecoverable(t *testing.T) {
	for i := 0; i < 3; i++ {
		recovered, stack := recoverCFault(i == 2)
		err, ok := recovered.(error)
		if !ok || err.Error() != "runtime error: invalid memory address or nil pointer dereference" {
			t.Fatalf("fault %d recovered %T %v", i+1, recovered, recovered)
		}
		if i == 2 {
			if !strings.Contains(stack, "cFaultViaGo") {
				t.Fatalf("recovered stack is missing Go fault frame:\n%s", stack)
			}
			if runtime.GOOS == "darwin" && !strings.Contains(stack, "llgo_test_fault") {
				t.Fatalf("recovered stack is missing C fault frame:\n%s", stack)
			}
		}
	}
}

func TestCFaultTraceback(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), cFaultTracebackChild+"=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("unrecovered fault child unexpectedly succeeded:\n%s", output)
	}
	for _, want := range []string{
		"panic: runtime error: invalid memory address or nil pointer dereference",
		"goroutine 1 [running]:",
		"cFaultViaGo",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("fault traceback is missing %q:\n%s", want, output)
		}
	}
	if runtime.GOOS == "darwin" && !strings.Contains(string(output), "llgo_test_fault") {
		t.Fatalf("fault traceback is missing C fault frame:\n%s", output)
	}
}
