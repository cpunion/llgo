# PCLN metadata packaging

Status: initial implementation. This design is stacked on the typed Go linker
options introduced by #2113.

## Goals

LLGo supports three independent packaging policies for the runtime function
and line metadata used by `runtime.Caller`, `runtime.Callers`,
`runtime.FuncForPC`, and stack traces or pprof symbolization on the available
unwinding paths:

- `-pclntab=embedded` keeps the metadata in the executable. This is the
  default and preserves existing behavior.
- `-pclntab=external` writes `<executable>.pclntab`. The executable loads that
  optional file on the first safe symbolization request. A missing, stale, or
  corrupt file is a cached soft failure; the program continues with the
  available native fallback.
- `-pclntab=none` does not generate the metadata and does not link the external
  loader or its path/probe code.

`-pclntab=external` is initially supported only for native executable builds
without `-target` on Darwin/Linux amd64/arm64. `ModeGen`, `c-archive`,
`c-shared`, named targets, and other OS/architecture combinations reject it.

The policy is deliberately separate from Go's `-ldflags=-s` and `-w`:

- Linked LLGo builds preserve DWARF by default, matching `cmd/link`.
  `-w=false` explicitly preserves it and `-w` explicitly omits it. Darwin
  `c-shared` keeps the platform-specific `cmd/link` default of omitting DWARF.
- `-s` records native symbol-table omission intent and implies `-w` unless
  `-w` is explicitly set; the explicit `-w` value wins regardless of argument
  order. Native symbol-table deletion remains a later phase.
- `-pclntab` controls LLGo runtime symbolization metadata.

`ModeGen` remains DWARF-free by default and emits DWARF only when `-w=false`
is explicitly requested and supported. Target builds derive `embedded`,
`external`, or host-side DWARF packaging from explicit policy and linker
capabilities. A backend that cannot retain DWARF rejects an explicit
`-w=false`; a backend that cannot omit retained input DWARF rejects `-w`.
Because native `-s` stripping is not implemented yet, `-s -w=false` records
the symbol-table intent without deleting native symbols.

The former `LLGO_DEBUG` and `LLGO_DEBUG_SYMBOLS` environment switches are not
part of this policy. Use `-w=false` to retain DWARF, `-w` to omit it,
and `-O0` when a debugger-friendly unoptimized build is required.

DWARF no longer changes whether LLGo runs its normal LLVM optimization
pipeline or C ABI lowering. Debug references are treated as metadata by SSA
compatibility rewrites, C ABI lowering preserves variable homes, and the
DWARF optimized marker follows the selected optimization level. Broad CI
therefore exercises the same DWARF-preserving default as ordinary linked
builds; focused debugger fixtures may still select `-w=false` explicitly to
make their artifact requirement self-documenting.

Consequently all combinations are meaningful. For example,
`-ldflags=-s -pclntab=embedded` keeps Go-compatible runtime symbolization,
while `-ldflags=-s -pclntab=external` keeps it in an optional deployment
artifact.

## Package boundaries

Go-compatible build syntax is parsed once in `internal/goflags`, which is
shared by command entry points and the `flags.txt` golden-test driver.
LLGo-specific command flags such as `-pclntab` remain in
`cmd/internal/flags`. Both paths write only typed `internal/build.Config`
fields; the build package does not parse command strings.

The golden-test `flags.txt` format accepts native Go build flags. It accepts
`-flag=value` and `-flag value`, single or double dashes, blank lines, comments,
shell-style quoting, and multiple ordinary flags on one line. An unquoted
argument-list form such as `-ldflags=-s -w=false` consumes the remainder of its
line; quote that value to put another build flag on the same line. The debug
fixture uses `-ldflags=-w=false`, so ModeGen debug metadata follows the same
linker semantics as the CLI.

This is syntax compatibility, not complete linker-backend support. Among
`-ldflags` options, this change translates only `-s` and `-w` into typed LLGo
semantics; all other linker tokens remain in the normalized raw flag list and
do not yet affect LLGo's native link. Only unpatterned and `all=` `-ldflags`
values are currently interpreted; other package patterns are rejected. The
existing `-gcflags` frontend subset (`-lang`, `-N`, and `-l`) is separate.

IR golden tests remain non-debug unless a fixture explicitly selects
`-w=false`, so their `CHECK-NEXT` fixtures continue to describe stable LLVM IR
and do not depend on the provisional DI implementation. Native integration
and LLDB suites exercise the explicit-DWARF executable path separately.

`internal/build` validates the selected target, enables compiler metadata,
coordinates link finalization, and owns output paths. `internal/pclnpost`
contains host-side ELF/Mach-O inspection and safe binary rewriting.
`internal/pclnmap` owns the versioned sidecar format. The runtime contains a
small build-tag-selected loader and an independent, dependency-minimal
decoder.

This separation keeps both binary-format policy and command syntax out of the
compiler pipeline.

## Link and strip order

Externalization needs final linked PCs and the native symbol table to remove
LTO inline copies. It therefore cannot be implemented only by suppressing IR
generation. The required final-artifact order is:

1. Generate funcinfo and line records, but keep the external payload out of
   the synthetic main module.
2. Link the final executable with temporary link-site sections and a fixed
   identity slot.
3. Analyze the unmodified executable, join final PCs to the encoded records,
   and stage the sidecar.
4. Write the executable identity and clear link-only site data.
5. Apply any future native symbol-table removal requested by `-s`.
6. Perform final platform signing after all executable mutations.
7. Atomically publish the sidecar, then report the executable and sidecar as
   separate outputs.

#2113 currently implements typed `-s`/`-w` intent and DWARF omission; native
symbol-table removal is a later phase. When that phase is added, it must use
the ordering above rather than adding an independent post-link rewrite before
PCLN analysis. The current external detach step signs a mutated Mach-O itself;
that signing responsibility must move to the end of the unified pipeline when
a later native-strip mutation is added.

`-pclntab=none` takes the generation-time path: compiler metadata and link
sites are not produced. `-pclntab=embedded` keeps the existing post-link
prebuilt-table rewrite.

Darwin embedded builds that emit DWARF retain PC-line sites, emitted without a
debug location, but suppress function-entry and closure-stub address sites so
inline anchors do not make LLDB expose inner lexical scopes too early. Linux
retains both classes because most Go symbols are intentionally absent from ELF
`.dynsym`, so `dlsym` alone cannot reconstruct every function entry. External
mode needs the final-address sites on both platforms before it can construct
the sidecar.

## Sidecar identity and addressing

The sidecar header contains a format version, runtime ABI version, target
tuple, pointer size, section descriptors, payload checksum, and the SHA-256
identity of the exact linked executable before detachment. The same identity
is written into a dedicated 32-byte executable section. A sidecar is accepted
only when both values match. It is a pre-mutation pairing token, not the digest
of the final detached, stripped, or signed executable.

All code addresses are stored relative to the lowest file-backed load segment.
At runtime they are relocated with the image base returned by `dladdr`, so the
format works for PIE/ASLR and non-PIE executables without relocations in the
sidecar.

## Loading and failure semantics

Only allocation and I/O-safe public symbolization paths may start a load.
Fatal signal handling never loads or waits; it immediately uses the available
fallback while a load is absent or in progress.

The loader has four states: unattempted, loading, loaded, and unavailable.
One caller performs validation and index construction. Concurrent callers
wait only on safe paths. Both success and failure are terminal and cached, so
the filesystem is never probed repeatedly.

The initial implementation uses one bounded read, capped at 512 MiB, instead
of `mmap`; read-only mapping remains a possible follow-up optimization.

The initial format is intentionally private to LLGo and versioned. It may grow
additional independently removable symbol classes after the PCLN path has
proved stable on ELF and Mach-O.
