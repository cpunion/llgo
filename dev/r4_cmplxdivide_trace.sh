#!/usr/bin/env bash
# Diagnostic wrapper only; preserve the GOROOT runner's source, flags and limits.
set -euo pipefail

if [[ "${1:-}" == build ]]; then
  shift
  exec "$R4_TRACE_LLGO" build -x -debug-trace="$R4_TRACE_DIR/build.json" "$@" \
    > >(tee "$R4_TRACE_DIR/build.stdout") \
    2> >(tee "$R4_TRACE_DIR/build.stderr" >&2)
fi
exec "$R4_TRACE_LLGO" "$@"
