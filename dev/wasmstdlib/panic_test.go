package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFullPanicCommandProfiles(t *testing.T) {
	for _, name := range []string{"EC32", "EC64", "WC32", "GJS", "GWASI", "GJS-reference", "GWASI-reference"} {
		p, err := fullProfile(name)
		if err != nil {
			t.Fatal(err)
		}
		cmd := fullPanicCommand(p, "/repo", "/goroot", "/compiled-test")
		if cmd.Program != "timeout" || cmd.Args[1] != "30s" || !slices.Contains(cmd.Args, "/compiled-test") || cmd.Args[len(cmd.Args)-1] != "-llgo.caller-panic-child" {
			t.Fatalf("%s did not reuse and bound the child binary: %+v", name, cmd)
		}
		joined := strings.Join(cmd.Args, " ")
		switch {
		case p.Reference || name == "GWASI":
			if !strings.Contains(joined, "go_"+p.GOOS+"_wasm_exec") || cmd.Env["GOMAXPROCS"] != "1" {
				t.Fatalf("%s must reuse the Go host helper: %+v", name, cmd)
			}
		case name == "WC32":
			if !slices.Contains(cmd.Args, "wasmtime") || !slices.Contains(cmd.Args, "exceptions=y") {
				t.Fatalf("missing WC32 runner: %+v", cmd)
			}
		default:
			if !slices.Contains(cmd.Args, "node") || !strings.Contains(joined, "emscripten") {
				t.Fatalf("missing JS runner: %+v", cmd)
			}
		}
	}
}

func TestFullFinalizerInvalidCommandProfiles(t *testing.T) {
	for _, profileName := range []string{"EC32", "EC64", "WC32", "GJS", "GWASI", "GJS-reference", "GWASI-reference"} {
		p, err := fullProfile(profileName)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range fullFinalizerInvalidCases {
			cmd := fullFinalizerInvalidCommand(p, "/repo", "/goroot", "/compiled-test", name)
			if !slices.Contains(cmd.Args, "/compiled-test") || cmd.Args[len(cmd.Args)-1] != "-llgo.finalizer-invalid-case="+name {
				t.Fatalf("%s %s did not reuse the child binary: %+v", profileName, name, cmd)
			}
		}
	}
}

func TestFullBuiltinPrintCommandProfiles(t *testing.T) {
	for _, profileName := range []string{"EC32", "EC64", "WC32", "GJS", "GWASI", "GJS-reference", "GWASI-reference"} {
		p, err := fullProfile(profileName)
		if err != nil {
			t.Fatal(err)
		}
		cmd := fullBuiltinPrintCommand(p, "/repo", "/goroot", "/compiled-test")
		if !slices.Contains(cmd.Args, "/compiled-test") || cmd.Args[len(cmd.Args)-1] != "-llgo.builtin-print-child" {
			t.Fatalf("%s did not reuse the child binary: %+v", profileName, cmd)
		}
	}
}

func TestFullPanicExitHelper(t *testing.T) {
	if os.Getenv("LLGO_FULL_PANIC_EXIT_HELPER") == "1" {
		os.Exit(2)
	}
}

func TestFullPanicValidationRejectsFalsePositives(t *testing.T) {
	exitErr := fullPanicTestExit(t)
	root := t.TempDir()
	dir := filepath.Join(root, "test", "go")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "caller_runtime_test.go"), []byte("panic() // PANIC_MARK\ncall() // PANIC_CALLER_MARK\n"), 0644); err != nil {
		t.Fatal(err)
	}
	const good = "panic: acceptance-boom\ngoroutine 1 [running]:\ncallerPanicBoom\ncaller_runtime_test.go:1\ncallerPanicCaller\ncaller_runtime_test.go:2\n"
	for _, tc := range []struct {
		name string
		out  string
		err  error
		want bool
	}{
		{"real panic", good, exitErr, true},
		{"successful child", good, nil, false},
		{"runner missing", good, os.ErrNotExist, false},
		{"unrelated crash", "memory access out of bounds", exitErr, false},
		{"wrong source line", strings.ReplaceAll(good, "go:2", "go:20"), exitErr, false},
		{"missing frame", strings.ReplaceAll(good, "callerPanicCaller", "unrelated"), exitErr, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateFullPanic(root, []byte(tc.out), tc.err) == nil; got != tc.want {
				t.Fatalf("accepted=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestFullFinalizerInvalidValidationRejectsFalsePositives(t *testing.T) {
	exitErr := fullPanicTestExit(t)
	for _, tc := range []struct {
		name string
		out  string
		err  error
		want bool
	}{
		{"non-function", "fatal error: runtime.SetFinalizer: second argument is int, not a function", exitErr, true},
		{"variadic", "fatal error: runtime.SetFinalizer: cannot pass *gotest.value to finalizer func(...*gotest.value) because dotdotdot", exitErr, true},
		{"wrong type", "fatal error: runtime.SetFinalizer: cannot pass *gotest.value to finalizer func(*int)", exitErr, true},
		{"successful child", "runtime.SetFinalizer: cannot pass", nil, false},
		{"unrelated crash", "memory access out of bounds", exitErr, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.name
			if name == "successful child" || name == "unrelated crash" {
				name = "wrong type"
			}
			if got := validateFullFinalizerInvalid([]byte(tc.out), tc.err, name) == nil; got != tc.want {
				t.Fatalf("accepted=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestFullBuiltinPrintValidationRejectsFalsePositives(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
		want bool
	}{
		{"exact", fullBuiltinPrintWant, nil, true},
		{"crlf", strings.ReplaceAll(fullBuiltinPrintWant, "\n", "\r\n"), nil, true},
		{"wrong exponent", strings.Replace(fullBuiltinPrintWant, "1e+07", "1e+007", 1), nil, false},
		{"runner noise", fullBuiltinPrintWant + "PASS\n", nil, false},
		{"failed", fullBuiltinPrintWant, os.ErrNotExist, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateFullBuiltinPrint([]byte(tc.out), tc.err) == nil; got != tc.want {
				t.Fatalf("accepted=%v, want %v", got, tc.want)
			}
		})
	}
}

func fullPanicTestExit(t *testing.T) error {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestFullPanicExitHelper$")
	child.Env = append(os.Environ(), "LLGO_FULL_PANIC_EXIT_HELPER=1")
	exitErr := child.Run()
	var exit *exec.ExitError
	if !errors.As(exitErr, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("child did not return a real exit status: %v", exitErr)
	}
	return exitErr
}

func TestFullPanicIsReportedAndReusesBuild(t *testing.T) {
	exitErr := fullPanicTestExit(t)
	for _, tc := range []struct {
		name                    string
		parentFails, childFails bool
	}{
		{"both pass", false, false},
		{"child failure", false, true},
		{"parent failure", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "test", "go")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			const source = "package gotest\nfunc TestWitness(t *T) {}\nfunc boom() {} // PANIC_MARK\nfunc caller() {} // PANIC_CALLER_MARK\n"
			if err := os.WriteFile(filepath.Join(dir, "caller_runtime_test.go"), []byte(source), 0644); err != nil {
				t.Fatal(err)
			}
			structured := func(_ string, cmd command) ([]byte, error) {
				if cmd.Args[0] == "env" {
					return []byte("/goroot"), nil
				}
				return json.Marshal(selectedPackage{Dir: dir, TestGoFiles: []string{"caller_runtime_test.go"}})
			}
			builds, children := 0, 0
			var artifact string
			run := func(_ string, cmd command) ([]byte, error) {
				if slices.Contains(cmd.Args, "./test/go") {
					builds++
					idx := slices.Index(cmd.Args, "-o")
					if idx < 0 {
						t.Fatal("normal test did not retain its binary")
					}
					artifact = cmd.Args[idx+1]
					if tc.parentFails {
						return []byte("FAIL\n"), exitErr
					}
					return []byte("--- PASS: TestWitness (0.00s)\nPASS\n"), nil
				}
				children++
				if artifact == "" || !slices.Contains(cmd.Args, artifact) {
					t.Fatal("child did not reuse the normal test binary")
				}
				if tc.childFails {
					return nil, os.ErrNotExist
				}
				if cmd.Args[len(cmd.Args)-1] == "-llgo.caller-panic-child" {
					return []byte("panic: acceptance-boom\ngoroutine 1 [running]:\ncallerPanicBoom\ncaller_runtime_test.go:3\ncallerPanicCaller\ncaller_runtime_test.go:4\n"), exitErr
				}
				out := "fatal error: runtime.SetFinalizer: cannot pass *gotest.value to finalizer func(*int)"
				if strings.Contains(cmd.Args[len(cmd.Args)-1], "non-function") {
					out = "fatal error: runtime.SetFinalizer: second argument is int, not a function"
				} else if strings.Contains(cmd.Args[len(cmd.Args)-1], "variadic") {
					out += " because dotdotdot"
				}
				return []byte(out), exitErr
			}
			reportPath := filepath.Join(root, "report.json")
			err := runFullAt(root, "WC32", reportPath, "go", "llgo", 0, 1, structured, run)
			if (err != nil) != (tc.parentFails || tc.childFails) {
				t.Fatalf("full audit result: %v", err)
			}
			if builds != 1 || children != 1+len(fullFinalizerInvalidCases) {
				t.Fatalf("builds/children = %d/%d", builds, children)
			}
			data, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatal(err)
			}
			var report struct{ Packages []fullPackage }
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Packages) != 1 || len(report.Packages[0].HostChecks) != 1+len(fullFinalizerInvalidCases) {
				t.Fatalf("missing host check: %s", data)
			}
			if (report.Packages[0].HostChecks[0].Status == "fail") != tc.childFails {
				t.Fatalf("wrong child status: %s", data)
			}
			if _, err := os.Stat(filepath.Dir(artifact)); !os.IsNotExist(err) {
				t.Fatalf("temporary binary directory retained: %v", err)
			}
		})
	}
}

func TestFullBuiltinPrintIsReportedAndReusesBuild(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte("package test\nfunc TestWitness(t *T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	structured := func(_ string, cmd command) ([]byte, error) {
		if cmd.Args[0] == "env" {
			return []byte("/goroot"), nil
		}
		return json.Marshal(selectedPackage{Dir: dir, TestGoFiles: []string{"main_test.go"}})
	}
	builds, children := 0, 0
	var artifact string
	run := func(_ string, cmd command) ([]byte, error) {
		if slices.Contains(cmd.Args, "./test") {
			builds++
			idx := slices.Index(cmd.Args, "-o")
			if idx < 0 {
				t.Fatal("normal test did not retain its binary")
			}
			artifact = cmd.Args[idx+1]
			return []byte("--- PASS: TestWitness (0.00s)\nPASS\n"), nil
		}
		children++
		if artifact == "" || !slices.Contains(cmd.Args, artifact) || cmd.Args[len(cmd.Args)-1] != "-llgo.builtin-print-child" {
			t.Fatalf("builtin-print child did not reuse the normal test binary: %+v", cmd)
		}
		return []byte(fullBuiltinPrintWant), nil
	}
	reportPath := filepath.Join(root, "report.json")
	if err := runFullAt(root, "EC32", reportPath, "go", "llgo", 0, 1, structured, run); err != nil {
		t.Fatal(err)
	}
	if builds != 1 || children != 1 {
		t.Fatalf("builds/children = %d/%d", builds, children)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report struct{ Packages []fullPackage }
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Packages) != 1 || len(report.Packages[0].HostChecks) != 1 || report.Packages[0].HostChecks[0].Status != "pass" {
		t.Fatalf("missing builtin-print host check: %s", data)
	}
	if _, err := os.Stat(filepath.Dir(artifact)); !os.IsNotExist(err) {
		t.Fatalf("temporary binary directory retained: %v", err)
	}
}
