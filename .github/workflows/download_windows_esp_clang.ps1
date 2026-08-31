param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("386", "amd64", "arm64")]
    [string]$Arch,

    [Parameter(Mandatory = $true)]
    [string]$Repository,

    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$SHA256,

    [Parameter(Mandatory = $true)]
    [string]$Destination
)

$ErrorActionPreference = "Stop"

$triple = switch ($Arch) {
    "386" { "i686-w64-mingw32" }
    "amd64" { "x86_64-w64-mingw32" }
    "arm64" { "aarch64-w64-mingw32" }
}
$asset = "clang-esp-$Version-$triple.tar.xz"
$archive = Join-Path $env:RUNNER_TEMP $asset
$url = "https://github.com/$Repository/releases/download/$Version/$asset"

& curl.exe --fail --location --retry 5 --retry-all-errors --output $archive $url
if ($LASTEXITCODE -ne 0) {
    throw "Downloading $url failed with exit code $LASTEXITCODE"
}
$actual = (Get-FileHash -Algorithm SHA256 $archive).Hash
if (-not $actual.Equals($SHA256, [StringComparison]::OrdinalIgnoreCase)) {
    throw "$asset SHA-256 is $actual, want $SHA256"
}

if (Test-Path -LiteralPath $Destination) {
    Remove-Item -LiteralPath $Destination -Recurse -Force
}
New-Item -ItemType Directory -Force $Destination | Out-Null
& tar.exe -xJf $archive -C $Destination --strip-components=1
if ($LASTEXITCODE -ne 0) {
    throw "Extracting $asset failed with exit code $LASTEXITCODE"
}

$clang = Join-Path $Destination "bin\clang.exe"
$llvmConfig = Join-Path $Destination "bin\llvm-config.exe"
$llvmReadobj = Join-Path $Destination "bin\llvm-readobj.exe"
$llvmDLL = Join-Path $Destination "bin\libLLVM-19.dll"
foreach ($required in @($clang, $llvmConfig, $llvmReadobj, $llvmDLL, (Join-Path $Destination "include\llvm-c\Core.h"))) {
    if (-not (Test-Path -LiteralPath $required)) {
        throw "The Windows ESP LLVM payload is incomplete: $required was not found"
    }
}

$expectedMachine = switch ($Arch) {
    "386" { "IMAGE_FILE_MACHINE_I386" }
    "amd64" { "IMAGE_FILE_MACHINE_AMD64" }
    "arm64" { "IMAGE_FILE_MACHINE_ARM64" }
}
$headers = (& $llvmReadobj --file-headers $clang) -join "`n"
if ($headers -notmatch [regex]::Escape($expectedMachine)) {
    throw "$asset contains a Clang executable for the wrong architecture"
}

$reportedVersion = (& $llvmConfig --version).Trim()
if ($reportedVersion -notlike "19.1.2*") {
    throw "$llvmConfig reports LLVM $reportedVersion, want 19.1.2.x"
}
$targets = ((& $llvmConfig --targets-built) -join " ").Trim()
foreach ($target in @("X86", "ARM", "AArch64", "AVR", "Mips", "RISCV", "WebAssembly", "Xtensa")) {
    if (($targets -split '\s+') -notcontains $target) {
        throw "$llvmConfig does not provide the required $target backend: $targets"
    }
}

"ESP_CLANG_ROOT=$Destination" | Add-Content -Encoding utf8 $env:GITHUB_ENV
"$($Destination.TrimEnd('\'))\bin" | Add-Content -Encoding utf8 $env:GITHUB_PATH
Write-Host "Installed $asset at $Destination"
