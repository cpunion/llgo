#!/usr/bin/env bash

# One existing coverage execution, with bounded failure evidence. This is not
# the synctest quarantine wrapper and must never retry or suppress a failure.
set -euo pipefail

if [[ "${RUNNER_OS:-}" != Windows || "${RUNNER_ARCH:-}" != X64 || "${LLGO_WINDOWS_ABI:-}" != mingw ]]; then
  echo 'Windows test-go diagnostics require the MinGW x64 CI lane' >&2
  exit 2
fi
if [[ $# -lt 3 || "$1" != go || "$2" != test ]]; then
  echo 'usage: go_test_windows_diagnostics.sh go test <coverage flags> ./test/go' >&2
  exit 2
fi
shift 2
test_args=("$@")
target_count=0
timeout_value=false
for arg in "$@"; do
  if [[ "$timeout_value" == true ]]; then
    [[ "$arg" == 45m ]] || { echo 'expected the existing 45m timeout' >&2; exit 2; }
    timeout_value=false
    continue
  fi
  case "$arg" in
    -timeout) timeout_value=true ;;
    -timeout=45m|-ldflags=*|-coverprofile=*|-covermode=atomic) ;;
    ./test/go) target_count=$((target_count + 1)) ;;
    *) echo "unsupported test-go diagnostic argument: $arg" >&2; exit 2 ;;
  esac
done
if [[ "$target_count" -ne 1 || "$timeout_value" == true ]]; then
  echo 'diagnostics require exactly one complete ./test/go execution' >&2
  exit 2
fi
: "${RUNNER_TEMP:?RUNNER_TEMP is required}"
command -v pwsh >/dev/null
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
diagnostic_dir="$RUNNER_TEMP/llgo-windows-test-go-diagnostics"
# Do not overwrite evidence or restoration state from an earlier invocation.
mkdir "$diagnostic_dir"
evidence_dir="$diagnostic_dir/evidence"
mkdir "$evidence_dir" "$diagnostic_dir/dumps"

# shellcheck disable=SC2329 # Called by the EXIT trap.
finish() {
  local status=$? cleanup_status=0
  trap - EXIT
  pwsh -NoProfile -NonInteractive -File "$script_dir/go_test_windows_wer.ps1" \
    -Mode Finish -Directory "$diagnostic_dir" >>"$evidence_dir/diagnostics.log" 2>&1 || cleanup_status=$?
  if [[ "$cleanup_status" -ne 0 ]]; then
    echo "Windows crash-reporting restoration failed (exit $cleanup_status); see diagnostics.log" >&2
    # Preserve the original test/setup failure; a cleanup failure also fails
    # an otherwise successful command and is retried by the workflow cleanup.
    if [[ "$status" -eq 0 ]]; then status=$cleanup_status; fi
  fi
  if [[ "$status" -ne 0 && -n "${GITHUB_OUTPUT:-}" ]]; then
    echo 'windows_test_go_failed=true' >>"$GITHUB_OUTPUT" || echo 'Could not record the diagnostic failure output' >&2
  fi
  exit "$status"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

pwsh -NoProfile -NonInteractive -File "$script_dir/go_test_windows_wer.ps1" \
  -Mode Prepare -Directory "$diagnostic_dir" >"$evidence_dir/diagnostics.log" 2>&1 || {
    prepare_status=$?
    tail -40 "$evidence_dir/diagnostics.log" >&2
    exit "$prepare_status"
  }

# -o retains the exact executable (including symbols) even when the test fails.
# cmd/go still executes its temporary go.test.exe, so WER uses that basename.
command=(go test -v -count=1 "-o=$evidence_dir/go.test.exe" "${test_args[@]}")
{
  printf 'GOTRACEBACK=wer'
  printf ' %q' "${command[@]}"
  printf '\n'
} >"$evidence_dir/command.txt"
set +e
GOTRACEBACK=wer "${command[@]}" 2>&1 | tee "$evidence_dir/test.log"
statuses=("${PIPESTATUS[@]}")
set -e
log_status=0
printf 'go_test_exit=%s\nlog_capture_exit=%s\n' "${statuses[0]}" "${statuses[1]}" >>"$evidence_dir/diagnostics.log" || log_status=$?
if [[ "${statuses[0]}" -ne 0 ]]; then exit "${statuses[0]}"; fi
if [[ "${statuses[1]}" -ne 0 ]]; then exit "${statuses[1]}"; fi
exit "$log_status"
