//go:build windows && !llgo

package windowsdiagnostics

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPowerShellHelperParses(t *testing.T) {
	// Parse the production script on the real CI PowerShell. Do not execute its
	// registry actions or mistake syntax validation for a WER integration test.
	shell, err := exec.LookPath("pwsh")
	if err != nil {
		t.Fatalf("pwsh is required for Windows crash diagnostics: %v", err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "dev", "go_test_windows_wer.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLGO_WER_SCRIPT_TO_PARSE", script)
	cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", `
$tokens = $null
$parseErrors = $null
$null = [System.Management.Automation.Language.Parser]::ParseFile($env:LLGO_WER_SCRIPT_TO_PARSE, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) { $parseErrors | Out-String | Write-Error; exit 1 }
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("parse WER helper: %v\n%s", err, out)
	}
}
