# Private single-slot channel follow-up: 2026-10-21

A numeric channel created with constant capacity one can use an inline buffer transaction when its entire SSA allocation stays within its creating function. This removes two runtime helper calls and channel gates from each private send/receive pair. The compiler derives field indices from the selected runtime source type before LLVM emission and retains sparse proofs only for the applicable physical instructions.

The proof inspects actual SSA operands across the whole function, including all direction aliases. Passing, returning, storing, capturing, converting to an interface, selecting, deferring or merging the channel through a Phi rejects the entire allocation. Pointer payloads and sizes other than four or eight bytes use the existing runtime path. Both physical-plan freeze and body emission revalidate the proof, including source mutations with stale referrer lists.

The inline transaction checks capacity, element size, buffer, count, closed-send state, timer/synctest state and the opposite waiter queue. Every miss enters the existing complete try-or-park operation. Numeric receives retain a volatile buffer clear for conservative collectors. Shared channels retain their existing synchronization; runtime layouts, scheduler protocols and public APIs do not change.

## Same-input native measurements

Darwin arm64, M4 Max, Go 1.27.0, LLVM 22.1.8, macOS 26.5 SDK, stripped default Os, no LTO flag. These are elapsed medians from seven rotating triplets after one warmup triplet for every one of the 17 original ordinary-Go scenarios. All 408 processes exited successfully and matched their checksums. Inputs, counts, compiler flags and raw samples are retained. Unrelated host load remains present; these are observations on this host rather than a quiet-host or cross-platform guarantee.

The comparison baseline is merged memory-fix tree `f1574c2f0b5f8e6c3b6bc59ac70a117690077e0a`. Idle and park elapsed fields are excluded from throughput claims.

| Requested workload | Go ms | Merged baseline ms | Candidate ms | Candidate / Go |
| --- | ---: | ---: | ---: | ---: |
| Buffered channel | 82.057 | 80.936 | 12.869 | 0.157x |
| Batch goroutine creation | 8.519 | 21.735 | 21.498 | 2.524x |
| Serial goroutine creation | 17.030 | 146.908 | 146.783 | 8.619x |
| Standard file I/O | 11.463 | 49.321 | 50.727 | 4.425x |
| Loopback TCP | 109.225 | 224.287 | 214.975 | 1.968x |

Only the buffered target reaches parity in this cohort. The other four targets remain open. Earlier panic-boundary experiments and prototype channel measurements retain separate frozen versions and are not added to these results.

Core build peak tree RSS was 1,008.7 MiB versus 951.0 MiB in the fresh baseline build; I/O was 2,405.4 versus 2,469.9 MiB. These individual resource samples are not paired causal memory estimates. The sparse source-only planning replaced an earlier prototype's eager recursive type conversion and per-instruction metadata; that prototype's core peak was 1,185.6 MiB. The production proof must not reintroduce either cost.

## Validation and identities

All compiler `TestCoro` tests passed. Allocation classification rejects escaping and stale-SSA cases, and standalone LLVM verification covers native and Wasm32 production layouts with no helper call in the transaction. Six real-runtime tests cover integer and floating payloads, GC between send/receive, direction aliases, close semantics, shared goroutine access and synctest fallback. Each passes three times at P1 and P4. The native fault CI lane compiles and repeats those real-runtime tests.

This is an affected compiler follow-up. Prior full native, GOROOT, fixture and demo acceptance records retain their own version identities; they are not relabeled as passes of this candidate.

Retained evidence: `20261021/performance-parity-r1/local-channel-r7` under the acceptance artifact root. `sources.json` identifies the frozen inputs, `build-result.json` contains joined build receipts, and `throughput-7triplets` contains all samples and image hashes.

- `llgo` SHA-256: `8fe513240f4b2f4279a46d09fa704428213e66a5b8892c046562b85b51dde773`; 37,595,954 bytes.
- `core-llgo` SHA-256: `9d602eb672fae9f69f6dedf0dc824e7e01adf12b8a4c43b96c51cd0cc6eadbed`; 4,505,696 bytes.
- `io-llgo` SHA-256: `f3e6b0f58ea61f9e8334c279a14bca71bc75815432d783a125c79f3f84ffb2bf`; 7,283,888 bytes.
