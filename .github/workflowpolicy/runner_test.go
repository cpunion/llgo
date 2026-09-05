package workflowpolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	directRunner = `${{ github.repository == 'xgo-dev/llgo' && fromJSON('["qiniu", "ubuntu-24.04"]') || 'ubuntu-24.04' }}`
	matrixRunner = `${{ github.repository == 'xgo-dev/llgo' && matrix.runner || matrix.os }}`
)

type workflowJob struct {
	RunsOn   any `yaml:"runs-on"`
	Strategy struct {
		Matrix struct {
			OS      []string    `yaml:"os"`
			Include []runnerRow `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
}

type runnerRow struct {
	OS     string   `yaml:"os"`
	Runner []string `yaml:"runner"`
}

// These are intentionally canonical expressions, not a general Actions
// interpreter. Checking the expression itself prevents a policy test from
// passing while the workflow still uses an unguarded self-hosted label.
func runnerKind(job workflowJob) (string, error) {
	expr, scalar := job.RunsOn.(string)
	if scalar && expr == directRunner {
		return "direct", nil
	}
	if scalar && expr == matrixRunner {
		return "matrix", nil
	}
	hasRunner := false
	for _, row := range job.Strategy.Matrix.Include {
		hasRunner = hasRunner || len(row.Runner) != 0
	}
	if hasRunner || strings.Contains(fmt.Sprint(job.RunsOn), "qiniu") || strings.Contains(expr, "matrix.runner") {
		return "", fmt.Errorf("self-hosted runner lacks the repository guard: %v", job.RunsOn)
	}
	return "", nil
}

func TestWorkflowRunnerPolicy(t *testing.T) {
	paths, err := filepath.Glob("../workflows/*.yml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("workflow discovery: paths=%v, err=%v", paths, err)
	}
	guarded, mac, windows := 0, false, false
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]workflowJob `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for id, job := range workflow.Jobs {
			kind, err := runnerKind(job)
			if err != nil {
				t.Errorf("%s/%s: %v", path, id, err)
				continue
			}
			if kind == "" {
				continue
			}
			guarded++
			if kind == "direct" {
				continue // Exact expression preserves Qiniu upstream and Ubuntu on forks.
			}
			rows := append([]runnerRow(nil), job.Strategy.Matrix.Include...)
			for _, os := range job.Strategy.Matrix.OS {
				rows = append(rows, runnerRow{OS: os})
			}
			if len(rows) == 0 {
				t.Errorf("%s/%s: guarded matrix has no OS rows", path, id)
			}
			for _, row := range rows {
				if !hostedOS(row.OS) {
					t.Errorf("%s/%s: fork fallback is not a GitHub hosted OS: %q", path, id, row.OS)
				}
				mac = mac || strings.HasPrefix(row.OS, "macos-")
				windows = windows || strings.HasPrefix(row.OS, "windows-")
				if len(row.Runner) != 0 && (!reflect.DeepEqual(row.Runner, []string{"qiniu", "ubuntu-24.04"}) || !strings.HasPrefix(row.OS, "ubuntu-")) {
					t.Errorf("%s/%s: unexpected upstream override for %q: %v", path, id, row.OS, row.Runner)
				}
			}
		}
	}
	if guarded == 0 || !mac || !windows {
		t.Fatalf("vacuous runner coverage: guarded jobs=%d macOS=%v Windows=%v", guarded, mac, windows)
	}
}

func hostedOS(name string) bool {
	return strings.HasPrefix(name, "ubuntu-") || strings.HasPrefix(name, "macos-") || strings.HasPrefix(name, "windows-")
}

func TestRunnerPolicyRejectsUnguardedAndInvertedSelectors(t *testing.T) {
	for _, runsOn := range []any{
		[]any{"qiniu", "ubuntu-24.04"},
		"qiniu",
		"${{ matrix.runner || matrix.os }}",
		strings.Replace(directRunner, " == ", " != ", 1),
		strings.Replace(matrixRunner, " == ", " != ", 1),
		strings.Replace(matrixRunner, "github.repository", "github.event.pull_request.head.repo.full_name", 1),
	} {
		if _, err := runnerKind(workflowJob{RunsOn: runsOn}); err == nil {
			t.Errorf("accepted unsafe selector %v", runsOn)
		}
	}
	job := workflowJob{RunsOn: "${{ matrix.os }}"}
	job.Strategy.Matrix.Include = []runnerRow{{OS: "ubuntu-latest", Runner: []string{"qiniu", "ubuntu-24.04"}}}
	if _, err := runnerKind(job); err == nil {
		t.Fatal("accepted an unused upstream runner override")
	}
}

func TestRunnerPolicyPreservesHostedOnlyJobs(t *testing.T) {
	for _, runsOn := range []any{nil, "ubuntu-latest", "macos-latest", "windows-2022", "${{ matrix.os }}"} {
		kind, err := runnerKind(workflowJob{RunsOn: runsOn})
		if err != nil || kind != "" {
			t.Errorf("hosted-only selector %v: kind=%q err=%v", runsOn, kind, err)
		}
	}
}
