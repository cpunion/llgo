//go:build !llgo

// Copyright 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by the Apache 2.0 license.

// These tests exercise shell control flow, not Windows exception handling or
// WER. The existing MinGW job remains the real-platform integration check.
package windowsdiagnostics

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeGo = `#!/usr/bin/env bash
set -eu
printf '%s\n' "$@" > "$MOCK_ARGS"
printf '%s\n' "${GOTRACEBACK:-}" > "$MOCK_TRACEBACK"
echo '=== RUN   TestDiagnosticFixture'
echo 'raw diagnostic stderr' >&2
exit "${MOCK_GO_EXIT:-0}"
`

const fakePowerShell = `#!/usr/bin/env bash
set -eu
while [[ $# -gt 0 ]]; do
  if [[ "$1" == -Mode ]]; then
    printf '%s\n' "$2" >> "$MOCK_MODES"
    case "$2" in
      Prepare) exit "${MOCK_PREPARE_EXIT:-0}" ;;
      Finish) exit "${MOCK_FINISH_EXIT:-0}" ;;
    esac
  fi
  shift
done
exit 99
`

type fixture struct {
	root, dir, shell string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is required for workflow script tests: %v", err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "runner with spaces")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"go": fakeGo, "pwsh": fakePowerShell} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for key, value := range map[string]string{
		"RUNNER_OS": "Windows", "RUNNER_ARCH": "X64", "LLGO_WINDOWS_ABI": "mingw",
		"RUNNER_TEMP": dir, "MOCK_ARGS": filepath.Join(dir, "args"),
		"MOCK_MODES": filepath.Join(dir, "modes"), "MOCK_TRACEBACK": filepath.Join(dir, "traceback"),
		"GITHUB_OUTPUT": filepath.Join(dir, "outputs"), "GOTRACEBACK": "original-parent-value",
		"MOCK_GO_EXIT": "0", "MOCK_PREPARE_EXIT": "0", "MOCK_FINISH_EXIT": "0",
	} {
		t.Setenv(key, value)
	}
	return fixture{root: root, dir: dir, shell: shell}
}

func (f fixture) run(t *testing.T, args ...string) (int, string) {
	t.Helper()
	args = append([]string{filepath.Join(f.root, "dev", "go_test_windows_diagnostics.sh")}, args...)
	cmd := exec.Command(f.shell, args...)
	cmd.Dir = f.root
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var e *exec.ExitError
	if errors.As(err, &e) {
		return e.ExitCode(), string(out)
	}
	t.Fatalf("run diagnostic script: %v\n%s", err, out)
	return 0, ""
}

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func coverageArgs() []string {
	return []string{"go", "test", "-ldflags=-linkmode=external -extldflags=-lsynchronization",
		"-timeout", "45m", "-coverprofile=coverage-test-go.txt", "-covermode=atomic", "./test/go"}
}

func TestSingleRunPreservesArgumentsAndEvidence(t *testing.T) {
	f := newFixture(t)
	if status, out := f.run(t, coverageArgs()...); status != 0 {
		t.Fatalf("exit %d: %s", status, out)
	}
	got := strings.Split(strings.TrimSpace(read(t, filepath.Join(f.dir, "args"))), "\n")
	want := append([]string{"test", "-v", "-count=1", "-o=" + filepath.Join(f.dir, "llgo-windows-test-go-diagnostics", "evidence", "go.test.exe")}, coverageArgs()[2:]...)
	// Git Bash normalizes separators on Windows; argument boundaries must stay.
	normalize := func(s string) string { return strings.ReplaceAll(s, "\\", "/") }
	if normalize(strings.Join(got, "\n")) != normalize(strings.Join(want, "\n")) {
		t.Fatalf("arguments = %q, want %q", got, want)
	}
	if got := read(t, filepath.Join(f.dir, "modes")); got != "Prepare\nFinish\n" {
		t.Fatalf("WER modes: %q", got)
	}
	if got := read(t, filepath.Join(f.dir, "traceback")); got != "wer\n" {
		t.Fatalf("child GOTRACEBACK = %q", got)
	}
	if got := os.Getenv("GOTRACEBACK"); got != "original-parent-value" {
		t.Fatalf("parent GOTRACEBACK changed: %q", got)
	}
	log := read(t, filepath.Join(f.dir, "llgo-windows-test-go-diagnostics", "evidence", "test.log"))
	if !strings.Contains(log, "=== RUN   TestDiagnosticFixture") || !strings.Contains(log, "raw diagnostic stderr") {
		t.Fatalf("incomplete raw log: %q", log)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "outputs")); !os.IsNotExist(err) {
		t.Fatalf("successful run signaled failure: %v", err)
	}
}

func TestFailureAndRestorationExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name, goExit, prepareExit, finishExit, modes string
		want                                         int
	}{
		{"test failure", "9", "0", "0", "Prepare\nFinish\n", 9},
		{"cleanup after success", "0", "0", "7", "Prepare\nFinish\n", 7},
		{"cleanup preserves test failure", "9", "0", "7", "Prepare\nFinish\n", 9},
		{"partial setup", "0", "5", "0", "Prepare\nFinish\n", 5},
		{"setup and cleanup failure", "0", "5", "7", "Prepare\nFinish\n", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			t.Setenv("MOCK_GO_EXIT", tc.goExit)
			t.Setenv("MOCK_PREPARE_EXIT", tc.prepareExit)
			t.Setenv("MOCK_FINISH_EXIT", tc.finishExit)
			if status, out := f.run(t, coverageArgs()...); status != tc.want {
				t.Fatalf("exit = %d, want %d: %s", status, tc.want, out)
			}
			if got := read(t, filepath.Join(f.dir, "modes")); got != tc.modes {
				t.Fatalf("modes = %q", got)
			}
			if got := read(t, filepath.Join(f.dir, "outputs")); got != "windows_test_go_failed=true\n" {
				t.Fatalf("failure output = %q", got)
			}
			if tc.prepareExit != "0" {
				if _, err := os.Stat(filepath.Join(f.dir, "args")); !os.IsNotExist(err) {
					t.Fatalf("test ran after failed setup: %v", err)
				}
			}
		})
	}
}

func TestRejectOtherScopes(t *testing.T) {
	for _, tc := range []struct {
		name, env, value string
		args             []string
	}{
		{"Linux", "RUNNER_OS", "Linux", coverageArgs()},
		{"MSVC", "LLGO_WINDOWS_ABI", "msvc", coverageArgs()},
		{"ARM64", "RUNNER_ARCH", "ARM64", coverageArgs()},
		{"other package", "", "", []string{"go", "test", "./cl"}},
		{"filtered tests", "", "", []string{"go", "test", "-run=TestOne", "./test/go"}},
		{"compile only", "", "", []string{"go", "test", "-c", "./test/go"}},
		{"missing timeout", "", "", []string{"go", "test", "./test/go", "-timeout"}},
		{"duplicate package", "", "", []string{"go", "test", "./test/go", "./test/go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
			}
			if status, out := f.run(t, tc.args...); status != 2 {
				t.Fatalf("exit %d: %s", status, out)
			}
			if _, err := os.Stat(filepath.Join(f.dir, "modes")); !os.IsNotExist(err) {
				t.Fatalf("WER changed for rejected command: %v", err)
			}
		})
	}
}

func TestLogCaptureFailureStillRestoresWER(t *testing.T) {
	for _, goExit := range []string{"0", "9"} {
		t.Run(goExit, func(t *testing.T) {
			f := newFixture(t)
			t.Setenv("MOCK_GO_EXIT", goExit)
			// Consume all output before failing, so the test process does not
			// acquire an unrelated broken-pipe status from this test fixture.
			tee := "#!/usr/bin/env bash\nwhile IFS= read -r line; do :; done\nexit 6\n"
			if err := os.WriteFile(filepath.Join(f.dir, "bin", "tee"), []byte(tee), 0755); err != nil {
				t.Fatal(err)
			}
			want := 6
			if goExit == "9" {
				want = 9
			}
			if status, out := f.run(t, coverageArgs()...); status != want {
				t.Fatalf("exit %d, want %d: %s", status, want, out)
			}
			if got := read(t, filepath.Join(f.dir, "modes")); got != "Prepare\nFinish\n" {
				t.Fatalf("WER modes: %q", got)
			}
		})
	}
}

func TestExistingEvidenceIsNotOverwritten(t *testing.T) {
	f := newFixture(t)
	if status, out := f.run(t, coverageArgs()...); status != 0 {
		t.Fatalf("first exit %d: %s", status, out)
	}
	if status, out := f.run(t, coverageArgs()...); status != 2 || !strings.Contains(out, "refusing to overwrite prior evidence") {
		t.Fatalf("existing evidence rejection: exit %d: %s", status, out)
	}
	if got := read(t, filepath.Join(f.dir, "modes")); got != "Prepare\nFinish\n" {
		t.Fatalf("second invocation changed WER: %q", got)
	}
}

func TestMissingPowerShellFailsBeforeChangingState(t *testing.T) {
	f := newFixture(t)
	// An empty PATH isolates the missing-tool case even when pwsh is installed
	// on the host. f.shell is absolute; all preceding checks are bash builtins.
	t.Setenv("PATH", t.TempDir())
	if status, out := f.run(t, coverageArgs()...); status != 2 || !strings.Contains(out, "pwsh is required") {
		t.Fatalf("missing-tool rejection: exit %d: %s", status, out)
	}
	for _, name := range []string{"modes", "args", "llgo-windows-test-go-diagnostics"} {
		if _, err := os.Stat(filepath.Join(f.dir, name)); !os.IsNotExist(err) {
			t.Fatalf("missing tool changed %s: %v", name, err)
		}
	}
}
