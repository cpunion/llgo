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

The first inference cut reduced the exact production inventory to 154.  The
raw-host operation cut then removed all 19 `schedulerwait` declarations.  The
exact local-export cut removed another eight declaration-level annotations.
The closed raw-host use-domain cut removed 11 declaration-wide `sync`
annotations.  The first conservative-default cut removed another nine
thread-independent lifecycle/notification declarations.  The terminal
raw-host cut removed two private stdio declarations, so the current exact
production inventory is 105:

| Directive | Count | Status |
| --- | ---: | --- |
| `noblock` | 60 | legacy bottom behavior; not structurally inferable |
| `sync` | 38 | legacy bottom behavior; remove after the general same-M foreign episode or replace with producer metadata |
| `schedulerwait` | 0 | removed; exact raw-host invocation is inferred from compiler-owned closure provenance |
| `contract` | 7 | executor/thread and foreign-pointer result facts |
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

The local-export inference is also occurrence-scoped.  A private builder joins
one exact C declaration to one unambiguous local `//export` Go body using the
frozen symbol and structural ABI.  It publishes only the target and certificate
digest in the immutable call-site plan; it does not add another plan authority.
Analysis, physical validation, and lowering consume that same target from the
exact source call/defer/go instruction.  Raw code-address and raw/plain ingress
continue to use the original C declaration.

The raw-host use-domain inference is likewise occurrence-scoped.  It does not
turn an external declaration into a globally synchronous capability.  The
ordinary frozen C default remains may-block and therefore still selects a
managed foreign wait from managed code.  An exact occurrence may execute
directly only when the existing immutable plan proves that its emitted owner is
inside the compiler-owned scheduler-stack closure.  A diagnostic audit then
allows the now-redundant source annotation to be removed only when the emission
universe is closed and every live use of the exact declaration has that form.
Managed calls, non-host raw calls, dynamic calls, address escape, ABI
ambiguity, and incomplete archive views all fail closed.

The conservative-default cut has a different proof obligation.  It applies
only when producer semantics independently establish that the typed C call is
thread-independent, has no managed callback or reentry, and retains no argument
beyond completion.  Its ABI must fit the typed worker record and every selected
target must provide that episode.  Managed occurrences are then colored
`WaitForeign` and use the ordinary foreign episode; an already-validated exact
raw-host occurrence remains direct.  The current cut covers seven POSIX
pthread lifecycle/notification operations and two native-only LLGo poll
descriptor allocation operations.  It deliberately excludes allocator/GC
bootstrap, retained roots or callbacks, process/control flow, FFI, TLS/errno,
and WASM/baremetal allocation paths.

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

For an exact declaration imported through a package archive, the consumer
matches the producer record by stable FunctionID, complete target/runtime
metadata, physical symbol, and typed ABI.  A producer contract may replace only
the conservative unannotated-C default reconstructed by the consumer.  An
explicit local generic or legacy contract must agree exactly or compilation
fails.  An identity-only record suppresses the reconstructed default and grants
no worker, same-M, event, or raw-host operation.

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

### 3.5 Exact local C declaration to Go export binding

An unannotated typed C declaration may resolve to a bodyful Go implementation
in the same frozen emission universe.  This is a link-identity proof, not a
claim that an arbitrary C function is nonblocking.  The frontend issues a
content-addressed binding certificate only when all of the following agree
exactly:

- one canonical bodyless C declaration and one canonical bodyful `//export`
  target;
- final physical symbol and structural C ABI;
- declaration identity, target function identity, and target link identity;
- no competing export/ABI directive and no explicit callable policy.

The declaration and definition remain distinct emission identities.  A global
alias is incorrect because the C symbol can still be observed as a raw code
address or entered from C, while managed Go needs the bodyful target's
coroutine ABI and effect.  Instead, `ProgramIR` records the target and
certificate on each exact static `call`, `defer`, or `go` occurrence.  Dynamic
calls, interface calls, function-value transport, and code-address observation
continue to name the original C declaration.

`AnalyzeSSA` substitutes the target only at that occurrence before unknown-call
classification, call-graph construction, and final `CallPlan` creation.  The
ordinary single Go fixed point then propagates every property of the bodyful
target: suspension, preemption, panic/outcome, cleanup, recursion, entry demand,
and function representation.  A target is not required to be synchronous; if
it parks, its caller is colored and lowered as a coroutine in the normal way.
There is no post-analysis effect subtraction, trusted-inline exception,
target-name allow-list, or additional whole-program analysis pass.

The dual entry remains explicit during lowering:

- a managed coroutine call/defer/spawn selects the frozen Go target;
- a raw/plain owner preserves the source C call and enters the target's
  separately demanded raw export entry;
- a plain managed call may preserve the C ABI round trip as an implementation
  detail, but its semantic target and effect remain the Go body;
- raw address publication continues to publish the C declaration, never a
  coroutine entry.

Raw/plain validation accepts the split only when the call-site certificate,
declaration binding, target FunctionID, physical symbol, and ABI all replay
exactly.  The target must have the raw/plain entry demanded by the same graph.
A raw-only export root is not counted as a managed scheduler root merely
because the same function also has independent managed coroutine demand.

Missing, ambiguous, ABI-mismatched, explicitly annotated, or non-static
bindings remain on the conservative foreign path.  Cross-archive use requires
the producer's existing export-binding record plus the target's Go effect and
physical-entry summary; the consumer must perform the same identity checks and
must not synthesize a global alias.  Until that consumer path is enabled, this
automatic redirect is intentionally local to one frozen emission universe.

## 4. Minimal explicit vocabulary

The final source-level vocabulary should describe only bottom semantics:

- `executor-safe`: the external call cannot wait for future progress and may
  execute while owning the executor;
- optional refinements for `any-thread`, `caller-thread`, managed reentry,
  retained memory, and no-return;
- explicit async adapter operation identity when it cannot be represented by a
  typed compiler intrinsic.

Scheduler/raw-host execution is not a declaration contract.  It is an
invocation fact derived from an exact compiler-owned raw-host root and its
closed static call closure.  A may-block declaration therefore stays
`ExternalUnknownForeign + BlockForeign` for managed callers while the exact
raw-host occurrence may execute synchronously on the already-owned host stack.

These may be emitted by a binding generator or embedded in a compiled library.
They must not be copied onto wrappers or callers.  Backend choices such as
worker, poll, or host loop are not semantic contract vocabulary; they are
selected later from target capabilities and the frozen semantic facts.

Legacy `noblock` and `sync` remain accepted during migration, but their
production inventory is monotonically bounded.  `schedulerwait`, `workeraddr`,
`workerresult`, and `worker` are rejected or have a zero-production target.

### 4.1 How the remaining source metadata shrinks

The remaining 98 legacy `noblock`/`sync` directives are not call-graph
coloring facts.  They
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
   occurrence whose recipe carries the bottom wait role.  The compiler proves
   membership in the closed raw-host call-closure; no foreign declaration is
   tagged to authorize it, and ordinary Go callers are still colored
   automatically.

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
| `sync` (38) | Ordinary declarations ultimately use the conservative same-M/event default.  Runtime-only direct calls move under a verified raw-host or executor adapter root.  Eleven fleet/worker and two private terminal-stdio declarations completed raw-host cuts; nine thread-independent pthread/poll declarations completed the temporary any-thread/no-reentry default cut. |
| `noblock` (60) | Opaque C needs a producer proof; LLGo-owned definitions may use a closed C/LLVM proof.  The fact is embedded/generated and never propagated through Go source. |
| `schedulerwait` (0; removal complete) | Per-leaf tags were deleted.  The compiler verifies each may-block occurrence against exact raw-host closure provenance while preserving managed `WaitForeign`. |
| `contract` (7) | Keep only irreducible behavior/provenance facts; derive ABI, arity, callback positions, wrapper flow, and exact local-export behavior. |

The desired endpoint is not necessarily zero callable records.  It is zero
manual Go coloring, zero compiler name allow-lists, and only a small number of
source-visible adapter-root contracts.  A generated library may contain many
exact callable records without spreading them through runtime or standard
library source.

## 5. Migration gates

The migration is complete only when executable tests enforce all of the
following:

1. no production `schedulerwait`, `workeraddr`, `workerresult`, or `worker`
   directive;
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

## 7. Full review: inferred effects, producer facts, and physical operations

The current architecture does not need another manually maintained coloring
layer.  Managed Go effects, callable identity, typed ABI, value provenance, and
most physical call shapes are already available before emission.  The
remaining design gap is narrower:

- exported Go functions are still treated as raw plain roots instead of
  compiler-generated C ABI ingress adapters;
- the v2 package archive now publishes and consumes exact foreign callable
  facts for typed static calls, while declarative export bindings intentionally
  remain non-authorizing until the ingress gate exists;
- the temporary unannotated-C default assumes any-thread/no-reentry;
- a few non-ordinary operations are still encoded as `sync` declarations.

These gaps should be closed directly.  Replacing the existing directives with
different caller annotations, a compiler symbol-name table, or a runtime
address-to-function registry would retain the same architectural debt.

### 7.1 One inferred Go body, adapters only at physical crossings

Every bodyful Go function has one source implementation and one inferred
primary representation:

- a proven non-suspending function has a plain primary;
- a suspending function has a coroutine primary;
- a function value used dynamically in incompatible physical contexts may
  additionally need a generated ramp or descriptor dispatch.

A C export, retained callback trampoline, reflection bridge, or same-M thunk is
an ABI adapter around that primary.  It is not a second source body.  A
function must not acquire both plain and coroutine bodies merely because an
external symbol names it.

In particular, `//export` must stop creating `RawPlainDemand` for the Go body.
The compiler instead freezes:

```
physical C symbol + physical C ABI
    -> generated ingress adapter
    -> exact managed FunctionID + managed primary
```

The binding is established before code addresses exist.  No adapter or runtime
path may recover a Go target by looking up the incoming program counter.

### 7.2 Complete fact ownership

Facts have one owner and one permitted source:

| Fact | Owner | Derivation |
| --- | --- | --- |
| Go suspension/executor effect | compiler | SSA seeds plus fixed point |
| Go callable representation | compiler | all direct and dynamic use sites |
| C symbol and typed ABI | frontend/binding generator | declaration and final target ABI |
| callback parameter/result shape | compiler | typed call occurrence and provenance |
| export symbol to Go target | compiler | `//export` declaration before lowering |
| C progress, affinity, callback timing, retention | producer | generated metadata, embedded library fact, closed proof, or conservative default |
| call-site physical operation | compiler | joined producer facts plus target capabilities |
| scheduler/event/cancellation state | runtime adapter | typed internal operation, never a C symbol spelling |

For bodyful Go, an imported library summary supplies the same producer effect
that local SSA analysis would have supplied.  A function value carries its
compile-time identity/effect descriptor through ordinary SSA provenance.
Converting a function to `any`, an interface, `uintptr`, or a library boundary
must not force runtime reverse discovery; the compiler either emits the
descriptor/summary or treats the value as open and conservative.

### 7.3 Conservative C default and operation selection

An unannotated typed C declaration has the source-compatible default:

- progress may block;
- affinity is the physical caller M;
- managed reentry during the call is possible, including through global C
  state rather than a function-typed argument;
- arguments are borrowed until the call returns.

The absence of a callback parameter is not `nocallback`.  The absence of a
visible store is not `noescape`.  Neither fact follows from a C signature.

The compiler selects one physical operation from the frozen facts:

| Operation | Required proof/capability | Meaning |
| --- | --- | --- |
| `Inline` | executor-safe | execute while retaining the managed executor |
| `SameM` | native replacement-M capability | detach the task, run C on the caller M, let a replacement M progress the scheduler, then strongly rejoin |
| `Worker` | any-thread, no reentry, compatible lifetime and ABI | run on the bounded shared foreign worker pool |
| `EventHost` | exact async/event adapter | submit, suspend, cancel, and complete through the target event loop |
| `RawHost` | verified runtime adapter root | execute below the managed scheduler; never selected for an ordinary source call |
| `Control` | exact compiler/runtime intrinsic | returns-twice, nonlocal transfer, process transition, or terminal operation |

On single-threaded WASM, RTOS, embedded, or baremetal targets, an unclassified
may-block call cannot silently run inline.  It must use an `EventHost` adapter
or fail before emission.  A threaded target may implement `SameM`; availability
of a generic worker alone is insufficient because the default preserves
caller-M affinity and callback reentry.

`#cgo nocallback` and `#cgo noescape` refine this default.  LLGo's cgo pipeline
does not currently preserve these directives and must add them as generated
producer facts.  A future optional executor-safe/noblock contract may be useful
for opaque external libraries, but it is the only performance refinement that
cannot generally be recovered from types or Go SSA.  It applies to the exact C
callable and never propagates as a Go source annotation.

The current single `managed-callback` reentry value is not precise enough for
lifetime validation.  Producer facts must distinguish no callback, callback
only before the call returns, and callback retained for later invocation.
`MemoryRetained` alone cannot identify which function argument is callable or
when its environment may be released.  The binding generator supplies only
this bottom timing/lifetime fact; callback positions, ABIs, adapters, and
managed target identities remain compiler-derived.

### 7.4 Library summary v2

`llgo.coro.library-effect-summary.v2` is now the hard-cut producer schema.
`CallableContractFacts` is not embedded unchanged because it also contains
consumer call-site invocations.  v2 has three collections:

1. **Managed functions**: the existing FunctionID, ABI hash, inferred effect,
   execution flags, representation, and physically emitted primary entries.
2. **Foreign callables**: exact declaration identity, physical symbol, typed
   ABI hash, target-neutral behavior contract, proof kind/digest, and any
   trusted refinement.
3. **Export bindings**: physical C symbol and ABI hash mapped to an exact
   managed FunctionID and primary.  The current record is declarative and
   grants no raw-entry or ingress capability; a generated adapter must publish
   and pass a separate versioned gate before lowering may call it.

The existing target triple, data layout, coroutine/scheduler/panic/function
representation ABIs, and target capabilities remain part of the enclosing
metadata.  The records contain no code pointer and no consumer-selected
worker/same-M/event recipe.

The emitter and importer validate schema, target profile, stable identity,
typed ABI, contract identity, managed/export binding, and digest before
admitting a fact.  Duplicate, missing, conflicting, or target-mismatched
records are rejected.  A missing callable record retains the conservative C
default; it never becomes executor-safe by omission.

Consumer calls to managed functions import their inferred effect and continue
coloring through the ordinary SSA fixed point.  Exact typed foreign
declarations also consume their producer identity and optional target-neutral
contract.  The final consumer selects worker or same-M only after the ordinary
typed call, plan certificate, target capabilities, frame retention, callback
shape, and physical ABI all pass their existing gates.  The imported overlay is
re-published unchanged if that consumer produces another archive; it is never
reconstructed from a code address or silently replaced by the consumer's
default.

Export bindings remain serialized and indexed but non-authorizing.  They do
not permit a raw entry, global alias, or C-to-Go ingress adapter until the
separate versioned ingress capability is generated and verified.  Metadata
availability by itself is not an execution capability.

### 7.5 Unified foreign ingress

Every generated C-to-Go adapter probes one runtime ingress mechanism:

1. If the physical M owns an active same-M foreign episode, attach the exact
   callback child below that episode's suspended parent and reuse the existing
   replacement-M handoff.
2. If no episode exists but the thread is runtime-owned, create an ordinary
   managed callback transaction on that M.
3. If C calls from a foreign-created thread, acquire a bounded extra-M/foreign
   ingress record, run the exact managed target with physical-thread affinity,
   and release the record on return.
4. A signal/IRQ adapter uses a restricted ingress profile whose entire managed
   closure is proven non-suspending and safe for that environment.

This handles callbacks passed as arguments, callbacks reached through C global
state, retained callbacks invoked later, libffi closures, BDWGC finalizers, and
shared-library exports through one target-binding scheme.  Callback positions
and closure context are still derived from the typed adapter.  Retained
callbacks additionally own an explicit lifetime token so unregister/destroy
can release the function value and environment.

The current reentry adapter accepts only ordinary return.  Completion handling
must be expanded as follows:

- return and recovered-return reconstruct C results;
- an unrecovered panic during a nested Go-to-C-to-Go episode performs a
  runtime-owned nonlocal exit to the outer same-M boundary, restores scheduler
  state, and resumes managed panic propagation;
- `Goexit` uses the same nonlocal exit for a callback belonging to an existing
  managed G, but is fatal for a callback entering from a foreign-created
  thread, matching the Go runtime constraint;
- scheduler cancellation is not injected through an active synchronous C
  stack.  It is recorded and observed after the episode returns.  Explicit Go
  cancellation through channels or contexts remains ordinary callback code.

The nonlocal exit is an implementation mechanism of the ingress adapter, not a
contract on each C function.  Native targets may use a boundary-owned unwind
record; targets without a safe nonlocal transfer reject escaping panic/Goexit
at that crossing and preserve diagnostics rather than returning silently to C.

### 7.6 The five bottom operation families

All irreducible semantics fit five orthogonal operation families:

1. **Normal foreign episode**: synchronous typed C call with inline, same-M, or
   worker lowering selected from producer facts.
2. **Event operation**: typed submit/register/wait/cancel/complete adapter used
   by files, sockets, timers, WASI/JS hosts, RTOS queues, and baremetal IRQs.
3. **Foreign ingress**: generated exact C ABI adapter for synchronous, retained,
   shared-library, signal, and IRQ callbacks.
4. **Scheduler/raw-host operation**: a verified executor-internal leaf such as
   the physical scheduler wait, doorbell, queue, clock, or allocator boundary.
5. **Control operation**: returns-twice/nonlocal transfer, fork/exec process
   transition, and terminal no-return.

Files, networks, and timers do not introduce new coloring systems.  Their Go
wrappers are ordinary inferred functions over family 2.  A helper such as
`ignoringEINTRIO(syscall.Read)` receives an exact typed operation descriptor
from compilation; it does not receive an untyped address whose semantics must
be rediscovered.

Special cases found in the current `sync` inventory map cleanly:

- `sigsetjmp`/`siglongjmp` are already compiler intrinsics.  Their four helper
  declarations become family-5 raw control entries.  A generic same-M thunk is
  invalid because it adds a frame below the saved context.
- `fork`/`execve` use a process-control adapter.  `fork` quiesces the scheduler
  fleet, repairs the child to one physical execution domain, and resumes the
  parent fleet; it must not start a replacement M while the process image is
  being forked.
- BDWGC allocation/root operations execute through a verified GC/runtime
  adapter.  Finalizer registration carries retained callback/data metadata.
- `pthread_key_create` retains its destructor despite returning synchronously;
  it needs retained-callback metadata rather than a global `sync` claim.
- `ffi_call` is a normal dynamic foreign episode and may reenter through the
  unified ingress.  closure preparation owns retained callback/context
  metadata.
- private terminal `fputs`/`fputc` declarations execute only at exact
  scheduler-stack abort occurrences.  Ordinary `clite.Fputc` remains a managed
  foreign call even though it shares the same physical C symbol.

### 7.7 Directive elimination budget

The 105 production directives are a migration budget, not an intended API:

| Current class | Target | Removal gate |
| --- | ---: | --- |
| `sync` 38 | 0 | conservative same-M/event default plus family-4/5 internal operations |
| `schedulerwait` 0 | 0 | complete: compiler-owned raw-host occurrence and closure proof |
| `noblock` 60 | 0 in handwritten Go | generated/embedded proof, closed LLGo-owned C proof, or conservative fallback |
| `contract` 7 | 0 in handwritten Go | typed result/lifetime flow, export binding, generated producer facts, or adapter-root metadata |

Eight declarations naming compiler-owned `//export` implementations are now
inferred from the exact export binding:

- notify prepare/one/all;
- semaphore prepare/release;
- timer request;
- poll deadline update and closing notification.

Eleven declarations used only by compiler-owned scheduler-stack closures now
retain the conservative external default without a declaration-wide `sync`
claim:

- fleet owner count, factory start/stop, owner create, and standby stop;
- worker create, queue initialize/query/stop/destroy, and exact raw-host call.

Two private terminal stdio declarations now use the same occurrence-scoped
proof:

- `coroTerminalFputs` and `coroTerminalFputc` are live only under the closed
  fail-stop scheduler-stack adapters;
- the gate keys the exact Go declaration identity rather than `fputs`/`fputc`,
  and separately proves that ordinary `clite.Fputc` retains managed
  `WaitForeign` demand.

Nine declarations whose producers are thread-independent and have neither
managed reentry nor retained arguments now use the ordinary temporary
any-thread/no-reentry default:

- pthread mutex destroy, read/write-lock initialize/destroy, and condition
  initialize/destroy/signal/broadcast;
- native-only LLGo poll descriptor allocate/free.

This second cut is not signature inference.  Source regressions freeze the
reviewed producer properties and target restrictions, while the compiler gate
proves that an unannotated typed declaration is automatically colored,
validated against the typed worker ABI, and physically lowered through the
foreign episode.  The native poll E2E exercises the exact allocation symbols
without a test-only `sync` escape hatch.  A structurally similar allocator,
callback registrar, or control primitive does not inherit this conclusion.

The closed-use audit is a migration gate, not a new lowering input.  It reads
the already-frozen function and call plans after raw-host validation and
requires all live exact uses to be scheduler-stack occurrences.  A separate
regression keeps each migrated symbol annotation-free while proving that its
managed demand remains empty, its raw demand remains present, and its external
contract remains may-block.

The cut is guarded by exact/ABI-mismatch/explicit-policy/ambiguity tests,
static call/defer/spawn propagation tests, raw-address and raw/plain-ingress
tests, the dual-entry scheduler-root regression test, the production directive
inventory, and real `time`/`sync` standard-library build acceptance.

The two foreign-pointer-result contracts (`mmap` and `gai_strerror`) move to
typed generated result-lifetime metadata.  Executor-safe poll/doorbell/queue
leaves move under verified adapter closures or LLGo-owned C proofs.  Unknown
external C remains correct but slower through the conservative default and
therefore needs no annotation merely to compile.

Generated archives may still contain many exact callable records.  The
architecture target is zero handwritten Go propagation metadata, not zero
machine-produced facts.

### 7.8 Hard migration gates

The reduction is accepted only in complete, ordered cuts:

1. **Complete:** add, consume, and transitively round-trip the v2 producer
   schema for managed functions and exact foreign callables, while retaining
   declarative export bindings as non-authorizing records;
2. generate exact export adapters and prove nested, global-state, retained, and
   foreign-thread ingress without address lookup;
3. reconcile return, recovered return, panic, `Goexit`, pending cancellation,
   and teardown across the C stack;
4. change open and unannotated typed-C calls to the conservative target policy;
5. **Scheduler-wait complete; control pending:** infer exact raw-host wait
   occurrences and replace remaining control operations with typed internal
   operations;
6. remove each remaining directive class and change its inventory gate
   directly to zero;
7. reject any later source-visible increase.

Executable coverage must include:

- a C function which calls Go without receiving the callback as an argument;
- a closed and an open dynamic C function pointer;
- nested same-M callbacks which suspend on timer, channel, and I/O;
- a callback retained and invoked later by a foreign-created thread;
- panic/recover, unrecovered panic, `Goexit`, and pending cancellation;
- imported library metadata success plus schema/ABI/target/digest mismatch;
- native same-M/worker selection and single-thread event/fail-closed selection;
- setjmp/longjmp, fork parent/child repair, GC roots/finalizers, and libffi
  closure ingress.

Until gates 1 through 3 pass, the temporary any-thread/no-reentry default must
not be changed.  Until a directive class reaches zero with its replacement
tests, its exact monotonic snapshot remains in force.
