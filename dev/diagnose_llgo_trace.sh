#!/usr/bin/env bash
set -euo pipefail

invocation=$(mktemp -d "$DIAGNOSTIC_OUTPUT/invocation.XXXXXX")
printf '%q ' "$@" >"$invocation/command.txt"
if [[ "${1:-}" == test ]]; then
  shift
  exec "$LLGO_REAL" test -v "-debug-trace=$invocation/build.json" "$@"
fi
exec "$LLGO_REAL" "$@"
