#!/usr/bin/env bash
set -euo pipefail

export DIAGNOSTIC_OUTPUT="$PWD/shard0-diagnostics"
mkdir -p "$DIAGNOSTIC_OUTPUT"
export LLGO_ROOT=$PWD
export LLGO_REAL="$RUNNER_TEMP/llgo-bin/llgo"
export LLGO_TEST_LLGEN="$RUNNER_TEMP/llgo-bin/llgen"
export CHECK_STD_SYMBOLS="$RUNNER_TEMP/llgo-bin/check_std_symbols"
export LLGO="$PWD/diagnostic-harness/dev/diagnose_llgo_trace.sh"
chmod +x "$LLGO"
{
  git rev-parse HEAD
  uname -a
  lscpu
  free -h
  go version
  clang --version
  dpkg-query -W libgc1 libgc-dev libc6
  printf 'trial=%s trace=%s\n' "${DIAGNOSTIC_TRIAL:-first}" "${DIAGNOSTIC_TRACE:-1}"
} >"$DIAGNOSTIC_OUTPUT/environment.txt"

# A dedicated process group makes the hard bound include nested test runners.
setsid timeout --signal=TERM --kill-after=10s 18m bash dev/test_go_version.sh 1.27 >"$DIAGNOSTIC_OUTPUT/pipeline.log" 2>&1 &
pipeline=$!
export DIAGNOSTIC_PIPELINE=$pipeline
tail --pid="$pipeline" -f "$DIAGNOSTIC_OUTPUT/pipeline.log" &
tail_pid=$!

# Read-only process snapshots do not stop threads. Only the late stack sample
# uses ptrace, and its presence is recorded so it cannot count as an ordinary
# passing trial. A separate process group also lets cleanup reap its sleeps.
setsid bash diagnostic-harness/dev/diagnose_shard0_watchdog.sh &
watchdog=$!

status=0
wait "$pipeline" || status=$?
kill -- "-$watchdog" 2>/dev/null || true
wait "$watchdog" 2>/dev/null || true
wait "$tail_pid" || true
printf 'Trial: %s; trace: %s; original shard-0 pipeline exit: %s\n' "${DIAGNOSTIC_TRIAL:-first}" "${DIAGNOSTIC_TRACE:-1}" "$status" >>"$GITHUB_STEP_SUMMARY"
if [[ -f "$DIAGNOSTIC_OUTPUT/ptrace-sampled.txt" ]]; then
  printf 'A late ptrace sample was taken; do not count this as an unperturbed passing trial.\n' >>"$GITHUB_STEP_SUMMARY"
fi
exit "$status"
