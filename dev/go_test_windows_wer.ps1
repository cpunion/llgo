param(
  [Parameter(Mandatory = $true)]
  [ValidateSet('Prepare', 'Finish')]
  [string]$Mode,
  [Parameter(Mandatory = $true)]
  [string]$Directory
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'WER diagnostics require Windows' }

# Never configure all-process dumping. cmd/go runs this basename even with -o.
$nativeKey = 'HKLM\SOFTWARE\Microsoft\Windows\Windows Error Reporting\LocalDumps\go.test.exe'
$key = 'Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows\Windows Error Reporting\LocalDumps\go.test.exe'
$statePath = Join-Path $Directory 'wer-state.json'
$backupPath = Join-Path $Directory 'wer-before.reg'
$evidence = Join-Path $Directory 'evidence'
$dumpDirectory = Join-Path $Directory 'dumps'

function Invoke-Registry([string[]]$RegistryArguments) {
  & reg.exe @RegistryArguments
  if ($LASTEXITCODE -ne 0) { throw "reg.exe failed with exit code $LASTEXITCODE" }
}

function Read-NativeMetadata([string]$Executable, [string[]]$NativeArguments) {
  try {
    & $Executable @NativeArguments 2>&1
    if ($LASTEXITCODE -ne 0) { throw "exit code $LASTEXITCODE" }
  } catch {
    "Metadata probe failed ($Executable): $($_.Exception.Message)"
  }
}

if ($Mode -eq 'Prepare') {
  # Missing build tools are configuration errors. Optional version/CPU probes
  # below must not prevent the coverage command from running.
  $null = Get-Command go, clang -CommandType Application -ErrorAction Stop
  if (Test-Path -LiteralPath $statePath) { throw 'WER restoration state already exists' }
  $existed = Test-Path -LiteralPath $key
  $addedValues = @('DumpFolder', 'DumpType', 'DumpCount')
  if ($existed) {
    # Export the entire exact key, including every value type and child key.
    # The backup stays outside the uploaded evidence directory.
    Invoke-Registry -RegistryArguments @('export', $nativeKey, $backupPath, '/y')
    $oldNames = (Get-Item -LiteralPath $key).GetValueNames()
    $addedValues = @($addedValues | Where-Object { $_ -notin $oldNames })
  }
  @{ Existed = $existed; Restored = $false; AddedValues = $addedValues } | ConvertTo-Json |
    Set-Content -LiteralPath $statePath -Encoding utf8
  if (-not $existed) { New-Item -Path $key -Force | Out-Null }
  New-ItemProperty -LiteralPath $key -Name DumpFolder -Value $dumpDirectory -PropertyType ExpandString -Force | Out-Null
  New-ItemProperty -LiteralPath $key -Name DumpType -Value 1 -PropertyType DWord -Force | Out-Null
  New-ItemProperty -LiteralPath $key -Name DumpCount -Value 1 -PropertyType DWord -Force | Out-Null
  Write-Output 'WER enabled only for go.test.exe: mini dump, maximum one file.'

  # An explicit allowlist, never an environment dump or processor serial ID.
  @(
    "ImageOS=$env:ImageOS"
    "ImageVersion=$env:ImageVersion"
    "RUNNER_ARCH=$env:RUNNER_ARCH"
    (Read-NativeMetadata -Executable go -NativeArguments @('version'))
    (Read-NativeMetadata -Executable go -NativeArguments @('env', 'GOOS', 'GOARCH', 'GOVERSION', 'CC', 'CGO_ENABLED'))
    (Read-NativeMetadata -Executable clang -NativeArguments @('--version'))
  ) | Set-Content -LiteralPath (Join-Path $evidence 'metadata.txt') -Encoding utf8
  $processorMetadata = try {
    Get-CimInstance Win32_Processor |
      Select-Object Name, Manufacturer, NumberOfCores, NumberOfLogicalProcessors |
      Format-List | Out-String
  } catch {
    "Processor metadata probe failed: $($_.Exception.Message)"
  }
  $processorMetadata | Add-Content -LiteralPath (Join-Path $evidence 'metadata.txt') -Encoding utf8
  exit 0
}

# Finish is idempotent, including when the coverage command was never reached.
if (-not (Test-Path -LiteralPath $statePath)) { exit 0 }
$state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
try {
  if (-not $state.Restored) {
    if ($state.Existed -and -not (Test-Path -LiteralPath $backupPath)) {
      throw 'Cannot restore the pre-existing WER key: backup is missing'
    }
    if ($state.Existed) {
      # Keep the original key and children in place: reg export/import preserves
      # values and types, not ACLs. Deleting that key would lose custom ACLs.
      if (-not (Test-Path -LiteralPath $key)) { throw 'The original WER key disappeared; cannot restore its permissions' }
      $currentNames = (Get-Item -LiteralPath $key).GetValueNames()
      foreach ($name in $state.AddedValues) {
        if ($name -in $currentNames) { Remove-ItemProperty -LiteralPath $key -Name $name }
      }
      Invoke-Registry -RegistryArguments @('import', $backupPath)
    } elseif (Test-Path -LiteralPath $key) {
      Remove-Item -LiteralPath $key -Recurse -Force
    }
    $state.Restored = $true
    $state | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding utf8
    Write-Output 'Original go.test.exe WER configuration restored.'
  }
} catch {
  # Keep the restoration error visible even if collecting the dump also fails.
  Write-Output "WER restoration failed: $($_.Exception.Message)"
  throw
} finally {
  # Expected fatal-test children can also create dumps. Keep only the newest;
  # do not label its cause without inspecting the dump and verbose test log.
  $dump = Get-ChildItem -LiteralPath $dumpDirectory -Filter '*.dmp' -File |
    Sort-Object LastWriteTimeUtc -Descending | Select-Object -First 1
  if ($null -ne $dump) {
    Copy-Item -LiteralPath $dump.FullName -Destination (Join-Path $evidence 'crash.dmp') -Force
    Write-Output "Collected mini dump: $($dump.Name), $($dump.Length) bytes."
  } else {
    Write-Output 'No WER dump was produced; preserve the verbose log, without inferring a crash cause.'
  }
}
