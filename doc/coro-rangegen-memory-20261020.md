# Rangegen compilation memory follow-up

PR #300 merged at `95699feec5f3795a67fb31437510d8aa2296d364` on
2026-10-20. This follow-up is **not ready to merge**. The diagnostic prototype
passes the original rangegen resource limits; the trace-free committed compiler,
exact-head CI, paginated review audit and complete new acceptance are pending.
S28 receipts remain historical and do not establish this version's results.

## Changes

The non-LTO staged native backend folds the allocation conditions resolved by
CoroCleanup, then runs GlobalDCE before the complete requested LLVM optimizer.
This removes unreachable cleanup/noalloc clones before IPSCCP builds its
module-wide analyses. Ordinary LLVM reachability retains resume/destroy bodies
and explicit `llvm.used`/`llvm.compiler.used` roots.

Optimized detached contexts discard local value names before parsing. Global
symbols, named types, metadata and target layout are retained. Frontend, debug
and IR-dump contexts keep their names. For inputs of at least 64 MiB, the pruned
module is serialized and reloaded into a fresh owning Program before optimization;
the old context's uniquing tables are released. Error paths retain a valid owner.

Large AArch64 native packages disable MachineOutliner on their own TargetMachine.
This module pass separates instruction selection from printing/freeing and would
otherwise retain every expanded machine function. Without that barrier, each
function can reach assembly output and be freed before the next. Both `arm64-`
and `aarch64-` triple spellings are handled. Small packages retain the target's
outlining policy. The requested IR optimization and code-generation level remain
in effect. Full LTO retains its separate presplit coro.id/noalloc elision path.

## Causal evidence, before the production freeze

All diagnostic cases use Go 1.27.0 and LLVM 22.1.8 on Darwin/arm64. The original
limits remain 25 minutes to compile, one minute to run and 10,240 MiB sampled
process-group RSS. The disk stop floor remains 10 GiB.

Post-CoroCleanup scalar cleanup and GlobalDCE reduced one preserved lowered
input from 265,481,840 to 199,181,376 bytes. All 6,348 unreachable cleanup clones
and 806 noalloc clones were removed; all 6,348 resume/destroy pairs remained.
GlobalDCE alone did not remove the cleanup clones.

On that same preserved, optimized stage input, disabling MachineOutliner reduced
standalone LLVM object-emission peak RSS from **9,594.7 to 3,131.8 MiB**. Both
objects contain **72,117,984 bytes** and have identical SHA-256
`b931e510fac4b2a5ebff43fd80678148bd319899aaef988969bd4732e8f4c329`.
This stage retains fake-use markers; it is causal backend evidence, not a
production executable acceptance result. Host load varied, so the stage timings
are not a controlled speed comparison or a universal code-size claim.

The combined diagnostic prototype completed both original native controls:

| Case | LLGo compile | LLGo run | Inner peak RSS | Outcome |
|---|---:|---:|---:|---|
| rangegen.go | 491.267s | 1.044s | 10,098.9 MiB | PASS |
| fixedbugs/issue9604b.go | 371.575s | 0.306s | 8,906.2 MiB | PASS |

Rangegen's sampled inner headroom is only **141.1 MiB**. This does not establish
a generous or universal memory bound. Both domains joined with unchanged source
identities. All 33 retained generated-input copies checked at that point match
the original 6,399,112-byte Go source, SHA-256
`9d8f29f06e42b86805069f5ac1b419c073df24fae91f42eb1b1e2501b8919395`.

The diagnostic SDK manifest is `a19b6bc205cdbd2c0081d478f75c1af099aa8f5e7a5e99473ae3e819dce1feae` and compiler SHA-256 is
`4db891e08049850a079f8b9d29a8ccb764b8c9f8c13abbdb7e16a38cf1983e30`. Neither identity is substituted for the future production
freeze. Evidence is retained under `rangegen-followup-20261020-r1`, including
`paired-native-outliner-result.json`, both complete native case directories,
`generated-input-identity-audit.json` and every earlier failure.

Deterministic regressions cover scalar cleanup ordering, the actual LLVM 22
machine-function pass boundary, suspended frame owners, metadata-only roots,
public symbols and target layout with local names discarded, and compaction
write failures. DCE-only and retained-machine-function controls fail as expected;
the combined trace-free candidate's focused tests pass. Existing full-LTO
elision and native staging controls also pass.

## Required integration gates

The committed trace-free compiler must complete fresh native controls and
exact-head CI, and every review/comment/thread page must be inspected. One new
source/compiler freeze must then account for full native test/go, all 1,082
selected GOROOT runtime cases, all 180 stdlib fixtures and all 73 runnable Go
demos. Existing expected failures must remain visible and classified. The extra
1,316 diagnostic/multifile cases remain outside this selection.

Fresh Go/LLGo performance, executable size and parked-goroutine memory results
must identify that same freeze. The prior S28 platform and GC/profiling boundaries
remain evidence for S28 only. Integration into `cpunion/llgo:llvm-coro` is allowed
only after the new gates and final exact-head checks are satisfied.
