# WebAssembly exception-handling comparison

Run `python3 dev/compare_wasm_eh.py` with Emscripten, Node, `wasm-tools`,
`llvm-dwarfdump`, and the selected Binaryen installation on `PATH`. Set
`EM_BINARYEN_ROOT` to select a complete Binaryen installation; optionally set
`LLGO` to include the existing Go panic/recover smoke test.

The script compiles the same C++ `throw`/`catch` and `setjmp`/`longjmp` fixture
with Emscripten's legacy EH mode and its direct standard `exnref` mode. It
also runs Binaryen's `--translate-to-exnref` pass on the legacy module. Each
variant is validated, checked for the expected EH instruction family and valid
DWARF structure, then executed with Node at `-O0` and `-O2`.

Local result on 2026-09-23 (Emscripten 6.0.8-git, patched Binaryen 132,
Node 26.8.1, wasm-tools 1.258.0, LLVM 22.1.8):

| Optimization | Legacy EH | Direct exnref | Binaryen translation |
| --- | ---: | ---: | ---: |
| `-O0` | 1,104,085 B | 1,106,840 B | 1,145,283 B |
| `-O2` | 238,188 B | 238,903 B | 238,176 B |

All six variants passed validation, DWARF verification, and execution. The
separate Go panic/recover baseline passed. These measurements are a smoke
comparison, not a performance result. They do not yet test an exception
crossing the Go/C++/JavaScript boundary, stack unwinding through a suspended
goroutine, or browser compatibility. Those must pass before selecting an EH
translation policy for LLGo output.

An exploratory LLGo build of a Go caller and an `LLGoFiles` C++ wrapper that
throws and catches internally did not link with this Emscripten version.
Without C++ link mode, `emcc` reported missing `__cxa_allocate_exception`
and `__cxa_throw`. Adding `-sDEFAULT_TO_CXX` instead left LLGo runtime entry
symbols and Asyncify's `malloc`/`free` unresolved. Adding
`-fwasm-exceptions` also produced Emscripten's Asyncify incompatibility
warning. The standalone C++ results above therefore do not establish that
the current LLGo browser link can use C++ EH; the Go/C++ boundary is an
explicit integration task.
