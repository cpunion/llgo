# WaitGroup caller-frame opportunity: 2026-10-22

A brief `sync.WaitGroup.Wait` can yield once and return after its peer completes without publishing a separate waiting frame. The compiler lifts this opportunity into an exact direct caller. The standard-library patch keeps its complete waiting loop in `llgoWait(opportunity bool)`: public `Wait` passes true; a caller which already yielded passes false if it still needs the loop. Semaphore publication, wakes, reuse checks, nil panics, race synchronization and synctest association retain their original implementations.

Admission requires the exact normalized wrapper and loop bodies, the existing canonical Add/layout/disabled-race/false-bubble proof, the real atomic Uint64.Load implementation and its frozen intrinsic binding, and a closed program without execution domains. Changed source, bindings, receiver/parameter names, predeclared booleans or noinline directives decline the optimization. The ready-peer query uses its existing synchronous outcome ABI. Both helper identities remain bound to the immutable logical plan. Typed call owners are retained before the new yield and through its completion, including integer-carried pointer provenance.

The first load bypasses the call only for an entirely zero state. A positive unbubbled counter with no waiter may query the same advisory ready-peer predicate and yield through the ordinary checked transition. After resumption another sequentially consistent load may complete the call. Every other case enters the full loop. Consuming the opportunity before yield prevents a second opportunity on that fallback.

## Original-input measurements

Darwin arm64, M4 Max, pinned Go 1.27.0, LLVM backend 22.1.8, actual LLD 23.1.3, macOS SDK 26.5, default Os, no LTO. Both original benchmark sources and all 17 scenario counts are unchanged. Two cohorts retain one warmup and seven measured rotating Go/baseline/candidate triplets: 816 samples, checksums and process peak RSS. All owned compilation finished before timing; unrelated shared-host load remains in the records. No sample was excluded.

The baseline is the accepted PR 311 runtime producer `a22d07fe7`, production-equivalent to merge `231f4f309` apart from its later workflow change. These are paired measurements of this candidate, not an accumulation of earlier improvements.

| Workload | First / Go | Repeat / Go | First / baseline | Repeat / baseline |
| --- | ---: | ---: | ---: | ---: |
| Buffered channel | 0.160x | 0.151x | 1.012x | 0.962x |
| Batch goroutine creation | 2.352x | 2.377x | 0.951x | 0.988x |
| Serial goroutine creation | 2.399x | 2.371x | 0.651x | 0.648x |
| Standard file I/O | 2.242x | 2.166x | 0.977x | 0.994x |
| Loopback TCP | 1.606x | 1.543x | 1.055x | 0.984x |

Serial creation decreases 34.9% and 35.2%. Batch, file and TCP variations do not establish a stable improvement; the first TCP cohort is 5.5% slower than baseline and is retained. **All four requested performance parity targets remain open.** The core image remains 2,954,960 bytes and the I/O image 4,898,368 bytes. No new held-goroutine RSS result is claimed.

## Validation and remaining acceptance

Eighteen source/binding admission and rejection cases pass, including changed atomic loads, observable extra work, stale/missing classification, execution domains, race mode and shadowed booleans. The generated caller has atomic loads, its own yield and the complete-loop fallback; LLVM verification and mandatory coroutine splitting pass. Host source-patch checks and three repetitions of the selected Go race semantics pass.

The original standalone eleven-check program and a separate eleven-check program without the locked-thread capability each pass three times at P1 and P4. The latter includes a peer which yields before Done, requiring fallback after the caller's opportunity. Original GOROOT sieve1 and sieve2 match fresh Go reference stdout at P1 and P4, each repeated three times. All these build/run domains are joined. A symbol-bearing strip capture has identical executed text to the measured image and confirms admission in the original `main.spawn` caller.

The complete compiler race/shuffle suite and fresh full native test/go campaign are running separately. Their eventual receipts must identify this source and compiler. Earlier full native, 1082 GOROOT, 180 fixture and 73 demo results retain their own versions and do not constitute acceptance for this change. Final project acceptance must follow a new freeze after all four performance targets reach parity.

Evidence: `20261021/wait-callframe-r1` under the acceptance artifact root. `source-manifest.json`, `measurement-source-proof.json`, `native-r2/active-admission.json`, all guarded native logs and `performance-r2/cohort-{1,2}` retain inputs and outcomes. An initial external harness setup failure was stopped during compilation, joined and classified separately; it supplies no native or timing pass. Explicit O3/full-LTO and source-expansion diagnostics remain separate evidence and were not adopted as measurements of this candidate.

Compiler SHA-256: `a7d0154cda012697a739847b244386e0123988d72b58fd74be4216880c58ea56`. Core image SHA-256: `e02a3db8419812f3af8342e4443b34596fac2a4959edf8f8638d9987b3860e81`. I/O image SHA-256: `ba184d745c011d22a84c05f12e6efd35ca85cb93297ec92388fe2c20c66e36f4`.
