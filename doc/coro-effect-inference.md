# Coroutine effect inference and foreign-boundary contracts

Status: implementation contract for the `llvm-coro` branch.

This document freezes the boundary between facts that the compiler must derive
and facts that an external implementation must promise.  It exists to prevent
`//llgo:coro` directives from becoming a second, manually maintained call graph.

The source language remains ordinary synchronous Go.  A Go function is colored
only by the compiler's whole-program or imported-summary analysis.  No caller
may need a coroutine directive merely because one of its callees can suspend.

## 1. Current inventory

At baseline `d7805fa5a`, after excluding tests, generated C ABI testdata, and
documentation, the repository has the following exact production inventory:

| Directive | Count | Meaning today |
| --- | ---: | --- |
| `noblock` | 66 | exact external declaration is executor-safe |
| `sync` | 60 | exact external declaration returns synchronously on this thread |
| `contract` | 58 | target-neutral progress/affinity/reentry/memory, often repeated only to carry a word-call arity |
| `workeraddr` | 40 | legacy function-address target and word arity |
| `schedulerwait` | 19 | scheduler-owned physical wait |
| `workerresult` | 2 | wrapper result position forwards a worker result word |
| `worker` | 1 | typed external declaration may use the worker |

Total: 246 directives in 52 production files.

This is precise but not the desired architecture.  `workeraddr`,
`workerresult`, the legacy `worker` spelling, and most repeated word-call
contracts describe information already present in SSA.  They are migration
debt, not irreducible foreign semantics.

### 1.1 Implemented checkpoint

The first inference cut reduces the exact production inventory to 154:

| Directive | Count | Status |
| --- | ---: | --- |
| `noblock` | 66 | legacy bottom behavior; not structurally inferable |
| `sync` | 60 | legacy bottom behavior; remove after the general same-M foreign episode or replace with producer metadata |
| `schedulerwait` | 19 | intentional scheduler adapter behavior |
| `contract` | 9 | seven executor/thread contracts and two foreign-pointer result facts |
| `workeraddr` | 0 | removed; target identity is producer-owned and arity is sink-derived |
| `workerresult` | 0 | removed; wrapper result flow is SSA-derived |
| `worker` | 0 | removed; typed C declarations use the frozen foreign default |

An executable monotonic gate enforces this inventory.  Removed directive
classes cannot silently return.

The address inference is deliberately not based on a suffix alone.  Only an
exact selected LLGo patch alternate is an annotation-free catalog entry.
Unannotated user declarations remain fail-closed.  One producer must reach
exactly one sink ABI; a missing sink or conflicting sink widths poisons the
producer before a worker certificate can be issued.

## 2. Frozen principles

### 2.1 Go functions are inferred

For every bodyful Go function the compiler derives:

- local suspension seeds from coroutine intrinsics, channel operations,
  goroutine creation, event operations, and foreign boundaries;
- transitive `Effect` and executor requirements over the exact SSA call graph;
- direct plain, direct coroutine, or dynamic descriptor representation from
  actual use sites;
- physical operation recipes before emission.

The result is propagated through package archives by the canonical library
effect summary.  A missing summary is opaque and never means `NoSuspend`.

There is no valid production `//llgo:coro` directive whose purpose is to color
a bodyful ordinary Go caller.

### 2.2 Compiler-visible structure is not a contract

The compiler must derive, rather than annotate:

- whether a function is bodyful Go, a typed C declaration, or an
  address-publication-only C declaration;
- physical symbol and structural ABI;
- the word count of an `llgo.syscall` occurrence;
- the exact producer that reaches a function-word operand;
- argument and result tuple positions;
- whether a function value is direct, closed dynamic, or open dynamic;
- wrapper-to-worker result forwarding;
- caller coloring and operation selection.

These facts are frozen in `ProgramIR`, callable facts, or library summaries.
They must never be reconstructed from a numeric function address at runtime.

### 2.3 External semantics need a bottom contract

Go types and SSA cannot prove that an opaque C implementation:

- cannot block;
- is safe on an arbitrary worker thread;
- does not call back into Go;
- does not retain a pointer after return;
- never returns;
- is an intentional scheduler-owned physical wait.

Only these semantic dimensions may require an explicit bottom-level contract.
The contract attaches to the exact external declaration or generated adapter,
never to its Go callers.

## 3. Required inference pipeline

### 3.1 Bodyful Go and imported Go

Bodyful Go is analyzed directly.  A bodyless imported managed-Go declaration
must match one exact archive summary by stable FunctionID, target/runtime
identity, structural ABI, and physical symbol.  The imported effect becomes a
seed in the consumer's ordinary fixed point.

Source-loaded bodyful Go remains authoritative over an archive fact.  Archive
metadata contains producer facts only; it does not publish consumer demand,
roots, call-site choices, or a whole-program plan digest.

### 3.2 Typed C calls

An ordinary typed C declaration provides its full ABI.  Its Go caller is
automatically colored when the declaration is not executor-safe.  Supported
arguments, results, callback positions, and frame-retention roots are derived
from the typed call occurrence.

The compatibility default for a general C call must eventually match Go's cgo
execution model:

- it may block;
- it executes on the calling physical M unless a stronger any-thread proof is
  available;
- managed callback/reentry is permitted through the frozen callback shape;
- arguments are borrowed only until the call returns unless generated metadata
  proves a different lifetime.

The runtime therefore detaches the managed execution domain while the current
M is in C and lets a replacement M continue the scheduler.  A proved
any-thread/no-reentry call may use the cheaper bounded worker path.  A proved
executor-safe call may execute inline.

`#cgo nocallback` and `#cgo noescape`, when present, are generated producer
metadata.  They refine the default but do not color Go callers manually.

### 3.3 Function address carriers

For `FuncPCABI0(target) -> uintptr -> private carriers -> llgo.syscall`:

1. the producer injects the exact target identity and physical symbol before
   the address is emitted;
2. forward SSA provenance carries that identity with the value;
3. the exact `llgo.syscall` sink supplies the word arity;
4. all paths from a producer must agree on one ABI;
5. a store, arithmetic operation, return, open parameter carrier, or unknown
   call destroys the proof;
6. active managed incoming edges must all be certified.

The word arity is a consumer-visible structural fact.  It must not be written
as `workeraddr` or repeated in a declaration contract.  The source declaration
itself remains the reviewable allow-list entry for patched platform catalogs.

Foreign pointer result provenance is separate from callability.  It is either
derived from a typed declaration/result flow or represented by a narrow
bottom-level result-lifetime fact; it must not be inferred from the returned
integer value.

### 3.4 Wrapper result flow

For a private wrapper around an exact worker sink, the compiler traces each
returned tuple element through `Extract`, representation-preserving conversion,
and agreeing `Phi` nodes back to a worker result position.  Every return must
agree.  Arithmetic, storage, interface conversion, an unknown call, or
different source positions ends the proof.

Consequently `workerresult` is compiler-derived metadata.  An annotation is not
allowed to override an SSA mismatch.

## 4. Minimal explicit vocabulary

The final source-level vocabulary should describe only bottom semantics:

- `executor-safe`: the external call cannot wait for future progress and may
  execute while owning the executor;
- `scheduler-wait`: this exact call is the target adapter's physical sleep/join
  boundary and is never a managed Go call;
- optional refinements for `any-thread`, `caller-thread`, managed reentry,
  retained memory, and no-return;
- explicit async adapter operation identity when it cannot be represented by a
  typed compiler intrinsic.

These may be emitted by a binding generator or embedded in a compiled library.
They must not be copied onto wrappers or callers.  Backend choices such as
worker, poll, or host loop are not semantic contract vocabulary; they are
selected later from target capabilities and the frozen semantic facts.

Legacy `noblock`, `sync`, and `schedulerwait` remain accepted during migration,
but their production inventory is monotonically bounded.  `workeraddr`,
`workerresult`, and `worker` have a zero-production target.

### 4.1 How the remaining source metadata shrinks

The remaining 145 legacy directives are not call-graph coloring facts.  They
assert behavior of opaque C implementations, so deleting them merely because a
signature looks harmless would be unsound.  They are reduced in this order:

1. A general typed C call uses the conservative foreign episode by default.
   It needs no `worker` or `sync` annotation for correctness.  A replacement M
   keeps managed work progressing while the calling M remains in C.
2. Binding generators publish `nocallback`, `noescape`, affinity, no-return,
   and optional executor-safe facts as exact callable metadata.  Source callers
   do not repeat them.
3. Compiled libraries embed those callable facts beside the existing Go effect
   summary.  Consumers match stable callable identity, physical ABI, target
   profile, and digest; absence or mismatch is opaque.
4. LLGo-owned C definitions may earn executor-safe facts from a conservative
   closed C/LLVM call-graph proof.  Unknown external calls, inline assembly,
   blocking primitives, callbacks, or incomplete LTO visibility end the proof.
5. A scheduler physical wait is selected by one typed scheduler operation
   recipe.  The recipe carries the bottom wait role, while its ordinary Go
   callers are still colored automatically.

This distinction matters: moving 145 names into a compiler table would reduce
comments but not architecture debt.  Producer metadata may contain many exact
callable records, but there should be only a few orthogonal semantic classes
and no manually maintained Go propagation graph.

### 4.2 Review decision: three semantic scopes

The final model has three scopes.  They must not be represented by one
declaration annotation which is then copied through the call graph.

1. **Managed Go.**  Body analysis, suspension coloring, function
   representation, entry demand, and per-call lowering are entirely inferred.
   This includes ordinary wrappers around `syscall`, cgo, files, sockets, and
   timers.
2. **Foreign callable facts.**  The compiler derives identity, symbol, typed
   ABI, argument/result layout, callback positions, and value provenance.
   Behavior which cannot be proved from those facts comes from a generator,
   an embedded library record, or a conservative target default.  It is never
   repeated on Go callers.
3. **Runtime adapter roots.**  A small number of executor-critical,
   scheduler-wait, event-submit, event-complete, and cancellation adapters own
   explicit bottom semantics.  The compiler verifies each adapter's closed
   body/call closure and derives all uses below that root.  Individual C leaves
   inside the verified closure do not each need a caller-coloring directive.

This scope split permits one physical C declaration to have two safe uses.  A
verified raw-host adapter may call it directly on the scheduler stack, while
an ordinary managed call to the same declaration uses the conservative
foreign episode.  The old global `sync` bit cannot express that distinction.

For a general synchronous C call, the source-compatible default is:

- it may block;
- it runs on the physical caller M;
- it may reenter through compiler-generated managed callback adapters;
- pointer arguments are borrowed until return.

On a native threaded target this selects the same-M foreign episode: detach
the active LLVM resume, release its managed execution permit, let a replacement
M continue the scheduler, strongly rejoin it, and restore the original resume.
On WASM, RTOS, and baremetal targets without replacement-M support, a
potentially blocking call must select a target event/host adapter or fail
closed.  Silently executing it on the sole executor is not a portable default.

`nocallback`, `noescape`, `any-thread`, executor-safe, no-return, retained
memory, and signal/IRQ safety are refinements.  They may improve lowering or
authorize a restricted adapter, but their absence keeps the conservative
semantics above.  In particular, the absence of a function-typed parameter
does not prove `nocallback`: C can call an exported Go adapter through global
state.

### 4.3 Same-M checkpoint and remaining callback work

The native implementation now has one orthogonal same-M foreign episode for:

- caller-thread declarations with no managed callback argument; and
- declarations with exact compiler-generated managed callback adapters.

Both cases reuse the existing detach/replacement/strong-join state machine.
There is no second scheduler and no runtime function-address reverse lookup.
An end-to-end gate verifies that a caller-thread C function stays on its
original M while a replacement M continues managed work.

This checkpoint is necessary but not yet sufficient to make every unannotated
C declaration callback-capable.  The remaining compatibility work is:

1. generate reentry-aware C ABI adapters for exported Go callbacks even when
   the callback is not passed as an argument of the current call;
2. bind each adapter to its exact managed target at compile time and publish
   that binding in library metadata;
3. reconcile panic, `Goexit`, cancellation, and teardown across the C stack
   instead of using the current fail-closed non-return outcome;
4. join contracts for closed dynamic C target sets and reject an open target
   whose ABI cannot be proved;
5. only then switch the unannotated typed-C default from the temporary
   any-thread/no-reentry worker policy to the conservative target policy above.

Changing the default before these gates would remove comments but regress cgo
semantics.

### 4.4 Legacy directive migration

The remaining directives have different removal rules:

| Legacy class | Migration |
| --- | --- |
| `sync` (60) | Ordinary declarations use the conservative same-M/event default.  Runtime-only direct calls move under a small verified raw-host or executor adapter root. |
| `noblock` (66) | Opaque C needs a producer proof; LLGo-owned definitions may use a closed C/LLVM proof.  The fact is embedded/generated and never propagated through Go source. |
| `schedulerwait` (19) | Replace per-leaf tags with typed scheduler-wait adapter operations and verify their raw-host closure. |
| `contract` (9) | Keep only irreducible behavior/provenance facts; derive ABI, arity, callback positions, and wrapper flow. |

The desired endpoint is not necessarily zero callable records.  It is zero
manual Go coloring, zero compiler name allow-lists, and only a small number of
source-visible adapter-root contracts.  A generated library may contain many
exact callable records without spreading them through runtime or standard
library source.

## 5. Migration gates

The migration is complete only when executable tests enforce all of the
following:

1. no production `workeraddr`, `workerresult`, or `worker` directive;
2. no ordinary bodyful Go function carries a coloring directive;
3. every function-address worker path has producer-forward identity and a
   sink-derived ABI before lowering;
4. every inferred wrapper result mapping is proven from SSA and included in
   the frozen certificate digest;
5. missing, ambiguous, escaped, or conflicting facts fail closed;
6. the production directive inventory is an exact monotonic snapshot, so a
   removed annotation class cannot grow back;
7. library export/import preserves the same facts without publishing
   consumer-specific decisions.
8. no unannotated typed C declaration defaults to any-thread/no-reentry after
   the general callback-capable same-M route is enabled;
9. raw-host authorization is attached to a verified adapter closure, not
   copied onto every foreign leaf in that closure;
10. every target either implements the selected blocking foreign episode or
    rejects it before code generation.

Moving annotations into a compiler name allow-list does not satisfy this gate.
Platform semantics belong in generated binding metadata or exact adapter
declarations; the compiler owns only derivation and validation.

## 6. Order of remaining semantic work

After the inference cutover, remaining compatibility work proceeds in this
order:

1. general same-M foreign episode, callback/reentry, cancellation, and teardown;
2. complete panic/defer/recover/Goexit reconciliation;
3. precise suspended-frame GC roots and barriers;
4. dynamic/sharded timer, channel, worker, and registry capacity;
5. dynamic/closure/method `go`, reflect/select, and callable dispatch;
6. complete WASM/WASI, RTOS, baremetal, and embedded event adapters;
7. full repository `test/*` and GOROOT compatibility gates.

This order keeps later standard-library work on one scheduler/event/cancellation
model instead of adding per-package coroutine policies.
