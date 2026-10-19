# Selected LLVM coroutine acceptance: 2026-10-18

The selected implementation, integration, compatibility and measurement checkpoint is complete. Four raw GOROOT failures, twelve existing expected-failure outcomes and twenty-two stdlib skips remain explicit; this is not full Go or all-platform compatibility.

Production freeze `828bfe06cb19879e2c8188a7fb41ba249077aa74`, source manifest `fdd0207f012878b4a0bdda9a362b8520a569e87c91441143e08b76c0b43a4fc2`, compiler SHA-256 `6b7d52bc7ece1fe1d92913747c26bbf2290e15a5dfaa0dfd202b5c4da49830ac`. The four compatibility scopes and refreshed performance samples use this one frozen production source/compiler pair. Host/CI/timer-fixture follow-ups retain their own identities; no historical R6 outcome counts as an R8 pass.

[PR #297](https://github.com/cpunion/llgo/pull/297) merged into `cpunion/llgo:llvm-coro` as `603c781ddcbda072d1b2812b53831d1e443ae93e`. [Exact-head CI](https://github.com/cpunion/llgo/actions/runs/39111825084) at `6fcca2681` passes 20/20, local native/WASI timer suites pass all three fixtures each, and the merged file tree matches the tested head. Fresh paginated reviews, inline/issue comments and review threads were empty at merge.

| Scope | Selected | Recorded result |
|---|---:|---|
| test/go | 276 tests | 276 PASS; no FAIL/SKIP; 45 nested compiler domains joined |
| Executable Go demos | 73 directories | 73 PASS; no FAIL/SKIP |
| Stdlib fixture packages | 180 packages | 180 package commands PASS; individual test skips below |
| GOROOT CI runtime cases | 1,082 unique cases | 1,078 runner PASS / 4 FAIL; actual assessments below |

All selected package and case sets are checked for uniqueness and completeness; source/compiler/corpus after-checks and controller/child closure evidence are required. The stdlib httptest entry is a new same-freeze full-package image, also executed 2,000 consecutive P=2 times; the remaining 179 packages use fresh isolated worker runs. Demo success records normal compilation/execution of the selected Go examples.

The initial four-worker stdlib controller reached the unchanged 40 GiB aggregate RSS ceiling at 40,962.1 MiB and exited 137. Its 125 complete successful package receipts were retained only after an external retirement audit verified all owned processes absent, matching source/compiler and image hashes, and unchanged original journals. Original crash locks and interrupted workspaces remain retained; no synthetic source-after event was added. The 54 missing/interrupted packages were freshly run by two workers under the same aggregate and per-package limits. This infrastructure interruption is separate from package semantic results.

## GOROOT assessment and retained failures

The [2026-10-19 follow-up](coro-acceptance-20261019.md) supersedes the noinit cause
in this historical table: LLGo already emitted a compatibility init-task header, but
staged emission lost main's package name. The corrected entry passes the original
case under unchanged limits. It also replaces the 22 skipped stdlib fixtures with
18 executable tests and removal of four obsolete placeholders. These newer outcomes
do not rewrite the raw R8 failures/skips or combine different freezes.

Actual passing executions: 1066. Assessment categories: `{"actual-pass": 1058, "actual-pass-preclassified-flaky": 4, "actual-pass-preclassified-not-applicable": 4, "expected-failure": 1, "expected-not-applicable-failure": 11, "raw-fail": 4}`. Existing expected-failure/not-applicable outcomes are reported separately from actual passes; no new xfail or golden update was introduced.

| Raw failed case | Classification | Evidence and limit |
|---|---|---|
| `fixedbugs/issue9604b.go` | generated-compilation-time-limit | The generated 669,627-byte program reaches the unchanged 25-minute LLGo compilation limit (25m0.069s; observed process-group RSS 7,209.2 MiB). The generated program is not executed; this is not a runtime semantics pass. |
| `noinit.go` | gc-specific-toolchain-ABI | The case reads the gc-private main..inittask layout through go:linkname. LLGo does not export that ABI; the actual failure is an undefined main..inittask symbol, rather than an initializer semantic mismatch. |
| `rangegen.go` | compiler-RSS-bound | The generated LLGo build exceeded the unchanged 10240MiB process-group RSS cap at 10292.4MiB. Compilation was stopped by the guard, so this run supplies no program semantics result. Historical R6 extended-cap diagnostics are separate evidence and do not rewrite this R8 failure. |
| `fixedbugs/issue78081.go` | recursive-coroutine-runtime-time-limit | The retained unmodified program exceeds the unchanged 1-minute execution limit, including one unmodified retry. Samples at 5/30 seconds show recursive useStack coroutine frame allocation/resume/release and active CPU use. A separate diagnostic-only run of the identical image finishes normally in 84.877s (80.16s user + 26.98s system; 8.52 MiB OS peak RSS) under a bounded 2-minute limit. This identifies an execution-cost limit in the sampled environment, not a main-matrix semantic pass; the raw FAIL remains. |

Existing expected outcomes, including their exact harness detail:

- `stackobj.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: this case requires typed cross-frame stack-object tracing and precise register liveness; LLGo uses conservative BDWGC roots, so precise stack-object GC is not currently an LLGo compatibility goal
- `fixedbugs/issue24491b.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: this case requires precise pointer lifetime through uintptr arguments and exact finalizer timing; LLGo uses conservative BDWGC roots, so this is not currently an LLGo compatibility goal
- `fixedbugs/issue45045.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: this case observes the exact finalization boundary of a replaced map key while the equal replacement remains live; LLGo uses conservative BDWGC roots, so exact finalizer timing is not currently an LLGo compatibility goal
- `fixedbugs/issue46725.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: this case requires exact interface and slice backing-object lifetime after the final use; LLGo uses conservative BDWGC roots, so this is not currently an LLGo compatibility goal
- `fixedbugs/issue54343.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: this case observes the exact receiver lifetime of a cleared bound method through a finalizer; LLGo uses conservative BDWGC roots, so exact finalizer timing is not currently an LLGo compatibility goal
- `fixedbugs/issue57823.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: this case requires exact slice and string data-pointer lifetime after the final use; LLGo uses conservative BDWGC roots, so this is not currently an LLGo compatibility goal
- `fixedbugs/issue15281.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: this case requires precise last-use liveness across channel operations; LLGo uses conservative BDWGC stack and register roots, so exact GC liveness is not currently an LLGo compatibility goal
- `fixedbugs/issue32477.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: this case requires precise defer and fault-path live maps with exact finalizer timing; LLGo uses conservative BDWGC roots, so this is not currently an LLGo compatibility goal
- `stackobj3.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: this case requires precise liveness for ambiguous by-value stack objects across calls; LLGo uses conservative BDWGC roots, so precise stack-object GC is not currently an LLGo compatibility goal
- `heapsampling.go`: expected-failure; runner_test.go:367: expected failure: latest main goroot run failure on darwin/arm64 Supplement: The existing xfail is an actual profiling gap, not a semantic pass. A same-freeze cold diagnostic with only the external xfail disabled exits 2 after 8.161s: main.allocInterleaved1:0 want objects in [45000:55000], got [0 0 0]. The source groups MemProfile samples by allocation stack/line; the frozen runtime uses size-class aggregate profile records without those allocation stacks.
- `maymorestack.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: LLGo uses fixed native stacks, so gc's growable-stack maymorestack debug hook has no corresponding behavior; supporting this toolchain-specific behavior is not an LLGo compatibility goal
- `fixedbugs/issue27518b.go`: expected-not-applicable-failure; runner_test.go:369: expected not-applicable failure: not applicable: this case requires precise stack-object and return-slot liveness across faults and defers; LLGo uses conservative BDWGC roots, so this is not currently an LLGo compatibility goal

The additional 1,316 compile/diagnostic/multifile coverage cases are outside the user-selected 1,082 runtime scope. Resource-stopped compilations do not supply runtime semantics results. Raw failed workspaces, logs and classifications remain retained.

## Standard-library test events and skips

All 180 selected fixture-package commands pass. Their top-level test events are **2,108 PASS / 22 SKIP**; including nested subtests, **2,386 PASS / 22 SKIP**. These are fixture results, rather than the complete upstream Go standard-library suite. No package lacks an individual test-event record. A skipped assertion does not count as a semantic pass.

| Skip category | Events | Exact tests and assessment |
|---|---:|---|
| stale-disabled-fixture | 1 | `hash/TestSkippedInterfaces`. This existing fixture always skips. Its message says Cloner/XOF are not exported, but the selected Go 1.27 hash source exports both. No LLGo implementation assertion for those interfaces is executed by this fixture. |
| placeholder-fixture | 1 | `math/TestFutureAPI`. This is explicitly a version-gating placeholder and always skips; it does not exercise an API or establish an implementation failure. |
| explicit-LLGo-fixture-gap | 3 | `debug/buildinfo/TestReadFileAndType`; `debug/buildinfo/TestRead`; `io/ioutil/TestReadDir`. The LLGo-specific fixture explicitly skips build-info emission checks. This checkpoint does not establish Go-compatible build information in LLGo binaries. The LLGo-specific fixture explicitly skips build-info reading checks. The LLGo-specific fixture explicitly skips its os.File.Readdir-dependent test. This behavior is outside the validated assertions. |
| disabled-fixture-TODO | 1 | `hash/adler32/TestBinaryMarshaling`. The existing test only skips with an unmarshaling TODO. This run supplies no marshaling assertions or causal diagnosis of the underlying implementation. |
| DNS-record-dependent-skip | 5 | `net/TestLookupTXT`; `net/TestLookupMX`; `net/TestLookupNS`; `net/TestLookupSRV`; `net/TestResolverLookupMethods`. The existing fixture requests TXT/MX/NS/SRV records for localhost and skips on a lookup error. The log omits the actual DNS error, so this result does not distinguish host DNS data/configuration from resolver implementation behavior or validate the successful-record path. |
| disabled-atomic-fixture | 8 | `sync/atomic/TestAndInt64`; `sync/atomic/TestAndUint32`; `sync/atomic/TestAndUint64`; `sync/atomic/TestAndUintptr`; `sync/atomic/TestOrInt64`; `sync/atomic/TestOrUint32`; `sync/atomic/TestOrUint64`; `sync/atomic/TestOrUintptr`. The existing test unconditionally skips before its bitwise assertions, citing platform-specific behavior. This run neither validates the skipped operation nor demonstrates a current implementation failure. |
| disabled-concurrency-fixture | 1 | `sync/atomic/TestConcurrentCAS`. This existing function contains only an unconditional skip citing a potential infinite loop; no concurrent CAS assertion runs, so this is a coverage gap rather than a reproduced current hang. |
| disabled-sync-fixture | 2 | `sync/TestPoolSkip`; `sync/TestCondSkip`. This existing placeholder only skips with a type-assertion warning. It executes no Pool/Cond operation and supplies no current failure reproduction. In this same package run, TestPoolBasic/MultipleTypes/Nil and TestCondBasic/Broadcast/Wait all pass; the stale skip warning does not negate those dedicated results. |

The detailed event contexts, source hashes and reporting classifications are retained in `stdlib-skip-classifications-s3r8.json`; no test, xfail, golden or timeout was changed to obtain these counts.

## Runtime and platform scope

The common production freeze passes the complete coroutine compiler/SSA selection (`^TestCoro`), full `internal/coro` and `runtime/internal/ffi` suites, 73 source/fleet checks including 26 linked fleet cases, runtime core/doorbell race checks, 2,462 FD/Open executions, 315 reflected positives and 18 intentional negatives, and 9 archive tests (20 images, 60 executions). Evaluated value receivers, reflected/FFI second handoff, captured context/large results, and public POSIX Open-to-File custody have dedicated evidence.

Resource handoff includes snapshots of evaluated value receivers, invocation-time reads for bound reflected methods, a second handoff after reflected/FFI calls, and public POSIX Open-to-File cancellation custody. Arbitrary MakeFunc results and raw RTTI/signature shapes do not acquire resource authority. Summary/record/digest metadata is v23; older archives must be rebuilt.

The native poll fix accepts already-coalesced readiness while preserving the first mailbox result. Real Ready-first/Closing-first regressions reject the old comparison and pass the new one; invalid/stale/retired IDs remain rejected. The strict native entry validator and G/P/ABI layouts are unchanged.

The timer failure was a conservative collector false root: libgc's static mmap endpoint hint could coincide with an adjacent page-front Timer. A mark watch identifies that source; five matched no-write controls fail and five hint-only clears pass. The integrated test selects the same 32 pairs away from that boundary, retains all 128 weak checks/five-second/retirement assertions, and adds retained-Timer and extra-G negative controls. Native Linux 1,100 repetitions and both controls pass. This is a fixture correction with an explicit collector boundary; arbitrary prompt weak collection/finalization is not established.

Latest CI covers the named native, JS/WASM command reactor, WASI command/TinyGC, wasip2/wasm-unknown probes, and Cortex-M emulator timer profiles. Nonpollable synchronous WASI host calls can stall beyond guest cancellation. WASI2 component-host lifecycle, embedding/service I/O beyond the command profiles, dynamic reflection without libffi/host support, Windows native coroutine acceptance, and real-board/RTOS IRQ/WFI integration remain outside this checkpoint.

## Performance and provenance

Fresh performance data uses the same production freeze: 272 throughput/progress/peak runs and 80 separate steady parked-memory runs. The report contains seven-pair medians/ranges, checksums, actual host load, stripped executable sizes, dependency scope and current OS RSS through 100,000 parked goroutines. See [the current performance checkpoint](coro-performance-baseline.md); it is a shared-host checkpoint, not a universal performance budget.

The retired R6 stdlib run remains 165 PASS / 1 actual FAIL / 14 unexecuted. Its source, partial closure, original failures and diagnostic supplements remain historical evidence. New results retain their actual freeze and test scope.

Primary receipts: `compatibility-final-receipt-s3r8.json`, `evidence-s3r8.json`, `pr297-integration-receipt-s3r11.json`, `timer-gates-s3r11/result.json`, and `perf-s3r8/final-report.json`. All are retained under the 20261018 result-state artifact directory.
