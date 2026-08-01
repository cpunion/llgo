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

## Outcome-plain V0 emission result

The full-program plan exposed a higher-leverage opportunity than more local
no-unwind pattern matching. After the narrow index experiment, 571 emitted
coroutines still had exactly `OutcomeStructured` and no real wait, park, or
yield effect. Reverting the three locally proven functions restores that count
to 574 in the final baseline.

These functions must not be reclassified as logically `NoSuspend`: they may
still panic, recover, run defers, or propagate Goexit. The first
`outcome-plain` replacement cohort now implements the separate physical entry
without changing those logical facts:

1. Preserve logical `OutcomeStructured` and `MayUnwind` facts.
2. Select `outcome-plain` only when the function has no `YieldOnly`,
   `AwaitStructured`, `MayPark`, platform/host/foreign wait, recursion, or
   `NeedsPreempt`.
3. V0 accepts only source-call-free, acyclic leaves whose frozen semantic
   recipes are `Debug/Phi/Jump/If/Return/Panic` and whose instruction cost is
   within `MaxPlainInstructions`. Roots, spawn/address/dynamic uses, recursion,
   raw or lowered incoming calls, and all external operations fail closed.
4. The hidden V0 ABI is `(g, out, completion, args...) -> void`.
   `completion` is `{status uint32, typeWord unsafe.Pointer, dataWord
   unsafe.Pointer}`; normal return and panic publish exactly one terminal
   outcome and return synchronously. The status space reserves Goexit for the
   next capability cohort. No LLVM frame, ramp, resume, or destroy entry is
   created.
5. An exact coroutine caller reconciles the immediate return/panic/Goexit
   status inline. `ManagedEntryOutcomePlain` is an explicit frozen plan and
   archive-summary dimension; consumers never infer it from a code pointer.
   An active importer also rejects a producer cost above its own finite
   `MaxPlainInstructions` budget instead of silently extending the no-poll gap.
6. Keep one source body. A boundary adapter may translate the logical outcome,
   but it must not clone the body or fall back to native unwind across a
   stackless boundary.

The implementation is issued by `ProgramIR` from frozen semantic recipes and
uses the same exclusive physical-emission transaction as a full coroutine.
The architecture debt gate records no second plan lookup, body session, or
body-state field. It fixes the legacy coroutine-only lifecycle entry counts at
zero, the shared managed begin/bind/complete consumers at exactly two, and the
exclusive capability arms at exactly `coroutine/outcome`; a renamed or second
session therefore cannot bypass the gate. Plan digest, plan summary and
library-effect schemas were advanced together, and an imported exact call
consumes the producer's published `$outcome` entry.

This cohort also crossed the design document's approximately 1,000-line
production review stop. Relative to `cpunion/llgo:llvm-coro` after PR #110
(`09ecca3bd`), the audited diff is production `+1,173/-125` (net `+1,048`),
tests `+806/-32` (net `+774`) and documentation `+131/-44` (net `+87`). The
review accepted the narrow overage
because the new volume closes one physical capability through graph planning,
canonical digest/summary, archive metadata, owner preflight, ABI emission and
caller reconciliation; it does not add another raw-SSA scan, fixed point,
source-body clone, LLVM-coroutine CFG builder, or emission session. The next
direct-call-DAG cohort must extend these same dimensions and transaction. A
second analysis/emission path is a failed architecture gate, not justified by
another 1,000-line allowance.

The exact native/wasm fixture compares V0 with the previous all-coroutine
emission by setting the leaf budget to `-1` for the baseline and `64` for V0.
Both variants compile the same source and run the same CoroSplit and LLVM O2
pipelines:

| Target | Post-split IR baseline | Post-split IR V0 | O2 object baseline | O2 object V0 |
| --- | ---: | ---: | ---: | ---: |
| Darwin arm64 | 80,833 B | 39,326 B | 4,600 B | 3,008 B (-34.6%) |
| wasm32/WASI | 80,797 B | 39,308 B | 3,485 B | 2,390 B (-31.4%) |

For this one-leaf fixture V0 also removes one frame allocation, one resume
entry, one destroy entry and three parent await-hook references. This is a
deterministic physical-cost gate, not yet a wall-clock throughput claim. The
native return-and-panic E2E additionally compiles, links and executes both
terminal payload/control-flow paths; it does not yet claim traceback-frame
fidelity for the removed leaf. Native and wasm structural tests verify that
CoroSplit cannot manufacture `$outcome.resume` or `$outcome.destroy`.

### Direct-call DAG cohort

The next cohort extends the same ProgramIR, SSA call plan, fixed point,
physical-emission session, completion ABI and archive metadata; it does not add
a second SSA scanner or coroutine CFG builder. A local candidate may add only
ordinary `Call` and `Extract` recipes to the V0 leaf language. Bottom-up
selection requires every counted call to be one exact, closed, static managed
edge whose target already publishes a proven local or imported outcome entry.
`AtomicCostDAG` is the longest path over the frozen ProgramIR CFG, with the
complete transitive callee cost added at each exact source call occurrence. Roots, references,
compiler-lowered incoming calls, open/dynamic targets, recursion, CFG cycles,
cost overflow and budget excess retain the full coroutine.

An outcome DAG forwards the scheduler-owned G, uses caller-owned result and
completion storage, and republishes child Panic or Goexit without scheduling.
A target-layout check rejects the optimization before plan selection when a
call result would exceed the native-stack single-object bound; the full
coroutine fallback continues to use managed result storage. The same proof
class and its content-addressed certificate are carried by plan digest v33,
diagnostic summary v7 and library-effect summary v5, so an imported DAG proof
can close a local DAG and contributes its full producer path certificate.

For the exact `Parent -> Middle -> Leaf` native/wasm fixture, disabling the
budget retains three coroutines while the DAG cohort retains only the root:

| Target | Post-split IR baseline | Post-split IR DAG | O2 object baseline | O2 object DAG |
| --- | ---: | ---: | ---: | ---: |
| Darwin arm64 | 125,975 B | 41,778 B | 6,240 B | 3,104 B (-50.3%) |
| wasm32/WASI | 125,921 B | 41,760 B | 4,720 B | 2,487 B (-47.3%) |

This removes two frames, two resume entries and two destroy entries. Native
and wasm32 verify before and after CoroSplit; the middle outcome body directly
calls the leaf outcome body, contains no coroutine intrinsic or scheduler
await hook, and republishes Panic/Goexit statuses to its parent.

### Path and post-LLVM certificate cohort

ProgramIR now freezes one minimal block projection per defined function:
canonical block/successor indexes, non-debug semantic work per block, and each
ordinary call at its exact source-instruction coordinate. Outcome selection
computes the longest entry-to-terminal path, takes the maximum rather than the
sum across mutually exclusive branches, rejects CFG/call cycles and overflow,
and hashes the projection together with FunctionID, proof class, cost, and the
complete transitive callee certificates. Physical planning reconstructs that
same certificate from the frozen direct-outcome recipes; any logical/physical
edge disagreement fails before emission.

Each emitted local outcome body publishes compiler-only
`llgo.coro.atomic_cost` metadata. Imported outcome callees publish dependency
rows containing their already ABI-checked producer certificate. A fail-closed
LLVM verifier runs both before and after CoroSplit, walks the actual LLVM CFG
and certified direct-call DAG, and rejects cycles, indirect calls, coroutine
intrinsics, unknown helpers, dynamic allocas, unsupported EH/control, and
variable-length memory intrinsics. Constant-length `memset`, `memcpy`, and
`memmove` are accepted with their byte count included in the abstract work
bound. The final funcinfo/pclntab data-only inline-assembly anchor is admitted
only through compiler-injected identity plus a digest of its complete assembly
payload; unmarked inline assembly remains rejected. The deterministic
`llgo.coro.post-llvm-atomic-cost.v1` report binds each semantic certificate to
the observed LLVM maximum before/after CoroSplit and after final optimization
and compiler-owned site insertion.

This closes the structural no-cut certificate used by outcome-plain selection;
it is not yet a target-cycle or wall-clock preemption-latency proof. LLVM work
units are deliberately reported separately from semantic work units. Machine
instruction latency, target-specific library calls/assembly, final machine
stack bytes, interrupts, and scheduler overhead still require the target cost
model and final-code certificate described in the runtime design.

### Remaining outcome-plain expansion

The original 574-body opportunity is not yet claimed. The current cohorts do
not accept defer/recover bodies, a Goexit producer, function values/interfaces,
or recursive/open-world paths. The next cohorts are:

1. add a target-machine cost/stack model if the structural bound is to become
   a strict wall-clock preemption-latency claim;
2. add exact panic source/trace ownership, Goexit, cleanup/defer and recover
   outcome transactions without cloning source bodies;
3. add descriptor/interface dispatch only after entry-kind metadata and every
   boundary adapter are closed across archives;
4. rerun representative standard-library executable size, direct/interface/
   channel throughput, and parked-frame memory baselines before broadening the
   default cohort.
