#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
profile="${1:?expected EC32, EC64, WC32, GJS, or GWASI}"
llgo_cmd="${LLGO:-llgo}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/llgo-wasm-caller-alloc.XXXXXX")"
trap 'rm -rf "${work_dir}"' EXIT
module="${work_dir}/caller.mjs"
args=()
runner="emscripten-runner.mjs"
case "$profile" in
EC32) args=(-target emscripten) ;;
EC64) args=(-target emscripten-memory64); runner=emscripten-memory64-runner.mjs ;;
WC32) args=(-target wasi); module="${work_dir}/caller.wasm" ;;
GJS) export GOOS=js GOARCH=wasm CGO_ENABLED=0 ;;
GWASI) export GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0; module="${work_dir}/caller.wasm" ;;
*) echo "unknown caller-allocation profile: $profile" >&2; exit 2 ;;
esac

timeout 5m "$llgo_cmd" build "${args[@]}" -o "$module" \
	"${repo_root}/internal/build/testdata/wasm-caller-alloc"
test -s "$module"
command=(node "${repo_root}/targets/${runner}" "$module")
if [[ "$profile" == WC32 || "$profile" == GWASI ]]; then
	command=(wasmtime run --dir=/ --env PWD --env PATH --env GOGC
		-W exceptions=y -W multi-memory=y -W max-wasm-stack=8388608 "$module")
fi
for policy in 100 1; do
	GOGC="$policy" timeout 60s "${command[@]}" > "${work_dir}/run.log" 2>&1 || {
		cat "${work_dir}/run.log"; exit 1;
	}
	if [[ "$(cat "${work_dir}/run.log")" != "wasm caller allocation ok" ]]; then
		cat "${work_dir}/run.log"
		echo "caller-allocation fixture did not produce its exact success marker" >&2
		exit 1
	fi
	printf '%s caller stack reuse GOGC=%s: zero allocations\n' "$profile" "$policy"
done
