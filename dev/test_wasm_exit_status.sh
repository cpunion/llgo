#!/usr/bin/env bash
set -euo pipefail

# A nonzero exit is necessary but not sufficient: reject build failures and
# deadlines by also requiring the specific failing test and its message.
llgo_cmd="${LLGO:-llgo}"
log="${1:?usage: test_wasm_exit_status.sh <log> [llgo target arguments...]}"
shift
status=0
timeout 5m "$llgo_cmd" test "$@" \
	-v -count=1 -timeout=30s -run='^Test(IntentionalHostExitFailure|NilStoreOperandOrder|NilPointerAndFunctionRecovery|LargeAggregateGCRoots|LargeStructGCRoots|RangeIteratorGCRoots)$' \
	./internal/build/testdata/wasm-test -args -llgo.intentional-exit-failure > "$log" 2>&1 || status=$?
cat "$log"
test "$status" -eq 1
grep -Fq 'intentional wasm exit-status probe' "$log"
grep -Eq '^--- FAIL: TestIntentionalHostExitFailure' "$log"
grep -Eq '^--- PASS: TestNilStoreOperandOrder' "$log"
grep -Eq '^--- PASS: TestNilPointerAndFunctionRecovery' "$log"
grep -Eq '^--- PASS: TestLargeAggregateGCRoots' "$log"
grep -Eq '^--- PASS: TestLargeStructGCRoots' "$log"
grep -Eq '^--- PASS: TestRangeIteratorGCRoots' "$log"
! grep -Eq '^(PASS|ok[[:space:]])' "$log"
