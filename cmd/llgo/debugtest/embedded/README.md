# Embedded debugger transport fixture

This fixture is the manual protocol baseline for LLGo embedded debugging. It
builds a host-side Cortex-M ELF with DWARF, derives unchanged flash bytes, and
uses the original ELF in two independent QEMU sessions:

- `gdb-multiarch` through GDB Remote;
- LLDB through `gdb-remote` with an explicit zero slide.

Run it on Linux with QEMU's ARM system emulator, GDB, LLDB 19 or newer, and the
LLVM 19 tools on `PATH`:

```sh
bash cmd/llgo/debugtest/embedded/runtest.sh
```

For a physical OpenOCD target, keep the same host ELF and connect the GDB
listed by the target configuration to the server's default port:

```text
target extended-remote :3333
monitor reset halt
load
```

The automated `llgo debug` process/server orchestration is intentionally built
on top of this artifact and transport contract rather than changing it.
