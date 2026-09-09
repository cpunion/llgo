#!/usr/bin/env bash
set -euo pipefail

if [[ $# == 0 ]]; then
	echo "usage: wasm_test_env.sh command [args...]" >&2
	exit 2
fi

# Match gorootRuntimeEnv in test/goroot/wasm_profile_test.go: CI credentials,
# host paths and diagnostic controls are not part of the program environment.
# Go's JS helper has a fixed argv/environment area. Keep ordinary variables
# and give both compilers the same environment without changing the parent job.
while IFS= read -r name; do
	case "$name" in
	ACTIONS_* | GITHUB_* | RUNNER_* | LLGO_DIAG_* | LLGO_R4_*) unset "$name" ;;
	esac
done < <(compgen -e)

exec "$@"
