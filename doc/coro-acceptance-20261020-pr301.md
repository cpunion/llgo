# PR #301: final rangegen follow-up acceptance

[PR #301](https://github.com/cpunion/llgo/pull/301) completes a new S29 local acceptance campaign for the compilation-memory follow-up to merged [PR #300](https://github.com/cpunion/llgo/pull/300). The original rangegen resource failure now passes under its unchanged limits. Existing GC, profiling and stack-hook boundaries remain explicit below. Final documentation-head CI and the fresh review audit are required before integration into `cpunion/llgo:llvm-coro`.

## One production freeze

Production commit: `cf54d9f6d634a526488a9512e41d48ccba838980`. The SDK is an exact tracked-source archive, and the compiler is freshly built from it. The original six untracked `.new` files are excluded from the archive and preserved unchanged. S28 results remain historical; none supplies a missing S29 result.

| Identity | SHA-256 |
|---|---|
| SDK source manifest | `c2ace95d379499905ea42bfad469595eb6962c8fc9839e3dc347ae077f728b70` |
| Fresh compiler | `7b04ddcb13e50c9615f6b3a7c19c112582e5fa64a55d5fe1e4c7fe4fe1dbd778` |

The production commit passes [its exact-head 20-job CI](https://github.com/cpunion/llgo/actions/runs/39380407147). A later documentation-only child must prove identical modes, types and blobs for every other tracked path, then pass its own exact-head 20-job CI. Runtime receipts continue to name this production freeze. The paginated PR reviews, inline comments, issue comments and review threads must be inspected again immediately before integration; remote head/base identities and the merged tree must also match.

## Unified local acceptance

| Selection | S29 result |
|---|---|
| Full native `test/go` | 276 top-level / 614 including subtests PASS; zero FAIL/SKIP; all 45 nested compiler domains joined and reclaimed |
| Selected GOROOT runtime cases | 1,082 accounted for: 1,070 actual passes and 12 existing expected-failure outcomes; zero raw primary runner failures |
| Standard-library fixtures | 180 fresh images PASS; 2,127 top-level / 2,405 including subtests PASS; zero FAIL/SKIP |
| Runnable Go demos | 73 fresh images PASS; all original per-case and aggregate limits checked |
| Go / LLGo comparison | All 272 throughput/progress/peak and 80 parked-memory runs complete; no host sleep during measurement |
| Twelve existing expected diagnostic cases | External labels disabled; all actual observations manually reviewed and retained |

All owned campaign processes have retired, and source/compiler/corpus/image identities match afterward. The extra 1,316 diagnostic/multifile GOROOT cases remain outside this selection. Supplemental controls and diagnostic repetitions do not increase the selected-case count.

## Original controls and compilation cost

All four controls pass both fresh preflight and separate selected-corpus executions. The unchanged inner bounds are 25 minutes to compile, one minute to execute and 10,240 MiB sampled process-group RSS. SDK/compiler identities match after every owned control domain joins.

| Case | Preflight compile / run | Preflight peak MiB | Corpus compile / run | Corpus peak MiB |
|---|---:|---:|---:|---:|
| `rangegen.go` | 345.357s / 1.129s | 10,192.5 | 624.057s / 1.251s | 7,273.4 |
| `chan/sieve1.go` | 13.318s / 0.969s | not separately reported | 22.900s / 0.294s | not separately reported |
| `chan/sieve2.go` | 23.134s / 0.293s | not separately reported | 32.114s / 0.301s | not separately reported |
| `fixedbugs/issue9604b.go` | 271.575s / 0.312s | 9,815.7 | 343.045s / 0.377s | 9,660.1 |

The worst rangegen observation leaves only **47.5 MiB sampled headroom**. These shared-host observations do not establish a universal memory bound or a controlled compilation-speed improvement. The generated 6,399,112-byte rangegen input is unchanged: SHA-256 `9d8f29f06e42b86805069f5ac1b419c073df24fae91f42eb1b1e2501b8919395`. All three live-captured corpus copies match the preserved preflight sources. Its old not-applicable label emitted the actual-success marker; no expectation rule was changed.

The backend changes, causal paired measurements, ownership/error-path regressions and retained unsuccessful prototypes are described in [the compilation record](coro-rangegen-memory-20261020.md). The five runtime shutdown corrections and their deterministic red/green evidence remain in the [PR #300 report](coro-acceptance-20261019-pr300.md); the successful S29 sieve executions above test the inherited runtime with this newly frozen compiler.

## Scheduling and retained controller records

The primary GOROOT controllers completed 1,081 unique cases. The load-sensitive `fixedbugs/issue78081.go` ran only after owned standard-library compilation joined, keeping its original limits. All 1,082 paths are present exactly once in the final receipt.

The first two stdlib workers completed 93 fixtures. After the main GOROOT domains joined and 49.4% CPU idle was observed, both workers retired strictly between fixtures. No compiler or test domain was interrupted. The remaining 87 fixtures completed in four disjoint cohorts of 22/22/22/21, on the same source/compiler with the same per-fixture bounds and 40 GiB aggregate RSS cap. Original partial-controller records and all 93 images/logs remain retained. This is a same-freeze completion across two scheduling windows, rather than a claim that all 180 fixtures ran in one eight-hour aggregate.

Idle post-processing controllers were also retired and recreated before admitting any test domain, so waiting for owned compilation stays outside the deferred-case timer. Both replan receipts retain process joins and zero admitted/interrupted test domains. No S28 aggregate-timeout or host-suspend incident is substituted for S29 evidence.

## Existing diagnostic boundaries

The same twelve already-selected cases were repeated with only their external expectation labels disabled. The SDK, compiler, runner, source programs and original 25m/1m/10 GiB limits remain unchanged. A raw failure remains a failure; this supplemental review neither adds selected cases nor changes primary expectation rules.

| Case | Raw result | Observed error | Existing boundary |
|---|---|---|---|
| `fixedbugs/issue15281.go` | FAIL | BUG: f1: after alloc: expected delta below 1MB, got:  10498048; BUG: f2: after alloc: expected delta below 1MB, got:  10489856 | Existing conservative-GC/precise-liveness applicability boundary |
| `fixedbugs/issue24491b.go` | FAIL | One-minute native execution deadline (exit -1) | Existing conservative-GC/precise-liveness applicability boundary |
| `fixedbugs/issue27518b.go` | FAIL | panic: heap object finalized at the wrong time (exit 2) | Existing conservative-GC/precise-liveness applicability boundary |
| `fixedbugs/issue32477.go` | FAIL | panic: heap object finalized at the wrong time (exit 2) | Existing conservative-GC/precise-liveness applicability boundary |
| `fixedbugs/issue45045.go` | FAIL | One-minute native execution deadline (exit -1) | Existing conservative-GC/precise-liveness applicability boundary |
| `fixedbugs/issue46725.go` | FAIL | panic: never GC'd (exit 2) | Existing conservative-GC/precise-liveness applicability boundary |
| `fixedbugs/issue54343.go` | FAIL | panic: never GC'd (exit 2) | Existing conservative-GC/precise-liveness applicability boundary |
| `fixedbugs/issue57823.go` | FAIL | panic: never GC'd (exit 2) | Existing conservative-GC/precise-liveness applicability boundary |
| `heapsampling.go` | FAIL | panic: main.allocInterleaved1:0 want objects in [45000: 55000], got [0 0 0] (exit 2) | Existing native allocation-stack sampling/profile limitation |
| `maymorestack.go` | FAIL | panic: mayMoreStack not called (exit 2) | Existing gc-toolchain growable-stack debug-hook applicability boundary |
| `stackobj.go` | FAIL | panic: expected collection at phase 1, got phase 3 (exit 2) | Existing conservative-GC/precise-liveness applicability boundary |
| `stackobj3.go` | FAIL | panic: bad liveness (exit 2) | Existing conservative-GC/precise-liveness applicability boundary |

Observed classification counts: 1 output-mismatch, 2 run-deadline, 9 test-assertion-panic. Test-assertion panics retain their exit code and explicit assertion text. Execution-deadline interpretations follow the unchanged finalizer-dependent source; they are source-based inference, not captured parked frames. The reviewed diagnostics contain no native entry `Invalid(0)`, SIGABRT or SIGSEGV outcome. All logs, available source/image artifacts and their hashes remain in the classification receipt.

## Performance and platform scope

The [fresh S29 comparison](coro-performance-20261020-pr301.md) records the same-source Go/LLGo workloads, seven alternating measured pairs, stripped file sizes, all raw ranges, external native library dependencies and steady versus full-lifecycle peak RSS at up to 100,000 parked goroutines. No timed sample is discarded. Shared-host load is recorded and remains a limitation; the result is not a general regression budget.

This full local execution campaign covers Darwin/arm64 with Go 1.27.0 and LLVM 22.1.8. CI coverage is a separate observation and does not turn these runtime measurements into Linux, Windows, other-architecture or WebAssembly acceptance. Precise-GC/finalizer liveness, native allocation-stack profiling and the gc growable-stack debug hook retain the boundaries observed above. File sizes exclude the external deployment closure, and process RSS is not an exact coroutine-frame size.

## Sealed evidence

Receipts are retained under the local `codegen-cost-s1` acceptance evidence directory. Each receipt identifies the frozen source/compiler and recursively names the raw logs, results, commands and artifacts used to establish its conclusion.

| Receipt | SHA-256 |
|---|---|
| `fulltestgo-final-receipt-s29.json` | `87e5a658f822e080b0135880afe34b0b996c4e20c8fb3eea335f08a05a36c891` |
| `goroot-final-receipt-s29.json` | `13d0cfd0ba4a1088f432e0ff252e453c09b4acb444ba9c386e64fb8113f13d66` |
| `stdlib-final-receipt-s29.json` | `31387882339e718497de8aabc636649b57695008373a13a39a900feaf366d944` |
| `demos-primary-final-receipt-s29.json` | `a2a6bf7a2ef18c75c36d72e07f63b94ca94368acac797e6d2fb3c9366e45bbdb` |
| `compatibility-final-receipt-s29.json` | `d72299da4db973fb2f70bbf8f5704ce8f16f5052af645a3171e94108cd0412e6` |
| `native-controls-final-s29-receipt.json` | `4f66482529391b69845fffb75d8d8636f53963b1e48dc7e4051964b1579fd779` |
| `native-controls-full-corpus-receipt-s29.json` | `1bd9898393146c5c2676698137f0ed10dad6cadd406d3ac41ff33ab092250f29` |
| `goroot-expected-classification-final-s29.json` | `52f8048d48aacbbdd254220695a6c0c089c9f9b735130ca1049adff7bed4e140` |
| `performance-power-final-s29.json` | `4a152266b200b1527b815a33485a089d5998250d01179657885fa68de9bfd2fd` |
| `perf-final-s29/final-report.json` | `cdf89419ffe0be37b4ec4f3088c4a0a1e51151fd2a98223480afe330220c5570` |
| `final-reviewed-receipt-s29.json` | `8970e025aa0baef7cb9fe0a44db3bd8644ef51166e0707013cfaca76c91531eb` |
