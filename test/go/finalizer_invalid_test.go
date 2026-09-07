//go:build !wasm

package gotest

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

const finalizerInvalidCaseEnv = "LLGO_TEST_FINALIZER_INVALID_CASE"

func TestRuntimeSetFinalizerRejectsInvalidArgumentTypes(t *testing.T) {
	if name := os.Getenv(finalizerInvalidCaseEnv); name != "" {
		p := &finalizerAssignableValue{keep: new(int)}
		switch name {
		case "non-function":
			runtime.SetFinalizer(p, 1)
		case "no parameters":
			runtime.SetFinalizer(p, func() {})
		case "two parameters":
			runtime.SetFinalizer(p, func(*finalizerAssignableValue, int) {})
		case "variadic":
			runtime.SetFinalizer(p, func(...*finalizerAssignableValue) {})
		case "wrong type":
			runtime.SetFinalizer(p, func(*int) {})
		default:
			panic("unknown invalid finalizer case: " + name)
		}
		os.Exit(0)
	}

	for _, name := range []string{"non-function", "no parameters", "two parameters", "variadic", "wrong type"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeSetFinalizerRejectsInvalidArgumentTypes$")
			cmd.Env = append(os.Environ(), finalizerInvalidCaseEnv+"="+name)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("SetFinalizer accepted invalid %s", name)
			}
			if !strings.Contains(string(out), "runtime.SetFinalizer:") {
				t.Fatalf("SetFinalizer error for %s:\n%s", name, out)
			}
		})
	}
}
