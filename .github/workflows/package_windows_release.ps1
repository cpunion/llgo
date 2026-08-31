param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("386", "amd64", "arm64")]
    [string]$Arch,

    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$LLGoExe,

    [Parameter(Mandatory = $true)]
    [string]$ToolchainRoot,

    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory
)

$ErrorActionPreference = "Stop"

$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$llgo = (Resolve-Path -LiteralPath $LLGoExe).Path
$toolchain = (Resolve-Path -LiteralPath $ToolchainRoot).Path
$readobj = Join-Path $toolchain "bin\llvm-readobj.exe"
if (-not (Test-Path -LiteralPath $readobj)) {
    throw "llvm-readobj.exe was not found in $toolchain"
}

$expectedMachine = switch ($Arch) {
    "386" { "IMAGE_FILE_MACHINE_I386" }
    "amd64" { "IMAGE_FILE_MACHINE_AMD64" }
    "arm64" { "IMAGE_FILE_MACHINE_ARM64" }
}
foreach ($binary in @($llgo, (Join-Path $toolchain "bin\clang.exe"))) {
    $headers = (& $readobj --file-headers $binary) -join "`n"
    if ($headers -notmatch [regex]::Escape($expectedMachine)) {
        throw "$binary is not a native windows/$Arch executable"
    }
}

$stageParent = Join-Path $env:RUNNER_TEMP "llgo-release-windows-$Arch"
if (Test-Path -LiteralPath $stageParent) {
    Remove-Item -LiteralPath $stageParent -Recurse -Force
}
$stage = Join-Path $stageParent "llgo"
$bin = Join-Path $stage "bin"
New-Item -ItemType Directory -Force $bin | Out-Null

foreach ($file in @("LICENSE", "README.md", "THIRD_PARTY_NOTICES.md")) {
    Copy-Item -LiteralPath (Join-Path $repositoryRoot $file) -Destination $stage
}
foreach ($directory in @("LICENSES", "runtime", "targets")) {
    Copy-Item -LiteralPath (Join-Path $repositoryRoot $directory) -Destination (Join-Path $stage $directory) -Recurse
}
Copy-Item -LiteralPath $llgo -Destination (Join-Path $bin "llgo.exe")
New-Item -ItemType Directory -Force (Join-Path $stage "crosscompile") | Out-Null
Copy-Item -LiteralPath $toolchain -Destination (Join-Path $stage "crosscompile\clang") -Recurse

# The Windows loader resolves LLGo's LLVM imports before main can adjust its
# child-process environment. Copy the transitive non-system DLL closure beside
# llgo.exe. The toolchain keeps its own copies beside clang.exe.
$toolchainBin = Join-Path $toolchain "bin"
$queue = [Collections.Generic.Queue[string]]::new()
$queue.Enqueue((Join-Path $bin "llgo.exe"))
$copied = @{}
$prohibited = '^(?i:msys-2\.0|cygwin1|libwinpthread[^\\/]*)\.dll$'
while ($queue.Count -ne 0) {
    $binary = $queue.Dequeue()
    $imports = (& $readobj --coff-imports $binary) -join "`n"
    foreach ($match in [regex]::Matches($imports, '(?m)^\s*Name:\s*(\S+\.dll)\s*$')) {
        $name = $match.Groups[1].Value
        if ($name -match $prohibited) {
            throw "$binary imports unsupported POSIX runtime $name"
        }
        if ($copied.ContainsKey($name)) {
            continue
        }
        $source = Join-Path $toolchainBin $name
        if (-not (Test-Path -LiteralPath $source)) {
            continue
        }
        $destination = Join-Path $bin $name
        Copy-Item -LiteralPath $source -Destination $destination
        $copied[$name] = $true
        $queue.Enqueue($destination)
    }
}
if ($copied.Count -eq 0) {
    throw "No packaged LLVM runtime DLLs were discovered for $llgo"
}

# Audit every executable component, not only clang.exe: Clang normally loads
# libclang-cpp, which in turn loads LLVM and the C++ runtime. A direct-import
# check on the driver alone would miss an accidental transitive POSIX ABI.
$packagedToolchainBin = Join-Path $stage "crosscompile\clang\bin"
$toolchainBinaries = Get-ChildItem -LiteralPath $packagedToolchainBin -File |
    Where-Object { $_.Extension -in @(".exe", ".dll") }
foreach ($item in $toolchainBinaries) {
    $binary = $item.FullName
    $imports = (& $readobj --coff-imports $binary) -join "`n"
    if ($imports -match '(?i:\b(?:msys-2\.0|cygwin1|libwinpthread[^\\/\s]*)\.dll\b)') {
        throw "$binary has an unsupported POSIX-runtime dependency"
    }
}

New-Item -ItemType Directory -Force $OutputDirectory | Out-Null
$archive = Join-Path $OutputDirectory "llgo$Version.windows-$Arch.zip"
if (Test-Path -LiteralPath $archive) {
    Remove-Item -LiteralPath $archive -Force
}
Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $archive -CompressionLevel Optimal
$hash = (Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant()
Write-Host "$hash  $([IO.Path]::GetFileName($archive))"
"archive=$archive" | Add-Content -Encoding utf8 $env:GITHUB_OUTPUT
