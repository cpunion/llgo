param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("386", "amd64", "arm64")]
    [string]$Arch,

    [Parameter(Mandatory = $true)]
    [ValidateSet("msvc", "mingw")]
    [string]$Profile,

    [Parameter(Mandatory = $true)]
    [string]$ReleaseRoot
)

$ErrorActionPreference = "Stop"

$release = (Resolve-Path -LiteralPath $ReleaseRoot).Path
$llgo = Join-Path $release "bin\llgo.exe"
$clang = Join-Path $release "crosscompile\clang\bin\clang.exe"
$readobj = Join-Path $release "crosscompile\clang\bin\llvm-readobj.exe"
foreach ($required in @($llgo, $clang, $readobj)) {
    if (-not (Test-Path -LiteralPath $required)) {
        throw "The extracted release is incomplete: $required was not found"
    }
}
$resolved = (Get-Command llgo.exe -ErrorAction Stop).Source
if (-not $resolved.Equals($llgo, [StringComparison]::OrdinalIgnoreCase)) {
    throw "PATH resolved llgo.exe to $resolved instead of the release artifact $llgo"
}
$resolvedClang = (Get-Command clang.exe -ErrorAction Stop).Source
if (-not $resolvedClang.Equals($clang, [StringComparison]::OrdinalIgnoreCase)) {
    throw "PATH resolved clang.exe to $resolvedClang instead of the release artifact $clang"
}

$source = Join-Path $env:RUNNER_TEMP "llgo-release-smoke-$Profile-$Arch"
if (Test-Path -LiteralPath $source) {
    Remove-Item -LiteralPath $source -Recurse -Force
}
New-Item -ItemType Directory -Force $source | Out-Null
@"
module example.com/llgo-release-smoke

go 1.27
"@ | Set-Content -Encoding ascii (Join-Path $source "go.mod")
@"
package main

import "fmt"

func main() {
	fmt.Println("hello from llgo windows/$Arch $Profile")
}
"@ | Set-Content -Encoding ascii (Join-Path $source "main.go")

$output = Join-Path $source "hello.exe"
Push-Location $source
try {
    & $llgo build -o $output .
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $output)) {
        throw "The release LLGo compiler failed to build the native smoke test"
    }
} finally {
    Pop-Location
}
$actualOutput = (& $output) -join "`n"
if ($LASTEXITCODE -ne 0 -or $actualOutput.Trim() -ne "hello from llgo windows/$Arch $Profile") {
    throw "The native smoke test failed: $actualOutput"
}

$expectedMachine = switch ($Arch) {
    "386" { "IMAGE_FILE_MACHINE_I386" }
    "amd64" { "IMAGE_FILE_MACHINE_AMD64" }
    "arm64" { "IMAGE_FILE_MACHINE_ARM64" }
}
$headers = (& $readobj --file-headers $output) -join "`n"
if ($headers -notmatch [regex]::Escape($expectedMachine)) {
    throw "The $Profile smoke executable is not native windows/$Arch"
}
$imports = (& $readobj --coff-imports $output) -join "`n"
if ($imports -match '(?i:\b(?:msys-2\.0|cygwin1|libwinpthread[^\\/\s]*)\.dll\b)') {
    throw "The $Profile smoke executable has an unsupported POSIX-runtime dependency"
}

Write-Host "Qualified the windows/$Arch $Profile release artifact"
