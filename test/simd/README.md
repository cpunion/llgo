# SIMD behavior tests

Add executable SIMD regressions as ordinary `Test*` functions in this package.
Use `goexperiment.simd` and architecture build tags to select applicable APIs.
The same tests run under official Go and LLGo; LLVM IR/instruction assertions
belong in compiler tests instead.

From the repository root:

```sh
GOEXPERIMENT=simd go test -count=1 ./test/simd/...
GOEXPERIMENT=simd llgo test -O0 -count=1 ./test/simd/...
GOEXPERIMENT=simd llgo test -O2 -count=1 ./test/simd/...

GOEXPERIMENT=simd GOOS=wasip1 GOARCH=wasm go test -exec=wasmtime -count=1 ./test/simd/...
GOEXPERIMENT=simd llgo test -O2 -target wasi -c -o /tmp/simd.test.wasm ./test/simd
wasmtime run -W exceptions=y -W multi-memory=y -W max-wasm-stack=8388608 \
  -W unknown-imports-trap=y /tmp/simd.test.wasm -test.v -test.count=1 -test.timeout=2m
```

WASI execution requires Wasmtime and LLGo's supported Binaryen (`WASMOPT`).
CI runs the native suite on amd64/arm64 and the WASI suite in the existing wasm
test-command job. Native LLGo runs at O0 and O2; WASI runs at O2 because the unoptimized
standard testing framework exceeds Wasmtime's local-variable limit. The current suite
covers only the implemented SIMD128 operations; more operation families can
add their own test files here.

The current partial SIMD implementation leaves unresolved method imports in
reflection metadata (for example, `Float32x4.StoreArray`). WASI execution uses
Wasmtime's `unknown-imports-trap` mode: unused imports do not prevent
instantiation, but executing any unsupported method traps and fails the suite.
This does not establish complete SIMD API support. Remove this accommodation
once those methods are implemented or unreachable method imports are eliminated.
