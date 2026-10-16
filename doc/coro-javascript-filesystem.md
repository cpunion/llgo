# JavaScript filesystem coroutine boundary

The Node runner adapts the asynchronous boundary of Go's `syscall/fs_js.go`,
not its public file methods. Go retains the descriptor table, current directory,
seek position, directory entries, flags and error mapping. The generic
freestanding file wrappers and POSIX-shaped poll/net overlays do not select JS.
WASI and wasm-unknown retain their existing adapters.

## Submission

The source patch replaces only `fsCall`. A private defined helper in `syscall/js`
converts its arguments with the existing `ValueOf` implementation and submits
one ordinary typed HostOp. Its callers infer suspension normally. No new coro
directive, public `FuncOf` callback ABI, scheduler or synchronous JS-to-Go reentry
is involved.

Class 4, verb 1 uses four argument words:

| Argument | Meaning |
| --- | --- |
| 0, 1 | UTF-8 method name pointer and length |
| 2, 3 | `[]js.Value` data pointer and element count |

Pointers 0 and 2 remain typed in the compiler's source ABI. The host immediately
copies the name and resolves the wasm32 handles into a JS argument array; it
retains neither a guest `DataView` nor pointers into Go argument storage. Standard
Go file reads/writes use independently allocated JS `Uint8Array` buffers, so their
pending callbacks do not hold a detachable view into growing WebAssembly memory.
This is not a guarantee for arbitrary user-created wasm-backed JS views.

The runner allows only the asynchronous methods used by Go's filesystem code.
Node's real `fs` methods execute them. In a browser, unavailable operations return
the existing `ENOSYS` error; a filesystem is not invented. `node:path` supplies
the existing Go wrapper's absolute-path bookkeeping on Node.

## Completion and early cancellation

One map keyed by exact source slot/generation tracks physical obligations in the
existing host reactor. A terminal callback publishes three result words:
result-value handle, error-or-null handle, and zero. Go retains its original
`mapJSError` conversion. Synchronous callbacks and throws are deferred through
the same microtask path; duplicate callbacks cannot publish or allocate twice.
Publication drains host actions but never recursively executes Go. Pending file
operations count as host obligations even when there is no alarm or queued turn.
Executor slices use a host task (`setImmediate` on Node, `setTimeout` fallback),
not an endlessly chained microtask. Thus a runnable `Gosched` loop cannot starve
the I/O/timer callbacks needed by other goroutines. Physical completion still
publishes through a microtask, preserving the no-reentry rule and exact receipt.

An observed cancellation does not acknowledge completion until the actual host
callback has joined. It allocates no result handles. A successful `open` whose
caller was already canceled is physically closed asynchronously before the join
is acknowledged. Fatal disposal likewise cleans up a late successful open without
calling guest exports or touching freed scratch memory. Normal completion with
unjoined operations is rejected.

## Acceptance and unresolved ownership

Source gates ensure public Go filesystem bodies remain and JS does not select
POSIX poll fields. A production-helper SSA gate checks the exact opcode, arity
and typed owner mask. Node protocol tests cover delayed/inline callbacks, throws,
duplicates, memory growth, exact cancellation identity, canceled-open close
barriers and late callbacks after disposal. A real Node fixture exercises file
creation, cwd, writes, seek, positional I/O, EOF, error mapping, descriptor-table
checks, directory iteration, cleanup and formatting. Consult the dated acceptance
ledger for the compiler version and actual result; host mocks alone are not an
end-to-end acceptance.

The unified v36 acceptance ran this fixture twice with the production Node runner
and passed all operations, printed its final marker and removed its test directory.
An additional real-fs tracking run observed two opens and two physical closes,
with no outstanding descriptor when `go.run` returned, before process exit could
hide a leak. CI uses that tracker with the production runner and actual Node fs.
This establishes the ordinary filesystem path, not the unresolved ownership
case below.

Post-publication resource cancellation is a separate, confirmed core ownership
gap: a ready scalar result can still be suppressed by late task cancellation.
An fd already published to the core then needs a disposition/cleanup owner;
merely waiting an extra microtask does not solve it. `syscall.Open` also performs
further asynchronous calls before returning an owned `os.File`, so consuming the
first scalar result alone is not a complete resource-adoption proof. This adapter
does not claim that broader cancellation case is solved.

### Finite borrowed-output acquisition boundary

Internal class 4, verb 2 requires method `open` and adds a fifth argument: a
typed `*int32` borrowed output, aligned and initialized to -1. The previous
unconsumed scalar-only form is removed, not retained as a second protocol.
`syscall/js.llgoFSOpenInto` is a real private Go adapter; its caller must register
an ordinary deferred close before submission. No capsule, extra core state,
compiler ownership ABI or global resource lookup is involved.

**Public `syscall.Open` and `os.File` paths have not switched to this primitive.**
Source gates preserve the original `fsCall("open", ...)` call. The direct
consumer is `runtime/_testgo/coro_js_resource_borrowed_fd`, which tests this
limited boundary rather than claiming public result delivery is solved.

On success the callback writes the fd to the output immediately before Post,
using a fresh memory buffer after any `memory.grow`. FD 0 is valid. Before that
transfer, canceled/failed-instance acquisition is closed by the host before
acknowledging physical join. After transfer, only the Go guard owns release;
late cancellation may discard the scalar result but cannot erase the output.
Duplicate callbacks do not write memory or close the transferred fd again.

If exact-operation or empty-output validation fails before the store, the
newly opened FD is still host-owned: the host joins its asynchronous close
before rejecting the instance, without posting a completion. A failed close
is fatal and is not retried or presented as successful cleanup. After the
store, a rejected or trapping Post is fatal but does not return ownership to
the host: it must not clear the Go output or close its FD again. The guest is
not resumed. Reclaiming already transferred resources after fatal instance
termination remains outside this normal cancellation protocol (see below).

| Result | Word 1 | Word 2 | Word 3 |
| --- | --- | --- | --- |
| Successful open (FD in borrowed output) | 0 | Null handle | 0 |
| Failed open | 0 | Original JS error handle | 1 |
| Observed cancellation after physical join/close | 0 | Null handle | 1 |

Word 3 is the private adapter's success discriminator, not a Go errno or a
second logical outcome. Only the Go wrapper interprets the error handle. Success allocates no
descriptor handle. Cancellation, close barriers, duplicate callbacks, disposal
and memory growth share the ordinary HostOp lifecycle. Publication accepts only
`WorkerOperationPosted`, not nonzero duplicate/closed/stale status values.

The opt-in real gate `LLGO_CORO_JS_RESOURCE_ACCEPTANCE=1` uses actual Node
`fs.open`/`fs.close`, a pre-registered Go defer, an additional fstat await,
suspended-frame GC, early/late cancellation and two physically delayed closes
while another Go cleanup makes progress. It checks all six FDs are closed before
`go.run` returns, not at process exit. On 2026-10-16 the fixed r3 compiler plus
borrowed-output adapter and host-task runner passed the real six-FD fixture;
the existing v36 standard-filesystem artifact also passed with the new runner
(two opens/two closes). A bounded 50-slice host-fairness regression fails on the
old microtask scheduler and passes with this change; the complete JS protocol
gate passed 20 repeats. These are the finite adapter's results, not public Open
ownership acceptance.

Transport alone cannot complete ownership. The known `syscall.Open` wrapper must
register an ordinary defer guard before submission, retain custody across
`fstat`, directory `readdir`, path resolution and descriptor-table registration,
and remove any provisional table entry when cleanup closes the fd. A normal
return may release that guard only at a proven final child-result handoff, not
immediately after storing `files[fd]`.

The higher-level chain also matters: `os.open`, the `ignoringEINTR` closure,
`openFileNolog`, `newFile` and `OpenFile` run after `syscall.Open`. Transferring an
integer to the first wrapper is not proof that the final caller owns an
`os.File`. The pending implementation therefore needs typed handoffs through
these known acquisition wrappers using the existing child-completion and defer
paths. It must not infer linear ownership for arbitrary Go integers, grow every
task/operation record, or treat a finalizer as deterministic cancellation
cleanup. These handoffs remain unimplemented. Fatal instance disposal after
transfer/failed publication needs a separate host-instance resource policy;
normal cancellation proof must not be presented as cleanup after arbitrary
fatal termination. Nor does the primitive promise cleanup after a public caller
has received an FD but has not yet registered its own defer.

Public `syscall/js.FuncOf`, dynamic reflection on freestanding targets, and
long-lived JS value-handle reclamation remain separate missing capabilities.
