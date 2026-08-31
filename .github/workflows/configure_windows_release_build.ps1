param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("386", "amd64", "arm64")]
    [string]$Arch,

    [Parameter(Mandatory = $true)]
    [string]$ToolchainRoot
)

$ErrorActionPreference = "Stop"

$toolchain = (Resolve-Path -LiteralPath $ToolchainRoot).Path
$bin = Join-Path $toolchain "bin"
$clang = Join-Path $bin "clang.exe"
$clangXX = Join-Path $bin "clang++.exe"
$llvmConfig = Join-Path $bin "llvm-config.exe"
foreach ($required in @($clang, $clangXX, $llvmConfig)) {
    if (-not (Test-Path -LiteralPath $required)) {
        throw "The Windows release toolchain is incomplete: $required was not found"
    }
}

# setup-deps supplies architecture-matched BDWGC, libffi, and the remaining
# runtime dependencies. Replace only its LLVM metadata so cgo links the host
# compiler against the exact LLVM payload shipped in the release archive.
$profilePC = Join-Path $env:RUNNER_TEMP "llgo-pkgconfig"
New-Item -ItemType Directory -Force $profilePC | Out-Null
$cflags = ((& $llvmConfig --cflags) -join " ").Trim().Replace('\', '/')
if ($LASTEXITCODE -ne 0) {
    throw "llvm-config failed to report C flags"
}
$ldflags = ((& $llvmConfig --link-shared --ldflags --libs all --system-libs) -join " ").Trim().Replace('\', '/')
if ($LASTEXITCODE -ne 0) {
    throw "llvm-config failed to report shared-library link flags"
}
@"
Name: LLVM 19
Description: Architecture-native LLVM 19 release payload
Version: $((& $llvmConfig --version).Trim())
Cflags: $cflags
Libs: $ldflags
"@ | Set-Content -Encoding ascii (Join-Path $profilePC "llvm-19.pc")

# The x86 lane uses an x64 MSYS2 installation only to provision dependencies;
# its target-local pkg-config wrapper is what keeps every linked library x86.
if ($Arch -eq "386") {
    $targetTools = $env:LLGO_MINGW_TARGET_TOOLS
    if (-not $targetTools -or -not (Test-Path (Join-Path $targetTools "pkg-config.cmd"))) {
        throw "The Windows 386 pkg-config profile is unavailable"
    }
    Add-Content -Encoding utf8 $env:GITHUB_ENV "PKG_CONFIG=$(Join-Path $targetTools 'pkg-config.cmd')"
    Add-Content -Encoding utf8 $env:GITHUB_PATH $targetTools
}

Add-Content -Encoding utf8 $env:GITHUB_ENV "CC=$clang"
Add-Content -Encoding utf8 $env:GITHUB_ENV "CXX=$clangXX"
Add-Content -Encoding utf8 $env:GITHUB_ENV "CGO_ENABLED=1"
Add-Content -Encoding utf8 $env:GITHUB_ENV "LLGO_GORELEASER_WINDOWS=1"
Add-Content -Encoding utf8 $env:GITHUB_PATH $bin
