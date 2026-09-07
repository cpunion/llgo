package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Reuse the ordinary test build: wasm guests cannot spawn the child needed by
// TestCallerPanicTraceback, but the host can execute that exact binary twice.
func fullPanicCommand(p profile, root, goRoot, artifact string) command {
	const arg = "-llgo.caller-panic-child"
	var args []string
	env := map[string]string{"GOMAXPROCS": "1"}
	switch {
	case p.Reference || p.Name == "GWASI":
		args = []string{filepath.Join(goRoot, "lib", "wasm", "go_"+p.GOOS+"_wasm_exec"), artifact, arg}
		env["GOWASIRUNTIME"] = "wasmtime"
		if !p.Reference {
			env["GOWASIRUNTIMEARGS"] = "-W exceptions=y -W multi-memory=y"
		}
	case p.Target == "wasi":
		args = []string{"wasmtime", "run", "-W", "exceptions=y", "--dir=" + root, artifact, arg}
	default:
		runner := "emscripten-runner.mjs"
		if p.Target == "emscripten-memory64" {
			runner = "emscripten-memory64-runner.mjs"
		}
		args = []string{"node", filepath.Join(root, "targets", runner), artifact, arg}
	}
	return command{"timeout", append([]string{"--kill-after=10s", "30s"}, args...), env}
}

func validateFullPanic(root string, out []byte, runErr error) error {
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || exit.ExitCode() < 1 || exit.ExitCode() > 2 {
		return fmt.Errorf("panic child must exit 1 or 2, not succeed, time out, or fail to launch: %v", runErr)
	}
	source, err := os.ReadFile(filepath.Join(root, "test", "go", "caller_runtime_test.go"))
	if err != nil {
		return err
	}
	want := []string{"panic: acceptance-boom", "goroutine 1 [running]:", "callerPanicBoom", "callerPanicCaller"}
	for _, marker := range []string{"PANIC_MARK", "PANIC_CALLER_MARK"} {
		line := 0
		for i, text := range strings.Split(string(source), "\n") {
			if strings.HasSuffix(strings.TrimSpace(text), "// "+marker) {
				line = i + 1
				break
			}
		}
		if line == 0 {
			return fmt.Errorf("missing panic source marker %s", marker)
		}
		location := fmt.Sprintf("caller_runtime_test.go:%d", line)
		if !regexp.MustCompile(regexp.QuoteMeta(location) + `(?:\D|$)`).Match(out) {
			return fmt.Errorf("panic child traceback missing exact source location %q", location)
		}
	}
	for _, text := range want {
		if !strings.Contains(string(out), text) {
			return fmt.Errorf("panic child traceback missing %q", text)
		}
	}
	return nil
}
