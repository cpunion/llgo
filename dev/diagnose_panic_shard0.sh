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
} >"$DIAGNOSTIC_OUTPUT/environment.txt"

# A dedicated process group makes the hard bound include nested test runners.
setsid timeout --signal=TERM --kill-after=10s 18m bash dev/test_go_version.sh 1.27 >"$DIAGNOSTIC_OUTPUT/pipeline.log" 2>&1 &
pipeline=$!
tail --pid="$pipeline" -f "$DIAGNOSTIC_OUTPUT/pipeline.log" &
tail_pid=$!

# Only sample after 16 minutes, outside ordinary completion times. Sampling is
# explicitly diagnostic, never counted as an unperturbed passing stress trial.
(
  sleep 960
  if kill -0 "$pipeline" 2>/dev/null; then
    ps -eo pid,ppid,pgid,pcpu,pmem,stat,wchan:32,etime,args --forest >"$DIAGNOSTIC_OUTPUT/processes.txt"
    for pid in $(pgrep -g "$pipeline" || true); do
      executable=$(readlink "/proc/$pid/exe" || true)
      case "$executable" in
        *llgo*|*runner-*|*.test)
          sudo timeout -k 2s 10s gdb --batch --nx -p "$pid" -ex 'set pagination off' -ex 'thread apply all bt 25' -ex detach >"$DIAGNOSTIC_OUTPUT/stacks-$pid.txt" 2>&1 || true
          ;;
      esac
    done
  fi
) &
watchdog=$!

status=0
wait "$pipeline" || status=$?
kill "$watchdog" 2>/dev/null || true
wait "$watchdog" 2>/dev/null || true
wait "$tail_pid" || true
printf 'Original shard-0 pipeline exit: %s\n' "$status" >>"$GITHUB_STEP_SUMMARY"
exit "$status"
