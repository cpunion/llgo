param(
  [Parameter(Mandatory = $true)]
  [string]$LLGo
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $LLGo)) {
  throw "LLGo compiler was not found at $LLGo"
}
$LLGo = (Resolve-Path $LLGo).Path
if (-not $env:LLGO_MSYS2_LOCATION) {
  throw "LLGO_MSYS2_LOCATION is not configured"
}

$root = (Get-Location).Path
# GitHub's windows-2022 host is amd64 and setup-deps installs clang64. A
# native arm64 Windows developer host can point at clangarm64 without keeping
# a divergent copy of this runtime validation script.
$clangBin = $env:LLGO_MSYS2_CLANG_BIN
if (-not $clangBin) {
  $clangBin = Join-Path $env:LLGO_MSYS2_LOCATION "clang64\bin"
}
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
$network = Join-Path $out "windows-network-smoke.exe"

# Keep the special-purpose fixtures beside the full test/... matrix. They
# cover minimal-runtime links and process behavior which a testing binary can
# accidentally satisfy through optional standard-library imports. Build from
# the nested runtime module so Go resolves its own go.mod rather than treating
# these packages as missing paths in the repository's main module.
Push-Location runtime
try {
  & $LLGo build -o $runtime .\_test\windowsruntime
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
  & $LLGo build -tags=nogc -o $stdlib .\_test\windowsstdlib
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
  & $LLGo build -o $ffi .\_test\windowsffi
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
  & $LLGo build -o $empty .\_test\windowsempty
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
  & $LLGo build -o $coreFault .\_test\windowscorefault
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
  & $LLGo build -o $network .\_test\windowsnetwork
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
} finally {
  Pop-Location
}

& .\.github\workflows\check_windows_imports.ps1 `
  -ReadObj $readObjExe `
  -Artifacts @($runtime, $stdlib, $ffi, $empty, $coreFault, $network)

Write-Host "==> windows-runtime-smoke.exe"
& $runtime
if ($LASTEXITCODE -ne 0) {
  throw "windows-runtime-smoke.exe exited with code $LASTEXITCODE"
}

Write-Host "==> windows-runtime-smoke.exe (unrecovered fault)"
$env:LLGO_TEST_UNRECOVERED_FAULT = "1"
$savedErrorActionPreference = $ErrorActionPreference
try {
  # Windows PowerShell 5 reports redirected native stderr as a terminating
  # NativeCommandError when ErrorActionPreference is Stop. This invocation is
  # intentionally expected to fail, and its stderr is the subject of the
  # assertions below.
  $ErrorActionPreference = "Continue"
  $faultOutput = & $runtime 2>&1 | Out-String
  $faultExitCode = $LASTEXITCODE
} finally {
  $ErrorActionPreference = $savedErrorActionPreference
  Remove-Item Env:LLGO_TEST_UNRECOVERED_FAULT
}
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
if ($normalizedFaultOutput.Contains("github.com/xgo-dev/llgo/runtime/internal/clite/tls.init")) {
  throw "unrecovered Windows fault traceback continued past runtime.goexit"
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
  @{ Name = "windows-core-fault-smoke.exe"; Path = $coreFault },
  @{ Name = "windows-network-smoke.exe"; Path = $network }
)) {
  Write-Host "==> $($artifact.Name)"
  & $artifact.Path
  if ($LASTEXITCODE -ne 0) {
    throw "$($artifact.Name) exited with code $LASTEXITCODE"
  }
}
