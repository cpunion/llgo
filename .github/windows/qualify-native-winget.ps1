param(
  [ValidateSet('386', 'amd64', 'arm64')][string]$GoArch,
  [ValidateSet('MSVC', 'MinGW')][string]$Profile
)
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path 'qualification').Path
$manifest = Join-Path $root "candidates/manifests/x/XGo/LLGo/$Profile/1.0.9"
$installer = Join-Path $manifest "XGo.LLGo.$Profile.installer.yaml"
$contents = Get-Content -Raw $installer
$contents.Replace('https://github.com/xgo-dev/llgo/releases/download/v1.0.9/', 'http://127.0.0.1:8765/') |
  Set-Content -Encoding utf8 $installer
$server = Start-Process -FilePath (Get-Command python).Source -ArgumentList @('-m', 'http.server', '8765', '--bind', '127.0.0.1') `
  -WorkingDirectory (Join-Path $root 'native') -PassThru
$winget = (Get-Command winget.exe).Source
$architecture = @{ '386' = 'x86'; amd64 = 'x64'; arm64 = 'arm64' }[$GoArch]
$links = Join-Path $env:LOCALAPPDATA 'Microsoft/WinGet/Links'
$savedPath = $env:PATH
$installed = $false
function Invoke-WinGet([string[]]$Arguments) {
  & $winget @Arguments
  if ($LASTEXITCODE -ne 0) { throw "winget $Arguments failed: $LASTEXITCODE" }
}
try {
  $available = $false
  for ($attempt = 0; $attempt -lt 15; $attempt++) {
    try {
      $null = Invoke-WebRequest -Uri 'http://127.0.0.1:8765/' -UseBasicParsing
      $available = $true
      break
    } catch { Start-Sleep -Seconds 1 }
  }
  if (-not $available) { throw 'Local archive server did not start' }
  Invoke-WinGet @('--info')
  Invoke-WinGet @('settings', '--enable', 'LocalManifestFiles')
  Invoke-WinGet @('validate', '--manifest', $manifest)
  if (Get-Command llgo.exe -ErrorAction SilentlyContinue) { throw 'Unexpected preinstalled LLGo' }
  Invoke-WinGet @('install', '--manifest', $manifest, '--scope', 'user', '--architecture', $architecture,
    '--accept-package-agreements', '--accept-source-agreements', '--disable-interactivity')
  $installed = $true
  Remove-Item Env:LLGO_ROOT -ErrorAction SilentlyContinue
  $env:PATH = "$links;$env:SystemRoot\System32;$env:SystemRoot"
  $command = (Get-Command llgo.exe).Source
  if ($command -ne (Join-Path $links 'llgo.exe')) { throw "Unexpected portable alias: $command" }
  $version = (& $command version | Out-String).Trim()
  if ($LASTEXITCODE -ne 0 -or $version -ne "llgo v1.0.9 windows/$GoArch") {
    throw "Installed compiler failed on a clean PATH: $version"
  }
  Write-Host "WINGET_NATIVE_STARTUP_OK $Profile windows/$GoArch $version"
} finally {
  $env:PATH = $savedPath
  try {
    if ($installed) {
      # A locally installed manifest need not correlate to the public catalog
      # ID (notably when that catalog no longer contains the x86 installer).
      Invoke-WinGet @('uninstall', '--name', "LLGo ($Profile)", '--exact', '--silent',
        '--accept-source-agreements', '--disable-interactivity')
    }
  } finally { Stop-Process -Id $server.Id -ErrorAction SilentlyContinue }
}
if (Test-Path (Join-Path $links 'llgo.exe')) { throw 'Portable alias remains after uninstall' }
Write-Host "WINGET_NATIVE_LIFECYCLE_OK $Profile windows/$GoArch 1.0.9"
