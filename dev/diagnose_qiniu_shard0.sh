#!/usr/bin/env bash
set -euo pipefail

# This harness does not change compiler code, test arguments, or GC settings.
export DIAGNOSTIC_OUTPUT="$RUNNER_TEMP/shard0-diagnostics"
mkdir -p "$DIAGNOSTIC_OUTPUT"
export LLGO_ROOT=$PWD
export LLGO_REAL="$RUNNER_TEMP/llgo-bin/llgo"
export LLGO_TEST_LLGEN="$RUNNER_TEMP/llgo-bin/llgen"
export CHECK_STD_SYMBOLS="$RUNNER_TEMP/llgo-bin/check_std_symbols"
export LLGO="$DIAGNOSTIC_HARNESS/dev/diagnose_qiniu_llgo.sh"
chmod +x "$LLGO"
{
  git show -s --format='commit=%H tree=%T parents=%P'
  git status --short
  uname -a
  lscpu
  free -h
  go version
  go version -m "$LLGO_REAL"
  clang --version
  dpkg-query -W libgc1 libgc-dev libc6
  printf 'cache_hit=%s cache_key=%s\n' "$DIAGNOSTIC_CACHE_HIT" "$DIAGNOSTIC_CACHE_KEY"
  for resource in cpu.max cpuset.cpus.effective memory.max memory.events; do
    if [[ -r "/sys/fs/cgroup/$resource" ]]; then
      printf '%s: ' "$resource"
      cat "/sys/fs/cgroup/$resource"
    fi
  done
} >"$DIAGNOSTIC_OUTPUT/environment.txt"

# An external deadline still fires if the tested runtime cannot run timers.
# Its process group includes the compiler and nested test programs.
setsid timeout --signal=TERM --kill-after=10s 18m bash dev/test_go_version.sh 1.27 >"$DIAGNOSTIC_OUTPUT/pipeline.log" 2>&1 &
pipeline=$!
export DIAGNOSTIC_PIPELINE=$pipeline
tail --pid="$pipeline" -f "$DIAGNOSTIC_OUTPUT/pipeline.log" &
tail_pid=$!
setsid bash "$DIAGNOSTIC_HARNESS/dev/diagnose_qiniu_watchdog.sh" &
watchdog=$!

status=0
wait "$pipeline" || status=$?
kill -- "-$watchdog" 2>/dev/null || true
wait "$watchdog" 2>/dev/null || true
wait "$tail_pid" || true
{
  printf 'Exact failed merge: 94677096ec9981665d37c506c6d4ea09a460ca86. Original arguments, Qiniu Ubuntu 24.04 large, LLVM 22, Go 1.27.\n\n'
  printf 'Package cache hit: %s. Pipeline exit: %s.\n\n' "$DIAGNOSTIC_CACHE_HIT" "$status"
  if [[ -f "$DIAGNOSTIC_OUTPUT/ptrace-sampled.txt" ]]; then
    printf 'Late ptrace diagnostics were taken: this cannot count as an unperturbed passing trial.\n\n'
  fi
  printf 'Completed package lines: '
  awk '$1 == "ok" && $2 ~ /^github.com\/xgo-dev\/llgo\/test/ {seen[$2]=1} END {for (p in seen) n++; print n+0}' "$DIAGNOSTIC_OUTPUT/pipeline.log"
  printf '\nLast output:\n```text\n'
  tail -35 "$DIAGNOSTIC_OUTPUT/pipeline.log"
  printf '\n```\n'
} >>"$GITHUB_STEP_SUMMARY"
exit "$status"
