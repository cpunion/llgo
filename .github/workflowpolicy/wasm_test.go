package workflowpolicy

import (
	"os"
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
