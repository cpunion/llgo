#!/usr/bin/env bash

set -euo pipefail

if [[ -z "${LLGO_WASMTIME:-}" ]]; then
  echo "LLGO_WASMTIME must name Wasmtime 44 or newer" >&2
  exit 2
fi
if [[ -z "${LLGO_LLDB:-}" ]]; then
  echo "LLGO_LLDB must name LLDB 22 or newer with the Wasm process plugin" >&2
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../../../.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

export LLGO_ROOT="${repo_root}"
export GOMEMLIMIT="${GOMEMLIMIT:-4GiB}"
export GOMAXPROCS="${GOMAXPROCS:-4}"

dwarfdump="${LLGO_DWARFDUMP:-}"
if [[ -z "${dwarfdump}" ]]; then
  dwarfdump="$(command -v llvm-dwarfdump || command -v llvm-dwarfdump-19 || true)"
fi
if [[ -z "${dwarfdump}" ]]; then
  echo "llvm-dwarfdump is required to verify the final WASI artifact" >&2
  exit 2
fi

(cd "${repo_root}" && go build -o "${tmp_dir}/llgo" ./cmd/llgo)

(
  cd "${script_dir}"
  "${tmp_dir}/llgo" debug \
    -target=wasip1 \
    -o="${tmp_dir}/program.wasm" \
    -lldb="${LLGO_LLDB}" \
    -wasmtime="${LLGO_WASMTIME}" \
    . -- \
    --batch \
    -o 'breakpoint set --file main.go --line 8' \
    -o continue \
    -o 'frame variable value result' \
    -o bt \
    -o continue
) 2>&1 | tee "${tmp_dir}/session.log"

grep -F 'main.increment(value=41)' "${tmp_dir}/session.log"
grep -F '(int) result = 42' "${tmp_dir}/session.log"
grep -F 'main.main at main.go:' "${tmp_dir}/session.log"
grep -F 'Process 1 exited with status = 0' "${tmp_dir}/session.log"

"${dwarfdump}" --verify --error-display=quiet \
  "${tmp_dir}/program.wasm" 2>&1 | tee "${tmp_dir}/dwarf.log"
grep -Fx 'No errors.' "${tmp_dir}/dwarf.log"
