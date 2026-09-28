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
GOEXPERIMENT=simd llgo test -O2 -target wasi -emulator -count=1 -timeout=2m ./test/simd/...
```

WASI execution requires Wasmtime and LLGo's supported Binaryen (`WASMOPT`).
CI runs the native suite on amd64/arm64 and the WASI suite in the existing wasm
test-command job. Native LLGo runs at O0 and O2; WASI runs at O2 because the unoptimized
standard testing framework exceeds Wasmtime's local-variable limit. The current suite
covers only the implemented SIMD128 operations; more operation families can
add their own test files here.
