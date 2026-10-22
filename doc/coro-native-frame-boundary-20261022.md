# Function-local native coroutine boundaries: 2026-10-22

This is an intermediate acceptance record. All four remaining parity targets—batch creation, serial creation, standard file I/O and loopback TCP—must reach Go performance before final project closeout. Buffered channels are already faster in the current original-input measurements. No final unified acceptance or new held-goroutine RSS result is claimed here.

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

The exact-576 full native campaign was cooperatively canceled after the CI regression. All recorded PID/start identities have retired; its interrupted nested marker and partial results remain preserved. It is not a complete native pass. Existing full native passing results remain associated with the first WaitGroup producer. A fresh exact corrected integration compiler, full native campaign and PR CI are required before this intermediate change can merge. No previous result is relabeled as the corrected producer.

## Remaining project gates

After the four parity targets are met, freeze a final producer and run complete native test/go, all 1082 GOROOT running cases, 180 standard-library fixtures and 73 runnable demos. Keep and classify every failure. The additional 1316 diagnostic/multifile cases remain outside the requested acceptance scope. Recheck size, Go-relative performance, held goroutine RSS and platform boundaries on that final producer; earlier passing versions do not substitute for it. Darwin arm64 is the local measured platform. Linux CI, portable host probes and real Windows/bare-metal deployments retain their separate evidence boundaries.

Evidence directories under `llgo-coro-evidence/20261021`: `wait-callframe-r1`, `wait-callframe-r2`, `native-wait-callframe-r1`, `frame-boundary-cap-r1`, `frame-boundary-scope-r1`, `guarded-forward-r1`, `zero-allocator-r1` and `dynamic-low-fault-r1`. Integration receipts are recorded separately in `native-boundary-integration-r1`.
