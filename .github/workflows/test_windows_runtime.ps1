param(
  [Parameter(Mandatory = $true)]
  [string]$LLGo
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $LLGo)) {
  throw "LLGo compiler was not found at $LLGo"
}
if (-not $env:LLGO_MSYS2_LOCATION) {
  throw "LLGO_MSYS2_LOCATION is not configured"
}

$root = (Get-Location).Path
$clangBin = Join-Path $env:LLGO_MSYS2_LOCATION "clang64\bin"
$clangExe = Join-Path $clangBin "clang.exe"
$llvmNmExe = Join-Path $clangBin "llvm-nm.exe"
$readObjExe = Join-Path $clangBin "llvm-readobj.exe"
$out = Join-Path $env:RUNNER_TEMP ("llgo-windows-runtime-" + [Guid]::NewGuid())
New-Item -ItemType Directory $out | Out-Null

$env:Path = "$clangBin;$env:Path"
$env:LLGO_ROOT = $root
$env:LLGO_BUILD_CACHE = "off"

# The common Windows runner executes amd64 binaries. Compile the raw SyscallN
# bridge for every Go-supported Windows architecture as well, so target ABI
# register or symbol regressions do not wait for native 386/arm64 runners.
$syscallAsm = Join-Path $root "runtime\internal\lib\runtime\_wrap\syscall_windows.S"
foreach ($syscallTarget in @(
  @{ Triple = "i686-pc-windows-msvc"; Symbol = "_llgo_windows_syscall" },
  @{ Triple = "x86_64-pc-windows-msvc"; Symbol = "llgo_windows_syscall" },
  @{ Triple = "aarch64-pc-windows-msvc"; Symbol = "llgo_windows_syscall" }
)) {
  $syscallObj = Join-Path $out ("syscall-{0}.obj" -f $syscallTarget.Triple)
  & $clangExe "--target=$($syscallTarget.Triple)" -c $syscallAsm -o $syscallObj
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
  $symbols = & $llvmNmExe --defined-only $syscallObj | Out-String
  if (-not $symbols.Contains($syscallTarget.Symbol)) {
    throw "$($syscallTarget.Triple) bridge is missing $($syscallTarget.Symbol)"
  }
}

$runtime = Join-Path $out "windows-runtime-smoke.exe"
$stdlib = Join-Path $out "windows-stdlib-smoke.exe"
$ffi = Join-Path $out "windows-ffi-smoke.exe"
$empty = Join-Path $out "windows-empty-smoke.exe"
$coreFault = Join-Path $out "windows-core-fault-smoke.exe"

# Keep the special-purpose fixtures beside the full test/... matrix. They
# cover minimal-runtime links and process behavior which a testing binary can
# accidentally satisfy through optional standard-library imports.
& $LLGo build -o $runtime .\runtime\_test\windowsruntime
if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}
& $LLGo build -tags=nogc -o $stdlib .\runtime\_test\windowsstdlib
if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}
& $LLGo build -o $ffi .\runtime\_test\windowsffi
if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}
& $LLGo build -o $empty .\runtime\_test\windowsempty
if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}
& $LLGo build -o $coreFault .\runtime\_test\windowscorefault
if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}

& .\.github\workflows\check_windows_imports.ps1 `
  -ReadObj $readObjExe `
  -Artifacts @($runtime, $stdlib, $ffi, $empty, $coreFault)

Write-Host "==> windows-runtime-smoke.exe"
& $runtime
if ($LASTEXITCODE -ne 0) {
  throw "windows-runtime-smoke.exe exited with code $LASTEXITCODE"
}

Write-Host "==> windows-runtime-smoke.exe (unrecovered fault)"
$env:LLGO_TEST_UNRECOVERED_FAULT = "1"
$faultOutput = & $runtime 2>&1 | Out-String
$faultExitCode = $LASTEXITCODE
Remove-Item Env:LLGO_TEST_UNRECOVERED_FAULT
Write-Host $faultOutput
$normalizedFaultOutput = $faultOutput.Replace('\', '/')
if ($faultExitCode -eq 0) {
  throw "unrecovered Windows fault exited successfully"
}
foreach ($expected in @(
  "runtime error: invalid memory address or nil pointer dereference",
  "main.windowsNilFault",
  "windowsruntime/main.go"
)) {
  if (-not $normalizedFaultOutput.Contains($expected)) {
    throw "unrecovered Windows fault output is missing '$expected'"
  }
}

Write-Host "==> windows-stdlib-smoke.exe"
& $stdlib
if ($LASTEXITCODE -ne 0) {
  throw "windows-stdlib-smoke.exe exited with code $LASTEXITCODE"
}

Write-Host "==> windows-stdlib-smoke.exe (os.Exit)"
$env:LLGO_TEST_OS_EXIT = "1"
& $stdlib
$exitCode = $LASTEXITCODE
Remove-Item Env:LLGO_TEST_OS_EXIT
if ($exitCode -ne 23) {
  throw "os.Exit(23) returned exit code $exitCode"
}

foreach ($artifact in @(
  @{ Name = "windows-ffi-smoke.exe"; Path = $ffi },
  @{ Name = "windows-empty-smoke.exe"; Path = $empty },
  @{ Name = "windows-core-fault-smoke.exe"; Path = $coreFault }
)) {
  Write-Host "==> $($artifact.Name)"
  & $artifact.Path
  if ($LASTEXITCODE -ne 0) {
    throw "$($artifact.Name) exited with code $LASTEXITCODE"
  }
}
