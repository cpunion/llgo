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

# The byollvm build tag deliberately omits the binding's platform pkg-config
# directives. Feed it the exact flags from the architecture-native LLVM while
# keeping target runtime dependencies such as BDWGC and libffi out of this
# host-compiler build.
Add-Content -Encoding utf8 $env:GITHUB_ENV "CC=$clang"
Add-Content -Encoding utf8 $env:GITHUB_ENV "CXX=$clangXX"
Add-Content -Encoding utf8 $env:GITHUB_ENV "CGO_ENABLED=1"
Add-Content -Encoding utf8 $env:GITHUB_ENV "CGO_CPPFLAGS=$cflags"
Add-Content -Encoding utf8 $env:GITHUB_ENV "CGO_LDFLAGS=$ldflags"
Add-Content -Encoding utf8 $env:GITHUB_ENV "LLGO_GORELEASER_WINDOWS=1"
# GoReleaser renders the existing Darwin/Linux sysroot environment templates
# before selecting this Windows build ID. Native Windows processes do not
# normally export PWD, so provide the repository root required by those
# otherwise-unused templates.
Add-Content -Encoding utf8 $env:GITHUB_ENV "PWD=$repositoryRoot"
Add-Content -Encoding utf8 $env:GITHUB_PATH $bin
