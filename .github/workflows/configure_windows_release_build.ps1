param(
    [Parameter(Mandatory = $true)]
    [string]$ToolchainRoot
)

$ErrorActionPreference = "Stop"

$toolchain = (Resolve-Path -LiteralPath $ToolchainRoot).Path
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path.Replace('\', '/')
$bin = Join-Path $toolchain "bin"
$clang = Join-Path $bin "clang.exe"
$clangXX = Join-Path $bin "clang++.exe"
$llvmConfig = Join-Path $bin "llvm-config.exe"
foreach ($required in @($clang, $clangXX, $llvmConfig)) {
    if (-not (Test-Path -LiteralPath $required)) {
        throw "The Windows release toolchain is incomplete: $required was not found"
    }
}

$cflagsOutput = & $llvmConfig --cflags
if ($LASTEXITCODE -ne 0) {
    throw "llvm-config failed to report C flags"
}
$cflags = (($cflagsOutput -join " ") -replace '\s+', ' ').Trim().Replace('\', '/')
$ldflagsOutput = & $llvmConfig --link-shared --ldflags --libs all --system-libs
if ($LASTEXITCODE -ne 0) {
    throw "llvm-config failed to report shared-library link flags"
}
$ldflags = (($ldflagsOutput -join " ") -replace '\s+', ' ').Trim().Replace('\', '/')

# The compiler host only links LLVM; target runtime libraries such as BDWGC
# and libffi are not dependencies of cmd/llgo. Provide the exact pkg-config
# protocol used by the LLVM Go bindings instead of installing an unrelated
# MSYS2 or vcpkg target environment into the release-build job.
$nativeTools = Join-Path $env:RUNNER_TEMP "llgo-release-tools"
New-Item -ItemType Directory -Force $nativeTools | Out-Null
$pkgConfig = Join-Path $nativeTools "pkg-config.cmd"
@"
@echo off
if /I "%~1"=="--cflags" (
  echo $cflags
  exit /b 0
)
if /I "%~1"=="--libs" (
  echo $ldflags
  exit /b 0
)
echo unsupported pkg-config request for the LLVM release build: %* 1>&2
exit /b 1
"@ | Set-Content -Encoding ascii $pkgConfig

Add-Content -Encoding utf8 $env:GITHUB_ENV "CC=$clang"
Add-Content -Encoding utf8 $env:GITHUB_ENV "CXX=$clangXX"
Add-Content -Encoding utf8 $env:GITHUB_ENV "CGO_ENABLED=1"
Add-Content -Encoding utf8 $env:GITHUB_ENV "LLGO_GORELEASER_WINDOWS=1"
Add-Content -Encoding utf8 $env:GITHUB_ENV "PKG_CONFIG=$pkgConfig"
# GoReleaser renders the existing Darwin/Linux sysroot environment templates
# before selecting this Windows build ID. Native Windows processes do not
# normally export PWD, so provide the repository root required by those
# otherwise-unused templates.
Add-Content -Encoding utf8 $env:GITHUB_ENV "PWD=$repositoryRoot"
Add-Content -Encoding utf8 $env:GITHUB_PATH $bin
