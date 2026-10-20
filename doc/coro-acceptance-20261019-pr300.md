# PR #300: compile costs and native shutdown

This report records the completed S28 unified acceptance for
[PR #300](https://github.com/cpunion/llgo/pull/300), targeting
`cpunion/llgo:llvm-coro`, on 2026-10-20. All selected cases are accounted for;
the retained compilation-resource failure and existing runtime boundaries are
listed explicitly. Earlier S11 results remain historical evidence.

## Production freeze

The S28 production commit is `3728ce895472195cd5b98a941b985e16d6465aa6`.
The SDK is an archive of that exact commit, and the compiler was freshly built
from that archive with Go 1.27.0 and LLVM 22.1.8 on Darwin/arm64.

| Identity | SHA-256 |
|---|---|
| SDK source manifest | `ecbdef8fb6295d52e73d76687aee2fda1d4cbe701a98890094a7292ca82b5083` |
| Fresh compiler | `0b0fafc808dc3dbe4cb7a520481cc130a7725a001c5a987c29cec108f42637f6` |

Source, compiler, corpus and image hashes are checked by the campaign receipts.
Documentation added after the freeze must retain this tested production identity;
a later documentation commit is not silently substituted for the archived SDK.

## Compilation changes

Coroutine splitting now performs structural CFG cleanup inside each SCC, before
all expanded resume/destroy bodies accumulate. Conditional-branch rewriting and
speculation remain deferred to the complete requested optimizer. Mandatory
coroutine cleanup and the requested optimization level remain enabled.

`Program.Dispose` releases captured Go frontend/type state as well as LLVM
ownership. The Darwin CLI defaults its own Go heap to GOGC 20, honoring explicit
GOGC and preserving the environment inherited by child tools and generated
programs. LLVM 22's AArch64 sink-and-fold subfeature is disabled to avoid its
observed quadratic large-frame use-list traversal; MachineSink remains enabled.
Staged native objects are emitted directly to files, avoiding a simultaneous
LLVM output buffer and its Go byte-slice copy.

The original `fixedbugs/issue9604b.go` passes in the final S28 campaign: LLGo
compilation takes **273.680 seconds**, execution **0.310 seconds**, and the
inner guard samples **9,998.0 MiB** peak process-group RSS. The unchanged bounds
are 25 minutes for compilation, one minute for execution and 10,240 MiB RSS.
The completed batch joined and its executable hashes were inventoried before
successful scratch cleanup. The sampled memory headroom is only 242 MiB; this
pass does not establish a generous memory margin. Its receipt is
`issue9604b-final-s28-case-receipt.json`, SHA-256
`cb652b8fd2a29ab7fac4f877ef9114c00825b4f6f24cc32309ca73e17f95645b`.

The earlier S6 experiment took 643.004 seconds with 6,801.0 MiB sampled inner
peak RSS under the same bounds. It remains historical causal evidence from a
different freeze, not a controlled performance comparison with S28. S28
`rangegen.go` still exceeds the 10 GiB compilation bound, as recorded below.
Neither a larger diagnostic budget nor an `-O0` experiment counts as an
original-limit acceptance pass.

## Native shutdown diagnosis and fixes

The final native entry validator's `Invalid(0)`, zero result and Failed lifecycle
were downstream symptoms. Failure-only captures and deterministic regressions
identified five earlier protocol boundaries:

| Failure condition | Correction |
|---|---|
| A producer advances result publication between separate state/result loads | Validate the reloaded state before accepting this publication skew; retain invalid-state rejection |
| A canceled direct-channel completion has already been removed from its inbox when typed unlink runs | Accept the existing Taken/zero canceled state |
| A completion arrives after compatibility entry and before shutdown scanning | Require empty owner-local queues without rejecting new asynchronous inbox work |
| A selected winner remains retained while its last attachment detaches | Accept sticky shutdown in Detaching/Ready, preserve the selected result lease, and dispose it exactly once |
| A donor has claimed demand but publishes after the receiver's empty final drain | Withdraw demand, retry until existing claims finish, then drain before permitting physical-owner return or terminal program close |

The fourth capture observed GWaiting, ParkDetaching, two expected attachments and
one remaining attachment. The old cancellation guard incorrectly treated the
retained winner as an incomplete Parked handshake. Its regression covers winner
attached, loser attached and Ready states, and checks sticky shutdown and exact
result disposal.

The fifth capture observed a nonempty transfer mailbox at route-close validation
after physical peers had returned. Clearing an unclaimed request does not join
an existing claim. `TryWithdrawPNeutralRunnables` preserves this distinction as
ordinary contention, and is used by peer, program and replacement-owner return
boundaries. Its regression pauses the exact claim both before and after mailbox
publication, checks one import, then retires both fleet domains.

All five conditions have deterministic red/green evidence. Complete core race
tests, the named program/channel/fleet host race selections and native source
contract checks pass. No diagnostic instrumentation is in the production SDK.

Before the final compiler rebuild, the committed runtime bytes passed 160,000
parallel loaded `sieve2` executions and 12,000 sequential `sieve1`/`sieve2`
executions across P=1,2,4,8,16,32. All owned processes joined and image hashes
matched afterward. These 172,000 executions used the preceding S11 compiler;
they are stability evidence, not a replacement for S28 unified acceptance.

Both original `chan/sieve1.go` and `chan/sieve2.go` also pass inside the fresh S28
GOROOT campaign with their original limits. The S28 compile/run times are
22.619s/0.299s and 33.780s/0.333s respectively. Both batches joined; their logs
and identities are recorded in `sieve-final-s28-case-receipt.json`. These are two
cases within the selected 1,082, not additional stability repetitions.

After all S28 compatibility compiler and performance domains joined, the fresh
S28 `net/http/httptest` image completed **2,000** full-fixture runs at P=2 in
295.621 seconds. Every run passed all seventeen top-level tests, for 34,000
repeated top-level PASS outcomes and no FAIL/SKIP. The image hash before and
after is `d43fb5c4fd8f42680b42aea1bb6c3145dc5240aa49babc31c94d58507dceaa05`;
source and compiler hashes match afterward. Each run keeps its original 8 GiB,
seven-minute outer and six-minute test limits. These are stability repetitions,
not additional fixture directories.

## Unified acceptance gates

| Gate | S28 status |
|---|---|
| Exact-head coroutine CI, 20 jobs | 20/20 PASS at `3728ce895`, [run 39216607641](https://github.com/cpunion/llgo/actions/runs/39216607641) |
| All PR reviews, inline comments, issue comments and review threads | All pages read; zero reviews/comments/threads at 2026-10-20 02:41:46 UTC; fresh final-head audit required before integration |
| Complete native `test/go` | 276 top-level / 614 including subtests PASS; zero FAIL/SKIP; all 45 nested compiler domains joined and reclaimed |
| 1,082 selected GOROOT runtime cases | Complete: 1,069 actual passes, 12 existing expected failures, one compilation-resource FAIL; all domains joined |
| 180 standard-library fixture directories | 180 PASS; 2,127 top-level / 2,405 including subtests PASS; zero FAIL/SKIP; same-freeze continuation after the retained aggregate interruption |
| 73 runnable Go demos | 73 PASS; all domains joined; suspended gotoken build independently rechecked awake under its original limits |
| Fresh Go comparison, executable sizes and parked-goroutine memory | Complete: 272 throughput/progress/peak and 80 parked-memory runs; no sleep during measurement |
| Underlying errors for twelve existing expected GOROOT failures | All twelve raw failures manually reviewed: nine test-assertion panics, two run deadlines, one output mismatch; no new expectation rule |
| Host-suspend calendar gap in `gotoken` demo compilation | Fresh awake build/run PASS in 79s/1s; no sleep during recheck; original 30-minute/three-minute limits |
| Final failure classification and process/source-after receipts | Complete; all campaign domains retired; source/compiler/corpus/image identities verified |

The original per-case limits remain in force. Every raw failure retains its log
and available artifacts. Expected failures are distinguished from actual passing
executions. The additional 1,316 diagnostic/multifile GOROOT cases remain outside
the selected runtime scope. Native profiling/precise-GC limits and target-specific
coverage must remain explicit in the final report.

The full native `test/go` receipt is `fulltestgo-final-receipt-s28.json`, SHA-256
`b4e758c823c0905512f91ef88a02bc1b04b3671509a8e0f0e726c780f1e75041`.
Its retained image, source, compiler and nested launcher hashes match after
execution. The original 10 GiB RSS, 35-minute build, 80-minute outer run and
75-minute test deadlines remain unchanged. Expected negative compiler diagnostics
retain their nonzero exits and are checked by passing parent assertions.

The complete GOROOT receipt is `goroot-final-receipt-s28.json`, SHA-256
`ac3a21115885dd294c6bb480d6776271adefc4a14811d35546d532f697c21282`.
All 1,082 unique selected paths are accounted for; SDK, compiler and Go test
corpus hashes match afterward. Raw runner counts are 1,081 PASS and one FAIL.
Of those runner passes, 12 are existing expected failures rather than passing
programs: ten conservative-GC/finalization boundaries, the native-stack
`maymorestack.go` debug-hook boundary, and `heapsampling.go`'s existing profiling
limitation. No new expected-failure rule was added. The ten GC cases are
`stackobj.go`, `stackobj3.go`, and `fixedbugs/issue15281.go`, `issue24491b.go`,
`issue27518b.go`, `issue32477.go`, `issue45045.go`, `issue46725.go`,
`issue54343.go`, and `issue57823.go`.

Supplemental runs disable only the external expectation labels and retain all
twelve underlying failures. The frozen SDK, compiler, runner and source programs
are unchanged. Every baseline Go build/run succeeds before comparison. The
original 25-minute compilation, one-minute execution and 10 GiB RSS bounds apply.

| Case | Observed native error | Existing boundary |
|---|---|---|
| `fixedbugs/issue15281.go` | stderr differs: about 10 MiB remains retained after GC clears the slice | Precise GC/liveness |
| `fixedbugs/issue45045.go` | One-minute run deadline in a finalizer-dependent map-key test | Precise GC/finalization |
| `fixedbugs/issue46725.go` | Test panic: `never GC'd` | Precise GC/finalization |
| `fixedbugs/issue54343.go` | Test panic: `never GC'd` | Precise GC/finalization |
| `stackobj3.go` | Test panic: `bad liveness` | Precise finalizer phase |
| `fixedbugs/issue57823.go` | Test panic: `never GC'd` | Precise GC/finalization |
| `heapsampling.go` | Test panic: sampled object counts are zero, expected 45,000–55,000 | Allocation-stack profiling |
| `maymorestack.go` | Test panic: `mayMoreStack not called` | gc growable-stack debug hook |
| `stackobj.go` | Test panic: expected collection at phase 1, got phase 3 | Precise finalizer phase |
| `fixedbugs/issue24491b.go` | One-minute run deadline in a finalizer-dependent uintptr-lifetime test | Precise GC/finalization |
| `fixedbugs/issue27518b.go` | Test panic: `heap object finalized at the wrong time` | Precise finalization after recovered fault |
| `fixedbugs/issue32477.go` | Test panic: `heap object finalized at the wrong time` | Precise finalization after recovered fault |

All nine panics are explicit test assertions with exit code 2. The two deadlines
are observed execution timeouts; their finalizer-channel interpretation follows
the unchanged source rather than a captured parked frame. These diagnostics
contain no native entry `Invalid(0)` or SIGABRT outcome. They do not turn any of
the twelve primary expected failures into actual passes or add selected cases.
Retained Go/LLGo image hashes, unchanged source copies, raw logs, interruption
receipts and individual classifications are recorded in
`goroot-expected-classification-final-s28.json`, SHA-256
`cc5b207e20d45030a2c37a4ff1efd6d517066305125e2846141f0e170175b8bb`.

Eight other previously classified cases actually pass in S28 (four marked
not-applicable and four marked flaky). They are included in the 1,069 actual
passes; this single execution does not broaden their documented support boundary.
The load-sensitive `fixedbugs/issue78081.go` ran after all owned compiler domains
joined, using the original one-minute execution bound. It passes with 22.106s
LLGo compilation and 29.221s execution. The three regular controllers and the
deferred controller all completed and joined without an infrastructure stop.

The first stdlib aggregate reached its unchanged **eight-hour** wall-clock
limit after **168 fixture passes**. Its sampled peak tree RSS was 25,540.9 MiB,
below the 40,960 MiB aggregate cap. Builds of `testing/synctest` and `time` were
interrupted; ten further fixtures had not started. Those two builds produced no
test outcomes, and no native test executable from either interrupted attempt
ran. The original shard completion/source-after receipts are unavailable because
the guard force-retired the cohort. The aggregate log, both original journals,
locks and complete interrupted scratch inventories are retained in
`stdlib-aggregate-timeout-incident-s28/` and the fixture cohorts. Incident receipt
SHA-256: `142348713c62c85a45fac06f732247e5df24f9eb11ba3c8f9172ff93a4d7ad87`.

Recovery verifies SDK/compiler identities, the retained 168 executable hashes,
retired process identities and absence of open scratch handles. The old scratch
and locks are renamed and retained. The original runner's identical-fingerprint
resume reuses only completed passes and freshly builds/runs the twelve unfinished
fixtures, retaining all original per-fixture and aggregate limits. This is a
same-version completion campaign; it is not a 180-fixture pass within one
eight-hour aggregate.

The continuation completed the remaining twelve fixtures in 1,887.789 seconds.
Both final shard runs exited normally, their source-after checks match, and
their fresh scratch was reclaimed. All 180 retained executable hashes match.
Final stdlib receipt: `stdlib-final-receipt-s28.json`, SHA-256
`8c2fc4ad1b25ef1d0610fd22853e33c6df135b8f37043e7a68117b158f00cbd2`.
The 168 completed images reused by the same-fingerprint continuation were
originally built fresh from S28, as were the twelve newly completed images.

All 73 demo directories produced fresh S28 executable images and passed their
runtime checks. Their controller joined normally, checked source/compiler/image
hashes afterward, and reclaimed scratch. The primary demo receipt is
`demos-primary-final-receipt-s28.json`. The aggregate took 11,559 calendar
seconds within its unchanged four-hour limit. Its monotonic guard elapsed time
was 7,169.353 seconds; the host-suspend interval and `gotoken`'s individual
calendar gap remain separately accounted for below.

The [fresh performance comparison](coro-performance-20261020-pr300.md) retains
all 352 warmup/measured executions, stripped executable sizes, and steady and
peak process RSS. At 100,000 parked goroutines, median steady RSS is 159.016 MiB
for LLGo and 267.812 MiB for Go. The executable size ratios are 3.286–4.090 times
Go's; serial spawn takes 8.601 times Go's elapsed time. Compute and ready-select
results improve in this shared-host checkpoint, but substantial spawn/I/O costs
remain. These are measured tradeoffs, not an all-workload performance claim.

The S28 `rangegen.go` raw FAIL is a generated-compilation resource limit. The
guard observed 10,814.2 MiB against the unchanged 10,240 MiB cap after 2m56.741s
of generated compilation (3m10.838s for the case). No generated LLGo executable
was produced or executed. The 6,399,112-byte generated source has SHA-256
`9d8f29f06e42b86805069f5ac1b419c073df24fae91f42eb1b1e2501b8919395`,
matching the historical S11 input. The complete failed work/cache, logs and a
378-file work inventory remain retained. Classification receipt:
`goroot-rangegen-classification-final-s28.json`, SHA-256
`674da1c2ace57801b15486626140bbfa3d9a51c35e7306a20a6aabb024ff64aa`.
This is an unresolved compilation-memory limit, not a native shutdown abort or
a semantic pass.

The final reviewed local receipt is `final-reviewed-receipt-s28.json`, SHA-256
`d39487d29ce124219da28b9d884ee1036b9c8101c44ae7e875bde2412930b446`.
It joins the complete primary receipt, manual raw-error review, awake recheck,
stability runs and all source-after checks. Integration additionally requires
20 successful CI jobs and a fresh complete review/comment/thread audit at the
final documentation-only head, with unchanged production blobs and target base.
Those remote gates are recorded on the PR and in the local integration receipt.
Darwin/arm64 execution does not establish execution on every target.

The 20-job CI matrix runs on Ubuntu 22.04 with LLVM 22 and Go 1.27.0. It covers
the native core/fleet, fault landing, linked and independent archives, reflected
resource handoff, standard I/O/timers/sync and GC ownership; JS/Wasm and WASI
command reactors; freestanding `wasip2`/`wasm-unknown` host probes; and the
Cortex-M timer host protocol with emulator execution. These named gates do not
establish the entire 1,082-case corpus or all 180 fixture directories on each
target, nor physical Cortex-M hardware support.

## Retained evidence

Local campaign artifacts are under
`~/.cache/llgo-coro-acceptance/20261019/result-state/codegen-cost-s1/`.
The relevant files include `final-s28-evidence.json`,
`bootstrap-final-s28-result.json`, `ci-final-s28-run.json`,
`review-final-s28/`, `unified-final-s28-wave1/`,
`unified-final-s28-post/` and `perf-final-s28/`.

During the stdlib campaign, the predecessor post-campaign and tail coordinators
retired while the original aggregate guard and both shard controllers kept
running. Their exit statuses are unavailable. This orchestration interruption is
recorded in `controller-retirement-incident-s28/`; the recovery coordinator in
`unified-final-s28-recovery-r2/` observes the existing guard and resumes pending
phases with the original commands and limits. Completed fixture results remain
in their original cohorts. An observed outer-guard retirement is recorded
separately from the child controllers' waits for test/build domains.
After the stdlib aggregate deadline, `unified-final-s28-recovery-r3/` and
`unified-final-s28-stdlib-resume-r3/` track the same-fingerprint continuation.

Disk maintenance was limited to closed task-owned historical rangegen build
caches. Byte-identical files were deduplicated with independent APFS COW clones;
22 large cache objects were then losslessly archived and all decompressed
payload hashes verified before removing their flat cache copies. The archive
retains 5,799,604,326 original bytes in 420,636,132 bytes, with SHA-256
`51aed7f24d24b0435bb1d1e3c3dfa01ce380b95517cf0964aaa7144edddea979`.
`retired-large-cache-archive-s28/` contains its manifest, verification receipt
and `restore.py`; each of the eleven historical roots has a
`large-cache-objects-archive-s28.json` index. Historical logs, generated Go inputs
and executables remain flat. Current S28 artifacts are unchanged. Maintenance
completed before performance preflight and its receipt is included in the
preflight/after identity checks.

After performance and the 2,000 httptest repetitions completed, the supplemental
raw-diagnostic controller stopped at its original 10 GiB free-space floor after
four completed cases. Its incomplete second batch has no test outcome; its
work and normally joined interruption receipts remain retained. The dependent
awake controller started no test. The continuation in
`expected-diagnostics-s28-controller-r4/` reuses only the four completed raw
outcomes and freshly runs the other eight with identical source/compiler,
runner and limits. This first continuation also stopped at the same floor before
its batch footer; partial error captures are retained without counting completed
runner outcomes. The final continuation in
`expected-diagnostics-s28-controller-r5/` waits for 20 GiB of admission headroom
stable for three minutes, preserving the original 10 GiB stopping floor.

Historical task files then received a second COW deduplication: 2,800 identical
closed files, representing 7,941,130,992 logical bytes, retain all original paths,
contents and extended attributes. Open files, all S28 paths, tools and the source
checkout are excluded. `retired-history-cow-dedup-s28-r4/` retains the open-handle
inventory, per-file before/after hashes and closure receipt. Its observed net
free-space gain is 6,512,992,256 bytes, including concurrent host activity. This
maintenance follows the performance measurement and changes no measured image.

The completed small-file continuation in
`retired-small-history-cow-dedup-s28-r6/` verifies another 110,453 COW replacements
covering 5,999,424,909 logical bytes, with all paths and bytes retained. Its
permission-denied predecessor stops after 371 verified replacements; the
continuation skips protected files and retains their permissions. These ledgers
overlap and are not additive space measurements.

Seventeen closed historical LLVM bitcode/cache objects are retained in
`retired-intermediates-archive-s28-r5/`, with per-root indexes and `restore.py`.
All 3,406,853,462 payload bytes were verified by streaming decompression before
flat storage reclamation. Archive SHA-256:
`5d7cf7699abfb1c04ad0894faacbbcb8fd0de4a4dd13d0f53d6dfa4897ab953f`;
archive size: 704,840,809 bytes. All current S28 artifacts, historical generated
Go inputs, failure logs and native executable images remain flat. This second
archive follows the performance interval.

The host entered clamshell sleep at 07:07:01 and fully woke at 08:22:24 on
2026-10-20 (Asia/Shanghai). The original `gotoken` demo build passed after 3,994
calendar seconds across that interval, exceeding its configured 30-minute build
budget in calendar time. The guard uses a Go timer, whose monotonic clock can
pause during system sleep. That semantic pass alone does not prove the calendar
limit. `host-sleep-incident-s28/` retains the power transitions and original
attempt. A fresh same-source/compiler awake recheck with identical stage commands,
environment and limits completes awake in **79 seconds compilation / one second
execution**, without a sleep event. The performance interval also has no sleep
event. Both power-event inventories, original suspended attempt and fresh image
hashes remain retained. The recheck receipt is
`demo-awake-s28-controller-r5/result.json`, SHA-256
`179eb184bb5a286eee4960b80741768ed5d368425923e19ce8a96b426ae7eb40`.
It is not an additional selected demo. All scoped idle-sleep assertions and
campaign processes retire after closure.

The five-fix investigation is retained in `native-shutdown-incident-s11/`,
including `fix5-validation-receipt.json`, `detaching-winner-cause-s25.json`,
`late-transfer-cause-s27.json`, their failed native images/captures, and red/green
regression logs. The directory name describes the originating incident; it is
not an assertion that all later evidence used the S11 source.
