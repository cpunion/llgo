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
$ErrorActionPreference = 'Stop'
$tokens = $null
$parseErrors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile($env:LLGO_WER_SCRIPT_TO_PARSE, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) { $parseErrors | Out-String | Write-Error; exit 1 }
# Exercise only the metadata function, never the registry operations.
$helper = $ast.Find({ param($node) $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Read-NativeMetadata' }, $true)
if ($null -eq $helper) { throw 'metadata helper is missing' }
. ([scriptblock]::Create($helper.Extent.Text))
$result = Read-NativeMetadata -Executable $env:ComSpec -NativeArguments @('/d', '/c', 'echo fixture-ok') | Out-String
if ($result -notmatch 'fixture-ok' -or $result -match 'probe failed') { throw "successful probe: $result" }
$result = Read-NativeMetadata -Executable $env:ComSpec -NativeArguments @('/d', '/c', 'exit /b 7') | Out-String
if ($result -notmatch 'Metadata probe failed' -or $result -notmatch 'exit code 7') { throw "failed probe: $result" }
$result = Read-NativeMetadata -Executable '__llgo_missing_metadata_probe__' -NativeArguments @() | Out-String
if ($result -notmatch 'Metadata probe failed') { throw "probe exception: $result" }
exit 0
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("parse WER helper: %v\n%s", err, out)
	}
}
