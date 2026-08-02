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
| Browser Wasm | Chrome DevTools + LLGo Language Extension | Built in |

Use `-backend=gdb` or `-backend=lldb` to override a native or GDB Remote
session, and `-gdb` or `-lldb` to select a debugger executable. For an already
running server, `-remote=host:port` skips server startup. A target's
`debug-server` command can use `{}` or `{elf}` for the host debug artifact and
`{debug-port}` for an automatically allocated loopback port. Targets with
OpenOCD interface/transport/target fields need no additional command.

`llgo lldb` remains the explicit compatibility command for opening an existing
artifact without building it.

The cross-platform tool pins, CI gates, artifact-size policy, and known
completion blockers are maintained in [ACCEPTANCE.md](ACCEPTANCE.md).

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
session. Browser sessions support embedded DWARF and an adjacent external
sidecar.

Run the focused fixture with:

```sh
LLGO_WASMTIME=/path/to/wasmtime \
LLGO_LLDB=/path/to/wasm-aware/lldb \
bash cmd/llgo/debugtest/wasi/runtest.sh
```

## Browser WebAssembly

Browser source debugging requires Chromium 123 or newer. The headless
acceptance lane pins Chrome for Testing 151.0.7922.71 and validates embedded
DWARF, external DWARF, and a real LLGo browser module. Select another Chromium
binary with `-chrome` or `LLGO_CHROME`.

Launch a development session with:

```sh
llgo debug -target=wasm ./path/to/main
```

`llgo debug` opens an isolated Chrome profile and DevTools, waits for the LLGo
WebAssembly Language Extension to register, and leaves execution behind a Run
button so source breakpoints can be set first. Use
`-debug-artifact=external` to keep DWARF in the adjacent `.debug.wasm`
sidecar. The main module and sidecar carry the same standard WebAssembly
`build_id`; a missing or stale sidecar is rejected before launch.

Build paths can be relocated without rewriting DWARF by repeating a
longest-prefix mapping:

```sh
llgo debug -target=wasm \
  -source-map=/build/checkout=/home/me/checkout \
  ./path/to/main
```

Source paths that remain unavailable are retained for symbolication but are
not advertised as local source files. Optimized builds can omit variable
locations according to DWARF; use `-O0` (the `llgo debug` default) when stable
local inspection is more important than optimized code shape.

When the LLGo extension is unavailable, the launch page still instantiates
and runs the module. DevTools or another consumer can then use the standard
Wasm name section and any separately produced source map for lower-level
symbolication, but Go expressions, scopes, and runtime-value presentation are
unavailable. Pass `-browser-devtools=false` to exercise this fallback without
opening DevTools.
