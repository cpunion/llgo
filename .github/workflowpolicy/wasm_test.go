package workflowpolicy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestWasmGJSComparisonUsesCheckoutRuntime(t *testing.T) {
	data, err := os.ReadFile("../workflows/wasm-acceptance.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Env   map[string]string
			Steps []struct {
				Run string
				Env map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	job, ok := workflow.Jobs["goroot-smoke"]
	if !ok {
		t.Fatal("missing WebAssembly startup sentinels")
	}
	comparisons := 0
	for _, step := range job.Steps {
		if !strings.Contains(step.Run, `"$RUNNER_TEMP/llgo" test`) {
			continue
		}
		comparisons++
		root := job.Env["LLGO_ROOT"]
		if override, ok := step.Env["LLGO_ROOT"]; ok {
			root = override
		}
		if root != "${{ github.workspace }}" {
			t.Fatalf("raw GJS comparison must use the checkout runtime, got LLGO_ROOT=%q", root)
		}
	}
	if comparisons != 1 {
		t.Fatalf("expected one raw GJS compiler comparison, got %d", comparisons)
	}
}

func TestWasmFailureStatusProbeIsMandatory(t *testing.T) {
	data, err := os.ReadFile("../workflows/wasm-acceptance.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run, If string
				Env     map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, step := range workflow.Jobs["goroot-smoke"].Steps {
		if !strings.Contains(step.Run, "bash dev/test_wasm_exit_status.sh") {
			continue
		}
		if step.If != "" || step.Env["PROFILE"] != "${{ matrix.profile }}" {
			t.Fatal("exit status probe is not mandatory on every profile")
		}
		for _, name := range []string{"EC32)", "EC64)", "WC32)", "GJS)", "GWASI)"} {
			if !strings.Contains(step.Run, name) {
				t.Fatalf("exit status probe has no mapping for %s", name)
			}
		}
		return
	}
	t.Fatal("missing wasm failed-test exit status probe")
}

func TestWasmFailureStatusProbeRejectsFalsePositives(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("GNU timeout unavailable")
	}
	script, err := filepath.Abs("../../dev/test_wasm_exit_status.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		accept     bool
	}{
		{"failed test", "printf '%s\\n' 'intentional wasm exit-status probe' '--- FAIL: TestIntentionalHostExitFailure' 'FAIL'\nexit 1\n", true},
		{"false success", "printf '%s\\n' 'intentional wasm exit-status probe' '--- FAIL: TestIntentionalHostExitFailure' 'FAIL'\nexit 0\n", false},
		{"compiler error", "echo 'compiler failed'\nexit 1\n", false},
		{"deadline", "exit 124\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fake := filepath.Join(dir, "llgo")
			// WASI does not inherit arbitrary host environment variables. The
			// opt-in must reach the guest as a test-binary argument.
			const checkArgs = "[[ \" $* \" == *\" -args -llgo.intentional-exit-failure \"* ]] || exit 2\n"
			if err := os.WriteFile(fake, []byte("#!/usr/bin/env bash\n"+checkArgs+tc.body), 0755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bash, script, filepath.Join(dir, "probe.log"))
			cmd.Env = append(os.Environ(), "LLGO="+fake)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.accept {
				t.Fatalf("accepted=%v, want %v: %v\n%s", err == nil, tc.accept, err, out)
			}
		})
	}
}
