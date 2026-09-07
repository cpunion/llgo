#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
export LLGO_ROOT="$repo_root"
export GOENV=off

llgo=${LLGO:-llgo}
browser=${LLGO_BROWSER:-}
if [[ -z "$browser" ]]; then
	browser=$(command -v google-chrome || command -v google-chrome-stable || command -v chromium || true)
fi
if [[ -z "$browser" ]]; then
	echo "WebAssembly browser acceptance requires Chrome or Chromium" >&2
	exit 1
fi

work_dir=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/llgo-wasm-browser.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT
export LLGO_BROWSER="$browser"
export LLGO_WASM_BROWSER_REPORT_DIR="${LLGO_WASM_BROWSER_REPORT_DIR:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/wasm-browser}"

node --test dev/wasmbrowser/browser.test.mjs

cp dev/wasmbrowser/browser.html "$work_dir/browser.html"
GOOS=js GOARCH=wasm "$llgo" build \
	-o "$work_dir/timers-gjs.mjs" ./internal/build/testdata/wasm-timers
"$llgo" build -target emscripten \
	-o "$work_dir/timers-ec32.mjs" ./internal/build/testdata/wasm-timers
"$llgo" build -target emscripten-memory64 \
	-o "$work_dir/timers-ec64.mjs" ./internal/build/testdata/wasm-timers
node dev/wasmbrowser/run.mjs "$work_dir" timers-gjs.mjs timers-ec32.mjs timers-ec64.mjs
