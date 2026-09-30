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
| Native Darwin/Linux/Windows | LLDB | Local process |
| Non-Wasm embedded | GDB | Target `debug-server`, OpenOCD, or `-remote` |
| WASI | Reserved | Separate frontend contribution; WAMR remains the execution runtime |
| Browser Wasm | Reserved | Separate Chrome frontend contribution |

Use `-backend=gdb` or `-backend=lldb` to override a native or GDB Remote
session, and `-gdb` or `-lldb` to select a debugger executable. For an already
running server, `-remote=host:port` skips server startup. A target's
`debug-server` command can use `{}` or `{elf}` for the host debug artifact and
`{debug-port}` for an automatically allocated loopback port. Targets with
OpenOCD interface/transport/target fields need no additional command.

`llgo lldb` remains the explicit compatibility command for opening an existing
artifact without building it.

Debug information can also be packaged without starting a debugger:

```sh
llgo build -debug-artifact=embedded ./app
llgo build -target=emscripten -debug-artifact=external -o app.mjs ./app
llgo build -target=cortex-m-qemu -debug-artifact=host -obin ./app
llgo build -debug-artifact=none ./app
```

`external` currently applies to Wasm executables. It writes a sibling
`app.debug.wasm`, leaves executable code/data sections and JavaScript glue
unchanged, and records the same build ID and debugger ABI in both modules.
`host` keeps embedded DWARF in the host ELF while `.bin`, `.hex`, and `.uf2`
remain deployment artifacts. Artifact reports list their separate byte sizes;
runtime `.pclntab` sidecars retain an independent role.

The automated embedded acceptance checks source breakpoints, parameters,
locals, globals, backtraces, server cleanup, and flash-byte invariance with
QEMU. [Physical probe acceptance](hardware/README.md) is explicitly opt-in and
is not claimed by a successful QEMU test. Wasm artifact acceptance uses
`dev/test_wasm_debug_info.py --artifact embedded --artifact external`; it
validates packaging and execution rather than debugger session capabilities.
