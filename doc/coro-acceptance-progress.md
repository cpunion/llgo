# LLVM Coroutine Acceptance Status

This ledger tracks the current implementation status and acceptance gates for
the `llvm-coro` stackless coroutine backend. Historical per-PR phase logs live
in git history rather than dated scratch files.

## Architecture Summary

The `llvm-coro` backend lowers managed Go functions into one of three physical
ABIs selected by whole-program effect and physical planning:

1. **Plain / `$outcome` ABI**: Non-suspending functions execute directly on the
   calling stack with zero coroutine frame allocation, propagating Go panics
   through explicit outcome words.
2. **Synchronous Fast-Ramp (`coro_sync_ramp_{plan,emit}.go`)**: General SSA
   planner and emitter replacing ad-hoc stdlib fast paths. Eligible coroutine
   primaries execute an acyclic or single-await synchronous prefix without
   allocating an LLVM coroutine frame, returning a nil handle on synchronous
   completion and allocating `$coro.slow` (or `$coro.box` on demand) only when
   suspension or fallback is required.
3. **LLVM Coroutine ABI (`$coro`, `ParkABIV2`)**: Stackless coroutine frames
   scheduled over bounded, allocation-free P/executor run slices, supporting
   native multi-P work distribution, `LockOSThread`, foreign callback reentry,
   native fault landing, `testing/synctest`, WASI/JS-WASM, and bare-metal
   Cortex-M targets.

## Continuous Verification Gates (20 CI Lanes)

Every merge to `llvm-coro` must pass all 20 lanes in
[`.github/workflows/coroutine.yml`](../.github/workflows/coroutine.yml):

| Category | Lanes |
|---|---|
| Core compiler, architecture & unit suites | `test (LLVM 22, Go 1.27.0)`, `test target adapters`, `test production build plan`, `test integration` |
| Native multi-P, fault, archive & resource handoff E2E | `test native fleet E2E`, `test native fault landing E2E`, `test native linked E2E`, `test independent archive E2E`, `test reflected resource handoff E2E` |
| Standard library & GC/timer semantics | `test stdlib file and syscall`, `test stdlib TCP`, `test stdlib time, timer and sync`, `test standard timer semantics and GC cancellation`, `test synctest and weak object ownership` |
| Portable / embedded targets | `test WASI command reactor`, `test WASI standard timers and TinyGC cancellation`, `test JS/WASM command reactor`, `test WASM host probes (wasip2)`, `test WASM host probes (wasm-unknown)`, `test Cortex-M standard timer host protocol` |

## Monotonic Architecture Gates

- `TestCoroProductionDirectiveInventory` (`internal/build/coro_directive_gate_test.go`):
  freezes the exact production `//llgo:coro` inventory (`contract: 8`,
  `noblock: 40`, `sync: 38`) and manifest SHA-256; caller-coloring and worker
  directives remain absent.
- `TestCoroArchitectureDebtIsMonotonic` (`internal/build/coro_architecture_gate_test.go`):
  enforces monotonic non-increase across all compiler/runtime architecture debt
  counters (`legacyWait: 0`, `nativeFork: 0`, `stagedFeatureGate: 0`).
- `TestRuntimeCoroPollPackedInlineAttemptCWrapper` (`runtime/poll_inline_attempt_source_test.go`):
  enforces the non-blocking `recv`/`send` + `MSG_DONTWAIT` inline poll contract.
- `TestNativeCacheBoundsZeroesAndSerializes` (`runtime/internal/coroalloc/cache_native_c_test.go`):
  verifies exact per-bin cache capacity bounds, zeroing, and multi-thread exit
  magazine drainage under `-std=c11 -Wall -Wextra -Werror -pthread`.

## Frozen Release Acceptance Scope

Final release qualification freezes one commit on `llvm-coro` and verifies:

- Complete native `test/go` suite (`0` failures / `0` skips).
- Selected Go 1.27.3 `GOROOT/test` runtime corpus (1,085 cases: 1,082 baseline
  cases plus 3 Go 1.27.3 additions).
- Selected standard-library package suite (180 packages).
- Runnable Go demo corpus (73 demos).
- Same-source, same-commit three-way performance, RSS, `vmmap` physical
  footprint, build time/RSS, and stripped binary size comparison against
  official Go 1.27.3 and stock LLGo (`3c6b33ecc`), documented in
  [`doc/coro-performance-baseline.md`](coro-performance-baseline.md).
