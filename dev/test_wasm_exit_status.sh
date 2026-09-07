#!/usr/bin/env bash
set -euo pipefail

# A nonzero exit is necessary but not sufficient: reject build failures and
# deadlines by also requiring the specific failing test and its message.
llgo_cmd="${LLGO:-llgo}"
log="${1:?usage: test_wasm_exit_status.sh <log> [llgo target arguments...]}"
shift
status=0
timeout 5m env LLGO_WASM_TEST_FAILURE=1 "$llgo_cmd" test "$@" \
	-v -count=1 -timeout=30s -run='^TestIntentionalHostExitFailure$' \
	./internal/build/testdata/wasm-test > "$log" 2>&1 || status=$?
cat "$log"
test "$status" -eq 1
grep -Fq 'intentional wasm exit-status probe' "$log"
grep -Eq '^--- FAIL: TestIntentionalHostExitFailure' "$log"
! grep -Eq '^(PASS|ok[[:space:]])' "$log"
