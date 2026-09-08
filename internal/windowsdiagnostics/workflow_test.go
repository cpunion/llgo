//go:build !llgo

package windowsdiagnostics

import (
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestWorkflowDiagnosticScope(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				ID, Name, Run, If, Uses string
				With                    map[string]string
			}
		}
	}
	data := read(t, filepath.Join("..", "..", ".github", "workflows", "go.yml"))
	if err := yaml.Unmarshal([]byte(data), &workflow); err != nil {
		t.Fatal(err)
	}
	var coverage, restore, upload bool
	for jobName, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "bash dev/go_test_windows_diagnostics.sh") {
				if jobName != "test" || step.ID != "test_coverage" {
					t.Fatal("diagnostics escaped the existing coverage step")
				}
				guard := `if [[ "$RUNNER_OS" == Windows && "${{ matrix.windows_abi }}" == mingw ]]; then`
				if !strings.Contains(step.Run, guard) || !strings.Contains(step.Run, "else\n  \"${go_test[@]}\" -timeout 45m") {
					t.Fatal("non-MinGW execution is no longer preserved")
				}
				coverage = true
			}
			if step.ID == "restore_test_go_wer" {
				if !strings.HasPrefix(step.If, "always()") || !strings.Contains(step.If, "matrix.windows_abi == 'mingw'") {
					t.Fatalf("restoration scope: %s", step.If)
				}
				restore = true
			}
			if step.Name == "Upload Windows test-go failure evidence" {
				for _, want := range []string{"always()", "runner.os == 'Windows'", "matrix.windows_abi == 'mingw'",
					"steps.test_coverage.outputs.windows_test_go_failed == 'true'", "steps.restore_test_go_wer.outcome == 'failure'"} {
					if !strings.Contains(step.If, want) {
						t.Fatalf("artifact condition lacks %s", want)
					}
				}
				if step.With["retention-days"] != "3" || step.With["if-no-files-found"] != "error" || !strings.HasSuffix(step.With["path"], "/evidence/") {
					t.Fatal("artifact retention or private-state boundary changed")
				}
				upload = true
			}
		}
	}
	if !coverage || !restore || !upload {
		t.Fatalf("incomplete workflow: coverage=%v restore=%v upload=%v", coverage, restore, upload)
	}
}

func TestWERRestorationSourceContract(t *testing.T) {
	// Static safety checks complement the Windows parser test. They do not
	// claim that registry operations or dump generation ran on this host.
	source := read(t, filepath.Join("..", "..", "dev", "go_test_windows_wer.ps1"))
	prepareStart := strings.Index(source, "if ($Mode -eq 'Prepare')")
	finishStart := strings.Index(source, "# Finish is idempotent")
	if prepareStart < 0 || finishStart <= prepareStart {
		t.Fatal("cannot identify preparation/restoration routes")
	}
	prepare, finish := source[prepareStart:finishStart], source[finishStart:]
	for _, want := range []string{
		"Invoke-Registry -RegistryArguments @('export', $nativeKey, $backupPath, '/y')",
		"$addedValues = @($addedValues | Where-Object { $_ -notin $oldNames })",
		"if (-not $existed) { New-Item -Path $key -Force | Out-Null }",
		"-Name DumpType -Value 1", "-Name DumpCount -Value 1",
	} {
		if !strings.Contains(prepare, want) {
			t.Fatalf("preparation safety contract missing %q", want)
		}
	}
	if strings.Index(prepare, "@('export'") > strings.Index(prepare, "Set-Content -LiteralPath $statePath") ||
		strings.Index(prepare, "Set-Content -LiteralPath $statePath") > strings.Index(prepare, "New-ItemProperty") {
		t.Fatal("backup and recovery state must precede the first mutation")
	}
	existingStart := strings.Index(finish, "if ($state.Existed) {")
	createdStart := strings.Index(finish, "} elseif (Test-Path -LiteralPath $key) {")
	if existingStart < 0 || createdStart <= existingStart {
		t.Fatal("missing distinct existing/new-key restoration routes")
	}
	existing := finish[existingStart:createdStart]
	if strings.Contains(existing, "Remove-Item -LiteralPath") || strings.Contains(existing, "New-Item -Path") {
		t.Fatal("pre-existing key must remain in place to preserve ACLs")
	}
	for _, want := range []string{
		"if (-not (Test-Path -LiteralPath $key)) { throw",
		"foreach ($name in $state.AddedValues)",
		"Remove-ItemProperty -LiteralPath $key -Name $name",
		"Invoke-Registry -RegistryArguments @('import', $backupPath)",
	} {
		if !strings.Contains(existing, want) {
			t.Fatalf("existing-key restoration lacks %q", want)
		}
	}
	if strings.Count(source, "Remove-Item -LiteralPath $key -Recurse -Force") != 1 ||
		!strings.HasPrefix(finish[createdStart:], "} elseif (Test-Path -LiteralPath $key) {\n      Remove-Item -LiteralPath $key -Recurse -Force") {
		t.Fatal("only a newly created exact key may be removed")
	}
	if !strings.Contains(finish, "Sort-Object LastWriteTimeUtc -Descending | Select-Object -First 1") ||
		!strings.Contains(source, "Join-Path $Directory 'wer-before.reg'") {
		t.Fatal("dump count or private registry backup boundary changed")
	}
}
