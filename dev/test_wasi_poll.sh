#!/usr/bin/env bash
set -euo pipefail

# Hold both ends open: stdin is not ready, but is not at EOF either. The
# guest must keep scheduling Go timers and descriptor cancellation callbacks.
poll_dir="$(mktemp -d)"
trap 'exec 3>&-; rm -f "$poll_dir/input"; rmdir "$poll_dir"' EXIT
mkfifo "$poll_dir/input"
exec 3<>"$poll_dir/input"

llgo_cmd="${LLGO:-llgo}"
log="${1:?usage: test_wasi_poll.sh <log> [llgo target arguments...]}"
shift
timeout 5m "$llgo_cmd" test "$@" -v -count=1 -timeout=30s \
	-run='^TestWASIPoll(DescriptorLifecycle|BlockedWait)$' \
	./internal/build/testdata/wasm-test -args -llgo.wasi-poll-stdin \
	<&3 > "$log" 2>&1 || { cat "$log"; exit 1; }
cat "$log"
grep -Fq -- '--- PASS: TestWASIPollBlockedWait/close' "$log"
grep -Fq -- '--- PASS: TestWASIPollBlockedWait/changed_deadline' "$log"
grep -Fq -- '--- PASS: TestWASIPollBlockedWait/timeout' "$log"
grep -Fq -- '--- PASS: TestWASIPollDescriptorLifecycle' "$log"
! grep -Eq '^(panic:|fatal error:|--- FAIL:|FAIL([[:space:]]|$))' "$log"
