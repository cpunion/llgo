param(
  [ValidateSet('386', 'amd64', 'arm64')][string]$GoArch,
  [switch]$Child,
  [string]$Root,
  [string]$HostArch
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

if ($Child) {
  Start-Transcript -Path (Join-Path $Root 'standard-user.log') -Force | Out-Null
  $principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
  if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'The installation probe must run as a standard user'
  }
  Write-Host "Standard user: $env:USERNAME"
  $bootstrap = Join-Path $Root 'bootstrap'
  $dependencies = @(Get-ChildItem (Join-Path $bootstrap "dependencies/$HostArch") -Recurse -File |
    Where-Object { $_.Extension -in @('.appx', '.msix') })
  Add-AppxPackage -Path (Join-Path $bootstrap 'Microsoft.DesktopAppInstaller_8wekyb3d8bbwe.msixbundle') -DependencyPath $dependencies.FullName
  # Match Repair-WinGetPackageManager for a newly created account. Local
  # manifest installation needs no catalog, but uninstall correlation does.
  if (-not (Get-AppxPackage -Name Microsoft.Winget.Source)) {
    $source = Join-Path $Root 'source2.msix'
    & curl.exe --fail --location --retry 3 --output $source 'https://cdn.winget.microsoft.com/cache/source2.msix'
    if ($LASTEXITCODE -ne 0) { throw 'Downloading the official WinGet source failed' }
    Add-AppxPackage -Path $source
  }
  $winget = Join-Path $env:LOCALAPPDATA 'Microsoft/WindowsApps/winget.exe'
  function Invoke-WinGet([string[]]$Arguments) {
    & $winget @Arguments
    if ($LASTEXITCODE -ne 0) { throw "winget $Arguments failed: $LASTEXITCODE" }
  }
  $architecture = @{ '386' = 'x86'; amd64 = 'x64'; arm64 = 'arm64' }[$GoArch]
  Invoke-WinGet @('--info')
  Invoke-WinGet @('validate', '--manifest', (Join-Path $Root 'manifest'))
  $installed = $false
  try {
    Invoke-WinGet @('install', '--manifest', (Join-Path $Root 'manifest'), '--scope', 'user',
      '--architecture', $architecture, '--accept-package-agreements', '--accept-source-agreements', '--disable-interactivity')
    $installed = $true
    Remove-Item Env:LLGO_ROOT -ErrorAction SilentlyContinue
    $env:PATH = "$env:SystemRoot\System32;$env:SystemRoot;" + [Environment]::GetEnvironmentVariable('PATH', 'User')
    Set-Location $Root
    $compiler = (Get-Command llgo.exe).Source
    Write-Host "Clean PATH compiler: $compiler"
    $output = (& $compiler version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $output -ne "llgo v1.0.8 windows/$GoArch") {
      throw "Clean PATH compiler failed ($LASTEXITCODE): $output"
    }
    Write-Host $output
  } finally {
    if ($installed) {
      Invoke-WinGet @('uninstall', '--name', 'LLGo (MSVC)', '--exact', '--source', 'winget',
        '--accept-source-agreements', '--silent', '--disable-interactivity')
    }
  }
  $env:PATH = "$env:SystemRoot\System32;$env:SystemRoot;" + [Environment]::GetEnvironmentVariable('PATH', 'User')
  if (Get-Command llgo.exe -ErrorAction SilentlyContinue) { throw 'llgo remains after uninstall' }
  Write-Host "WINGET_STANDARD_USER_OK MSVC windows/$GoArch"
  Stop-Transcript | Out-Null
  exit 0
}

$Root = Join-Path $env:PUBLIC 'llgo-winget-standard'
New-Item -ItemType Directory -Force $Root | Out-Null
Copy-Item -Recurse (Join-Path $env:RUNNER_TEMP 'winget-bootstrap') (Join-Path $Root 'bootstrap')
Copy-Item -Recurse (Join-Path $PSScriptRoot 'winget-candidates/manifests/x/XGo/LLGo/MSVC/1.0.8') (Join-Path $Root 'manifest')
Copy-Item $PSCommandPath (Join-Path $Root 'probe.ps1')
& icacls.exe $Root /grant '*S-1-5-32-545:(OI)(CI)M' /T /Q
if ($LASTEXITCODE -ne 0) { throw 'Could not grant the test user access to the fixture' }
& winget settings --enable LocalManifestFiles
if ($LASTEXITCODE -ne 0) { throw 'Could not enable local manifest validation' }
# The admin setting belongs to the invoking account. Use WinGet's documented
# machine policy so that the separate standard account can use local manifests.
$policy = 'HKLM:\Software\Policies\Microsoft\Windows\AppInstaller'
New-Item -Path $policy -Force | Out-Null
New-ItemProperty -Path $policy -Name EnableLocalManifestFiles -PropertyType DWord -Value 1 -Force | Out-Null

$user = 'llgowgtest'
$password = ConvertTo-SecureString ([Guid]::NewGuid().ToString('N') + 'aA1!') -AsPlainText -Force
$account = New-LocalUser -Name $user -Password $password -AccountNeverExpires
try {
  $usersGroup = [Security.Principal.SecurityIdentifier]'S-1-5-32-545'
  if (-not (Get-LocalGroupMember -SID $usersGroup | Where-Object { $_.SID -eq $account.SID })) {
    Add-LocalGroupMember -SID $usersGroup -Member $user
  }
  $credential = [PSCredential]::new("$env:COMPUTERNAME\$user", $password)
  $HostArch = if ($env:RUNNER_ARCH -eq 'ARM64') { 'arm64' } else { 'x64' }
  $arguments = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$Root\probe.ps1`"", '-Child',
    '-GoArch', $GoArch, '-Root', "`"$Root`"", '-HostArch', $HostArch)
  $process = Start-Process -FilePath (Get-Command powershell.exe).Source -ArgumentList $arguments -Credential $credential -LoadUserProfile -Wait -PassThru
  if (Test-Path (Join-Path $Root 'standard-user.log')) { Get-Content (Join-Path $Root 'standard-user.log') }
  if ($process.ExitCode -ne 0) { throw "Standard-user validation failed: $($process.ExitCode)" }
} finally {
  Remove-LocalUser -Name $user
}
