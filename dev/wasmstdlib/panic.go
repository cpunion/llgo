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

// Reuse the ordinary test build: wasm guests cannot spawn the child processes
// needed by fatal-path tests, but the host can execute that exact binary again.
func fullChildCommand(p profile, root, goRoot, artifact, arg string) command {
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

func fullPanicCommand(p profile, root, goRoot, artifact string) command {
	return fullChildCommand(p, root, goRoot, artifact, "-llgo.caller-panic-child")
}

var fullFinalizerInvalidCases = []string{"non-function", "no parameters", "two parameters", "variadic", "wrong type"}

func fullFinalizerInvalidCommand(p profile, root, goRoot, artifact, name string) command {
	return fullChildCommand(p, root, goRoot, artifact, "-llgo.finalizer-invalid-case="+name)
}

func fullBuiltinPrintCommand(p profile, root, goRoot, artifact string) command {
	return fullChildCommand(p, root, goRoot, artifact, "-llgo.builtin-print-child")
}

func fullGoexitLifecycleCommand(p profile, root, goRoot, artifact string) command {
	return fullChildCommand(p, root, goRoot, artifact, "-llgo.main-goexit-lifecycle-child")
}

const fullBuiltinPrintWant = "" +
	"1e+07\n" +
	"(1e+07-1e+07i)\n" +
	"(1.5+0i)\n" +
	"NaN\n" +
	"+Inf\n" +
	"-Inf\n" +
	"(1+NaNi)\n" +
	"(1+Infi)\n" +
	"(1-Infi)\n"

func validateFullBuiltinPrint(out []byte, runErr error) error {
	if runErr != nil {
		return fmt.Errorf("builtin-print child failed: %w", runErr)
	}
	got := strings.ReplaceAll(string(out), "\r\n", "\n")
	if got != fullBuiltinPrintWant {
		return fmt.Errorf("builtin-print output = %q, want %q", got, fullBuiltinPrintWant)
	}
	return nil
}

func validateFullGoexitLifecycle(out []byte, runErr error) error {
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || exit.ExitCode() < 1 || exit.ExitCode() > 2 {
		return fmt.Errorf("Goexit lifecycle child must exit 1 or 2, not succeed, time out, or fail to launch: %v", runErr)
	}
	worker := strings.Index(string(out), "WORKER_RETURNING")
	deadlock := strings.Index(string(out), "no goroutines (main called runtime.Goexit) - deadlock!")
	if worker < 0 || deadlock < 0 || worker > deadlock {
		return errors.New("Goexit lifecycle child did not release its worker before reporting the last-goroutine deadlock")
	}
	return nil
}

func validateFullFinalizerInvalid(out []byte, runErr error, name string) error {
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || exit.ExitCode() < 1 || exit.ExitCode() > 2 {
		return fmt.Errorf("invalid-finalizer child must exit 1 or 2, not succeed, time out, or fail to launch: %v", runErr)
	}
	want := "runtime.SetFinalizer:"
	switch name {
	case "non-function":
		want += " second argument is"
	case "variadic":
		want += " cannot pass"
		if !strings.Contains(string(out), "because dotdotdot") {
			return fmt.Errorf("invalid-finalizer %s diagnostic is missing %q", name, "because dotdotdot")
		}
	case "no parameters", "two parameters", "wrong type":
		want += " cannot pass"
	default:
		return fmt.Errorf("unknown invalid-finalizer case %q", name)
	}
	if !strings.Contains(string(out), want) {
		return fmt.Errorf("invalid-finalizer %s diagnostic is missing %q", name, want)
	}
	return nil
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
