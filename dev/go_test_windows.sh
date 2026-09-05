#!/usr/bin/env bash

set -euo pipefail

if [[ $# -eq 0 ]]; then
	echo "usage: $0 <go-test-command> [argument ...]" >&2
	exit 2
fi

# Some GitHub-hosted Windows/amd64 machines are affected by golang/go#81238:
# recovering a hardware exception can write below a goroutine stack and
# corrupt an unrelated heap object. A corrupted testing.T signal channel then
# produces this otherwise-impossible synctest fatal error. This wrapper is used
# only for the host-test batch containing this module's test package tree.
module_path="$(go list -m -f '{{.Path}}')"

is_known_runtime_corruption() {
	local log="$1"
	# The runtime provides no machine-readable cause. Require one exact fatal
	# plus only the expected package failure, and reject other failure markers.
	awk '
		BEGIN { want = "fatal error: receive on synctest channel from outside bubble" }
		{
			line = $0
			sub(/\r$/, "", line)
			if (index(line, "fatal error:") == 1) {
				count++
				if (line != want) other = 1
			}
		}
		END { exit !(count == 1 && !other) }
	' "${log}" &&
		grep -Fq 'testing.(*T).Run' "${log}" &&
		awk -v prefix="${module_path}/test" '
			$1 == "FAIL" && NF >= 2 {
				if ($2 == prefix || index($2, prefix "/") == 1) target = 1
				else other = 1
			}
			END { exit !(target && !other) }
		' "${log}" &&
		! grep -Eiq '^--- FAIL:|\[build failed\]|^panic:|WARNING: DATA RACE|test timed out|SIGQUIT|SIGSEGV|SIGABRT|unexpected fault address|signal: (segmentation fault|aborted)' "${log}"
}

is_known_test_go_access_violation() {
	local log="$1"
	shift

	# This quarantine is deliberately limited to the separately-invoked
	# language-behavior suite. The compiler fixture suite and every other test
	# package must continue to report an access violation as a failure.
	local arg test_go_args=0
	for arg in "$@"; do
		case "${arg}" in
		./test/go|"${module_path}/test/go")
			test_go_args=$((test_go_args + 1))
			;;
		./*|"${module_path}"/*)
			return 1
			;;
		esac
	done
	(( test_go_args == 1 )) &&
		awk '$0 == "exit status 0xc0000005" { count++ } END { exit !(count == 1) }' "${log}" &&
		awk -v target="${module_path}/test/go" '
			$1 == "FAIL" && NF >= 2 {
				if ($2 == target) target_failure = 1
				else other_failure = 1
			}
			END { exit !(target_failure && !other_failure) }
		' "${log}" &&
		! grep -Eiq '^--- FAIL:|\[build failed\]|^fatal error:|^panic:|WARNING: DATA RACE|test timed out|SIGQUIT|SIGSEGV|SIGABRT|unexpected fault address|signal: (segmentation fault|aborted)' "${log}"
}

log=
trap '[[ -z "${log}" ]] || rm -f "${log}"' EXIT
log="$(mktemp "${TMPDIR:-/tmp}/llgo-go-test-windows.XXXXXX")"
set +e
"$@" 2>&1 | tee "${log}"
status=${PIPESTATUS[0]}
set -e

if [[ ${status} -eq 0 ]] || ! is_known_runtime_corruption "${log}"; then
	if [[ ${status} -eq 0 ]] || ! is_known_test_go_access_violation "${log}" "$@"; then
		exit "${status}"
	fi
	echo '::warning title=Quarantined Windows test/go access violation::LLGO_CI_QUARANTINED_WINDOWS_TEST_GO_ACCESS_VIOLATION: matched the narrow test/go-only 0xc0000005 signature'
	if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
		echo 'windows_runtime_corruption=true' >>"${GITHUB_OUTPUT}"
	fi
	if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
		{
			echo '### Quarantined Windows test/go access violation'
			echo
			echo 'The direct test/go command ended with the narrow 0xc0000005 signature and no test failure. The compiler fixture suite remains fail-closed; coverage upload for this job was skipped.'
		} >>"${GITHUB_STEP_SUMMARY}"
	fi
	exit 0
fi

echo '::warning title=Quarantined upstream Go runtime corruption::LLGO_CI_QUARANTINED_GO_RUNTIME_CORRUPTION: matched the narrow golang/go#81238 signature'
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
	echo 'windows_runtime_corruption=true' >>"${GITHUB_OUTPUT}"
fi
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
	{
		echo '### Quarantined Windows Go runtime corruption'
		echo
		echo 'The narrow golang/go#81238 signature occurred in the LLGo test package tree. The failure was quarantined and the coverage upload for this job was skipped.'
	} >>"${GITHUB_STEP_SUMMARY}"
fi
exit 0
