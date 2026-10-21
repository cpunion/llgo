param(
  [ValidateSet('386', 'amd64', 'arm64')][string]$GoArch,
  [ValidateSet('MSVC', 'MinGW')][string]$Profile
)
$ErrorActionPreference = 'Stop'
$identifier = "XGo.LLGo.$Profile"
$version = '1.0.8'
$architecture = @{ '386' = 'x86'; amd64 = 'x64'; arm64 = 'arm64' }[$GoArch]
$manifest = Join-Path $PSScriptRoot "winget-candidates/manifests/x/XGo/LLGo/$Profile/$version"
$winget = (Get-Command winget.exe).Source
$originalPath = $env:PATH
function Invoke-WinGet([string[]]$Arguments) {
  & $winget @Arguments
  if ($LASTEXITCODE -ne 0) { throw "winget $Arguments failed: $LASTEXITCODE" }
}
function Refresh-Path {
  $links = Join-Path $env:LOCALAPPDATA 'Microsoft/WinGet/Links'
  $env:PATH = "$links;$originalPath;" + [Environment]::GetEnvironmentVariable('PATH', 'User')
}
function Check-Compiler([string]$ExpectedVersion) {
  Refresh-Path
  $compiler = (Get-Command llgo.exe).Source
  $output = (& $compiler version | Out-String).Trim()
  if ($LASTEXITCODE -ne 0 -or $output -ne "llgo v$ExpectedVersion windows/$GoArch") {
    throw "Installed compiler returned: $output"
  }
  Write-Host "Installed command: $compiler"
  Write-Host $output
  return $compiler
}
Invoke-WinGet @('--info')
Invoke-WinGet @('settings', '--enable', 'LocalManifestFiles')
Invoke-WinGet @('validate', '--manifest', $manifest)
Remove-Item Env:LLGO_ROOT -ErrorAction SilentlyContinue
if (Get-Command llgo.exe -ErrorAction SilentlyContinue) { throw 'Unexpected preinstalled llgo command' }
$installed = $false
try {
  if ($Profile -eq 'MSVC' -and $GoArch -ne '386') {
    # Start from the real published source entry. x86 has no 1.0.6 package.
    Invoke-WinGet @('install', '--id', $identifier, '--exact', '--version', '1.0.6',
      '--architecture', $architecture, '--source', 'winget', '--accept-package-agreements',
      '--accept-source-agreements', '--disable-interactivity')
    $installed = $true
    $null = Check-Compiler '1.0.6'
    Invoke-WinGet @('upgrade', '--manifest', $manifest, '--architecture', $architecture,
      '--accept-package-agreements', '--accept-source-agreements', '--disable-interactivity')
  } else {
    Invoke-WinGet @('install', '--manifest', $manifest, '--architecture', $architecture,
      '--accept-package-agreements', '--accept-source-agreements', '--disable-interactivity')
    $installed = $true
  }
  Invoke-WinGet @('list', '--name', "LLGo ($Profile)", '--exact', '--accept-source-agreements', '--disable-interactivity')
  $compiler = Check-Compiler $version
  $smoke = Join-Path $env:RUNNER_TEMP "winget-hello-$GoArch-$Profile"
  New-Item -ItemType Directory -Force $smoke | Out-Null
  Set-Content -Encoding utf8 (Join-Path $smoke 'main.go') 'package main; import "fmt"; func main() { fmt.Println("winget hello") }'
  Push-Location $smoke
  try {
    & $compiler build -o hello.exe main.go
    if ($LASTEXITCODE -ne 0) { throw 'Installed compiler could not build hello' }
    $output = (& (Join-Path $smoke 'hello.exe') | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $output -ne 'winget hello') { throw "Hello failed: $output" }
  } finally { Pop-Location }
} finally {
  if ($installed) {
    Invoke-WinGet @('uninstall', '--name', "LLGo ($Profile)", '--exact', '--silent', '--disable-interactivity')
  }
}
Refresh-Path
if (Get-Command llgo.exe -ErrorAction SilentlyContinue) { throw 'The llgo command remains after uninstall' }
Write-Host "WINGET_LIFECYCLE_OK $identifier windows/$GoArch $version"
