# Exact-version assembly regressions

This nested module exercises the library versions reported in llgo issues
[#2464](https://github.com/xgo-dev/llgo/issues/2464),
[#2552](https://github.com/xgo-dev/llgo/issues/2552),
[#2576](https://github.com/xgo-dev/llgo/issues/2576),
[#2708](https://github.com/xgo-dev/llgo/issues/2708),
[#2707](https://github.com/xgo-dev/llgo/issues/2707), and
[#2691](https://github.com/xgo-dev/llgo/issues/2691).
The modules and checksums are pinned; no test resolves `latest`.

| Package | Independent oracle and intended scope |
| --- | --- |
| `huff0` | Manual Zstandard Huffman tables/bitstreams (table logs 1 and 9), 8 KiB 1X/4X decode, unaligned destinations, guards and truncated input. No library encoder supplies the expected output. |
| `crc32` | Bitwise polynomial division for IEEE, Castagnoli and a custom polynomial; bulk, threshold, unaligned, update and streaming calls. Hardware dispatch prerequisites are checked and logged. |
| `gohex` | Independent upper/lower nibble alphabets, bulk/unaligned encode/decode and invalid/odd input. AMD64 public AVX/SSE dispatch is logged. |
| `websocket` | Public client frames checked by an independent raw RFC6455 server. In v1.8.15 the public `mask()` uses `maskGo`; a separately labeled private-symbol test directly executes `maskAsm` against bytewise XOR and key-rotation expectations. |
| `modernc` | Linux/AMD64 public `Y__builtin_mul_overflowUint128` with four independently specified 128-bit multiplication/overflow vectors, through the package's ABI0 assembly wrapper. |
| `purego` | Public Dlopen/Dlsym/SyscallN/registered memmove, independent strlen expectations, overlapping-copy effects and missing-symbol error. This is a retained failing llgo regression, not a source rejection or skip presented as success. |

Run with Go 1.27.1 and LLVM 22 selected on `PATH`:

```sh
cd test/asm
go test -count=1 ./...
llgo test -v ./modernc
llgo test -v ./huff0 ./crc32 ./gohex ./websocket ./purego
```

The architecture/source build tags are part of applicability; modernc's test
requires Linux/AMD64, and gohex's assembly test requires AMD64. Required tools or
hardware features fail rather than silently skipping an intended assembly path.
Compilation of an assembly symbol does not prove its execution: for example,
CRC AVX512 and gohex AVX require their logged runtime feature predicates.

The 2026-10-01 diagnostic run used actual Linux/AMD64 execution in an owned
container, Go 1.27.1, LLVM 22.1.8, the current llgo runtime and a local plan9asm
API worktree. Native Go passed all six packages. Actual llgo passed modernc,
huff0, crc32, gohex and websocket (including the direct private kernel); purego
failed at the unchanged `syscall15X` assembly symbol's missing Go declaration.
CRC reported AVX512=false and gohex reported AVX=false/SSE4.1=true, so this run
does not establish execution of those unavailable ISA branches. This diagnostic
is not a passing pinned CI result or a claim that every issue is closed.

The remaining purego entry is a native C/g0 trampoline, not an ordinary Go
function. Its dynamic 15-GP/8-FP call, callbacks, Go closure bridge and fixed
callback-address table need an explicit source/ABI contract. Adding a guessed
Go signature or dropping the callback assembly would not establish correctness.
The Darwin/ARM64 whole-stdlib diagnostic separately retains the unresolved
`runtime.memequal_varlen` R26 closure-entry contract; these Linux results do not
promote that target to success.
