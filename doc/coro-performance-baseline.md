# LLVM Coroutine Performance Baseline

This document defines the repeatable same-source benchmark methodology and
records the current performance checkpoint for `llvm-coro` compared against
official Go and stock LLGo.

## Benchmark Methodology

- **Fixtures**: Standard-Go programs under
  [`benchmark/coro_core/testdata/`](../benchmark/coro_core/testdata/) compiled
  identically across official Go, stock LLGo (`3c6b33ecc`), and `llvm-coro`
  using `-Os -p=1 -trimpath -ldflags='-s -w'` (for LLGo) and
  `-trimpath -ldflags='-s -w'` (for Go).
- **Pairing & Isolation**: Samples alternate brands in reversed order per round
  with warmup excluded, verifying identical checksums and exit status across all
  brands.
- **Metrics**:
  - **Throughput / Latency**: Wall time measured inside the fixture (excluding
    process startup) plus retired instruction counts across `GOMAXPROCS=1` and
    `GOMAXPROCS=4`.
  - **Memory**: Same-commit `ps` RSS and Darwin `vmmap` physical footprint on
    `empty` (`pure_idle`) and `held` (0 / 1,000 / 100,000 parked goroutines
    post-`runtime.GC()`).
  - **Build & Artifact Cost**: Cold and warm `./io` build time, peak build
    process-group RSS, and stripped binary sizes.

## Key Optimization Milestones

1. **General Synchronous Fast-Ramp (`cl/coro_sync_ramp_{plan,emit}.go`)**:
   Replaced four stdlib-specific SHA-256-pinned compiler passes with a general
   SSA fast-path planner and demand-boxed SingleAwait emitter, removing frame
   allocations on uncontended `sync`/`internal/poll`/`os`/`net` fast paths.
2. **Forwarding Poll-Wait Inlining & Native Readiness (`cl/coro_poll_wait.go`,
   `runtime/_patch/net`, `runtime/_patch/internal/poll`, `runtime/internal/coro`)**:
   Inlined single-block poll-wait wrappers into the caller's `ParkABIV2` frame,
   added acyclic `$outcome` poll prepare/finish helpers, direct owner kqueue
   readiness completion (`CompleteOwnerPollEventV2`), and same-G parent
   continuation chaining (`ContinueIssuedExecutorParentResume`) with zero
   out-of-line runtime calls in `coroReduceExecutorRunActionPreparedV1`.
3. **Multi-Core Fleet & Allocator Scaling (`runtime/internal/coro`,
   `runtime/internal/coroalloc`, `runtime/internal/corodoorbell`)**:
   Added bitmask peer-demand gating (`HasPeerRunnableDemand`), single-peer
   co-location hints (`HasReadySurplus`), static-pool `pthread_key` per-thread
   frame magazines (`cache.c`), lock-free overflow `GC_free` batching
   (`free_batch_gc.go`), and bounded pre-sleep spinning.

## Stage C Reference Snapshot (Darwin arm64, Go 1.27.3)

Measured on Apple M4 Max (`darwin/arm64`, LLVM 22, Go 1.27.3) prior to final
Stage D frozen three-way archival (ratio = `llvm-coro / Go 1.27.3`, lower is
better):

| Workload | `GOMAXPROCS=1` (`llvm-coro / Go`) | `GOMAXPROCS=4` (`llvm-coro / Go`) |
|---|---:|---:|
| `buffered` | ~0.14× | ~0.14× |
| `spawn 10k` (batch) | ~0.33× | ~0.71× |
| `park 10k` | ~0.51× | ~1.3× |
| `select` | ~0.67× | ~0.65× |
| `handoff` | ~0.82× | ~0.71× |
| `spawn 1×10k` (serial) | ~0.88–0.91× | ~0.62–0.75× |
| `parallel` | ~0.95× | ~0.94× |
| `file-syscall` | ~1.01× | ~0.98–1.01× |
| `file` | ~0.96–1.02× | ~1.03–1.06× |
| `tcp` | ~1.04–1.08× | ~1.01–1.16× |

On Darwin arm64 `tcp`, user-space LLGo overhead accounts for <3% of samples;
remaining latency vs Go is dominated by XNU kernel time (`kevent` and
`recv(MSG_DONTWAIT)`/`send(MSG_DONTWAIT)` vs Go's `read`/`write` on non-blocking
sockets, retained per `TestRuntimeCoroPollPackedInlineAttemptCWrapper`).

See [`benchmark/coro_core/README.md`](../benchmark/coro_core/README.md) for
invocation commands and raw workload definitions.
