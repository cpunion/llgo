# LLGo debug sessions

`llgo debug` is the target-aware entry point for building and debugging an
LLGo program:

```sh
llgo debug [build flags] [package] [-- debugger arguments...]
```

It enables DWARF, uses `-O0` unless an optimization level was selected, builds
one executable package, and owns any temporary artifact and local debug-server
process. An explicit `-o` keeps the artifact. `-ldflags=-w` and
`-debug-artifact=none` are rejected because the resulting program cannot be
source-debugged.

The automatic backend depends on the selected target:

| Target | Backend | Session transport |
| --- | --- | --- |
| Native Darwin/Linux | LLDB | Local process |
| Non-Wasm embedded | GDB | Target `debug-server`, OpenOCD, or `-remote` |
| WASI | Wasmtime guest-debug + Wasm-aware LLDB | Built in |
| Browser Wasm | Browser DevTools | Added by the browser debugger task |

Use `-backend=gdb` or `-backend=lldb` to override a native or GDB Remote
session, and `-gdb` or `-lldb` to select a debugger executable. For an already
running server, `-remote=host:port` skips server startup. A target's
`debug-server` command can use `{}` or `{elf}` for the host debug artifact and
`{debug-port}` for an automatically allocated loopback port. Targets with
OpenOCD interface/transport/target fields need no additional command.

`llgo lldb` remains the explicit compatibility command for opening an existing
artifact without building it.

## WASI tool matrix

WASI guest debugging uses Wasmtime's built-in gdbstub and LLDB's WebAssembly
process plugin:

- Wasmtime 44 or newer (`-g`/`--gdbstub` support);
- upstream or wasi-sdk LLDB 22 or newer with the `process/wasm` plugin;
- Python scripting in LLDB for LLGo runtime formatters.

The wasi-sdk 33 LLDB build supports source breakpoints, parameters, locals,
stepping, and Wasm call stacks, but is built without Python. `llgo debug`
detects that capability and continues in raw source-debug mode with a clear
notice. A scripting-capable Wasm LLDB additionally loads the shared LLGo
runtime adapter and reads the `llgo.debugger` custom-section record.

The current LLGo WASI runtime imports a shared linear memory. Wasmtime 47 can
debug Wasm locals but does not yet expose `SharedMemory` through its guest-debug
RSP memory map, so globals and runtime-backed formatters remain gated by
[Wasmtime issue #14062](https://github.com/bytecodealliance/wasmtime/issues/14062).
This does not affect source breakpoints or stack/parameter/local inspection.

An embedded-DWARF module is currently required for the automated Wasmtime
session. External Wasm DWARF remains a valid build artifact, but debugger-side
resolution is part of the external/browser acceptance work.

Run the focused fixture with:

```sh
LLGO_WASMTIME=/path/to/wasmtime \
LLGO_LLDB=/path/to/wasm-aware/lldb \
bash cmd/llgo/debugtest/wasi/runtest.sh
```
