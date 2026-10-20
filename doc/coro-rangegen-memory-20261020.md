# Rangegen compilation memory follow-up

PR #300 merged at `95699feec5f3795a67fb31437510d8aa2296d364` on
2026-10-20. [PR #301](https://github.com/cpunion/llgo/pull/301) completes the new
S29 local acceptance campaign at production commit
`cf54d9f6d634a526488a9512e41d48ccba838980`. All four native controls, complete
native test/go, 1,082 selected GOROOT cases, 180 stdlib fixtures and 73 demos are
accounted for on one exact source/compiler freeze. GOROOT has 1,070 actual passes
and twelve existing expected failures; their twelve raw failures have been
reviewed separately. Fresh performance, size and parked-memory measurements are
complete. All campaign domains joined and identities match afterward.

The [final acceptance report](coro-acceptance-20261020-pr301.md) records identities,
retained failures, process joins and platform boundaries. The
[fresh comparison](coro-performance-20261020-pr301.md) retains shared-host ranges
and the measured spawn/I/O and executable-size costs. S28 receipts remain
historical. Integration requires a documentation-only equivalence proof,
exact final-head 20-job CI and a fresh complete review/comment/thread audit.

## Production S29 acceptance

The frozen SDK manifest is
`c2ace95d379499905ea42bfad469595eb6962c8fc9839e3dc347ae077f728b70`.
The freshly built production compiler SHA-256 is
`7b04ddcb13e50c9615f6b3a7c19c112582e5fa64a55d5fe1e4c7fe4fe1dbd778`.
The source archive is taken from the clean tracked production commit, with the
six original untracked `.new` files excluded and preserved unchanged.

Exact-head CI [run 39380407147](https://github.com/cpunion/llgo/actions/runs/39380407147)
passes 20/20. Full native test/go passes **276 top-level / 614 including subtests**,
with zero FAIL/SKIP and all **45 nested compiler domains** joined and reclaimed.
The original native controls all pass under their unchanged limits:

| Case | LLGo compile | LLGo run | Inner peak RSS |
|---|---:|---:|---:|
| rangegen.go | 345.357s | 1.129s | 10,192.5 MiB |
| chan/sieve1.go | 13.318s | 0.969s | not separately reported |
| chan/sieve2.go | 23.134s | 0.293s | not separately reported |
| fixedbugs/issue9604b.go | 271.575s | 0.312s | 9,815.7 MiB |

Rangegen's preflight has only **47.5 MiB sampled headroom** under the original
10,240 MiB inner process-group limit. Its separate execution in the 1,082-case
corpus also passes: **624.057s compilation, 1.251s execution and 7,273.4 MiB**
sampled inner peak. All three generated source copies match the original
6,399,112-byte input and SHA-256 below. The four-case corpus batch joined, with
unchanged SDK/compiler identities; the complete corpus is now accounted for.
The existing not-applicable classification prints an actual-success marker for
this execution and is not counted as an expected failure. No expectation rule
was changed. Both observations remain retained; they do not establish a
universal memory or compilation-speed bound on the shared host.

The other three controls also pass inside the selected corpus, with their
batches joined: sieve1 compiles/runs in 22.900s/0.294s, sieve2 in 32.114s/0.301s,
and issue9604b in 343.045s/0.377s at 9,660.1 MiB sampled peak. All four original
controls therefore have separate successful preflight and full-corpus executions
on this same freeze. The full 1,082-case selection is complete. Supplemental runs
with external expectation labels disabled retain nine explicit test-assertion panics, two
execution deadlines and one output mismatch. Every raw error was reviewed; no
new expectation rule or entry Invalid(0)/SIGABRT outcome was observed.

The first 93 stdlib fixtures completed with their original bounds. After the main
GOROOT domains joined, the host showed 49.4% CPU idle. Both stdlib workers then
retired strictly between fixtures, with zero test or compilation interruption,
all process domains joined and source-after checks matching. The remaining 87
fixtures completed in four disjoint cohorts of 22/22/22/21 on the same SDK/compiler.
The original 60m build, 7m run, 6m test, per-stage RSS, scratch/cache and disk-floor
bounds remain; aggregate RSS remains 40 GiB. Original partial-controller records
and every completed image/log remain retained. The second window took 10,726.266
seconds. All 180 unique fresh images, 2,127 top-level / 2,405 total PASS outcomes,
source-after checks and final process joins are verified. This is completion on
one source/compiler across two scheduling windows, rather than a single
eight-hour aggregate claim.

The native-control and full-native-test receipts verify unchanged SDK/compiler
identities after their owned domains join. All review/comment/thread pages have
been read at this production head; a fresh audit is required at the final
integration head.

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

The diagnostic SDK manifest is
`a19b6bc205cdbd2c0081d478f75c1af099aa8f5e7a5e99473ae3e819dce1feae`
and compiler SHA-256 is
`4db891e08049850a079f8b9d29a8ccb764b8c9f8c13abbdb7e16a38cf1983e30`.
Neither diagnostic identity is substituted for the archived production freeze
above. Evidence is retained under `rangegen-followup-20261020-r1`, including
`paired-native-outliner-result.json`, both complete native case directories,
`generated-input-identity-audit.json` and every earlier failure.

Deterministic regressions cover scalar cleanup ordering, the actual LLVM 22
machine-function pass boundary, suspended frame owners, metadata-only roots,
public symbols and target layout with local names discarded, and compaction
write failures. DCE-only and retained-machine-function controls fail as expected;
the combined trace-free candidate's focused tests pass. Existing full-LTO
elision and native staging controls also pass.

## Integration protocol

All new S29 local gates are complete. Native test/go passes 276 top-level / 614
including subtests, with all 45 nested compiler domains joined and reclaimed.
The 1,082 GOROOT cases comprise 1,070 actual passes and twelve existing expected
failures; all twelve have new raw diagnostics and reviewed classifications.
All 180 stdlib fixtures and 73 fresh demo images pass. The extra 1,316 diagnostic
and multifile GOROOT cases remain outside this runtime selection.

The fresh Go/LLGo comparison completes all 272 throughput/progress/peak runs and
80 parked-memory runs. At 100,000 parked goroutines, median steady RSS is LLGo
159.016 MiB versus Go 267.828 MiB. Spawn, some I/O workloads and stripped file
sizes retain explicit costs. The full local execution campaign is Darwin/arm64;
CI coverage does not extend these measurements to other platforms. Shared-host
load and precise-GC/profiling/gc-stack-hook limits remain documented.

The final documentation-only child must prove every other tracked mode, type
and blob identical to the accepted production commit. It must then pass its own
exact-head 20-job CI. Inspect every review, inline-comment, issue-comment and
review-thread page again at the final head; check stable head/base identities,
retained untracked files and the unchanged remote target. Merge into
`cpunion/llgo:llvm-coro` only after those checks, then verify the merge parents
and that its file tree equals the accepted head. These final integration
receipts identify the documentation head without relabeling runtime results.
