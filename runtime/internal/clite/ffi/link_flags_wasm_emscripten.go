//go:build wasm && llgo.wasm.emscripten

package ffi

// Emscripten does not ship libffi as a system library. Compile the upstream
// WebAssembly backend with the runtime so reflect calls and MakeFunc use the
// target ABI in both wasm32 and Memory64 builds.
const LLGoFiles = "_wrap/libffi/prep_cif.c; _wrap/libffi/types.c; _wrap/libffi/ffi_wasm.c; _wrap/libffi_wasm_adapter.c"
