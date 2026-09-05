#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

module_path="$(go list -m -f '{{.Path}}')"
export MODULE_PATH="${module_path}"

fail() {
	echo "$1" >&2
	exit 1
}

run_access_violation() {
	local package_arg="$1"
	local package_suffix="$2"
	local extra_output="$3"
	bash dev/go_test_windows.sh bash -c '
		printf "exit status 0xc0000005\\n"
		printf "FAIL\\t%s/%s\\t0.1s\\n" "$MODULE_PATH" "$1"
		printf "%s" "$2"
		exit 1
	' "${package_arg}" "${package_suffix}" "${extra_output}"
}

output_file="$(mktemp)"
trap 'rm -f "${output_file}"' EXIT
export GITHUB_OUTPUT="${output_file}"

run_access_violation ./test/go test/go ''
grep -Fxq 'windows_runtime_corruption=true' "${output_file}" ||
	fail "test/go quarantine did not set the coverage-skip output"

if run_access_violation ./cl cl '' >/dev/null 2>&1; then
	fail "compiler fixture access violation was unexpectedly quarantined"
fi

if run_access_violation ./test/go test/go $'--- FAIL: TestRealFailure\n' >/dev/null 2>&1; then
	fail "test/go access violation with a test failure was unexpectedly quarantined"
fi
