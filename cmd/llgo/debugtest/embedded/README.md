# Embedded debugger transport fixture

This fixture validates the automated embedded path of `llgo debug`. It builds
a host-side Cortex-M ELF with DWARF, starts and stops the target-configured QEMU
GDB server, derives unchanged flash bytes, and runs two independent sessions:

- `gdb-multiarch` through GDB Remote;
- LLDB through `gdb-remote` with an explicit zero slide.

Run it on Linux with QEMU's ARM system emulator, GDB, LLDB 19 or newer, and the
LLVM 19 tools on `PATH`:

```sh
bash cmd/llgo/debugtest/embedded/runtest.sh
```

For a physical target with OpenOCD configuration, `llgo debug` starts OpenOCD,
loads the image, and connects the GDB listed by the target configuration:

```sh
llgo debug -target=rp2040 .
```

To use an externally managed OpenOCD session instead, keep the same host ELF
and connect to its GDB port:

```sh
llgo debug -target=rp2040 -remote=:3333 .
```

The guarded, repeatable probe acceptance and its explicit flash confirmation
are documented in [`../hardware`](../hardware/README.md). Ordinary CI never
claims ownership of a physical probe.
