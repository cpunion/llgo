#!/usr/bin/env bash
set -euo pipefail

profile="${1:?GC profile required}"
evidence="${2:?evidence directory required}"
llgo_cmd="${LLGO:-llgo}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
mkdir -p "$evidence"
args=()
extension=wasm
case "$profile" in
  EC32) args=(-target emscripten); extension=mjs ;;
  EC64) args=(-target emscripten-memory64); extension=mjs ;;
  WC32) args=(-target wasi) ;;
  GJS) export GOOS=js GOARCH=wasm CGO_ENABLED=0; extension=mjs ;;
  GWASI) export GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 ;;
  *) echo "unknown GC profile: $profile" >&2; exit 2 ;;
esac
export LLGO_BUILD_CACHE=on LLGO_WASI_THREADS=0
for tool in timeout "$llgo_cmd"; do
  command -v "$tool" >/dev/null || { echo "required tool missing: $tool" >&2; exit 2; }
done

build_fixture() {
  local name="$1" package="$2"
  local build_dir="$repo_root"
  if [[ "$package" == runtime/* ]]; then
    build_dir="$repo_root/runtime"
    package="${package#runtime/}"
  fi
  if ! (cd "$build_dir"; timeout 5m "$llgo_cmd" build "${args[@]}" \
      -o "$evidence/$name.$extension" "./$package") > "$evidence/$name-build.log" 2>&1; then
    cat "$evidence/$name-build.log"
    return 1
  fi
}

run_guest() {
  local module="$1" policy="$2"
  shift 2
  if [[ "$extension" == mjs ]]; then
    local runner=targets/emscripten-runner.mjs
    if [[ "$profile" == EC64 ]]; then runner=targets/emscripten-memory64-runner.mjs; fi
    env GOGC="$policy" LLGO_GOGC_EXPECT="$policy" timeout 60s node "$runner" "$module" "$@"
  else
    timeout 60s wasmtime run --dir=/ --env PWD --env PATH \
      --env "GOGC=$policy" --env "LLGO_GOGC_EXPECT=$policy" \
      -W exceptions=y -W multi-memory=y -W max-wasm-stack=8388608 "$module" "$@"
  fi
}

positive() {
  local name="$1" package="$2" expected="$3"
  build_fixture "$name" "$package"
  for policy in 100 1; do
    if ! run_guest "$evidence/$name.$extension" "$policy" > "$evidence/$name-$policy.log" 2>&1; then
      cat "$evidence/$name-$policy.log"
      return 1
    fi
    grep -Fxq "$expected" "$evidence/$name-$policy.log"
    printf '%s %s GOGC=%s PASS\n' "$profile" "$name" "$policy"
  done
}

positive standalone runtime/internal/test/gc-standalone 'gc standalone ok'
positive gc internal/build/testdata/wasm-gc 'wasm gc ok'
positive liveness internal/build/testdata/wasm-gc-liveness 'wasm gc liveness ok'
positive lifecycle internal/build/testdata/wasm-lifecycle 'wasm lifecycle ok'
build_fixture pacing internal/build/testdata/wasm-gc-pacing
for policy in 100 1 0 off; do
  if ! run_guest "$evidence/pacing.$extension" "$policy" > "$evidence/pacing-$policy.log" 2>&1; then
    cat "$evidence/pacing-$policy.log"
    exit 1
  fi
  grep -Fxq 'wasm gc pacing ok' "$evidence/pacing-$policy.log"
  printf '%s pacing GOGC=%s PASS\n' "$profile" "$policy"
done

build_fixture reentry runtime/internal/test/gc-reentry
exit_code=0
run_guest "$evidence/reentry.$extension" 1 > "$evidence/reentry.log" 2>&1 || exit_code=$?
if [[ "$exit_code" == 0 || "$exit_code" == 124 || "$exit_code" == 137 ]] || \
    ! grep -Eiq 'unreachable|invalid opcode|wasm.*trap' "$evidence/reentry.log"; then
  cat "$evidence/reentry.log"
  echo "GC reentry must trap promptly, got status $exit_code" >&2
  exit 1
fi
printf '%s reentry REJECTED (unsupported)\n' "$profile"

tag=llgo.wasm.workers
if [[ "$extension" == wasm ]]; then tag=llgo.wasi_threads; fi
exit_code=0
timeout 60s "$llgo_cmd" build "${args[@]}" -tags="llgo.wasm.gc.linear,$tag" \
  -o "$evidence/unsupported.$extension" ./internal/build/testdata/wasm-gc \
  > "$evidence/concurrent-rejection.log" 2>&1 || exit_code=$?
if [[ "$exit_code" == 0 || "$exit_code" == 124 || "$exit_code" == 137 ]] || \
    ! grep -Fq 'supports only a single mutator' "$evidence/concurrent-rejection.log"; then
  cat "$evidence/concurrent-rejection.log"
  echo "concurrent linear GC configuration was not explicitly rejected" >&2
  exit 1
fi
printf '%s concurrent mutators REJECTED (unsupported)\n' "$profile"
