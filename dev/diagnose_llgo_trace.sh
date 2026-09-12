#!/usr/bin/env bash
set -euo pipefail

invocation=$(mktemp -d "$DIAGNOSTIC_OUTPUT/invocation.XXXXXX")
printf '%q ' "$@" >"$invocation/command.txt"
printf 'pid=%s started=%s trace=%s\n' "$$" "$(date -u +%FT%TZ)" "${DIAGNOSTIC_TRACE:-1}" >"$invocation/start.txt"
if [[ "${1:-}" == test && "${DIAGNOSTIC_TRACE:-1}" == 1 ]]; then
  shift
  exec "$LLGO_REAL" test -v "-debug-trace=$invocation/build.json" "$@"
fi
exec "$LLGO_REAL" "$@"
