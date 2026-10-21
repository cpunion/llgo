# Native inline boundary follow-up: 2026-10-21

A native push immediately after `PrepareInlineAwaitCompiler` can consume its private `pendingInlineStart` receipt instead of traversing older frames again. A normal leaf pop before `FinishInlineAwaitReturnCompiler` can similarly consume `pendingComplete`. The exact current P/G/frame/handle, immediate edge, depth, completion and native machine-boundary records remain checked. Signal landing always retains the complete `ResumePanicBoundaryMayLand` validation. Root entry, uncertain phases, real suspension, exceptional completion and ancestor unwinding retain the original full validators.

No runtime layout, thread-control declaration or public ownership protocol changes. This follows the semaphore candidate in PR #304 and is not a parity-completion claim.

## Same-input native measurements

Darwin arm64, Go 1.27.0, LLVM 22.1.8, macOS 26.5 SDK, original stripped O2 flags and all 17 original ordinary-Go scenarios. Two independent cohorts each retained one warmup plus seven rotating Go/baseline/candidate triplets: 816 successful processes, no sample exclusion, matching checksums and positive OS peak RSS records. The baseline is frozen R13 (`a17cb2ada`, production-equivalent to documentation follow-up `b88029433`).

| Workload | First candidate / Go | Repeat candidate / Go | First candidate / R13 | Repeat candidate / R13 |
| --- | ---: | ---: | ---: | ---: |
| Buffered channel | 0.152x | 0.156x | 0.926x | 1.010x |
| Batch goroutine creation | 2.502x | 2.442x | 1.022x | 1.000x |
| Serial goroutine creation | 6.788x | 6.465x | 1.061x | 1.016x |
| Standard file I/O | 3.994x | 4.010x | 0.885x | 0.853x |
| Loopback TCP | 1.861x | 1.988x | 0.900x | 0.968x |

File I/O improves by 11.5% and 14.7% in the two cohorts. The first serial-creation comparison is 6.1% slower and the repeat 1.6% slower; no serial-creation gain is claimed. Even the unchanged buffered hot loop varies by 7.4% in the first comparison, so these shared-host samples are not a quiet-host regression guarantee. Four requested parity targets remain open, and earlier gains are not summed across versions.

## Validation and identities

Core and allocator race suites and the complete runtime host suite pass. The real C-fault and foreign-reentry fault probes pass using the frozen runtime. Original Go 1.27 `test/chan/sieve2.go` passes 50 times at P1 and 50 at P4, with every guarded process joined. Core tests cover exact start/normal-return receipts, current/ancestor boundaries, full signal-landing refusal after completion, and malformed immediate ownership. Prior broad native/GOROOT/fixture/demo and large parked-memory records retain their own versions.

Evidence is retained in `20261021/performance-parity-r1/panic-receipt-r15`. Both complete matrices, source hashes, sampler profiles of the R13 baseline, native fault receipts and all 100 sieve logs remain available. Builds use the unchanged R7 compiler; source and compiler identities are recorded separately.

- `llgo` SHA-256: `8fe513240f4b2f4279a46d09fa704428213e66a5b8892c046562b85b51dde773`; 37,595,954 bytes.
- `core-llgo` SHA-256: `6e6cc6062b29e55a52e1c31ff10a563f4bccb38b7c0f7bcea9168448756c79e6`; 4,506,352 bytes.
- `io-llgo` SHA-256: `9e0aa89fff0ac156abf1b8cb0e438da590b252470bccfaaf58e67f4cf9c96ce7`; 7,284,560 bytes.
