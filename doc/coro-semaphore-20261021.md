# Brief semaphore wait follow-up: 2026-10-21

[PR #304](https://github.com/cpunion/llgo/pull/304) targets `llvm-coro` and builds on the private-channel optimization in [PR #303](https://github.com/cpunion/llgo/pull/303). Each must pass its exact-head CI before merge.

Before publishing an ordinary semaphore wait, its active unlocked resume may yield once if its current P already has a ready peer. This single opportunity is outside the retry loop. A token arriving meanwhile is consumed by the original atomic CAS; a remaining wait retains the existing adjacent prepare/park span, keyed registry, cancellation and cleanup protocol. Durable waits, execution domains, locked owners and empty local queues retain direct park. The hint returns only a boolean and never transfers queue or scheduler authority.

Two small common-path changes accompany it. An empty native panic cursor returns before repeating the landing proof, after checking the program capability; any actual token still requires the complete landing proof. General regular-memory equality uses the existing portable `clite.Memcmp` fallback after preserving empty/scalar/alignment cases. No field comparison or scheduling ownership check is removed.

## Frozen same-input results

Evidence: `20261021/performance-parity-r1/sema-final-r13` and `runtime-scalar-r14-7quartets` under the retained acceptance root. Darwin arm64, Go 1.27.0, LLVM 22.1.8, macOS 26.5 SDK, original stripped default Os flags, original source and counts, shared host. All 17 scenarios ran one warmup plus seven rotating quartets (544 successful processes, all checksums and positive OS peak RSS records retained). The four images are original Go, private-channel commit `266633c8b`, this runtime candidate and a separate scalar-struct compiler prototype. Only the runtime candidate is adopted here; the last prototype's small differences do not establish the requested throughput improvement.

| Requested workload | Go ms | Private-channel baseline ms | Runtime candidate ms | Candidate / Go |
| --- | ---: | ---: | ---: | ---: |
| Buffered channel | 84.177 | 13.133 | 13.316 | 0.158x |
| Batch goroutine creation | 8.570 | 22.484 | 21.365 | 2.493x |
| Serial goroutine creation | 17.639 | 150.751 | 113.308 | 6.424x |
| Standard file I/O | 10.660 | 50.494 | 49.360 | 4.630x |
| Loopback TCP | 99.450 | 224.081 | 223.428 | 2.247x |

Serial creation improves by about 25% versus the same-cohort private-channel baseline. Four requested parity targets remain open. These are shared-host observations and do not establish a portable or quiet-host guarantee. Earlier cohorts are not summed into these gains. The 0/1,000/5,000 parked measurements remain in the quartet receipt; the prior large-population memory and full native/GOROOT/fixture/demo acceptance records keep their own versions.

## Validation

The complete runtime host suite and core/allocator race suites pass. The closed real native scheduling-island plan passes with all original conservative thread-control contracts. Real C-fault and foreign-reentry fault probes pass using the frozen production runtime. Ten real-runtime channel/semaphore/equality tests pass three times each at P1 and P4 (60 top-level passes, no skips), including a locked owner and every byte of an unaligned 255-byte equality operand. The CI fault lane repeats those tests. Source proof checks require the one yield to remain outside the retry loop and retain the adjacent source publication/park span. Core tests verify that hint reads do not consume a peer and reject missing, queued, critical and locked contexts. Pending-panic tests retain a token during critical execution, then detach it exactly once.

Exploratory R9/R10/R11/R12 cohorts contained legacy synchronous annotations on native owner creation/reuse and are not adopted: their C paths can wait for owner acknowledgement. Their logs and rejection reasons are retained. This candidate preserves all original thread-control declarations. R13 also moves the bounded opportunity out of the retry loop rather than carrying a once flag across suspension.

- `llgo` SHA-256: `8fe513240f4b2f4279a46d09fa704428213e66a5b8892c046562b85b51dde773`; 37,595,954 bytes.
- `core-llgo` SHA-256: `96ac6e6c2dbd1fdd19a8be1778986ed40a34ec77c47d5419d0a536ab2e5ac9e4`; 4,506,112 bytes.
- `io-llgo` SHA-256: `d3e5714929938763b8f6cae4b3b1d394b1f36e2ca346e919667efd71de92545f`; 7,284,320 bytes.
