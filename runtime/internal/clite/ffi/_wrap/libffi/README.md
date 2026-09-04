# libffi WebAssembly backend

This directory contains the files required to build the WebAssembly backend
from libffi 3.7.1. They are compiled only for LLGo's Emscripten profiles;
native targets continue to use their installed libffi.

The generated `ffi.h` and `fficonfig.h` were produced by libffi's Emscripten
configure build. LLGo changes their architecture and `size_t` selection to use
compiler pointer-size macros, allowing one source snapshot to serve both
wasm32 and Emscripten Memory64. Local includes replace installed-header
lookups. The upstream license is in `runtime/LICENSES/Libffi-MIT.txt`.
