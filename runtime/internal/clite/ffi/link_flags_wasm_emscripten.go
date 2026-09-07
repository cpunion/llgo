//go:build js && wasm

package ffi

// The raw Go-compatible js/wasm profile and the named Emscripten profiles use
// the same physical Emscripten ABI. Emscripten does not ship libffi as a system
// library, so compile the upstream WebAssembly backend with the runtime for
// GJS, wasm32, and Memory64 reflect calls and MakeFunc.
const LLGoFiles = "_wrap/libffi/prep_cif.c; _wrap/libffi/types.c; _wrap/libffi/ffi_wasm.c; _wrap/libffi_wasm_adapter.c"
