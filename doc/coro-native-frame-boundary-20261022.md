# Function-local native coroutine boundaries: 2026-10-22

This is an intermediate acceptance record. All four remaining parity targets—batch creation, serial creation, standard file I/O and loopback TCP—must reach Go performance before final project closeout. Buffered channels are already faster in the current original-input measurements. The later held-memory measurements below are diagnostics; no final unified acceptance is claimed here.

PR 312 merged into `cpunion/llgo:llvm-coro` as `edb37b42c41db5e9bbb9d6fd2bef4c750c70d738`. Its exact tip `1152887eec2736d4ddde186ad07cbd0d3633bfed` passed CI run 39725656588, 20/20. Paginated reviews, inline comments, issue comments and threads were empty immediately before merge. The merge tree equals the tested tip tree. The first frozen compiler producer, corresponding to `799af6a90`, separately passed full native test/go: 307 top-level tests, 691 including subtests, with all 46 nested compiler domains joined and reclaimed. Those receipts retain their producer identity. The corrected compiler's original core image is byte-identical; its I/O image has five independently ordered adjacent store instructions, and was remeasured separately.

## Isolated descriptor and landing candidates

Candidate B carries a conservative negative native-fault capability in descriptor bit 4 and the existing frame publication byte. It changes no G/Frame field or size. Only recognized published values can omit a root landing; legacy and unknown descriptors retain complete behavior. The compiler reuses its frozen function-local boundary decision, including the dynamic panic-on-fault override. Existing inline runtime hooks and signal/landing validators remain unchanged.

Candidate C adds a second projection to the existing capability fixed point. Whole-program runtime-service demand remains fully transitive. Physical landing demand stops only at a frozen direct await of a locally defined physical coroutine child, which owns its own landing. Imported, raw, plain, outcome, publication and unknown edges remain conservative. A root still installs its logical runtime G when an inline child needs the sole boundary. This distinction is necessary for real native signal recovery.

B's complete core race suite passes (582 top-level tests). C's complete compiler coroutine contracts pass under race/shuffle: 403.029 test seconds, all processes joined. The unchanged architecture debt check passes without increasing its budget. Both candidates passed the original eleven-check program, the separate active-profile eleven-check program, and original sieve1/sieve2 comparisons against fresh Go references at P1/P4, each three times. Real protected-page faults recover with panic-on-fault enabled and terminate when disabled. C also passed six runs of the exact low-address-1 control across a yielded child and a distinct caller: twelve recovered faults in total. Machine code proves the child retains a native landing while the caller omits the duplicate.

## Original-input results

All rows use the unchanged ordinary-Go benchmark sources, original 17 scenario counts, default Os and no LTO. Each accepted pair of cohorts retains a warmup and seven rotating Go/baseline/candidate rounds: 816 samples, checksums, guard receipts and OS peak RSS. No owned compiler runs during accepted timing. B's first additional 408-sample cohort is retained as diagnostic because a compiler overlapped its warmup; it is not used for acceptance. The replacement pair is B cohorts 2 and 3.

| Case | B / Go, two cohorts | B / exact PR 312 tip | C / Go, two cohorts | C / B |
| --- | ---: | ---: | ---: | ---: |
| Batch goroutine creation | 1.929x / 1.927x | 0.844x / 0.842x | 1.984x / 1.970x | 1.005x / 1.002x |
| Serial goroutine creation | 1.993x / 2.026x | 0.867x / 0.866x | 1.985x / 2.002x | 0.993x / 0.996x |
| Standard file I/O | 2.313x / 2.467x | 0.971x / 1.050x | 2.127x / 2.124x | 0.930x / 0.917x |
| Loopback TCP | 1.543x / 1.565x | 1.022x / 1.010x | 1.450x / 1.507x | 0.961x / 0.975x |

Ratios compare producers within each paired cohort; differences in the Go reference between separate campaigns are retained. B improves batch creation by 15.6% and 15.8%, serial by 13.3% and 13.4%. Its file/TCP variation does not establish improvement, including the retained 5.0% file slowdown. C improves file I/O by 7.0% and 8.3%, TCP by 3.9% and 2.5%; it adds no stable creation gain. All four parity targets remain open.

C's stripped core image is 2,823,632 bytes and I/O image 4,570,048 bytes, compared with B's 2,954,960 and 4,898,368 bytes. A fixed group of 28 I/O resume symbols has 644 native-boundary-related call sites in B and 77 in C. This is a code-size observation, not a performance or resident-memory claim.

## Retained experiment without adoption

Candidate D preserves guarded fast paths when a forwarding wrapper would otherwise become an unconditional target ramp, including Done → Add. Old-code controls fail for four guard types while ordinary forwarding passes; the corrected focused and architecture tests pass. Full compiler race/shuffle passes in 417.696 test seconds. Native original-input checks and actual Done SC CAS (AArch64 ldaxr/stlxr) pass. Two 408-sample cohorts show batch creation only 1.3–1.6% faster and serial creation 0.35–0.43% slower, with no stable material overall gain. D is not adopted. Its complete evidence remains available.

## Retained allocator experiment without adoption

Candidate E branches from C, excluding D. BDWGC-specific AllocZ used the same private scalar-only GC_malloc wrapper and allocation profiling, while omitting its second memset. The installed public GC_malloc contract guarantees cleared non-atomic allocations. Its new language-level test dirties escaping objects, drops roots, collects, and verifies fresh scalar and pointer-containing values over eight rounds, fourteen byte classes and 512 typed nodes per round. Host contract checks, three Go race repetitions and exact LLGo native P1/P4 runs, each count=3, pass. All original native checks and two 408-sample cohorts pass, but performance does not establish a benefit: the first cohort regresses buffered channels 5.0%, serial creation 5.4%, file I/O 6.6% and TCP 4.3%; the second is near baseline. E is not adopted, including its additional test file. All evidence remains retained.

## Compatibility and integration

The added negative compiler cases keep imported declarations, missing physical definitions, outcome/raw calls and disagreeing owners conservative. Exact local physical child calls remain the only cutoff. The unchanged architecture debt gate passes. A dynamic integer-to-pointer diagnostic builds and recovers in Go; both the accepted tip and candidate reject it with the same `uintptr-to-pointer conversion has no traceable exact pointer provenance` diagnostic. This is an existing source/lifetime boundary, not an accepted native run and not a candidate regression.

The integration contains B/C production code and additional negative tests. D/E are excluded. Exact integration tip `576663756` passed the complete host coroutine compiler race/shuffle contracts, but CI run 39737500737 exposed a real linked synctest regression: a source-context-independent child received on its inherited bubble channel without its logical runtime G installed. Native landing demand and runtime adapter context are separate services. Execution-domain channel/timer adapters must retain the latter even when the frame may omit the former. The runtime adapter now includes the immutable execution-domain predicate when deciding to install the logical G; ordinary programs with that feature disabled retain the context-free path.

The new focused linked fixture preserves a blocking child receive across `synctest.Wait`, then closes the channel and verifies completion. The exact same fixture fails on the old runtime with `receive on synctest channel from outside bubble` and passes with only the runtime adapter policy changed. Both outcomes repeat three times each at P1 and P4. The focused fixture joins the existing linked acceptance gate; all three fixtures pass in the linked acceptance gate, including original timer/select/Cond and heap/WaitGroup/weak ownership (259.44 test seconds; all guarded processes joined).

The exact-576 full native campaign was cooperatively canceled after the CI regression. All recorded PID/start identities have retired; its interrupted nested marker and partial results remain preserved. It is not a complete native pass. Corrected producer `a91efe048` passed exact-head CI 39741270868, 20/20. Its first full native run failed at the fixed 75-minute limit after 291 top-level / 637 total passes; the primary panic payload was not preserved, and its guard also failed final cleanup. A separate PID/start audit found all 3,622 recorded identities retired and all 39 normal nested domains joined. An independent replay with a predeclared 120-minute test / 125-minute guard budget passed all 307 top-level / 691 total tests, with all 46 nested compiler domains joined and reclaimed. The failed first run remains a separate receipt.

## Additional native ownership failures

An ordinary-Go concurrent LockOSThread fixture exposed a defect outside that passing corpus. Two independently locked tasks returning or calling Goexit fail at P1 three times, while the fresh Go reference passes. A diagnostic-only predicate trace identifies the detached previous P-local lock owner as the rejection. A stronger nested-lock/barrier/ring/Gosched fixture with 2, 8, 16 and 64 participants fails at both P1 and P4; Go passes all eight configurations. This blocked the earlier integration. The follow-up below implements multiple locked owners and retains the original failures as separate receipts.

The investigation also found an independent standby-cache capacity race. ReleaseOwner tested the eight-M cache limit before waiting for the returned pthread to acknowledge PARKING, but did not reserve a credit before cond_wait released the mutex. A deterministic acknowledgement gate makes sixteen concurrent releases admit sixteen cached Ms on the old code. The correction counts pending reservations before waiting; only eight enter standby and the other eight are strongly joined. It also makes queued reuse cancellation continue its original dispatch when another return has claimed the freed credit, rather than poisoning a full cache. A second deterministic gate reproduces that cancellation failure on the old code.

Both corrected C controls pass plain, AddressSanitizer/UndefinedBehaviorSanitizer and ThreadSanitizer runs. The old cancellation TSAN run additionally reports a thread leak after the intentional failed exit and is retained as a failed run. Existing native-factory controls and both new regressions pass Go race/shuffle three times. That earlier cache-only revision did not implement multiple locked owners; its concurrent LockOSThread gate remained open. Results for `a91efe048` above retain that producer and do not count as a full pass of this follow-up revision.

Three additional scheduler/ABI comparison cohorts retain all 720 samples, but every cohort violates its predeclared host-load admission or measurement gate. They are diagnostic only. None establishes Go parity or authorizes adoption of those isolated prototypes.

## Multiple locked owners and physical-thread retirement

This follow-up keeps suspended locked tasks on their actual pthreads in a cold per-P list. Ready selection remains in the existing scheduler FIFO. A foreign caller returning while another locked task is suspended adopts that list under the generation-bound return handoff; it never lends a pinned pthread to the clean cache. Locked compute tasks and unlocked peers both service ready affinity owners. G/Frame layouts and the execution quota remain unchanged.

Three additional failures were reproduced independently. First, exiting the primordial OS thread without removing its BDWGC registration makes a successor collection abort with `thread_suspend failed`. A C control joins the original thread before collecting: old code fails three times and explicit initial-thread unregister passes three times. Factory-created threads keep their existing GC wrapper cleanup. Second, retired private M directory entries were never reused, imposing a lifetime limit despite low concurrent thread counts. Serial 12000 locked exits failed with directory exhaustion while Go passed. An atomic retirement record now joins physical departure, parent-lineage acknowledgement and temporary readers before granting exactly one slot reclaim. An epoch protects slot reuse, and native thread/token outputs become readable only after OwnerReady acknowledges their publication. The concurrent directory limit remains 10000. Third, an unbalanced locked exit could race the original foreign caller's return request and enter the clean standby cache. A raw test barrier makes that ordering deterministic: the predecessor fails three times waiting for the pthread TLS destructor; the correction marks the M tainted and strongly joins it, passing three times.

The isolated clean-r7 source passed the complete native fleet group: 34 top-level checks / 35 including subchecks, full core/factory/thread race tests and runtime source contracts. Existing cross-return park/compute cases, sixteen locked peers with actual TLS/thread retirement, managed callbacks, task Abort and Shutdown all pass. A foreign-blocked owner services 12000 locked exits; a separate preceding candidate passed that test three times. Standalone serial and locked-parent 12000-exit programs also pass against fresh Go controls. The C retained-owner, standby pending-credit and full-cache cancellation fixtures all pass plain, AddressSanitizer/UndefinedBehaviorSanitizer and ThreadSanitizer runs. A first sanitizer replay incorrectly compiled an already-included C implementation twice; its duplicate-symbol setup failure is retained separately from the corrected complete replay.

Public test/go regressions cover nested locks, P1/P4 rings with 2/8/16/64 participants, ordinary return and Goexit, and both ordinary and locked-parent directory reuse beyond 12000 exits. Both public regressions now pass exact-411 native acceptance and Go race controls. The integrated test harness gives the 12000-exit native capacity fixture a predeclared 90-second runtime budget for slower CI hosts; ordinary fleet controls keep their 10-second limit. Exact tip `e322bd760` passes CI run 39804061858, 20/20. Paginated reviews, inline comments, issue comments and threads are all empty immediately before merge. PR 313 merged into `cpunion/llgo:llvm-coro` as `81cfcc47b37ca99793a9f6e8bc7292210ee86ad4`; its merge tree equals the tested tip tree. The complete native producer retains its identity below. This is an intermediate functional integration, not final project acceptance.

Exact producer `411ddec19` subsequently passed its full core/factory/thread race checks three times, runtime contracts, the parent-return and 12000-exit native controls three times each, and both public LockOSThread test/go regressions under Go race and LLGo. Its complete native test/go passes 309 top-level tests / 703 including subtests, with all 46 nested compiler domains joined and reclaimed. CI found the new primordial-thread wrapper was missing from the frozen production-contract inventory; an owner-manifest audit proves exactly one additional foreign `sync` declaration and all previous 85 entries unchanged. The reviewed inventory becomes 8 contract / 40 noblock / 38 sync. No Go caller is annotated and no removed directive class is restored. An independent JS/WASM job failed before testing because the package source returned HTTP 403. Both failures and the follow-up CI retain separate receipts.

The later seven-brand comparison campaign retained 560 more original-input samples. Both cohorts breached the predeclared load gate, so all are diagnostic. Together with the earlier 720 samples, they establish no new parity result and do not authorize adoption of those performance prototypes.

Evidence: `locked-owner-clean-r7`, `locked-owner-clean-r6`, `locked-owner-clean-r5`, `locked-owner-lifetime-capacity-r1`, `locked-owner-directory-trace-r1` and `gc-main-exit-r1`, under the same evidence root. Every red run, fixture setup failure and positive producer retains its own source identity.

## Merged PR 313 performance baseline

The exact-e322 source/compiler produced fresh stripped images from the unchanged ordinary-Go fixtures, default Os and no LTO. The merge commit has the same source tree. Two predeclared cohorts each retain a warmup and seven alternating Go/LLGo rounds, 160 total samples. Both meet the predeclared load admission and measurement gates; no owned compiler runs during timing. Go 1.27 reference images are byte-identical to the preceding reference producer. Every checksum, output, guard receipt and peak RSS sample is retained.

| Original case | LLGo / Go, cohort 1 | LLGo / Go, cohort 2 |
| --- | ---: | ---: |
| Buffered channel | 0.1542x | 0.1547x |
| Batch goroutine creation | 1.9591x | 1.9833x |
| Serial goroutine creation | 2.0105x | 2.0291x |
| Standard file I/O | 2.1337x | 2.1686x |
| Loopback TCP | 1.5294x | 1.5276x |

All four requested parity targets remain open. These accepted measurements establish the merged functional baseline; they do not establish parity or replace the final frozen acceptance. Later allocator, zero-state and backend-inlining prototypes remain isolated. Their expanded-duration sampling is diagnostic, and none is adopted in this record.

Evidence: `locked-owner-integration-r1`, `locked-owner-integration-r2`, `native-locked-owner-integration-r1`, `locked-owner-directive-review-r1` and `merged-313-comparison-r1`. Native producer `411ddec19` and CI tip `e322bd760` differ only in the host inventory test and documentation; all compiler/runtime production source blobs are byte-identical. The native result retains the 411 identity.

## Current held-memory diagnostic: 2026-10-23

Fresh held-program images use the exact e322 compiler/runtime producer, whose source tree equals merge 81cfcc47b. At GOMAXPROCS=1, GOGC=100 and GOMEMLIMIT=off, one warmup and three alternating measured processes per cell each retain seven RSS observations after the ready/GC barrier. All 24 processes, including the explicit collector-setting controls, release and join successfully.

| Parked goroutines | Go RSS | LLGo default RSS |
| --- | ---: | ---: |
| 0 | 4.313 MiB | 6.781 MiB |
| 1,000 | 7.156 MiB | 7.766 MiB |

These are process RSS medians, separate from vmmap physical footprint. The zero-population LLGo map has about 128 KiB resident across all six native stacks, including the main thread; oversized resident stacks do not explain the roughly 2.5 MiB RSS difference. Explicit GC_MARKERS=1 does not materially change RSS. The startup marker policy already exists in this producer, and the additional sampled threads are the native factory/worker threads. Older memory rows retain their original producer identities and do not establish a controlled improvement relative to this run.

Evidence is in `collector-workers-memory-r1`. Its first stack captures have a console/artifact pathname collision and are unusable as stacks; corrected `measure-r2` captures preserve the actual samples. The first run's RSS and vmmap artifacts remain valid. Bounded helper inlining, a bounded spawn batch and package annotation elision pass their focused/native controls but remain isolated without a material measured benefit. Selective runtime-library LTO variants are still being screened; neither is adopted or a parity result.

## Private runtime-library optimization candidate: 2026-10-23

Candidate S2 optimizes and jointly links only `runtime/internal/coro`, `runtime/internal/runtime` and `runtime/internal/coroalloc` at O2. Application and standard-library modules retain native Os objects. Two predeclared compiler-free screening cohorts retain 320 alternating Go/current/runtime-Os/runtime-O2 samples, using the original five sources/counts, unstripped images and the same default runtime settings. Both meet the load gate; all samples and failed admission observations remain available. This screening does not replace exact final stripped acceptance.

| Case | Runtime-O2 / Go, cohort 1 | Cohort 2 | Time reduction from current |
| --- | ---: | ---: | ---: |
| Batch creation | 1.6368x | 1.6591x | 16.9% / 17.4% |
| Serial creation | 1.7197x | 1.7457x | 13.7% / 13.0% |
| File I/O | 2.0147x | 2.0213x | 6.0% / 3.3% |
| TCP | 1.4185x | 1.4160x | 6.2% / 7.0% |

All four parity targets remain open. S2's 33 guarded CLI build/run steps pass, including original/active eleven-check programs, sieves, zero-after-GC and locked-thread tests at P1/P4. Fresh and cached stripped core/I/O builds produce byte-identical images at each output path. Stripped sizes are 2,888,720 and 4,651,456 bytes, about 1.7% and 1.4% larger than the merged baseline. Fresh default-policy held-memory screening is essentially unchanged: empty current/S2 RSS is 6.781/6.750 MiB; 1,000 parked tasks is 7.766/7.781 MiB. No material baseline-memory improvement is established.

Proposed integration S3 narrows the policy to the measured native Darwin arm64/default Os profile. Cross targets, custom archive/linker choices and debug/IR-output profiles retain ordinary package code generation. A versioned cache field separates the emitted object policies. Raw bitcode requires an LLVM-aware archive index: wrapped C bitcode controls pass with Apple ar, while the same raw payload fails to link; the linked LLVM archive writer fixes that case. S3 uses the existing writer only for the three private bitcode packages. Native application objects and explicit custom-archiver fallback have real link/run regressions. S3 commit `4ef0db8c2` passed CI20/20 and fresh native test/go (309 top-level /703 total tests). All46 nested compiler domains exited and were reclaimed; request hashes and unchanged source were verified. It merged as `f87e2245f` via PR314, whose merge tree matches the tested commit. Two compiler-free stripped-image cohorts retain240 original-input samples and meet every load gate: batch1.6496/1.6538x Go, serial1.7607/1.7426x, file2.0143/2.0062x, TCP1.3561/1.3604x. Allfour parity targets remain open.

Evidence: `runtime-lto-screen-r1`, `runtime-selective-lto-r1`, `runtime-selective-lto-r2` and `runtime-library-integration-r1`. S2 and accepted S3 retain separate producer identities. No runtime/core ownership checks or G/Frame layout change.

## Compiler root-completion receipt candidate: 2026-10-23

The compiler already proves the complete zero-park singleton root graph before final suspend. A private pending kind preserves that proof until the adjacent issued physical resume returns, where the scheduler consumes the exact P/G/action/frame identity. Other completions and the generic Resumed API keep full validation. The pending enum uses the existing representation; G/Frame layout and size stay unchanged. Broken issued-owner identities fail before mutation.

The complete core race suite passes three shuffled repetitions. All33 isolated native CLI build/run controls pass, including original/active eleven-check programs, sieve1/sieve2, zero-after-GC and locked-thread tests at P1/P4. Two predeclared compiler-free cohorts retain240 original stripped-input samples, with all load gates met. Batch creation takes1.5373/1.5612x Go, reducing time from S3 by6.1%/4.9%; serial takes1.6983/1.6999x Go, reducing time by3.3%/4.2%. Neither meets parity. File/TCP show no reliable improvement. These isolated runtime measurements do not replace exact committed-head native/CI acceptance, which is still pending for this candidate.

Evidence: `root-completion-receipt-r1` and `root-completion-comparison-r1`. Independent worker-start, queue-page and LSE/LTO target-feature experiments retain their own sources and results; none is included here.

Exact commit `44aa95842` subsequently passed CI20/20 and fresh native test/go:309 top-level/703 total, with all46 nested compiler domains joined and reclaimed. The frozen source still matches. Two fresh-producer stripped-image cohorts retain240 samples and meet every load gate: batch1.5552/1.5567x Go, serial1.6654/1.6993x, file1.9114/1.8939x and TCP1.3533/1.3566x. Batch improves5.6%/5.9% and serial3.1%/2.9% from S3; allfour parity gates remain open. Evidence: `root-completion-freeze-r1` and `root-completion-exact-comparison-r1`.

An independent lazy-logical-baton prototype exposed a base C owner-recycling gap. Full-cache cancellation deliberately preserves a published STARTING request, while ReleaseOwner waited only for DISPATCHING/RUNNING. A returning parent could therefore receive -1 and mark the Go M failed before its C-to-Go entry. A deterministic fixture holds STARTING, fills the cache, and calls ReleaseOwner before dispatch; the old code fails with that exact -1. The fix waits through only a valid published, unacknowledged STARTING request and keeps exact token/slot revalidation and the original strong-return/join protocol. Invalid token/slot controls still reject. Plain, ASan/UBSan and TSan controls pass five times each, and the complete corofleet race suite passes three shuffled repetitions. The fixture is part of the ordinary host test suite. Commit `cfc7b221c` subsequently passed exact-head CI20/20 and fresh native test/go309 top-level/703 total. All46 nested compiler domains exited and were reclaimed; request hashes and unchanged frozen source were verified. PR315 merged as `337eff1c6`, whose tree equals the tested tree. The44aa performance record retains its original producer identity; exact-cfc7 timing remains a separate gate. The lazy-baton prototype remains isolated and unadopted. Evidence: `queued-owner-release-r1` and `syscall-lazy-baton-r1`.


## Lazy native fleet poll storage candidate: 2026-10-23

The native fleet previously rooted eight maximum-size poll buffers even when only the program owner ran. Keep one pointer per route and let the route's unique physical owner allocate its fixed-capacity buffer before its first physical wait. The buffer remains rooted at the same address for process life; capacity, doorbell/poll handling and generation/owner validation remain unchanged. An unmaterialized buffer cannot be used to wait. Invalid deadlines do not allocate.

A deterministic memory-shape test rejects the old131136-byte static array. The new empty root array occupies64 bytes on native64; route materialization and stable-address controls pass. The complete native fleet source-island race group passes in three fresh shuffled processes. That group uses process-lifetime tombstones: a first count3 attempt and its baseline control both fail on repeated startup, and those failures remain recorded. The new focused controls also pass three repetitions, all runtime reactor source contracts pass, and all33 native CLI controls pass, including sieves, GC-zeroed memory and locked-thread ownership at P1/P4.

One warmup plus three alternating measured processes for each population/brand retain24 held runs, with seven RSS samples per process. All release barriers and process joins pass. Same ordinary Go source, exact-cfc7 SDK and default collector settings:

| Parked goroutines | Go RSS | cfc7 RSS | Isolated candidate RSS |
| --- | ---: | ---: | ---: |
| 0 | 4.359 MiB | 6.781 MiB | 6.641 MiB |
| 1,000 | 7.203 MiB | 7.781 MiB | 7.656 MiB |

The observed reduction is128–144 KiB, with a remaining baseline gap to Go. These isolated measurements do not cover a new committed head. Exact-head CI/fullnative and final combined performance/memory acceptance remain required. No other performance prototype is included. Evidence: `fleet-poll-lazy-storage-r1`, including the preserved failed repeat setup and corrected fresh-process controls.

## Remaining project gates

After the four parity targets are met, freeze a final producer and run complete native test/go, all 1082 GOROOT running cases, 180 standard-library fixtures and 73 runnable demos. Keep and classify every failure. The additional 1316 diagnostic/multifile cases remain outside the requested acceptance scope. Recheck size, Go-relative performance, held goroutine RSS and platform boundaries on that final producer; earlier passing versions do not substitute for it. Darwin arm64 is the local measured platform. Linux CI, portable host probes and real Windows/bare-metal deployments retain their separate evidence boundaries.

Evidence directories under `llgo-coro-evidence/20261021`: `wait-callframe-r1`, `wait-callframe-r2`, `native-wait-callframe-r1`, `frame-boundary-cap-r1`, `frame-boundary-scope-r1`, `guarded-forward-r1`, `zero-allocator-r1` and `dynamic-low-fault-r1`. Corrected integration receipts are in `native-boundary-integration-r2` and `native-frame-boundary-integration-r3`. Additional failures and cache controls are in `locked-context-baseline-r1`, `locked-island-trace-r1`, `locked-context-handshake-r1` and `native-standby-capacity-r1`.
