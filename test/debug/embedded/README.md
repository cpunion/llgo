# Embedded debugger transport fixture

This fixture validates the automated embedded path of `llgo debug`. It builds
a host-side Cortex-M ELF with DWARF, starts and stops the target-configured QEMU
GDB server, derives unchanged flash bytes, and runs two independent sessions:

- `gdb-multiarch` through GDB Remote;
- LLDB through `gdb-remote` with an explicit zero slide.

Run it with QEMU's ARM system emulator, a GDB that supports ARM, LLDB, and the
LLVM 22 tools on `PATH`. `LLGO_GDB` and `LLGO_LLDB` can select their executables:

```sh
bash test/debug/embedded/runtest.sh
```

See the [debugging guide](../../../doc/debugging.md) for user-facing session
commands and the [physical-probe acceptance](../hardware/README.md) for opt-in
hardware validation.
