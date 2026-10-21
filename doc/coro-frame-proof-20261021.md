# Current native frame proof: 2026-10-21

The C recover marker reads only the saved scalar in its own current frame record. LLVM leaf inference previously treated both `frameaddress(0)` and that read as possible native faults. Its capability then propagated through `recoverMark` and WaitGroup method publication into ordinary goroutine creation. This change recognizes the exact current-frame `i64` read on LP64 AArch64/x86-64 Darwin/Linux. The C expression keeps that read visible in unoptimized proof IR without a temporary-pointer alias. The mark and native frame liveness walk retain their existing meaning. Actual AArch64 O2 marker assembly remains identical.

The proof admits no caller-frame dereference, derived address, write, wide or over-aligned read, naked function, unsupported ABI, or arbitrary pointer. Those retain fault demand. Other intrinsics keep their original fault behavior. Progress, retention, thread-control contracts, runtime layouts, signal landing and ownership validation do not change.

LLVM 22's target implementations read the current FP for depth zero and walk memory only for older depths: [AArch64 lowering](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/llvm/lib/Target/AArch64/AArch64ISelLowering.cpp), [x86 lowering](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/llvm/lib/Target/X86/X86ISelLowering.cpp). The current frame record is materialized when its address is taken. The language reference distinguishes [current and older frames](https://releases.llvm.org/22.1.0/docs/LangRef.html#llvm-frameaddress-intrinsic).

## Original-input measurements

Darwin arm64, Go 1.27.0, LLVM 22.1.8 and macOS 26.5 SDK. The baseline is frozen R15, production-equivalent to merge `3593d4bb3` (tested source HEAD `71f4402c8`). All 17 original ordinary-Go scenarios, counts, stripped default O2 flags and non-LTO settings are unchanged. Two independent cohorts each retain one warmup and seven rotating Go/baseline/candidate samples: 816 successful runs, checksums and OS peak-RSS observations. No sample exclusion or diagnostic-count substitution. Shared-host variability remains visible in the full records.

| Workload | First / Go | Repeat / Go | First / R15 | Repeat / R15 |
| --- | ---: | ---: | ---: | ---: |
| Buffered channel | 0.157x | 0.157x | 0.987x | 0.994x |
| Batch goroutine creation | 2.363x | 2.410x | 0.950x | 0.953x |
| Serial goroutine creation | 5.658x | 5.738x | 0.891x | 0.884x |
| Standard file I/O | 3.933x | 3.784x | 0.994x | 0.994x |
| Loopback TCP | 1.994x | 1.997x | 0.994x | 1.003x |

Serial creation improves 10.9%/11.6% and batch creation 5.0%/4.7% in the two cohorts. Four requested performance parity targets remain open. Earlier gains are not added across versions. The core image decreases from 4,506,352 to 4,423,680 bytes (82,672 bytes); the I/O image remains 7,284,560 bytes. No new large parked-memory acceptance is claimed.

## Validation

Exact current-frame proof has deterministic red/green coverage. Older frames, following the saved pointer, derived addresses, writes, over-wide/aligned accesses, naked functions, ILP32, bare metal and Wasm retain fault demand. The complete LLVM proof suite, all `TestCoro` compiler tests, the real native target-plan proof, and real C producer inference at O0/O2/Oz pass. Real C-fault and foreign-reentry recovery probes pass with the candidate compiler; each guarded process has exited and been joined.

The seven-file native channel/semaphore/equality/caller/recover gate passes at P1/P4, each count=3: 84 top-level passes, zero skips, all processes joined. The guarded native build takes 263.6 seconds with a 5,143.5 MiB sampled peak tree RSS. CI includes this gate and the complete LLVM proof suite.

Evidence is retained under `20261021/performance-parity-r1/frame-slot-r21`: complete source hashes, build identities, all measurements, red/green proof, producer, compiler/target and fault logs. The separate R17 initial-run receipt and R18/R20 opt-in LTO diagnostics are not adopted and are not acceptance substitutes. Earlier full native/GOROOT/fixture/demo receipts retain their own versions.

Compiler SHA-256: `b36c4f7756c0146b9216b803353461a5d8a391a955a29f96988109595556ae2b`. Core image SHA-256: `a13b88b6655dfcfec2f0e25e8ce5a08b8b32432ad183a623211c1c824b4536c6`. I/O image SHA-256: `a19816ea2f0722d2a1339ebd6ad2b2111f62b1d45521c89aebc329d48f605135`.
