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

# Emscripten represents process termination as a JavaScript ExitStatus. When
# os.Exit runs in a synchronous js.FuncOf callback, that status must unwind
# through the surrounding emval call instead of becoming a syscall/js panic.
case "${PROFILE:-}" in
	EC32|EC64|GJS)
		callback_log="${log%.log}-callback.log"
		callback_dir="$(mktemp -d)"
		trap 'rm -rf "$callback_dir"' EXIT
		callback_module="$callback_dir/exit-callback.mjs"
		case "$PROFILE" in
			EC32) callback_args=(-target emscripten); callback_runner=targets/emscripten-runner.mjs ;;
			EC64) callback_args=(-target emscripten-memory64); callback_runner=targets/emscripten-memory64-runner.mjs ;;
			GJS) callback_args=(); callback_runner=targets/emscripten-runner.mjs ;;
		esac
		timeout 5m "$llgo_cmd" build "${callback_args[@]}" -o "$callback_module" \
			./internal/build/testdata/wasm-exit-callback > "$callback_log" 2>&1
		callback_status=0
		timeout 5m node "$callback_runner" "$callback_module" >> "$callback_log" 2>&1 || callback_status=$?
		cat "$callback_log"
		test "$callback_status" -eq 7
		! grep -Eq '^(panic:|fatal error:)' "$callback_log"
		;;
esac
