# Native fault landing for stackless coroutines

## Status and scope

This document defines the native Darwin/Linux bridge from synchronous machine
faults and an unexpectedly escaping legacy synchronous panic into LLGo's
structured coroutine panic/defer/recover protocol. It is part of the LLVM 22
stackless coroutine profile. WebAssembly, bare-metal, and embedded targets do
not acquire this host-signal mechanism or any associated frame storage.

The bridge is required because a signal callback may not unwind or `longjmp`
through an active `llvm.coro.resume`. Such an unwind abandons the physical
resume invocation without publishing a coroutine transition. Conversely,
jumping directly from a signal to the scheduler would skip defers in ordinary
synchronous Go calls nested inside that resume.

## Semantic model

Closed-world analysis first decides whether the final reachable program can
call `runtime/debug.SetPanicOnFault`. The result is a hashed program-bootstrap
capability, not a source annotation. A program without that capability emits
neither compiler landing hooks nor a resume-time query. For a capable program,
each native physical resume is surrounded by a short-lived legacy panic
boundary from its first instruction:

1. Allocate a native `sigjmp_buf` and a synthetic `runtime.Defer` record.
2. Execute `sigsetjmp` and push that record on the current physical/logical
   runtime G's legacy defer chain.
3. Invoke the exact `llvm.coro.resume` operation.
4. On normal return, pop the exact boundary.
5. If a signal becomes a Go panic, ordinary synchronous Go frames first run
   their existing legacy defer/recover machinery. Only a panic which remains
   unhandled reaches the synthetic boundary.
6. The boundary stages the existing persistent `panicNode`, keyed by the exact
   active coroutine handle, and immediately resumes that same handle again.
7. A compiler-owned gate, which precedes every ordinary or operation-specific
   resume decision, consumes the staged node and enters the structured
   coroutine cleanup path. It never returns to the faulting source
   continuation.

This composes the two execution domains instead of making every synchronous Go
function a coroutine merely because any memory access can fault when
`debug.SetPanicOnFault` is enabled.

The boundary cannot be selected only from the current logical G's dynamic
`paniconfault` bit. A function may enable the bit and fault again in the same
resume segment, before the scheduler can observe another suspension. The
closed-program capability therefore provisions the landing eagerly, while the
dynamic bit remains the policy check on the exceptional stage path. The root G
receives one immutable capability bit from the validated bootstrap and every
spawned G inherits it. That bit reuses scheduler-G tail padding and is returned
by the existing issued-resume transaction, avoiding a task-local lookup in the
hot path.

The analysis is per function and propagates over the frozen ordinary, dynamic,
interface, spawn, and compiler-lowered call graph. A library-effect summary
publishes the producer's transitive capability for each exported managed entry;
consumers continue propagation from bodyless `EmitExternal` declarations. The
summary record is versioned, and a canonical digest of nonzero imported facts
participates in the whole-program plan, package cache, and bootstrap identities.
Thus separately compiled libraries cannot silently turn landing emission on or
off without invalidating every affected artifact.

## Top-level and inline resumes

The scheduler's handle wrapper owns the boundary for a top-level physical
resume. Static inline child resumes need their own nested boundary around the
direct `llvm.coro.resume` intrinsic. Otherwise a child fault would jump past
the child's and parent's active inline transaction to the scheduler.

The inline boundary is emitted in the parent's current resume segment. Its
jump buffer and synthetic defer record use the compiler's target-aware
`SigjmpBuf` and `runtime.Defer` layouts rather than a duplicated byte-size
table, and are not live across a parent suspend.
LLVM 22 must still see the direct child ramp/resume/done/destroy sequence and
retain `coro_elide_safe` frame elision. `TestCoroElideStaticChildFrameContract`
is the mandatory optimization gate for this property.

Boundaries nest in the ordinary defer chain, so synchronous C-to-Go reentry and
multiple inline children always land at the nearest active physical resume.

## Compiler control-flow contract

For a capability-positive native program, `ssa.CoroBuilder` owns an
unconditional resume-landing dispatch which runs before both its default
run-decision gate and every per-suspend override. Capability-negative programs
omit this dispatch entirely. The landing callback may only branch to the
compiler-provided normal gate or to a frontend-owned shared fault block.
Therefore:

- every case-0 resume edge is covered exactly once;
- no landing scope or staged token crosses `llvm.coro.suspend`;
- park/select/timer/channel-specific gates cannot bypass fault delivery;
- WebAssembly and freestanding targets emit neither the gate nor its hooks;
- CoroSplit is never patched after frame layout has been chosen.

The runtime hook returns one detached `panicNode` token on the exceptional
retry and nil on ordinary resumes. Type and data words are extracted and the
node is released on the cold branch, so no two-word scratch record is added to
every coroutine frame.

For a function without an owner cleanup drainer, the cold branch publishes the
pair through the existing terminal panic path. For a function with cleanup,
the already frame-resident continuation word distinguishes the two cases:

- continuation zero: the fault came from source execution and enters the
  canonical Recover cleanup base;
- continuation nonzero: the fault occurred while draining a defer and replaces
  the current panic overlay while preserving the normal, RunDefers, Goexit, or
  cancellation base.

No additional per-frame mode bit is required.

## Runtime ownership and invariants

The synthetic boundary and jump buffer have native activation lifetime. The
panic value remains in the runtime-allocated `panicNode`; staging changes only
its owner marker from the temporary `Defer` address to the exact coroutine
handle. The retry gate detaches it exactly once. Interface payloads are already
stable: indirect values are boxed in runtime-managed storage and direct values
are encoded in the interface data word.

All push, pop, stage, and take operations fail closed on a mismatch. Required
checks include:

- initialized task and non-nil active handle;
- exact current boundary at normal pop or nonlocal landing;
- top panic node owned by that boundary;
- exact staged-handle match at the compiler retry gate;
- no duplicate take or release;
- no scheduler-visible transition between landing and retry.

The scheduler action remains open during retry. The LLVM state index still
names the preceding suspend, so retry re-enters its compiler landing gate and
branches away before re-executing source side effects.

## Signal policy

The shared SA_SIGINFO trampoline remains the source of SIGSEGV, SIGBUS, and
SIGFPE conversion, but the coroutine runtime installs it through a distinct
entry. Coroutine mode uses a thread-local recursion guard and skips the legacy
process-global dynamic-unwind snapshot; the legacy runtime retains its original
process-wide guard and libunwind capture. Boundaries use `sigsetjmp(..., 0)`
like ordinary LLGo defer frames. The trampoline explicitly unblocks the fault
signals before entering panic handling, avoiding a signal-mask save on every
ordinary resume.

The C trampoline converts platform `si_code` details into two small facts:

- a kernel-synchronous FPE or certified nil-page memory fault panics by default;
- another memory fault is eligible only when `SetPanicOnFault` was enabled at
  the interrupted instruction.

User-, queue-, timer-, thread-, or other asynchronously generated signals are
never converted. A callback rejection returns directly to C, which restores
the default disposition and re-raises the exact signal before any Go defer or
`recover` can intercept it. An admitted callback raises the existing runtime
panic, allowing nested synchronous defers to recover before the coroutine
bridge is involved.

The callback snapshots the interrupted PC and admission result into one
allocation-free M-local state word. The high bits of that same word hold the
exact nested native-boundary depth; low bits hold presence, first-panic capture,
and admission. Presence is recorded even on architectures whose `ucontext`
adapter cannot supply a diagnostic PC. Clearing or promoting a snapshot
preserves the remaining outer-boundary depth. Only after control has escaped
signal context may the one PC be promoted into the logical G's lazy traceback
store. For an opted-in non-nil memory fault, the recovered runtime error also
implements `Addr() uintptr` using `si_addr`.

Compiler-generated nil, bounds, divide, channel, and unsafe-construction faults
continue to use target-neutral explicit-status edges; the signal path is only
for genuine host faults such as protected `mmap` pages. Unknown blocking or
unsafe foreign calls remain worker operations. A direct foreign call is a
trusted synchronous boundary; if it faults while `SetPanicOnFault` permits
recovery, the same nearest-boundary rule applies. A fault not admitted by Go's
fault policy remains terminal rather than continuing after foreign state may
have been corrupted.

## Mandatory gates

The implementation is incomplete unless all of the following hold:

- LLVM 22 verifies and splits all affected presplit functions.
- Static child frame elision remains active with the inline boundary.
- Every normal and per-site resume path passes through the landing gate.
- Boundary state never appears in wasm, bare-metal, or embedded IR.
- A closed program without reachable `SetPanicOnFault` contains no generated
  landing calls and matches the pre-boundary handoff throughput within noise;
  a capability-positive program binds the bootstrap bit to every root/child.
- A capability imported through a bodyless library declaration reaches its
  consumer and changes both plan/cache identity and final bootstrap flags.
- Named-source runtime tests execute the production transport and cover nested
  boundaries, normal/invalid/active-panic pop, panic staging, handle mismatch,
  duplicate take, and token validation; the real fault execution test covers
  payload release and panic replacement during cleanup.
- `TestRecoverAfterFaultPreservesNamedResult` passes under RSS limits, and the
  executable fault gate covers disabling the policy, suspending, enabling it,
  and faulting directly in the same root resume.
- Existing coroutine core, `test/*`, and selected standard-library file/network
  tests remain green before merging the topic branch into `llvm-coro`.

## Local validation snapshot (2026-08-25)

The final LLVM 22 topic build passed the positive protected-page fixture,
including named-result recovery, exact `Addr`, panic replacement during defer
cleanup, and enabling `SetPanicOnFault` after `Gosched` in the same root resume.
With the dynamic policy disabled, the same non-nil access terminated by signal
and did not reach the fixture's unexpected-recover marker.

The final WASI fixture built and ran under Wasmtime. Its symbol table contained
no panic-boundary, `sigsetjmp`, or fault-handler entry. Separate wasm32 and
bare-metal compiler IR tests likewise retained none of those markers.

For the capability-negative `pure_handoff` fixture, generated resume functions
contained zero boundary calls and were instruction-identical in the measured
hot region. Seven paired, process-by-process interleaved groups of 100 runs had
medians of 2.3494 seconds on the `llvm-coro` base and 2.3380 seconds on this
topic (-0.48%, noise parity). The stripped Mach-O grew from 746,272 to 764,624
bytes; section content grew by 4,556 bytes, while 16 KiB of the file delta was
segment page alignment rather than loaded content.
