param(
  [Parameter(Mandatory = $true)]
  [ValidateSet('msvc', 'mingw')][string]$Profile,
  [Parameter(Mandatory = $true)]
  [ValidateSet('amd64', 'arm64', '386')][string]$GoArch,
  [Parameter(Mandatory = $true)][string]$HostCompilerDirectory
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'release-lib.ps1')
$root = (Get-Location).Path
$metadata = Get-Content -Raw '.windows-dist/metadata.json' | ConvertFrom-Json
$commit = (& git rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $metadata.commit -ne $commit) {
  throw 'The GoReleaser metadata does not describe this checkout'
}
if ($metadata.version -notmatch '^[0-9A-Za-z][0-9A-Za-z.+-]*$') {
  throw "Invalid release version: $($metadata.version)"
}
$llvmVersion = '22.1.8'
$buildTime = ([DateTimeOffset]$metadata.date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
Assert-ReleaseCompilerArtifact -Root $HostCompilerDirectory -Metadata $metadata -GoArch $GoArch -LLVMVersion $llvmVersion
$stage = Join-Path $env:RUNNER_TEMP "llgo-release-$GoArch-$Profile"
if (Test-Path $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
New-Item -ItemType Directory -Force $stage | Out-Null
Copy-Item -LiteralPath (Join-Path $HostCompilerDirectory 'bin') -Destination $stage -Recurse
$bin = Join-Path $stage 'bin'
$executable = Join-Path $bin 'llgo.exe'
foreach ($entry in @('LICENSE', 'LICENSES', 'README.md', 'WINDOWS.md', 'THIRD_PARTY_NOTICES.md', 'runtime', 'targets')) {
  Copy-Item -LiteralPath (Join-Path $root $entry) -Destination $stage -Recurse
}
Copy-Item -LiteralPath (Join-Path $HostCompilerDirectory 'licenses') `
  -Destination (Join-Path $stage 'LICENSES/windows-runtime') -Recurse

# Inspect using CI-only tools from the host artifact; no ESP SDK is published.
$readObj = Join-Path $HostCompilerDirectory 'inspection/llvm-readobj.exe'
if (-not (Test-Path $readObj)) { throw 'The compiler artifact is missing its inspection tool' }
Copy-ReleaseDLLs -ReadObj $readObj -Executable $executable -GoArch $GoArch -Profile 'msvc' -CheckOnly
@{
  version = $metadata.version
  commit = $commit
  date = $buildTime
  goos = 'windows'
  goarch = $GoArch
  abi = $Profile
  llvm_version = $llvmVersion
  compiler_sha256 = (Get-FileHash -Algorithm SHA256 $executable).Hash.ToLowerInvariant()
  distribution = 'native'
} | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $stage 'release.json')

# Directory records must be STORE for WinGet compatibility.
$zipName = "llgo$($metadata.version).windows-$GoArch-$Profile.zip"
$zip = Join-Path $root ".windows-dist/$zipName"
& python (Join-Path $PSScriptRoot 'release_zip.py') create $zip $stage
if ($LASTEXITCODE -ne 0) { throw 'Creating or verifying the Windows ZIP failed' }
$zipChecksum = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLowerInvariant()
"$zipChecksum  $zipName" | Set-Content -Encoding ascii "$zip.sha256"
Write-Host "Created $zip"
