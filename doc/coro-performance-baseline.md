# LLVM coroutine performance baseline

This document records a reproducible local comparison between the current
stackless coroutine prototype and LLGo `main`. It is a directional engineering
baseline, not a cross-machine performance claim.

## Compared revisions and environment

- LLGo `main`: `ab2fe9c81523`
- coroutine base: `15feb5690677`, plus exact-interface devirtualization,
  CFG-based preemption safepoints, and the `Tfn`/`Ifn` ABI correction
- Go 1.26.5, LLVM 22.1.8, Darwin arm64, Apple M4 Max, 16 logical CPUs
- cold compiler processes were limited to 4 GiB; generated programs were
  limited to 1 or 2 GiB depending on the workload
- timings below are medians unless a range is shown

The same source fixtures were compiled by both compilers. Native executable
footprints use Mach-O section sizes. A benchmark loop is itself a coroutine
preemption boundary, so the single-call and 16-call batch results are reported
separately.

When comparing uncommitted compiler variants, give each variant an independent
empty `GOCACHE`. The development compiler identity is revision-based, so `-a`
alone can still reuse package artifacts produced by a different dirty-tree
variant and invalidate a code-size comparison.

## Compiler analysis and emission

The final native `syncbench` executable has:

| Metric | `main` | coroutine | Ratio |
| --- | ---: | ---: | ---: |
| File bytes | 5,348,272 | 18,344,416 | 3.43x |
| `__text` bytes | 1,700,732 | 10,494,276 | 6.17x |
| linked resume entries | 0 | 5,169 | — |
| linked destroy entries | 4 unrelated suffix matches | 5,169 | — |
| cold-build peak RSS | 1,151.6 MiB | 3,008.1 MiB | 2.61x |

The minimal concurrent program is a harsher fixed-cost measurement:

| Metric | `main` | coroutine | Ratio |
| --- | ---: | ---: | ---: |
| File bytes | 128,224 | 1,189,008 | 9.27x |
| `__text` bytes | 24,796 | 429,268 | 17.31x |
| linked coroutine ramps | 0 | 47 | — |

This establishes that native code size is still a primary deficit. The large
native zero-fill region is mostly the fixed fleet/M directory and is not stored
in the executable, but it is also unsuitable as the default embedded profile.

An exact guarded slice/string-index experiment was deliberately removed after
measurement. Its complete analysis, digest, and lowering plumbing removed only
three coroutine bodies, reduced text by 8,356 bytes, and reduced the file by
768 bytes. That result did not justify roughly 470 lines of additional
cross-layer machinery.

## Core operation throughput

| Benchmark | `main` | coroutine | Coroutine / `main` |
| --- | ---: | ---: | ---: |
| Direct call, one per poll loop | 1.30 ns/op | 70.42 ns/op | 54.2x |
| Exact interface call, one per poll loop | 3.90 ns/op | 58.20 ns/op | 14.9x |
| Direct call, batch of 16 | 9.52 ns/batch | 43.23 ns/batch | 4.54x |
| Exact interface call, batch of 16 | 49.80 ns/batch | 43.43 ns/batch | 0.87x |
| Buffered channel round trip | 15.71 ns/op | 52.42 ns/op | 3.34x |
| Two-ready-case select | 27.69 ns/op | 63.35 ns/op | 2.29x |
| Unbuffered two-channel handoff | 8.67 µs/op | 61.27 µs/op | 7.07x |

The batch result is the useful call-path measurement: one backedge poll is
amortized across 16 calls. Exact interface devirtualization makes the coroutine
direct and interface batches equivalent, while the single-call result exposes
the current poll/requeue tax. The handoff path remains the largest runtime
hotspot and also reports about 29 KiB/op of allocator traffic versus 384 B/op
on `main`.

Relative to the unoptimized coroutine snapshot, the two compiler changes are
material:

- exact-interface lowering reduced the fixture's interface call from roughly
  6–16 µs/op to about 70 ns/op;
- the CFG safepoint plan reduced direct and exact-interface single-call loops
  from about 70 ns/op to about 35 ns/op in the initial 16-P measurement.

## Stackless concurrency and target portability

For 10,000 joined goroutines, the stackless binary measured
6.35–6.99 µs/op with 65.1 MiB peak RSS. The `main` backend measured
65.7–268.4 µs/op with 498.8 MiB peak RSS in the same run. This workload is
favorable to stackless frames and unfavorable to the current native
thread-per-goroutine backend, but it demonstrates the intended scaling
property.

For parked goroutines:

| Backend | Idle RSS | Parked goroutines | Peak RSS | Approx. incremental RSS |
| --- | ---: | ---: | ---: | ---: |
| `main` | 5.4 MiB | 1,000 | 25.0 MiB | 20.1 KiB/G |
| coroutine | 24.1 MiB | 10,000 | 47.2 MiB | 2.4 KiB/G |

The coroutine runtime has a much higher fixed native scheduler cost but a much
lower incremental cost per parked goroutine. Embedded profiles must remove the
native fleet tax rather than treating this native measurement as target
neutral.

The same minimal concurrent source builds for `wasip1` only on the coroutine
branch:

- output size: 270,709 bytes;
- code section: 257,753 bytes;
- data section: 11,060 bytes;
- 566 functions and 13 WASI imports;
- execution succeeds under Wasmtime at about 18.4 MiB host RSS.

The `main` build attempts to link host libuv and BDWGC archives into WebAssembly
and fails. This is evidence that the coroutine runtime core is independent of
those native libraries; it is not yet evidence of bare-metal completion.
RP2040 still lacks board hooks plus required atomic/compiler-rt/libc support in
both compared configurations.

## Next emission optimization

The full-program plan exposed a higher-leverage opportunity than more local
no-unwind pattern matching. After the narrow index experiment, 571 emitted
coroutines still had exactly `OutcomeStructured` and no real wait, park, or
yield effect. Reverting the three locally proven functions restores that count
to 574 in the final baseline.

These functions should not be reclassified as `NoSuspend`: they may still
panic, recover, run defers, or propagate Goexit. The safe optimization is a
separate physical `outcome-plain` entry:

1. Preserve logical `OutcomeStructured` and `MayUnwind` facts.
2. Select `outcome-plain` only when the function has no `YieldOnly`,
   `AwaitStructured`, `MayPark`, platform/host/foreign wait, recursion, or
   `NeedsPreempt`.
3. Require a whole-call-path atomic-cost proof before removing the coroutine
   poll capability.
4. Pass caller-owned result and completion storage through a hidden physical
   ABI. Normal return, panic, recover, and Goexit publish one terminal outcome
   and return synchronously; no LLVM frame, ramp, resume, or destroy entry is
   created.
5. Let an exact coroutine caller reconcile that immediate outcome inline.
   Dynamic descriptors and archive metadata gain an explicit entry kind; they
   must not infer it from a code pointer.
6. Keep one source body. A boundary adapter may translate the logical outcome,
   but it must not clone the body or fall back to native unwind across a
   stackless boundary.

This must be a separate replacement cohort. Its acceptance gate is:

- native and wasm32 panic/recover/defer/Goexit matrices pass;
- no eligible function retains ramp/resume/destroy symbols;
- the representative standard-library fixture reduces text materially;
- direct/interface/channel regressions remain within an explicit threshold;
- unsupported dynamic/archive cases fail closed rather than silently choosing
  a legacy plain call.

Until that ABI exists, the current comparison remains valid and the 574
outcome-only bodies are an identified size opportunity, not an already claimed
optimization.
