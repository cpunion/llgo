//go:build wasm && !llgo.wasm.emscripten

package ffi

// libffi's WebAssembly backend supports the Emscripten ABI but not
// the WASI C ABI. Keep the low-level boundary linkable for test binaries that
// do not use dynamic reflection; unsupported calls fail explicitly in the
// stub instead of accidentally linking the host machine's libffi archive.
const LLGoFiles = "_wrap/libffi_wasm_stub.c"
