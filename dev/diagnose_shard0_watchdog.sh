#!/usr/bin/env bash
set -euo pipefail

for minute in {1..17}; do
  sleep 60
  if ! kill -0 "$DIAGNOSTIC_PIPELINE" 2>/dev/null; then exit 0; fi
  {
    date -u +%FT%TZ
    ps -eo pid,ppid,pgid,pcpu,pmem,stat,wchan:32,etime,args --forest
    free -h
  } >"$DIAGNOSTIC_OUTPUT/processes-minute-$minute.txt"
  printf '[diagnostic] minute=%s pipeline=%s still running; process snapshot saved\n' "$minute" "$DIAGNOSTIC_PIPELINE"
  if [[ "$minute" != 16 ]]; then continue; fi
  date -u +%FT%TZ >"$DIAGNOSTIC_OUTPUT/ptrace-sampled.txt"
  sampled=0
  for pid in $(pgrep -g "$DIAGNOSTIC_PIPELINE" || true); do
    executable=$(readlink "/proc/$pid/exe" || true)
    case "$executable" in
      */llgo|*/runner-*|*.test)
        # Keep the diagnostic file owned by the runner, not root.
        # shellcheck disable=SC2024
        sudo timeout -k 2s 10s gdb --batch --nx -p "$pid" -ex 'set pagination off' -ex 'thread apply all bt 25' -ex detach >"$DIAGNOSTIC_OUTPUT/stacks-$pid.txt" 2>&1 || true
        sampled=$((sampled + 1))
        if (( sampled >= 6 )); then break; fi
        ;;
    esac
  done
done
