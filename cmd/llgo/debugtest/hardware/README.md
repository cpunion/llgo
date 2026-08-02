# Opt-in physical probe acceptance

This test runs the shared embedded source-debug fixture on a real target. It is
never part of ordinary CI and refuses to start unless the operator explicitly
confirms that the selected device may be halted and flashed.

The normal path uses the target's checked-in OpenOCD and GDB configuration:

```sh
LLGO_HARDWARE_CONFIRM=flash \
LLGO_HARDWARE_TARGET=rp2040 \
LLGO_HARDWARE_GDB=arm-none-eabi-gdb \
bash cmd/llgo/debugtest/hardware/runtest.sh
```

The test builds a host-side ELF with DWARF, starts the configured probe server,
loads the ELF, stops at the fixture's source breakpoint, checks parameters,
locals, aggregates, globals, and the backtrace, then detaches. Set
`LLGO_HARDWARE_SERVER` to override the target's server command.

An already running GDB Remote server can be selected with
`LLGO_HARDWARE_REMOTE=host:port`. Remote and custom-server modes do not alter
target memory by default, so the exact generated ELF must already be present on
the device. Set `LLGO_HARDWARE_LOAD=1` only when that server is allowed to reset
and load the new ELF.

Only connect one test process to a probe. The command intentionally has no
automatic retry: loss of probe ownership, power, reset, or transport should
remain visible to the operator.
