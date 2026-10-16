# LLVM coroutine acceptance status

This ledger records current acceptance, separately from the historical phase
notes in the design documents. Update a row only with a test of the relevant
compiler and generated program. Host-Go unit tests do not establish LLGo
source compatibility, and one platform's result does not establish another's.

## Current checkpoint: 2026-10-17 (Asia/Shanghai)

PRs [#262](https://github.com/cpunion/llgo/pull/262) and
[#263](https://github.com/cpunion/llgo/pull/263) are merged into `llvm-coro`
as `113f68c32` and `7cc805a0d`. Their exact heads `06502a0a8` and `3b9571606`
each passed all 18 coroutine CI jobs (runs `38856508230` and `38857042105`);
review, inline-comment and issue-comment inventories were empty. No main
branch was synchronized or changed. PR
[#264](https://github.com/cpunion/llgo/pull/264) is also merged as `5e6cd10b8`:
head `4f5aaa575` passed all 18 coroutine CI jobs in run `38866563774`, with
empty review/comment/thread inventories. Its fresh v67 complete `test/go`
run passed **268/268**, zero failures/skips, including nested compiler probes
and both original caller-PC regressions. It completed at 08:00:52 UTC with
matching source-after, joined children and reclaimed private scratch. Binary
SHA-256 is `5ca681fc6f646bd6fc78eb8551ce2a87625cf35543017831ff8b17944c781dc8`;
run peak is 6360.5MiB. The v64 failed cohort remains retained separately.

The six previously rejected byte-alias-dependent standard-library packages
now all have complete actual passing reruns: v65 covers `net/rpc`,
`net/http/cookiejar`, `net/http/httputil`, `net/http/fcgi` and
`net/http/pprof`; v69 covers `net/rpc/jsonrpc` (3 tests). Both cohorts match
source-after and reclaim private scratch. They are affected gates, not a
single-snapshot 180-package pass.

The declaration-only `runtime/cgo` package retains gc SSA bodies that LLGo
does not emit. Exact managed-link pairing now includes those omitted bodies
and declared methods. Its complete actual package passes 2/2 tests on v66
and v69. Managed constructor/method values additionally need a source-typed
forwarding facade, owned by the emitted implementation package. The existing
Go forwarding adapter is reused for function values, methods, interfaces,
reflection and go/defer; ordinary direct calls retain the canonical fast path.
Build admission and lowering now share the exact static spawn proof in SSAPlan,
removing their duplicate method resolvers. Receiver/argument types, non-capture,
closed targets and preemptible coroutine-primary checks remain enforced.
Host race, native/wasm32 CoroSplit and unchanged architecture-budget gates pass.
The IR test helper now obtains immutable runtime type declarations from the
production loader rather than host-Go importing LLGo-only sources; the existing
metadata and noinline assertions pass unchanged.

The stronger actual cgo route fixture deliberately covers constructor values,
bound/method-expression/interface/reflect calls, deferred deletion, spawned
deletion, yield/GC and P=1/2. It exposed successive adapter admission and
cross-package symbol failures in v66/v68/v69/v70; none is recorded as passing.
Carrier publication now freezes its ordinary Go definition symbol and every
consumer reads that same definition key; a missing key fails closed. Restoring
the old symbol lookup fails the new three-package identity test. The fresh
linked-program result is recorded below. Immutable v70 also completes a new
whole `test/go` run: **268/268**, zero failures/skips, source-after matching,
children joined and scratch reclaimed at 08:41:59 UTC. Binary SHA-256 is
`6813a06a978452cb944ff975e6233efa5c5cea050b0e206877124c5d30db0e17`;
run peak is 6503.3MiB. The same v70 also completes **73/73 native Go demos**,
zero failures, at 09:13:31 UTC. Every package is freshly compiled and executed;
the final source-after matches and private scratch is reclaimed after child
join. Its cohort-manifest SHA-256 is
`90246b7317c30a9ad6b079f7c368a114b598ed3d9a4f90fc9a3c48037d7227dd`,
and completed journal SHA-256 is
`e6ac74c4623a37b3340ad08ee3fab7c592ee2d3c819670a71633e678c6c6eefa`.
The 180-package stdlib cohort and v65 eight-shard GOROOT rerun remain live.

The cgo follow-up is now [PR #265](https://github.com/cpunion/llgo/pull/265),
head `bea4a1ff7`, targeting `llvm-coro` only. Immutable v71 completes the
stronger linked cgo fixture **10/10 times**, including both reflection forms,
spawned deletion and P=1/2, plus the Go control. Compiler SHA-256 is
`358f257ae71ca8169492050bc44d824c2ce03518cfa70dc2de011f716e92e038`;
source manifest is `c55615ceac327f9454430dbad4bfb5f3f6908b67226cdc24203ad7693fcc3601`;
binary is `164e49ea966d91d62eaa97cf86c10d0d06951b4f988707d4fbb547000d8496be`.
Source-after matches; combined build/run peak is 2706.1MiB. The broad gate
passes cl 155.553s, cltest 8.183s and the actual architecture gate 0.495s,
with unchanged budgets. CI run `38871898480` completes all 18 jobs successfully.
After rechecking the empty review/comment/thread inventories, the exact head
is merged as `a616440ee` at 08:40:54 UTC. No main branch is changed.

GOROOT shard 0 completes 136 cases with 135 passes and one `inline_callers.go`
stdout mismatch on the older v65 source. The unchanged original case passes a
focused strict v71 rerun (63.49s; matching source before/after, 2835.2MiB peak).
The older failure remains in its original cohort; this is not an eight-shard
or final-head full-GOROOT pass. During shard 1 the older source also rejects
`noinit.go`'s private gc `main..inittask` symbol, and `rangegen.go` stops at the
existing 24GiB per-build RSS cap; these are distinct ABI/resource outcomes,
not new xfails. Its caller-stack failure in unchanged `fixedbugs/bug347.go`
passes a strict focused v76 rerun (29.18s case / 1491.9MiB peak, matching
source-after). The live shard's old failure is retained as-is.

Cleanup scope work is isolated from the merged PRs. Review rejects the broad
all-source-defer experiment: Go main return does not promise completion of
other goroutines' ordinary defers, so the v74 normal-defer diagnostic is not
an oracle for a Go semantic violation. Its exit 86 and reclaimed fd remain
recorded; the broad v75 attempt also retains its channel-close failure. The
new executable regression instead tests LLGo's existing structured shutdown:
both tasks claim their stop before entering cleanup, then rendezvous through
channels and close an actual fd exactly once. Immutable v76 compiles and passes
the linked regression and ten repeat executions, with source-before/after
matching. Compiler SHA-256 is
`26e7cd871d0b228ba7b469f6044c8c19c25c71460938a89360f7cb49da6c3015`;
manifest is `08a0ae19243378716969d3e92b82b9f8ca76a0be4bb1dd069915921a694b21dc`;
binary is `a824efcc8a9487ab87086daf4e7a51ffb31b01c5e77e386d123e7e4dd8068f71`.
The same v76 leaf fixture with the older v75 compiler/runtime aborts; both
source manifests remain unchanged. This explicit mixed-version negative is
not a same-snapshot acceptance pass. The permanent native CI table now runs
the new fixture. A fresh broad compiler selection also passes: cl 113.108s,
cltest 6.833s and build/bootstrap/architecture 0.681s, peak 670.4MiB.

The private finite internal-cleanup scope now uses the shared static/dynamic
await reconciliation; source Go defers do not acquire this mask. Native and
wasm32 pre/post-CoroSplit tests check that distinction and ordinary-call zero
overhead; the architecture access budget is unchanged. The scope retains the
existing cancellation byte, exact owner receipt, physical join, consumed
completion, panic overlay, frozen cause, operation cancellation and preemption.
It adds no G/frame/park/completion fields or allocations. The compact channel
path previously accepted only None/Idle and rejected valid cleanup states at
prepare, commit or completion. One validated cancellation predicate now admits
only ordinary or deferred/frozen stops and preserves the receipt across park
reuse. A 24-combination test covers issued/compatibility preparation and local,
routed and pre-suspend completion; 14 invalid/interrupting inputs stay
zero-mutation rejections. Full core race/shuffle and selected Node/wasm core
tests pass (4.756s / 0.166s, combined peak 369.8MiB). Restoring only the old
compact-channel file through a Go overlay passes all six ordinary controls
but fails all 18 cleanup combinations, isolating the runtime transition fix
from unrelated compiler changes. Public resource API seeds
remain disabled; these gates do not establish public resource delivery.

PR [#266](https://github.com/cpunion/llgo/pull/266) is now merged into
`llvm-coro` as `890c8b998` (09:48:52 UTC). Exact head `ad5d61b29` completes all
18 jobs in run `38877870414`; reviews, inline/issue comments and review threads
were empty immediately before integration. Main remains untouched.

The next isolated branch implements closed static resource delivery in the
existing cleanup drainer: one active tag per physical frame, existing typed
result slots, typed env/argument captures, and a separate nil-output producer
fallback. The normal lane claims only after child completion is consumed;
panic/cancel uses the same drainer and finite internal drop scope. External
range-yield defer registration remains separate from yield-owned temporary
resources. Source recipes now publish exact typed delivery records; an import
without physical helper binding still rejects them rather than losing custody.
Helper placement/edge validation and relocation use the existing shared
gateways; the monotonic architecture budget is unchanged.

Native and wasm32 IR verifies before/after CoroSplit. The broad selection
passes cl 227.200s, cltest 7.344s and architecture 0.996s (626.1MiB peak).
Immutable v77 also compiles and runs a real fd fixture covering normal caller
ownership, a discarded `go` entry, GC while a C acquisition is pending, and
parent cancellation before delivery. Three opens have exactly three closes;
user continuation never resumes on the canceled lane. Source-after matches;
compiler SHA-256 is `09cbd6e13d984204f42bec32a62f1e14bdbbe8054c89d12f76b967a255a4e135`,
source manifest `4260c9262ab8fc6b11be9a3832c56653eff127abc06c62066b444408bb70b539`,
binary `066464c7dd0daae1b5b831fb85b185bfcc2d2efd3cb1ae0e3ec77d6bf679ae58`.
Combined peak is 1183.9MiB. A one-prefix omission negative builds successfully
but fails with `open=3 close=2` (exit 86), then explicitly reclaims its fd;
source-after matches. v78 also executes an internal drop interrupted between
two awaits successfully. These initial prototypes auto-dropped a normal
discarded entry, so **they do not establish Go-compatible normal-discard
semantics**. Review corrects that rule: normal discarded raw fd results must
remain open, while canceled or panicking undelivered results are dropped.
The fallback stays armed through source RunDefers and is retired only at
terminal Return commit, using existing status/continuation words. Native and
wasm32 IR, dynamic/library selections and unchanged architecture gates pass
(cl 12.585s, architecture 1.530s; 1301.1MiB peak). A new actual cohort covers
normal-discard preservation, cancellation of an independent nil-output entry,
parent cancellation and late stop during panic-driven internal drop. This is not yet
dynamic descriptor, bodyless archive, reflection second-handoff or stdlib
adoption acceptance; public standard-library seeds remain unchanged.

The corrected immutable v79 linked fd fixture passes all three modes and
**30/30 repeat runs**: normal discarded results stay open, independent and
parent-owned canceled acquisitions close exactly once, and a panic-driven
internal drop finishes its second await despite late shutdown. Source-after
matches. Compiler SHA-256 is
`f88b5f727b95acf572e259b9eed8c9720de358e84771b7dbeea27f273eefa361`,
manifest `e042c061b8a3e12f0546a5b111e12ac69c541ace30b4ae3bb72373e535744495`,
and linked image
`8fe73d05a05177a05762cec449635ef8d2474ce1786fe173384484f09b4bed0d`.
Removing only the compiler's internal shield leaves modes 0/2 passing but
mode 1 fails with `open=2 close=1` (exit 86, explicit fd reclaim); the existing
parent-prefix omission negative independently proves parent custody.

Further review removes a synthetic pre-return RunDefers pass for resource-only
frames: the existing terminal drainer already owns that cleanup. This avoids
another result spill/copy. Large nil-output fallbacks now allocate only on the
nil-output branch; a provided parent slot bypasses the allocation. Native and
wasm32 IR gates require the exact branch, no unconditional aggregate AllocZ,
no oversized frame, and pre/post-CoroSplit verification. The selected compiler
gate passes 8.052s and the unchanged architecture gate 1.072s (1286.2MiB peak).
The next actual cohort adds deferred, range-yield and second-site acquisitions;
these routes are not yet recorded as passing. Permanent native CI now includes
the typed resource fixture rather than relying only on local scripts.

The first expanded v80 cohort is retained as a compile failure, not a pass:
the static-defer invocation had no relocated source occurrence, so its resource
slot remained unbound when the drop prefix was emitted. The fix routes static
defer through the existing cleanup emission gateway. A deferred initializer
and a later undelivered-result drop now occupy distinct frozen SitePlan phases;
both still use one owner-local drainer and shared helper/await resolver. Large
deferred result slots reuse the producer's already materialized allocator edge.
The native/wasm32 gate now verifies both ordinary and deferred large consumers.
The actual six-mode rerun and unchanged architecture gate remain required.

Immutable v81 closes that static-route gate: the permanent native test passes
all six modes, **60/60 executions**, including deferred and range-yield ownership
and canceling the second static acquisition site (43.39s test / 1257.8MiB peak,
matching source-after). Compiler SHA-256 is
`e64865c15297d0f0810421b7646086bc63ebd7e0a67780fb1dee785342e6ecc6`;
manifest is `87fa254f9904020726470eaf1d39e27699d7b6a6c108554f5fa41ffd677539de`.
The final selected compiler/architecture gates pass 10.845s / 0.845s with
unchanged budgets (1241.4MiB peak). [PR #267](https://github.com/cpunion/llgo/pull/267)
targets `llvm-coro` at exact head `7fe24b518`; CI run `38888860337` is pending.

PR #267 is now merged as `be11dc9c9` at 11:15:25 UTC. The exact head passes
all 18 jobs in run `38888860337`; paginated reviews, inline/issue comments and
all review threads are empty immediately before merge. Main remains untouched.

The reflection failure is isolated on `coro/opaque-function-family-20261016`.
A native debugger stops at an indirect call through a nil plain entry in
`main.f$outcome`: source-only CHA had closed the signature around a normal
literal, while the actual MakeFunc descriptor publishes only coroutine entry.
Four minimal plan regressions fail on the old implementation (assertion,
comma-ok assertion, external function result and unsafe load). The correction
retains opaque callable domains in the existing SSA flow; exact scalar-flow
and explicit closed-call proofs remain valid. Host core race passes 23.471s;
the compiler selection passes 38.732s, unchanged architecture 0.807s, and
native/wasm32 open-vs-closed-family CoroSplit gate 3.369s. Actual unchanged
GOROOT execution is still required before calling the reflection bug fixed.

The unchanged original GOROOT `fixedbugs/issue27695b.go` now passes a strict
immutable v82 rerun (54.988s LLGo build, 302ms run; 2027.2MiB peak), with
matching source-after. Compiler SHA-256 is
`d08c1a825f7db497a927319ee8725ced9bfd44b7427e7c923d643b8295399184`;
manifest is `f5c0d8e4fbf91ecc6d2d6c18862f7e8e613470397543e3d4a69ab81fededd1ed`.
An isolated v82 compiler overlay removes only opaque-domain retention for a
same-runtime actual negative: it builds successfully but the application again
exits 2 with `invalid zero-ticket coroutine run decision` (2066.3MiB peak,
matching source-after). This isolates the fix from runtime changes. The first
complete v82 `test/go` attempt compiles successfully, then stops before test
execution because the caller PATH omitted `/usr/sbin/lsof`. It is retained as
an infrastructure failure; after verifying the dead owner and no open handles,
its 227384KiB private scratch is reclaimed and the built binary retained.
A fresh v83 complete `test/go` uses the corrected PATH and is active. Review
extends the same existing function-leaf
schema to opaque struct/slice/map/channel values, rather than fixing scalar
assertions alone; all eight origin regressions and full core race pass
(73.659s, 1578.5MiB peak). That aggregate extension needs its own final
executable/IR acceptance; v82's scalar result is not relabeled as testing it.

The immutable v83 permanent native gates pass all six resource modes (60/60)
and the unchanged original reflection/GC case, cgo-handle routes and claimed
cleanup channel (67.966s resource package / 239.151s reflection package,
2881.3MiB combined peak; source-after matching). Broad host compiler testing
additionally catches one over-conservative rule: ordinary Go function-field
loads must not be treated as new opaque producers, or Linux mmapper loses its
existing uintptr pointer-result proof. The refinement traces unsafe/opaque
storage instead, preserves ordinary typed storage, and adds an indirect unsafe
storage-wrapper case. All nine origins, closed plain families, native/wasm32
universal-entry IR and the unchanged Linux mmap/read discrimination now pass
(core race selection 1.238s, cl 6.252s, architecture 0.912s; 1279.5MiB peak).
The broad rerun and actual final-refinement execution are complete on v84:
cl 180.453s, unfiltered cltest 7.561s, architecture 0.767s and full core race
24.585s pass. Immutable actual execution passes all six resource modes (60/60),
unchanged reflection/GC, cgo-handle and claimed-cleanup-channel routes
(2753.0MiB combined peak, matching source-after). Source is `8d3209239`,
compiler SHA-256 `ba4afdad5e59390f94ab13d3ca426702b30631a3fee8bcaf7975ece5eae9371c`,
manifest `66df1c59ada538c676f6f7d9e31150bc2c4ded3450f63e9a000eec6904edfb5a`.
[PR #268](https://github.com/cpunion/llgo/pull/268) is merged as `cec411c36`
at 11:42:49 UTC after all 18 jobs in run `38893085392` pass at the exact head.
Paginated reviews, inline/issue comments and threads are empty directly before
merge. No pointer-proof expectation or architecture budget is relaxed.

Old shard 2 also reports `fixedbugs/issue27695c.go`; a strict unchanged-case
v84 rerun passes (135.434s LLGo build / 323ms application, 2039.2MiB peak,
matching source-after). The first launcher supplies `v84` instead of numeric
`84` and exits before running a case; corrected `r2` is the actual pass.

The next isolated branch implements the optional typed descriptor delivery
tail. Ordinary descriptors retain all nine base fields; extensions nest the
complete base, preserving wasm32 padding, and contain only version, reserved
and two existing managed helper descriptors. Publication checks exact output,
environment and argument types, helper ABI hashes, context-free provenance and
void results. Positive capability control flow dominates each tail read.
Unguarded universal dispatch and reflection refuse the capability; code identity
queries remain valid. Narrow closed nonresource calls retain their one-word
entry load. Source producer publication still rejects until its physical guard,
graph placements, library helpers and reflection second handoff are bound.
This is an ABI primitive, **not completed dynamic/public resource acceptance**;
public standard-library seeds remain off.

The first broad compiler rerun is retained as a failure: creating a descriptor
trap before a later package-owned `llvm.trap` declaration produces the invalid
renamed intrinsic `llvm.trap.1`. The shared package declaration path now adopts
an already-created LLVM intrinsic only with the exact C signature, no closure
environment and no instantiated linkage; mismatches reject. Standalone native
and wasm32 tests cover this declaration-order regression. The existing closed
plain-family gates pass again (11.870s), without altering verifier checks.
The delivery capability is also folded into the existing allowed-flags mask:
ordinary universal calls retain precisely the old validation mask/trap, and
frozen closed nonresource calls gain no delivery probe. Full SSA passes 17.482s,
unfiltered cltest 15.260s, initial selected compiler 43.199s, architecture 2.215s
and host FFI tests 0.299s. The final broad compiler rerun passes (323.209s),
followed by immutable v85 at source `06d6cd956`: all six real-resource modes
pass **60/60** executions, and the original reflection/GC case, cgo route and
claimed-channel cleanup all pass. Compiler SHA-256 is
`13aa706f36e3f219f121a965b9716d3de7104db6659d1d5e64ba3fcfed49b1d0`;
source manifest is `6a7c691216dd09de5e1c9134ed9cef83f70f55bb5a0e14d0843ff9e12bd75bec`.
Source-after matches; combined peak is 2865.8MiB. These are static delivery
and descriptor ABI gates, not public dynamic resource acceptance.
PR [#269](https://github.com/cpunion/llgo/pull/269), exact head `06d6cd956`,
passes all 18 coroutine CI jobs in run `38898722459`. Fresh paginated
review/inline/issue-comment inventories and GraphQL review threads are empty;
the exact head is merged into `llvm-coro` as `578504b0c` at 13:02:50 UTC.
No main branch is changed.

The v83 complete `test/go` run retains **266/268 passes and two failures**:
both nested compiler probes report missing `gc/gc.h`. The identical retained
test image and compiler pass both focused probes with and without the original
per-invocation cache isolation; neither replay establishes the original cause
or changes the complete result to a pass. A fresh complete v85 run is live.
GOROOT shard 2 completes 132/135 cases: the older `issue27695c` already has a
strict v84 pass, while `issue5856` still fails a strict unchanged-case v85
replay (defer caller reports line 17 instead of 28). `issue75764` exercises
wrapper tail-call performance; its older failure remains pending a current
replay, not classified as an environment-only failure or a new xfail.

The next isolated branch freezes typed hidden managed invocations without
inventing SSA targets. A result-delivery condition uses the existing exact
scalar/CHA/open-target flow before the sole effect fixed point. Closed
nonresource and nil-only families retain the identical FunctionPlan, no helper
edges and no cleanup storage demand. Potential resource calls add local
custody/cleanup and ordinary managed helper effects; resource ownership is not
a propagating Go effect. Producer metadata is also projected from checked
archive delivery facts. The plan digest binds exact source occurrences and
rejects lost producer/helper/cleanup facts. Full core tests pass (5.123s),
including the complete race gate (24.040s). Physical dynamic helper binding,
library helper publication and reflection second handoff are still gated;
public standard-library seeds stay off.
The broad compiler selection also passes (226.290s / 1244.4MiB peak), as do
the final model/archive checks and unchanged architecture gate (1.067s).

Subsequent isolated physical work retains conditional managed occurrences in
the frontend's existing lowered-call inventory, with one ProgramIR-builder
storage/merge authority for exact and conditional records. Pointer-free
lowering facts can represent the same typed managed transport without a fake
FunctionID; missing exact targets and mixed descriptor/no-unwind/elision
shortcuts still reject. Full core race passes (99.681s / 1669.5MiB peak).
The first frontend architecture probe detects two extra direct plan authorities;
construction is then narrowed to the existing ProgramIR builder and the gate
passes unchanged (0.872s). No budget is raised. Typed private helper metadata
also reuses the ordinary descriptor/primary-thunk factory without changing
helper FuncRep or cloning its source body. Native and wasm32 tests verify
before/after CoroSplit, and reject mismatched helper signatures; public source
publication still rejects while dynamic frame custody is unbound. These are
private intermediate commits, not a completed dynamic resource-delivery path.
The broad compiler selection passes (154.627s / 1285.2MiB peak); the selected
private-factory/compiler gates and unchanged architecture gate also pass
(7.280s / 0.813s). The factory keeps original source-facade identity separate
from its canonical primary, preserving go:linkname function-value signatures.
Immutable v89 executes the existing static resource modes **60/60** plus
original reflection/GC, cgo routes and claimed-channel cleanup successfully
(61.617s / 204.835s, source-after matches; peak 2757.9MiB). Full SSA and cltest
also pass (20.558s / 19.370s). Compiler SHA-256 is
`5ba48275be2c8cba27e139afb27cc66563ffe5b4bbff7bd9158025dcef6695ae`,
manifest `29e101a979065e19e4ab5fdc6dbf8bfe2f4557af95bd3fe895df58bc164a1dc3`.
The private work rebases onto the merged integration branch with the identical
Git tree `d7b13e8d53b10d9ac6d611dbe6b73d4e2c8166c6`; this later ledger update
changes only documentation. These execution gates establish refactor safety,
not the still-unbound dynamic resource path.
This planning slice is [PR #270](https://github.com/cpunion/llgo/pull/270),
head `c5a501c86`, targeting `llvm-coro`. Immutable v86 passes all 60 resource
executions plus original reflection/GC, cgo routes and claimed-channel cleanup
(2784.2MiB peak; source-after matches). Compiler SHA-256 is
`af765a2c414a904b2fb74ead6914f506c5aa50397f4f67ee48b459c1512f8bdf`,
manifest `0708714a92f890468a6ea7b2bde9f5132655d4c542f2243313136d24cb830099`.
The first compiler launcher raced snapshot extraction and failed before
producing a compiler; retained `r1` is not a compiler acceptance pass. The
complete frozen-source `r2` build is the compiler used above. PR CI is pending.
CI head `1d139f475` later passes 16 jobs but fails the Cortex-M lane's host
production-PC-cache fixture at its shared 60s deadline, before the linked
Cortex-M program step. That log cannot separate race compilation and actual
cache execution. New head `6fb992a92` separates a bounded 3m build from a
stricter 20s test / 30s process execution limit, without changing production
declarations, policy sizes or collision/race assertions. Warm verification
passes 4.371s; an explicitly empty nested GOCACHE also passes (cold build
15.037s, executions 1.388s/1.321s, matching source-after, 256.9MiB peak).
This does not establish the failed GitHub worker's original stage/cause.
Run `38909611903` verifies the corrected head; the old failure is retained.
That run completes **18/18 successful jobs** at 14:05:55 UTC. After fresh
paginated review/comment and GraphQL thread checks remain empty, exact head
`6fb992a92` merges as `402beabea` at 14:08:35 UTC.

The fresh complete v85 `test/go` run now passes **268/268**, zero failures or
skips, at 13:09:34 UTC. Source-after matches at 13:09:35; all child process
domains are joined and private scratch reclaimed. Test-image SHA-256 is
`26aeca95e3fa6ceb1952b830923966c07883ca80f2919d66a1658aa319683592`,
run peak 6536.8MiB. The old v83 whole-run failure remains unchanged.

Strict unchanged-case v85 `issue75764` still fails: 9.041us versus 164.291us
for 1,000/10,000 wrappers, ratio 18.17 against the original threshold 9.
It is a wrapper/stack-inspection performance gap, not a new xfail. A separate
defer-caller probe establishes the `issue5856` cause: the logical panic stack
omits gc's `runtime.gopanic` frame and consequently shifts Caller skip indices.
The isolated fix reuses the existing exact CompletionRecord before and after
recover, snapshots the live owner even without retained destroyed frames, and
materializes one interned logical runtime frame only on stack observation.
No G/Frame/CompletionRecord field or scheduler protocol is added. Runtime core
race, production PC-registry race, Go control and unchanged architecture gates
pass. Immutable v87 passes the unchanged original GOROOT `issue5856.go`
(72.83s case, 2911.1MiB peak; source-after matches). Compiler SHA-256 is
`478da962e09d39e846a0f3ea872f258c1c2caebce26909d4e11adfeb971039b8`,
manifest `7d7d2bc242dfc49b10829b461b45e8feb9d225b7c9c4f2db1ef03db61c86264e`.
The new linked before/after-recover, retained-frame and repeated-query tests,
plus the existing caller-exhaustion/line-info regression selection, all pass
(line-info 64.67s, source-after matches; children joined, scratch reclaimed).
Exact PR271 head `5da4a2315` additionally has immutable v88 execution:
original `issue5856` passes 65.55s, resource modes pass 60/60, and original
reflection/GC, cgo routes and claimed-channel cleanup all pass (127.810s,
source-after matches, 2787.2MiB peak). Compiler SHA-256 is
`4d77e3ca026743be80a895c496f0bc3212e4213620f51392b0a60563782ba16d`,
manifest `930567042003860c465914653bec1dd550d05dd6ac253e4d6432b4120c4f0b77`.
PR [#271](https://github.com/cpunion/llgo/pull/271) passes all 18 CI jobs in
run `38906867661`; fresh paginated reviews/comments and review threads are
empty. After PR270 merges, another fresh complete review/comment/thread check
is empty and the exact head merges as `4232087aa` at 14:10:44 UTC. Both PRs
target `llvm-coro` only; no main branch is changed. These 18-job coroutine CI
results are not a final all-workflow/full-corpus claim.

The older GOROOT shard 3 completes **134/135**, with only `issue29504.go`
missing the original marker line in its panic traceback. The unchanged case
passes a strict current v89 replay (103.70s case, source-after matches,
3084.9MiB peak). The older failed cohort is retained; versions are not stitched
into a fictitious same-snapshot full pass. The eight-shard script continues
into shard 4. The live v70 stdlib cohort has 71/180 completed packages, all
passing at that checkpoint; neither long cohort is yet complete.

The next development cohort wires conditional delivery consumers into the
production frontend, rather than registering test-only helpers. Exact typed
recipes bind their non-debug source ordinal and source/cleanup/result-drop
placements before the existing effect fixed point. Final value-flow decisions
prune both the pointer-free lowering ledger and all emission placements; a
closed nonresource/nil-only family keeps no helper or cleanup ownership.
The caller's `nounwind` certificate cannot certify a dynamically selected
initializer/drop. Both exact and descriptor helpers now share the existing
frozen-record validator and SitePlan observation boundary.

Physical consumers reuse the result slot, one frame active tag and existing
drainer/child-await engine. They read the optional tail only on `HasDelivery`,
arm only after initialization, claim after completion/result consumption and
clear before dropping. Compiler helpers never inherit direct-defer recover
permission. Native/wasm32 pre/post-CoroSplit tests pass with **CompleteRuntimeABI**
and the full helper observer, including ordinary calls, local defer and looped
heterogeneous defer (2.686s / 1229.7MiB). The final focused helper/ledger/cleanup
gate passes 5.985s / 1297.9MiB and the unchanged architecture gate passes
0.930s / 699.1MiB. The initial architecture failure (one extra observation
authority) is retained: the correction shares the existing boundary rather
than raising a budget. These are emitted-IR/host gates, not a claim that a
dynamic resource producer is already publishable or verified end-to-end.
The producer-publication, unguarded-dispatch and FFI rejection gates remain;
bodyless library helpers, external defer owners, conditional allocation for
large deferred dynamic results, output-loan lifetime proof, reflect's second
handoff and standard-library adoption remain required before public seeds.
The full compiler and immutable linked-program regressions are tracked
separately. The ongoing older v70 stdlib cohort now has **76/180** complete,
all passing; the older GOROOT eight-shard run remains in shard 4.

This cohort is now draft [PR #272](https://github.com/cpunion/llgo/pull/272),
exact head `a2f727c5b`, targeting `llvm-coro` only. Immutable v90 compiler
SHA-256 is `a5c4cd0f052dbd91b8822dcf989cac579f5fe96dceb1ba049b8939182c11acde`,
manifest `cb646fec2c18f245bec8ad8ceca9e2f2571f163f77c8f076f3ddba810340d6a5`.
The six existing resource modes pass **60/60** (48.093s); all original
reflection/GC, cgo and claimed-channel routes pass (205.550s), source-after
matches, peak2808.7MiB. These establish regression safety, not a publishable
dynamic resource producer. Host race selection passes24.916s/1315.3MiB, full
SSA14.220s and cltest14.207s pass/1032.6MiB, and the existing broad
coro/emission compiler selection passes204.070s/382.8MiB. CI `38917619760`
is still running at this checkpoint (16/18 successful, no failure observed);
initial paginated review/comments and GraphQL threads are empty. The new
isolated full `test/go` build completes and its runtime suite is running.

An extra **unfiltered** `go test ./cl` is not a passing receipt: legacy tests
fail to load a complete runtime profile (undefined coro GC/synctest hooks),
then the package's default10m timeout fires inside
`TestRunAndTestFromTestgo/cgocfiles`. The same profile failure is reproduced by
unchanged v89 `TestRewriteGlobalStrings` (2.323s/1223.1MiB), before this
consumer increment. The duplicate unfiltered v90 run is stopped, its processes
are joined, and the failure record is retained. No test/golden/xfail is changed.
This phase's broad compiler gate is the explicit coro/emission selection,
while runtime compatibility is exercised by the separately isolated full
`test/go` suite; instruction goldens remain outside the requested phase scope.

The next isolated branch generalizes the existing delivery condition to exact
managed helpers, so a statically known allocator and dynamically selected
initializer/drop share the same predicate and sole effect graph. Exact target
identity, demand, effects and conditional source all remain digest-bound;
raw/no-unwind/unwind-only/terminal-elision combinations still fail closed.
Core focused tests pass0.450s/365.6MiB, full core race passes24.606s/1555.5MiB,
frontend metadata tests pass2.766s/1214.7MiB and the unchanged architecture
gate passes0.944s/696.1MiB. This is a core/metadata checkpoint only: large dynamic
defer physical allocation is not yet wired, and the branch is not integrated.

The same isolated follow-up removes a distinct unnecessary allocation:
nonresource source-defer calls pass a nil output to their static or descriptor
child, since Go discards deferred results. They join the same child transaction
but do not load/copy an unused aggregate. Resource guards still provide their
protected result slot and claim only after completion. A compile-time invocation
option records discard semantics independently of recover permission; no
runtime task/frame field is added. Native/wasm32 pre/post-CoroSplit gates with
a 131073-byte result prove that neither static nor dynamic parents introduce
an allocator or large alloca (0.525s/1293.7MiB). Existing delivery/defer/cleanup
regressions pass14.822s/1305.2MiB, the unchanged architecture gate passes
0.929s/743.9MiB, and the new ordinary Go control (yielding large return, LIFO loop
defer, panic/recover) passes0.312s. Its actual LLGo linked execution is pending;
this follow-up remains unintegrated. Resource-bearing large dynamic defer
still needs its exact conditional allocator and does not silently fall back.

PR272 completes all18 coroutine CI jobs successfully and, after a fresh empty
paginated review/comment inventory and empty GraphQL threads, merges at
15:08:12 UTC as `8a45e5537`. Exact head is `a2f727c5b`; external receipt is
`root/ci-38917619760-complete.json`. No main branch changes. The long full
`test/go`, stdlib180 and GOROOT evaluations remain independent ongoing cohorts.

Immutable v91 verifies the discarded-result follow-up on source `8ee494728`:
compiler `49e43dff36709cf4ef143973d554b17b6b305c11df595fc2ee467811cb71c8cf`,
manifest `b6fa6a16a5af0b9aa5c959e25a935f4ed0058072b5e53568bcd1201b9335077b`.
The same ordinary Go program covers yielding 131073-byte results, static and
dynamic LIFO defer and panic/recover. Go and LLGo match in all12 executions
(P=1/2, three repeats per implementation); snapshot/program source-after match,
peak1207.8MiB. Program SHA-256 is
`4439a03d91848bef619d46a13724cc69006c60a8719ca4d0c5af2515774250c8`.
This exact artifact precedes the next allocator increment, not a current-head
dynamic resource-publication proof.

The subsequent local allocator cohort now binds the real static allocator to
the same source condition, ledger and cleanup placement. Large deferred custody
storage is allocated only inside the positive HasDelivery branch; base-only
descriptors retain nil output. The owner frame stores a typed address rather
than the large result. Missing frozen runtime dependencies are rejected for
active consumers instead of materialized during emission. Native/wasm32 gates
prove that allocation is unreachable after removing the positive branch,
the ledger contains both exact conditional allocators, and split frames remain
below128KiB (2.599s/1242.9MiB before the ledger addition). The final race-selected
delivery/cleanup/defer cohort, including the ledger addition, passes
124.511s/1200.5MiB; unchanged architecture passes0.970s/738.2MiB. The first
fixture attempt correctly rejects a missing AllocU declaration for its newly
added loop-defer case; the fixture now supplies the same normal node allocator
and releaser, without changing production/runtime semantics. This branch is
still unintegrated; a final immutable linked regression is pending. The latest
long-run observation is v90 test/go94 completed top-level tests with no failure,
v70 stdlib81/180 all passing, and v65 GOROOT shard4 still running.

The subsequent exact v92 head `f430e3465` completes its immutable linked
cohort: resource modes 60/60 (50.846s), the original reflection/GC, cgo and
channel routes all pass (153.613s total), and the large discarded-result
program matches Go in 12/12 executions. Source-after matches and private
scratch is reclaimed; combined peak is 2852.8MiB. Compiler SHA-256 is
`160db16028e25aa7a87059e73bc4c9020b6b45fff587309642a13b0d31cde9a6`,
manifest `848bea65acdf015746314d3d706be69eea0dad34f919379cb0f5582c35b3e540`.
The exact-head broad compiler gate passes205.310s/1246.8MiB and architecture
passes0.828s/699.6MiB. Draft [PR273](https://github.com/cpunion/llgo/pull/273)
targets only `llvm-coro`; its CI is still running at this checkpoint.

The independent original full v90 `test/go` cohort now finishes **269/269**,
zero failures/skips, at15:33:17 UTC with matching source-after at15:33:18,
joined children and reclaimed cache/work scratch. Peak6824.2MiB; test binary
SHA-256 `11290d54eb1a581230150a5bc8a65184a36094f6efe94bfb740ee8dbd5794902`.
Receipt is `acceptance/test-go-v90-private-r1`, attempt
`dd340943b35cba23-531b5073`. This is not a v92 suite result: the fresh v92
full run includes the new large-defer tests and remains independently running.

The remaining original `issue75764` performance failure has a concrete
algorithmic cause candidate: `DeferredPanicTraceActive` walks every ordinary
await ancestor even when none can own a recover scope. The current follow-up
uses one inherited bit in the existing Frame flag byte, a shared child-link
publication point and an O(1) negative observation. Positive observations
retain the complete CompletionRecord identity/payload validation. No new G,
Header, Frame capacity, recovery counter or completion protocol is introduced.
Complete host runtime/coro race passes4.914s/390.4MiB, including nested alias
recovery and forged-positive rejection; actual unchanged-case validation and
cross-target layout verification are pending, not yet a performance pass.

PR273 subsequently passes all18 CI jobs (`38922569789`) at exact head
`f430e3465`. Fresh paginated reviews/inline comments/issue comments and GraphQL
threads are empty with no next page; exact base/head is MERGEABLE/CLEAN. It
merges at15:48:00 UTC as `4377699c0`, targeting only `cpunion/llgo:llvm-coro`.
The new performance branch incorporates that merge without changing main.

The scope-cache experiment now has an unchanged-case causal control. Exact
v92 fails original GOROOT `fixedbugs/issue75764.go`: short8us, long161.417us,
ratio20.177125 against allowed9 (source-after matches; peak2988.4MiB). Exact
v93 source `b5f5cb2c3` passes the same untouched case (85.88s runner,
peak2801.3MiB; source-after matches). Its compiler SHA-256 is
`6bc1904698f15b3ca36e20ac22c2054bfc835eac5464dd471f6285f66c30b9c2`,
manifest `633a4042a2fbf16e6bdaa8c05937f4cf4bba9bfcbff9e0f353d3f03d8f32cebf`.
Both original logs are retained; this is a scaling-gate result, not a quiet
whole-program performance comparison. Exact v93 host runtime/coro race passes
5.726s/356.6MiB; unchanged architecture passes0.683s/665.5MiB. The first
architecture invocation accidentally selected no tests and is explicitly not
counted; the corrected invocation runs TestCoroArchitectureDebtIsMonotonic.
The first 32-bit host-test compile exposes an existing overflowing constant
in a branch guarded by pointer width. A nonconstant uint64-to-uintptr
conversion now lets the original high-word assertions compile on32-bit;
neither the 64-bit value nor any assertion is reduced. Final portable compile,
positive panic/traceback regressions and repeat original-case gates remain
pending for the follow-up head.

Final immutable v94 `dd9295cfa` completes the caller follow-up's local gates:
compiler SHA-256
`8e6fdd66020abae7495f0a790884dab43c9be44e5189698a4f8a6da1d7e7f039`,
manifest `773e3808bb2097dc1aea33037a91cef0effad6feb59bcbdc597588e245d21235`.
Complete runtime/coro race passes5.875s/371.2MiB; linux/386 and js/wasm test
binary compilation pass382.9MiB/347.0MiB, retaining the exact compile-time
Frame capacity assertion. Broad compiler passes199.861s/1277.5MiB. All three
unchanged original GOROOT cases `issue75764`, `issue5856`, `issue29504` pass
with matching source-after, combined peak3002.8MiB. Draft
[PR274](https://github.com/cpunion/llgo/pull/274) targets only `llvm-coro`;
CI38927305156 and final review are pending. Independent older GOROOT shard4
finishes135/135 with no failures/skips (peak7888.2MiB), and shard5 starts.

Production-phase review finds a separate integration omission: capability
discovery still assumes every lowered helper has an SSA target and rejects
typed managed-dispatch delivery helpers. Adding DiscoverCoroProgramPredicates
to the existing production recipe test reproduces the exact failure for
Dynamic, Certified and Variadic (0.623s/1262.7MiB), retained as the negative
cohort. The fix respects the existing invocation domain and source CallPlan;
it neither invents SSA targets nor enables all optional services. A known
resource family propagates initializer/drop service bits and predicate masks
through the original producer closure; open and closed nonresource families
do not acquire them merely from the helper domain. The selected full predicate,
capability and production-recipe race suite passes1.986s/1205.0MiB.

The first expanded fixture reused Plain across resource and nonresource phi
families; the current conservative representation union then retained the
nonresource guard, violating the original Closed pruning assertion. That failed
cohort is preserved. The new resource fixture uses a distinct Neutral leaf so
the existing Closed/nil-only assertions stay unchanged; no assertion or xfail
is relaxed. Separating representation equivalence from per-call exact target
precision remains an identified optimization, not a claimed completed fix.

Immutable v95 `6008bd7b6` verifies the capability follow-up: compiler
`5311c0c3d7eb31d360b2d9160c90c9ee5c1bd087a60458d922c8518be327cdfd`,
manifest `ee85f2bbf95bd6f17171f0798dd751d12b08c6060d0fa86aa4504b0e061fcbde`.
Build passes/706.8MiB; selected predicate/capability/production recipe race
passes1.915s/1210.9MiB; architecture0.833s/728.6MiB; broad compiler
144.480s/1243.0MiB. All60 linked resource modes and the three original
reflection/GC, cgo and channel routes pass with matching source-after
(reflection routes171.028s, combined peak2727.1MiB).

The new isolated fixture retains one exact static borrowed-result producer
and invokes an ordinary yielding callable after any/channel erasure, including
loop-defer result discard. Its SHA-256 is
`1214eb1badf8831fdd08e5dce5da8019766def517f231efea84af261a4ff38db`.
Exact v94 rejects it in production predicate discovery with
`lowered call "coro.delivery.drop:dynamic:0:5" in "exercise" has no target`
(683.8MiB). Exact v95 compiles it and passes all6 P=1/2 linked executions;
binary `1cd9d4ea3e68c4f0a16272670a1de0917ec33ea3f1ed06310da5773e96874ab4`,
peak1149.8MiB. Both cohorts match snapshot/fixture source-after. The fixture
and its subtest now join the existing opt-in ResourceDeliverySlot acceptance
gate, without another workflow or runtime protocol. Earlier exploratory typed
channel/fallback-body variants also pass on v94 and are retained as controls,
not falsely counted as causal negatives. The final private-intrinsic fixture
is LLGo-only and is not presented as an unchanged Go-control program.

PR274 completes all18 CI jobs at exact head `dd9295cfa` and merges at16:26:41
UTC as `e7b649074`, after fresh empty paginated review/comment inventories and
empty GraphQL threads. Receipt `root/ci-38927305156-complete.json`; only
`cpunion/llgo:llvm-coro` changes. The capability follow-up is draft
[PR275](https://github.com/cpunion/llgo/pull/275), head `17b394366`, run
38932391824, with empty initial paginated reviews/comments/threads. Incorporating
PR274 changes history only: tree remains `119790bdf05c413932794889ce615433765b7b89`.

Final v96 `0b08fdb94` also passes its broad compiler gate166.169s/1303.7MiB
and architecture0.713s/737.2MiB. The extended linked resource gate passes all60
FD executions plus6 erased-callable executions (66.068s), and the original
reflection/GC/cgo/channel routes all pass138.760s, source-after matches,
combined peak2847.9MiB. The fresh v96 complete test/go run remains running;
its binary is25932224bytes, SHA-256
`5eebfcc25db09970055d55bdaa5724b656019e12cddf4d15e3a6bc357799fb94`,
build peak8480.8MiB. This is80bytes larger than v92's same full test binary,
not a whole-program performance claim.

The complete unfiltered v92 test/go run finishes **271/271**, zero failures
or skips, at16:21:18 UTC with matching source-after16:21:21; all children and
private scratch are joined/reclaimed. Binary is
`7a46512471fe16bfe0baaddf79429cbe7b0e670e0e2513b8cb53c7b16f9766c8`.
Memory numbers are phase-specific: v92 build peak8346.6MiB, runtime phase
peak6797.1MiB. Likewise the earlier full v90 has build peak8764.8MiB and
runtime peak6824.2MiB; the latter is not the whole cohort's memory maximum.

Actual v92/v96 binary inspection identifies a pre-existing remaining cost:
DeferredPanicTraceActive and deferredPanicTraceScope both have coroutine
primaries; the v96 observer ramp requests504bytes. The new borrowed-storage
getter is inlined; linkAwaitChild is one plain18-instruction ARM64 helper and
allocates nothing. Only the owner-only readonly scope primitive now carries
the existing no-preempt contract, without suppressing callee suspend/unwind.
The v97 annotation-only experiment correctly fails physical validation on
original issue75764: analysis selects a synchronous outcome but physical cycle
validation does not yet consume its frozen no-preempt certificate. The original
failure/source-after is retained; v97 broad compiler132.283s/1289.3MiB and
analysis core race18.345s/1556.2MiB passing are not mistaken for actual success.

The replacement physical gate consumes the same exact-body certificate as
analysis, never a late comment, symbol name or missing NeedsPreempt bit. New
native/wasm32 tests reproduce the original rejection (0.486s/1227.7MiB), then
pass with the valid proof and reject absent/empty proof even while an unrelated
function remains certified. Ordinary preemptive-loop polls and bounded-cycle
tests stay unchanged. Final selected race passes1.679s/1183.8MiB. Actual binary
and unchanged panic/traceback gates remain pending for this experiment.

The complete unfiltered v96 test/go cohort now finishes **271/271**, zero
failures/skips, at16:59:15 UTC, source-after16:59:20, joined children and
reclaimed scratch. Build peak8480.8MiB and runtime peak6732.8MiB are separate
phase maxima. PR275 passes all18 CI jobs at exact head `17b394366`, fresh
paginated review/comment/thread inventories are empty, and it merges at17:09:07
UTC as `4e7bdc0dc`. Receipt is `root/ci-38932391824-complete.json`.

Immutable v98 source `4187b0d73` completes the observer proof-consumption
replacement: compiler
`2173ddb0d29b39247b5c0419a23761d558feb27818a1580fd258d888ac9a912b`,
manifest `afbde3c6e691a89e9eb32c81958ce4a183b81a40f7683c78d27525cf1166ff91`.
Build passes/735.2MiB; selected native/wasm32 certificate/poll/bounded-loop race
passes1.636s/1176.2MiB; unchanged architecture0.714s/720.5MiB; broad compiler
132.802s/1331.6MiB. All three unchanged GOROOT originals `issue75764`,
`issue5856`, `issue29504` pass with source-after matches, combined peak2891.5MiB.
An independently built unchanged issue75764 image also passes P=1/2 and retains
SHA-256 `53b6f88a1e056a8b1369dd2ab87d6e245299424c9a5020fa3e6764407d4c2361`.
Both observer primaries are now `$outcome`, with no coroutine/resume/destroy
entry, rather than the pre-existing504-byte coroutine observer ramp. A direct
disassembly of the actual outcome symbols is retained in
`root/trace-observer-v98-outcome-assembly.txt`; no coroutine-frame allocator
call is present. The wrapper uses128bytes of bounded native stack and preserves
cold panic-trace propagation; this is not a claim that all status overhead or
the entire runtime.Callers path is eliminated. The first audit asks for the
plain spelling and reports a missing-symbol warning; the separate outcome
disassembly supplies the actual instruction proof.

A separate field-snapshot experiment (`00b113ac7`, immutable v99) makes pointer
checks explicit and caches header/parent values without a nounwind annotation.
Its core race passes10.160s/408.2MiB and broad compiler169.129s/1257.2MiB;
the same original cases also pass. Binary inspection still selects the same
outcome ABI, with no additional demonstrated benefit. It is deliberately not
promoted: the branch/artifacts remain, and development switches back to the
verified no-preempt/proof-consumption branch instead of deleting or resetting
the experiment. Bodyless helper binding, resource publication and the other
remaining semantic gates are unchanged and still unfinished.

PR [#276](https://github.com/cpunion/llgo/pull/276) merges as `861541425`
at17:59:46 UTC after all18 jobs in CI38938054648 pass at exact head
`6a2d0c271`, with fresh empty paginated review/comment/thread inventories.
Immutable final-head v100 full test/go passes **271/271**, zero failures/skips,
completed17:56:25 UTC, matching source-after17:56:26, joined children and
reclaimed scratch. Its binary is
`1cabd4adcf1cb6ac1f4720464afadeb2a16619eb23034afcd6e58006affcd17c`
(25931536bytes); separate build/run peaks are8448.6/6567.0MiB. The same compiler
passes the60FD+6opaque linked resource runs and all three original reflection
routes, source-after matching, combined peak2740.3MiB. This is the latest
integrated full test/go evidence, not a full stdlib/GOROOT/platform pass.

The next local branch implements archive-only typed helpers as leaves in the
same Graph and plan digest v43; Functions remains source-only. Missing helpers,
source-identity conflicts, mixed invocation domains, invalid metadata and
builder overrides fail closed. Transitive capabilities and true/false predicate
requirements use the existing owner fixed point. Native/wasm32 physical guards
and typed helper declarations pass verification before/after CoroSplit, with
no fabricated SSA bodies. A real compiler-produced indexed archive is consumed
through importcfg; private helper declarations are absent from consumer SSA.
The native executable passes three repeated explicit-cancellation runs, including
normal result ownership, ignored defer results, one drop of resource99 and both
asynchronous drop steps. A strengthened run waits for cleanup before main exits
and also passes three times (29.260s/746.2MiB). Analysis/runtime-core race pass
23.345s/1663.8MiB and5.441s/127.0MiB. This local feature is not yet integrated
or accepted by a final immutable whole-suite run. The broad compiler regression
passes119.470s/1333.4MiB after restoring the original unrelated preflight
diagnostics. Both genuinely private helpers and existing bodyless helper
declarations pass native/wasm32 pre/post-split physical verification
(5.672s/1296.6MiB); the latter uses the same exact imported-primary capability
matrix and ordinary static helper path. No architecture budget, semantic
assertion or instruction golden is relaxed.

Immutable v101's exact-source archive gate passes29.509s/740.5MiB,
full compiler regression131.451s/1205.5MiB,
analysis race20.749s/1505.6MiB and unchanged architecture/build gates
0.668s/347.3MiB. Its fresh complete test/go run remains active. The native linked
CI lane now names the real archive execution test explicitly; the earlier
18-job workflow did not include that new E2E fixture in its enumerated list.

The first real archive probe exposes an export-data declaration with no SSA
parameter values: defer operand validation now consumes its complete typed
signature, and physical ABI context detection no longer mistakes absent body
parameters for a closure environment. A three-argument real imported declaration
regression passes. The native test island also adds the actual production
cleanup-shield bridge, rather than a stub. Earlier exploratory archive runs
r3–r7 incorrectly assumed main return would explicitly cancel a live compute
goroutine; their failure logs are retained, not classified as compiler negatives.
The corrected gate requests cancellation while the program is running and checks
the exact canceled resource identity. Descriptor publication, external defer
owners, output-loan lifetime and reflection resource handoff remain unfinished.

The following isolated branch closes the source output-loan proof boundary,
not descriptor publication. It reuses the existing interprocedural borrowed
allocation/parameter analyzer for the borrowed result value and both helper
output parameters, after consuming the already frozen call-site lifetime
contracts. The physical preflight checks all three exact identities; guard ABI
v2 participates in the existing source/library hash. There is no new runtime
storage, allocation, check or directive. Corrected native/wasm32 escape fixtures
fail18/18 against immutable v101 (0.594s/1262.9MiB), then pass after the proof;
the initial closure fixture instead violated the earlier exact return-projection
gate and is not evidence of a new lifetime bug. Escaping initializer/drop
parameters separately reproduce the missing check. A real indexed archive
still passes three cancellation/cleanup executions (25.357s/722.7MiB), and the
shared value/parameter/allocation proof race group passes1.418s/346.8MiB.
The invalid bodyful-C/declaration fixture is rejected by the existing contract
shape gate, not treated as a causal lifetime failure. Immutable v102 then passes
the full compiler regression146.628s/1264.5MiB and three real archive executions
32.547s/767.8MiB. The original60FD+6opaque resource runs and all three reflection
routes also pass, combined peak2815.6MiB and source-after matching. Final whole
test/go/stdlib/platform acceptance of this follow-up remains pending.

The next isolated experiment publishes guarded context-free resource producer
descriptors from the same source/archive recipe. Ordinary descriptors keep the
nine-field base and funcval stays two words; resource-only cold tails reference
the existing typed initializer/drop descriptors. The emitter carries the exact
descriptor type beside the function value instead of recovering it from an
erased code word. Real export-data declarations additionally require the exact
checked library identity and structural ABI; their synthetic marker alone is
never authority. Captured/receiver adapters, raw/plain resource publication,
external defer owners and reflection/FFI second custody handoff remain closed.

An independent API-only probe against immutable v102 reproduces the original
publication rejection (0.519s/1193.8MiB), then passes on the new implementation
(1.136s/1218.7MiB); earlier probes with an opaque-call fixture or renamed test
helper fail for unrelated reasons and are not causal evidence. Native/wasm32
publication and original missing-guard gates pass1.690s/1239.4MiB. Real static
and dynamic indexed archives pass six executable runs together
(64.855s/1250.7MiB); the dynamic child must have a managed Dispatch/ResultDelivery
CallPlan and an actual capability branch before cancellation, not a silently
devirtualized call. Each executable checks one cleanup of resource99, both
async drop steps and no canceled user continuation. Unchanged architecture
passes0.704s/551.6MiB; broad compiler regression145.278s/1290.7MiB. This experiment is not yet integrated or whole-suite
accepted.

The next small follow-up removes the need for a library to take its producer's
address first: a consumer can promote DirectPlain/DirectCoro to Dispatch around
the exact same compatible published primary. ABI/effect/entry/symbol checks,
argument/result schemas and raw/opaque rejection remain unchanged; no producer
fact is mutated, no reverse downgrade or extra body is allowed, and no-demand
values emit no symbol. The real archive fixture now has no Published seed.
It reproduces the old representation mismatch (0.549s/590.3MiB), then passes
three dynamic cleanup runs. Combined direct/dynamic execution passes six times
(73.778s/1198.7MiB), broad compiler149.634s/1279.5MiB and selected race
37.978s/1179.2MiB. This replaces the artificially pre-published fixture rather
than weakening its dispatch/custody assertions.

Immutable v101 full test/go completes **271/271**, zero failures/skips, at
19:16:36 UTC; source-after19:16:39 matches, all children join and scratch is
reclaimed. Retained binary SHA-256 is
`652ab791224c41cdeee7e41c741048143fe589d360f0633b4d8195b7ad8f68cb`
(25931536bytes); build/run peaks8325.8/6552.8MiB are separate maxima. This is
the archive-consumption candidate, not final acceptance of the later lifetime
or descriptor changes. Immutable v103 passes broad compiler152.903s/1229.9MiB,
two real archives63.470s/1204.7MiB, selected race53.429s+1.411s/1341.4MiB,
and original60FD+6opaque plus three reflection routes/source-after matching
with peak2752.4MiB. Its73-demo cohort is still running. Current CI/review gates
for PR277/278/279 remain open; no previous-head CI is substituted.

The first PR270 CI head fails only the new host SSA fixture's missing `!llgo`
test-domain constraint; `1d139f475` supplies the same constraint as its helper
tests. The llgo-tag gate passes 0.433s and the full host race suite 22.317s;
production source is unchanged. Run `38906611100` replaces the superseded run,
whose known failure is retained. This isolated panic fix is rebased on that
corrected test-only head, not on main.

GOROOT shard 1 also reports `fixedbugs/issue27695b.go` in the reflection/GC
path. A strict unchanged-case v79 rerun still fails with `invalid zero-ticket
coroutine run decision` (228.29s case; 2086.2MiB peak, source-after matched).
At that snapshot this is a real implementation gap, not an ABI exclusion or a
new xfail; subsequent v82/v84 passes and the causal negative are recorded above.

The v58 expvar retry now compiles and executes all four tests successfully.
It retains source `d5d6c5627`, source-manifest SHA-256
`4e19f44822bab94594cd031dccfa17ff0d8d9932a909d98ea6f692d1ccf11fe3`
and compiler SHA-256
`7e23b89ae2f218b4aaf3a2406fe76c3cb3427132b6713f005e20fb1dc8459ddc`.
The build uses a 24GiB hard cap and 8GiB Go soft limit, peaks at 15521.4MiB,
and produces binary SHA-256
`0a42885d908653bcaae5715f88590fdab7243b6b664124ace2254dd3079d95c5`.
The cohort finishes with its source unchanged and private scratch reclaimed.
The earlier 16GiB failure remains a separate resource-limited attempt.

A follow-up structural-key correction removes source-package qualification
from exported struct fields and interface methods only, matching Go identity.
Private members, named types and field tags remain distinct. It also makes
compiler-generated delivery result tuples agree with their source-expressible
anonymous aliases; it does not enable physical resource guards. Native64 and
wasm32 interface IR passes verification before/after CoroSplit, visibility and
delivery-key gates pass, and the broad compiler selection passes (290.167s).
Restoring only the old key builder fails the new positive cases while retaining
the private/tag/named negative distinctions. The new executable `test/go`
regression covers interface calls, bound method values and reflection with an
actual channel suspension and a by-value aggregate. Its Go control passes all
three subtests. Immutable v59 now also compiles and executes all three through
LLGo, with source-manifest SHA-256
`bc21bb7e11a3ce7c826c6b9282f71740987d6f6dcedb5c3222f77c08c782d486`
and compiler SHA-256
`21b1ba92c1668b659c31cb8a7bc818dd8d221105a4c78a586e0a8a90c12ac034`.
The fresh 267-test whole-package run was interrupted by three consecutive
transient scratch-scan ENOENT races, not a semantic test failure. Its partial
successes are not a completed suite result. The separate r7 acceptance runner
retries only this exact transient diagnostic, under one unchanged five-second
deadline; 11 fault-injection tests retain fail-closed behavior for exhausted
retries, missing roots, permissions, mixed errors and incomplete totals.

The next compiler correction closes another real rejection: a closed dynamic
call with only plain targets used to manufacture an unreachable coroutine
branch, despite its correctly non-awaiting effect. Publication now selects one
existing ManagedEntryKind; one shared emitter handles ordinary function,
interface and deferred descriptor calls. This replaces mutually exclusive
boolean facts instead of adding another dispatch engine. Uniform plain
families also close the existing static-outcome graph and need no caller
coroutine frame; mixed/open families and coroutine descriptors with optional
outcome twins retain their original paths. Nil faults still enter the same
structured cleanup, and results are claimed once. Native64/wasm32 nullable,
aggregate, interface and deferred-call IR gates pass (eight cases, 4.492s),
as do the full internal/coro race/shuffle gate (25.232s) and monotonic
architecture gate (0.997s). Restoring only the preceding inference rejects all
eight new frame-free assertions; the deferred plain cases remain passing.
The original native/wasm32 fixture separately reproduces the old invented-await
rejection. Broad cl selection passes (163.256s / 1237.0MiB), SSA selection
passes (0.589s) and broad host build selection passes (576.659s / 8096.3MiB).
The latter host selection spans the working-tree follow-up; it is not an
immutable whole-source executable gate. Immutable v60 freezes compiler source
`3b9571606`, source-manifest SHA-256
`9089511effe3e59af0ad6f2313ab031dd8eb1f618cb19476721c680af0915ebc`
and compiler SHA-256
`ea0af20049350841d443d90a3787e03f035e1396e9d746ee2fa4f24de0b224ab`.
The new aggregate/nil/deferred-callback regression and the cross-package
interface/method-value/reflection regression pass 100 executions each (200
top-level passes), under a 4GiB tree cap; sampled peak is 24.1MiB. Source and
binary SHA-256 match before/after; binary SHA-256 is
`888387e61c3ea7ab684e6ee55e036543fed04affa12fdffac711a879016ff5c1`.
The complete v60 package, including nested compiler probes, passed all 268
top-level tests with zero failures/skips at 06:30 UTC. Run peak was 6439.4MiB;
all children joined, private scratch was reclaimed, and source-after matched.
This is a whole-package result, not an aggregation of the repeated selections.
The newer v64 compiler/runtime's separate whole-package run completed with
266 passes and two caller-PC regressions, detailed below. It does not inherit
the v60 pass.

A separate runtime follow-up replaces the unbound uint64/empty
runtime/debug.readGCStats stub with the signed-duration buffer protocol and
the existing collector's ReadMemStats snapshot. It reports real cycle counts
and available pause data only; collectors without pause timestamps retain an
empty history rather than invented zero-duration pauses. The consistency and
stale-quantile Go control passes ten repetitions. Runtime source v61 is frozen
as `80755b9f9`, manifest SHA-256
`56904406135fd8bd24f88fc5994f692e869328d158f1c96bc8cb8d6b7e88db17`.
It reuses the unchanged v60 compiler with explicit LLGO_ROOT. The initial v61
actual package run passed six tests but exposed stale cross-goroutine caller
PC identities in PrintStack. After the independent caller-PC correction below,
v63 passes all seven actual runtime/debug tests, with source-after matched
and private scratch reclaimed. This does not implement unavailable collector
pause telemetry or the other debug setting/heap-dump stubs, and is not a full
runtime/debug semantic compatibility claim.

Logical caller PCs now identify immutable process-wide frame metadata instead
of G-local sequence numbers. The previous IDs aliased in process-wide symbol
caches and could not be decoded by another G. The fix removes each G's
synthetic-frame table, uses one 64-bucket atomic append-only metadata registry,
and retains O(1) live-frame PC memoization. Production-extracted host race
tests cover concurrent publication, deduplication, immutable old PCs, collisions,
invalid tags and namespace exhaustion (2.613s / 125.4MiB). The same real LLGo
cross-G program fails on v62, then passes 100 native executions on v63
(6.8MiB sampled peak); source hashes match after execution.

The existing logical-stack instrumentation is also enabled for default and
named wasm targets; it does not require native frame pointers or PC-line asm.
Explicit metadata-free mode and named non-wasm target boundaries are retained.
Focused compiler gates pass (17.547s / 1258.9MiB). With v64 compiler and v65
source manifest
`780722bce8895d5d7a8eee00284ecddd3c45bdbae74eb01b8871f3c483b0f72b`,
the actual Node/wasm cross-G program passes all identity/file/line assertions
and writes the exact completion marker through standard os.Stdout. The old
compiler's wasm execution panics. This is not baremetal caller-stack or wasm
dynamic-reflection acceptance. The native CI fixture and JS CI lane now require
this observable marker. The build integration oracle accepts both inferred
outcome and coroutine helper entries while retaining exact token allocation,
zeroing, propagation and update checks; its previous outcome-only match caused
PR #264's initial single CI failure, and the corrected focused gate passes.

The complete v64 `test/go` run exposed two additional regressions:
`TestRuntimeLineInfoAndStack` and `TestRuntimeStatementLineInfo`. The registry
incorrectly rejected tag 2, the legal `Callers` return-PC minus one used by
panic-spliced Caller and FuncForPC. It now accepts all three nonzero marker
forms while rejecting aligned/non-synthetic markers. The production-extracted
race test reproduces this rejection before the fix and passes after it;
the permanent linked fixture now covers adjusted PCs and recovered panic frames.
Immutable v67 source manifest is
`917b11b1bafa42d4c8711c8981ec0c2d40cb4c7f33fd83d384504e31deedafcf`.
Using the unchanged v64 compiler, the fixture passes 100 native executions and
actual Node/wasm execution (2126.7MiB combined build/run peak). Both original
failing test programs also pass unchanged (116.77s and 84.46s, 2989.1MiB tree
peak), with source-after matched. The retained v64 whole-package failure is not
rewritten as a pass. The fresh post-fix complete v67 run now passes all 268
top-level tests, as recorded at the current checkpoint above.

The v48 GOROOT rerun reached case 213/1082 before its 8GiB private-scratch cap
stopped it (sampled tree peak 17313MiB). This is a resource interruption, not
a completed suite; no new xfail is added. Its original noinit private-gc-ABI
failure and prior full-suite evidence remain recorded separately.

PR [cpunion/llgo#261](https://github.com/cpunion/llgo/pull/261) is now merged as
`d292d73d5`, after head `33887a5bd` passed all 18 coroutine CI jobs in run
`38849290434`; review, inline and issue-comment inventories were empty. It
closes the maphash/Go-types, Darwin x509 and frozen panic-phi gaps below. This
remains a development checkpoint, not full-objective completion.

PR [cpunion/llgo#260](https://github.com/cpunion/llgo/pull/260) was merged into
`llvm-coro` as `941e17146` after head `37b115dbc` completed all 18 coroutine CI
jobs in run `38844866845`. Review, inline-comment and issue-comment inventories
were empty. This is a verified development checkpoint, not completion of the
full objective. It includes the existing integration merge, dynamic-result and
cleanup corrections, typed delivery source facts, static embed images and the
allocation-query fast filter; no newer main was merged and cpunion/main was not
changed. Physical resource-delivery guards still fail closed. New stdlib
compatibility changes below require their own affected gates and follow-up CI;
the preceding green run is not attributed to those changes.
The destination is `cpunion/llgo:llvm-coro`. The acceptance environment is
Darwin/arm64, Go 1.27 and LLVM 22. No new upstream-main synchronization is
required for this work.

The current phase prioritizes executable `test/` and `_demo` coverage.
`cl/_test*/**` instruction goldens are temporarily outside the acceptance
gate. Compiler unit tests that check semantics, ABI, frame layout and redundant
code generation remain applicable.

The previous run was interrupted, and its `/tmp` compilers, source snapshots
and logs are no longer present. Historical completed results below remain
dated evidence, not a fresh whole-tree pass. The unfinished v17 suite cannot
be marked complete. Current results are being retained under
`~/.cache/llgo-coro-acceptance/20261015/`. Each numbered compiler has an
independent source snapshot; active snapshots and runner scripts are not edited.
The stable v25 compiler SHA-256 is
`478022dd03e2348a6906252de84b26760b9b490183a653f2ba4cb583032f48b4`.
The v30-fs1 compiler adds the internal/syscall/unix source-overlay registration;
its SHA-256 is
`8c6accd1165551f2c98e03ba8e17e4fcdfe51be5d1000f4bb23e5d030872cd2d`.
The integrated v34 compiler includes the ProgramIR authority consolidation;
its SHA-256 is
`7e53b50acd57d08ef860b307e2cf5d2da1d0f0f72b2cd98a0a198efd3969f5f9`.
The integrated v35 compiler adds private native-object workspaces, the frozen
unevaluated-reader certificate correction, the freestanding FFI capability
guard and shared WASI buffer operations. Its SHA-256 is
`26a11570c7b8e691925b4cd797e5258cd7e0c244484f9d86a49d36c882434fb0`.
Fresh `test/go`, core stdlib and GOROOT runs use its immutable snapshot.
The v36 compiler fixes physical-parameter validation to consume the exact
frontend replacement type while retaining the original SSA identity proof.
Its SHA-256 is
`662ed86ee6071a0754e49dbf10d46e0ea87899c451676e93ecf99a5049bd75c9`.
The associated snapshot also contains the Darwin clock fast-path correction
and the JavaScript filesystem adapter. Their acceptance is recorded separately;
ongoing v35 suites do not switch compilers or sources.
The v37 compiler integrates the frame-initialization, reflection-header
provenance and Go 1.27 generic-method fixes. Its fixed source snapshot also
contains the small baremetal function-PC cache. SHA-256:
`45b9f368a4ed86143f850d7af21b715465c9b7eed53927b70dff4ed4e6c2b60f`.
The new optional resource-custody experiments are deliberately excluded from
this checkpoint. A 180-package executable `test/std/...` evaluation used v37
in two fixed shards. It was stopped for disk safety after 22 package passes,
two compiler failures, three compile timeouts and two externally interrupted
compilations; 151 packages were not started. Both interrupted-shard source
fingerprints still match. This is not a completed suite. The two compiler
failures (archive/tar and archive/zip) pass in later focused cohorts below.
The completed earlier `test/go` gate retains its fixed v22 compiler and snapshot
(`de085c2c260dd21ed17694ad5346b4648a9830e3a9efc2556444cdf784ac9a70`);
its nested compiler probes do not switch versions mid-suite.

The integrated v38 compiler SHA-256 is
`1a57443e0bdf377d72c26b31ee5d69ff13e46ac03eade4d35c44a04fa500600a`.
Its 4,775-file source manifest is unchanged before and after the integrated
gates. It includes the final critical-region/conditional-edge proofs, repeated
allocation identity, logical caller-stack exhaustion, and the Node borrowed
output and host-task fairness changes. The later explicit-method-receiver
x/tools fix is tested in a separate v38-derived snapshot, not silently added
to v38. Three test-only expectation/coverage updates were also evaluated with
a frozen Go overlay, preserving the original v38 test failures as evidence.

The integrated v42 compiler SHA-256 is
`51d4f1683ab055c61a920a95cbb0535268365b20a2bb9873abe338dabd0554e0`.
It adds the reviewed generic-instance/descriptor corrections, one semaphore
prepare ABI for ordinary and durable waits, correct main-panic reporting with
live peers, and production semantic method-slot DCE for closed full-LTO
executables only. It also pins the minimal x/tools range-function `goto` fix
to `github.com/cpunion/tools v0.48.1-0.20261015201615-15d3318a86e1`.
The full `test/go` rerun passed all 266 top-level tests, with no failures or
skips, using this immutable source/compiler pair. Nested
compiler tests use a fingerprinted forwarding launcher that isolates and
reclaims only their own cache after subprocesses join; it does not change
test arguments or compiler output. The 180-package stdlib rerun is separately
controlled by disk limits. Allocation-sinking and result-slot GC experiments
are not in v42.

The integrated v43 compiler SHA-256 is
`a8abcbdd054924dc8a0b4d739fc9f8af6aa3c48fed7d833c13821148d08abfa8`.
It combines the shared raw-lowered-helper capability with frozen unpublished
pointer-cell allocation placement and consumed scalar pointer-result clearing.
Its new executable GC regression exposed a static-outcome admission mismatch
for a finalizer closure containing `close(chan)`: physical validation rejects
the unsupported channel-close recipe. This is an integration failure, not a
passing v43 runtime cohort; the compiler and logs remain unchanged for diagnosis.
The independent GC-only cohort passes the original `chans` test and both small
live-caller probes ten times each, existing reflection GC, borrowed-FD cleanup
and ordinary/fake/standard timer gates, and three MakeFunc startup/GC runs.

The follow-up v44 compiler SHA-256 is
`3781944ab833d8b22893296e2b84365a89dddf4b1652e29d8d53fc47ddb17f65`.
The single source-semantic classifier now keeps `close` non-suspending while
rejecting outcome-only lowering until that task-bound physical recipe exists.
Seventeen builtin/operation capability cases and the real finalizer-closure
production-plan regression check that distinction; the latter reproduces v43's
failure before the fix. Integrated v44 compiler/IR gates now pass; the permanent
live-caller GC regression passes ten executions and unchanged original `chans`
passes five. The extended GC/resource/timer gates also pass, with identical
source manifests before/after. Node borrowed-resource cleanup and all three
WASI standard/fake-time timer GC programs also pass on v44, followed by the
host-protocol tests; the combined linked tests took 197.040s / 1440.6 MiB,
including compilation. Source manifests match before/after.
The fresh demo cohort is incomplete;
its first attempt stopped on a transient scratch-scan race, and its second on
`goimporter-1389` exceeding the 8GiB compiler cap. Those infrastructure/resource
interruptions are preserved separately from semantic results. The third attempt
uses 12GiB only for that importer and retains 8GiB for other compiler invocations
and 2GiB for programs. It passed five demos before the 10GiB disk floor stopped
the cgo build. The independent stdlib rerun has 20/180 completed package passes;
the remaining-package cohort stopped at the same disk floor while compiling
crypto/ecdsa. Both runners joined their children, reclaimed private scratch and
verified source fingerprints. They must resume with the same inputs after space
recovery; neither interruption is a semantic failure or a complete suite pass.

A subsequent source review found a v44 gap not covered by those passes:
descriptor coroutine calls discarded the value returned by the child-await
helper, then loaded the same result slot again. The first load now retires
scalar pointer fields, so the second can read nil. The follow-up separates
completion reconciliation from result loading; every dynamic branch converges
on one load/retirement. The correction is committed as `d93ff685c`. Fixed v45
compiler SHA-256 is
`50ed9b374add317f01f766b9ac2b59706fc98d0f134bbcaef20c4a48de5880c7`.
Its 12 native64/wasm32 IR cases pass, while adding only those tests to unchanged
v44 fails all 12 with duplicate pointer loads. The real function-value,
interface, closure and GC program passes 10/10 v45 executions (P=1 and P=2
internally) and the Go control passes; unchanged v44 compiles it and then
panics on the cleared result. Source manifests match before/after. The original
disk-stop and interrupted outer scan attempts are retained separately; the
successful bounded reruns exit zero. This is focused evidence, not a full v45
compatibility rerun.

Commit `17bbfe58d` then consolidates coroutine/outcome/descriptor result tuple
construction. Exported `R0`, `R1`, ... fields let an internal Go helper declare
an identical anonymous struct alias, preserving strict named-type identity
without a pointer-layout reinterpretation mechanism. Parsed-source alias tests
and selected compiler/Coro/Emission/SSA gates pass (279.126s / 1254.8 MiB).
The typed delivery-recipe/selection and real-FD boundary/value tests also pass
as host tests; FD tests run with race detection (2.366s / 397.2 MiB). Physical
static/dynamic cleanup, reflection, archives and public API adapters remain
unimplemented. Those type/model tests do not close public resource delivery.

The resource-delivery development branch extends the existing archive summary
to v17, without adding a second metadata section. Optional producer records
resolve initializer/drop identities and exact ABI hashes through the shared
cross-package index; malformed, missing and target-width-incompatible records
fail before lowering. Ordinary functions omit the record. Framed record
round-trip, cross-package lookup, immutable value and negative tests pass with
race detection (1.516s / 376.3 MiB). This validates metadata transport/index
consumption only: compiler publication, typed physical helper binding and actual
source-unavailable archive execution are still pending.
The complete `internal/coro` race/shuffle suite passes on this branch
(95.035s / 1660.3 MiB). Compiler import/publication also explicitly rejects a
nonzero delivery record until its physical cleanup recipe is bound; the real
imported-entry/descriptor LLVM gate passes with a new unbound-record negative
case (0.573s / 1231.3 MiB including host compilation). Metadata is not silently
dropped into ordinary resource-result lowering.

The `02606d89a` CI run 38820862342 exposed three acceptance-wiring problems:
both WASM target lanes selected the basic network host runner for the stdlib
TCP fixture; target-core compiled an incomplete host-Go runtime configuration;
the Linux clock test required a synchronous primary although the exact-static
outcome twin was already available. The follow-up selects the matching TCP
runner, uses production-tagged cross-compilation (six targets passed locally,
60.016s), and inspects the actual synchronous clock entry (39.632s / 1023.5 MiB
locally). No production runtime shim or fallback is added. The downloaded CI
WASM TCP artifact completes with 593 operations, 16 cancellations and 17 alarms,
not the obsolete 594/17/18 totals. Acceptance now requires physical cancellation
of all six read/write/accept/connect/recvfrom/recvmsg families, both DNS query
types, every existing Go result assertion and zero pending operations/listeners/
datagrams/descriptors. A fresh CI run remains required.

Fresh immutable v46 source/compiler acceptance passed the first demo, then
stopped on a missing `/usr/sbin` in the local runner PATH during handle-checked
cleanup. The parallel full-test/go build stopped at the 10GiB disk floor before
producing a completed test result. Both inactive process domains were inspected
with absolute-path lsof; their marked scratch and lock receipts were preserved
under `scratch-interrupted-lsof` / `interrupted-lsof.lock`. Adding `/usr/sbin`
does not change source/compiler inputs, but disk admission still prevents a
fresh full-suite run. These are infrastructure stops, not new suite passes.

After disk recovery the same v46 cohorts resumed with the repaired PATH. Full
test/go compiles successfully (7633.7 MiB sampled process-tree peak). At
00:13 UTC all three cohorts stopped again at the unchanged 10GiB disk floor:
32/73 demos and 6/180 stdlib packages passed; test/go recorded 233 top-level
passes and no semantic failure before interruption. None is a complete v46
suite pass. Demo/stdlib runners joined their children, reclaimed private scratch
and verified their source fingerprints. The full-test outer interruption left
a nested join marker: the marked PIDs and all snapshot/compiler processes were
subsequently absent, absolute-path lsof found no scratch handles, and the source
and retained test-binary hashes still matched. Scratch and the lock were moved
to `scratch-interrupted-disk-0013` / `interrupted-disk-0013.lock`, not deleted or
misrepresented as an automatic join receipt. Resuming that package must rerun
the whole test binary; its 233 partial passes are not reusable package acceptance.
CI head `bb860510d` passes the earlier target/clock/TCP
corrections. Two later workflow gates exposed further host-test wiring:
WASI reactor discovery used the obsolete `.llgo/clibs_build` location instead
of the current XDG crosscompile cache, and a host-SSA test lacked its matching
`!llgo` build constraint. The corrected actual C reactor lifecycle test passes
(0.648s / 91.6 MiB); the llgo-tag package test passes (0.424s / 265.3 MiB), as
do analyzer/build vet and production-tagged runtime vet. No runtime fallback or
semantic test assertion is removed by these corrections.

Resource-cleanup review also removes the completion dispatcher's assumption
that every compiler-owned cleanup has a source SSA Recover block. The LLVM
regression and existing static-cleanup/parent-drain gates pass (1.976s /
1245.4 MiB). This enables a no-source-defer drainer but does not yet insert a
resource guard. Two required integration gates are explicit in the core
contract: internal drops cannot recover a parent's panic, and discarded-result
entry ownership must remain protected until actual terminal Return rather than
an early source-level commit followed by a compiler preemption poll.

The shared static/descriptor await path now carries explicit emission-only
cleanup ownership and parent-recover permission separately. Generated helpers
for deferred builtins use the former without the latter: they pass no parent
panic payload, publish no recover alias, and reject ReturnRecovered, while
retaining strong join and the existing cleanup panic/cancellation reconciliation.
Native/wasm32 pre/post-CoroSplit deferred-print gates pass alongside source
defer/recover, dynamic cleanup and parent-drain regressions (2.857s / 1221.5 MiB).
There are no new runtime/frame fields. Actual resource guard insertion and the
discarded-result entry guard are still pending.
All selected `TestCoro*` / `TestEmission*` compiler tests then pass (93.136s /
1263.6 MiB), followed by the monotonic architecture gate (0.602s / 686.6 MiB).
The final targeted positive run passes (1.341s / 408.3 MiB). An isolated Go
overlay changing only the internal helper back to source-defer permission
fails both native/wasm32 cases at the actual handoff's leaked panic authority
(0.477s / 1228.6 MiB); production sources are not reverted. Earlier attempts
to add a source panic hit missing runtime-type helpers in the small test
fixture and are retained as setup failures, not the negative control.

Fixed v47 source is commit `73095ebc0`; compiler SHA-256 is
`06f2532dfa11ae2b16003b5368e8704fd120d079167da891ffd4e8d47af59092`.
The source manifest SHA-256 is
`517b7f580a9c21f7698ae404835efe8a193274f34b15beca45a9c9c4ea5a6346`,
and matches after executable acceptance. The real `TestBuiltinPrintOutputMatchesGo`
passes (31.352s / 868.1 MiB including host test and child compilers). The native
borrowed-FD fixture passes ten executions and its Node counterpart passes three
(48.299s / 1247.7 MiB including both builds). Each task uses a bounded private
Go cache reclaimed on completion. This validates the changed cleanup path,
not the unimplemented public acquisition-return guard.

Initializer review retains ordinary automatic coloring. A proposed plain-only
initializer gate was tested locally but not retained: a typed pointer store may
carry a nil-fault effect, and denying that effect would require unnecessary
annotations. Zero result storage before initialization, finish initialization
before arming the resource guard, and do not start acquisition until afterward.
Existing plain/outcome/coro completion owns initialization failure/cancellation;
no additional partial-initialization resource state is needed.

The final v46 demo attempt retained 47/73 passes before the shared filesystem
again reached the 10GiB floor while building `reflectfunc`. It reclaimed its
private scratch and verified source hashes. The six stdlib package passes and
233 partial test/go passes remain unchanged; all three full local cohorts are
currently stopped, not running. Sufficient stable free disk is still needed
to finish these gates. Completed v47 focused tests have no remaining compiler
processes and do not replace the incomplete full cohorts.

At 00:48 UTC the shared filesystem recovered over 190GiB free. All three v46
cohorts resumed with identical fingerprinted inputs and unchanged limits.
Full test/go rebuilds and reruns the entire package, rather than adopting the
233 interrupted test results; demos/stdlib reuse only completed package passes.
The preserved full-test scratch/lock and its manual inspection receipt remain.

The resumed v46 demo cohort completed at 01:14 UTC: **73/73 passed**, zero
semantic failures, scratch reclaimed and source hashes unchanged. The v47
four-package executable gate also completed: `test` 41 passed / 1 helper skipped,
`test/cgo` 3 passed, `test/syncpool` 1 passed, and `test/llgoext` 23 passed
(4845.7 MiB sampled process-tree peak). The source manifest still matches.
Full v46 test/go completed at 01:27 UTC: **266/266 passed**, zero failures or
skips, run peak 6454.0 MiB. The retained binary SHA-256 is
`789b4c4becb69976fbfdba6f9ac88144a2dfba3994a41b8380afb31040339db9`;
all children joined, private scratch was reclaimed and source hashes match.
After 15/180
stdlib package passes, the sequential runner was deliberately terminated and
joined to repartition the other 165 packages into four disjoint lanes. Its
interrupted AES compilation is retained as an infrastructure interruption, not
a semantic failure. All four lanes keep the same source/compiler and per-stage
limits; their aggregate compile bound is 48GiB, plus 12GiB for full test/go.

The typed borrowed-result source frontend now freezes exact initializer/drop
identities and canonical result types in the existing ProgramIR. Its compiler-
only function operands reuse the static-reference analysis capability instead
of manufacturing runtime funcvals or descriptor demand; ordinary publication
of the same function still retains managed dynamic-call semantics. Invalid
shapes and a missing physical guard fail closed. This is source planning only:
it does not expose an unprotected output pointer or enable a public acquisition
adapter. All selected compiler `TestCoro*` / `TestEmission*` regressions pass
(91.422s / 1162.0 MiB), the complete core race/shuffle suite passes
(17.733s / 1648.4 MiB), and the architecture/selected build gates pass
(0.663s / 698.6 MiB). No architecture budget was relaxed.
Hidden initializer/drop calls are now frozen as ordinary lowered managed calls
in that same graph, even without explicit source calls. Independent initializer-
only and drop-only suspension/executor seeds both color producer and caller
automatically without requiring Dispatch (1.405s / 1239.7 MiB). The architecture
and affected build gates still pass (0.776s / 324.5 MiB). The broad build-driver
selection first hit its 4GiB process-tree bound (4098.8 MiB). Restarting that
whole selection at 8GiB passes (790.155s / 7888.6 MiB), including the enabled
native scheduler/cleanup E2E and production code-generation probes; opt-in
platform/resource acceptance tests remain explicitly skipped. The first run's
resource diagnostic is retained, not relabeled as a semantic failure.

The shared lowered-helper resolver also had a cleanup-specific optimization
gap: selecting a synchronous outcome entry lost the active drainer and reset
its panic/nil-fault continuation as if executing ordinary source code. The
outcome transaction now carries that existing owner into its existing cleanup-
aware completion dispatcher. No new runtime/frame fields or cancellation
protocol are added. Native/wasm32 LLVM verification and exact continuation-
storage checks pass before and after CoroSplit (0.592s / 1230.4 MiB). Restoring
only the old resolver with a Go overlay fails both targets at the intended
panic/nil-fault base reset; this is the final negative control. Earlier attempts
with a symbol mismatch or source-line/continuation-value collision are retained
as fixture-check failures, not extra runtime regressions. All selected compiler
`TestCoro*` / `TestEmission*` tests pass (150.552s / 388.1 MiB), followed by the
architecture gate (0.798s). Vet passes with the existing CI policy: full vet for
core/build and `-unsafeptr=false` for cl's intentional unsafe-pointer fixtures.
An initial unrestricted cl vet reports the unchanged builtin fixture at line
569; no source assertion or CI policy is changed to suppress a new warning.

Fixed v48 source is `09db805cd`, manifest SHA-256
`de7de50fb475e9baae13ed16499cf951dc10225338250a819a269aecf7d5d344`;
compiler SHA-256 is
`bbb7ca35ee684a82c437142087d6cc8887b585246e84ae0c0a8e2404fae22a5e`.
Real defer-print output passes against Go (53.216s / 804.5 MiB); native borrowed
FD cleanup passes ten executions and Node three (72.765s / 1298.7 MiB including
builds). Source and compiler hashes still match. The four-package basic rerun
completes `test`, `test/cgo` and `test/syncpool`, but is stopped by repeated `ps`
sampling timeouts while compiling `test/llgoext` (tree peak 5558.5 MiB). This is
not a complete four-package v48 gate; the v47 complete result is retained.

The v46 stdlib cohorts have 35 distinct package passes at this observation
(15 original plus 20 disjoint remaining-package passes). `crypto/ecdh`,
`crypto/ecdsa` and `crypto/elliptic` reach the 25-minute compilation bound;
`crypto/x509` fails semantic preflight at its unsupported floating-point syscall
bridge. These are different failure classes. After load rose to 44.57 and `ps`
sampling repeatedly timed out, remaining-package lanes 2 and 3 were terminated
and joined; their scratch was reclaimed and source hashes match. Lanes 0/1
continue. Interrupted hex/hpke compilations and every prior receipt remain;
no complete package pass is discarded and no resource guard is disabled.

The v48 full 1082-case GOROOT selection has also started, with 24GiB per-case
and 28GiB outer RSS caps, a 25-minute build timeout, unchanged CI case selection
and unchanged xfail/not-applicable files. The first attempt's PATH omitted
`/usr/sbin`, so its memory guard refused `sysctl` before case execution. The
second attempt appends the system directories and restarts the whole selection;
it is running and is not a fresh full-GOROOT success claim.

The Darwin x509 development branch uses an exact five-word-plus-float C
function type at the existing low-level bridge. It reuses typed foreign-worker
record lowering, rather than extending word-call.v1 or pretending a float is a
uintptr. The parsed production-source LLVM gate verifies park/resume and the
actual double C operand; selected typed/raw-worker regressions pass
(2.533s / 1251.2 MiB). A separate immutable v49 source (`881ab7c0e`, manifest
`9fc3ac4ffe1c234cc207136b385f15db6e85e247f70a63c9676dce596a4a640b`)
uses the unchanged v48 compiler. Its real x509 compile passes the FP/opaque
boundary and next correctly rejects CFDataToSlice's erased uintptr-to-pointer
conversion. That needs an accurate typed CF pointer adapter, not a permissive
integer provenance rule. The x509 package is still not accepted.
The broad source-patch/Darwin selection finishes with one assertion failure:
the two correct WASM patch files arrived in a different order. The test now
compares exact membership and also checks raw traps remain ENOSYS; no production
WASM behavior or instruction golden changes. The complete source-patch selection
and architecture gate pass on the corrected test (2.196s / 692.5 MiB).

The second full-GOROOT attempt is also retained as an infrastructure stop:
three scratch scans raced with removal of an owned temporary archive directory.
The separate v3 local runner permits only that precise child-ENOENT race to
retry within five seconds, still requires a complete valid scan, and rejects
other errors immediately. The RSS guard, memory/time caps and frozen v2 runner
are unchanged. A third whole-selection attempt restarts at the beginning; no
interrupted GOROOT cases are adopted as completed batch acceptance.

Completed v18-v41 source snapshots have been compressed individually under
`root/completed-snapshots/`. Every archive was extracted and checked against
both the source SHA-256 manifest and recursive content comparison before
removing its expanded copy. Archives, manifests, logs and commands remain;
this is storage reclamation, not loss of the earlier source evidence.

### Executable compatibility follow-up (03:16 UTC)

The immutable v46 stdlib evaluation has **60 distinct completed package passes**
at this observation. Two disjoint lanes remain active; two were deliberately
stopped, joined and reclaimed to reduce concurrent compilation. This is not a
completed 180-package pass. Three crypto builds hit their 25-minute limits.
Semantic failures include Darwin x509's floating/pointer boundaries, TLS/expvar
preflight, hidden embed initialization and the go/types hasher crash. No new
xfails or instruction-golden changes hide those results.

The embed failure is closed independently on v51, source `0caa65501` (manifest
SHA-256 `8180af0fd3f0db2fd4ee10affc8a8f9f6b65ae0ac4236dbc31a7c407d1741a86`),
compiler SHA-256
`19899027d4d59be88fe8c35fe683949e3bfc105b500d9a1afc5973f44f1acf2a`.
The complete real `test/std/embed` package compiles and executes successfully;
binary SHA-256 is
`203a2b98625206c9c17fbefad3412385423b8f7bce9307f57689c6f211d5ab7f`.
Children joined, private scratch was reclaimed and source hashes match.
Compiler-generated FS tables and slice headers are now static read-only data;
there are no hidden MakeSlice/AllocZ init calls. Native64/wasm32 LLVM gates check
layout, nil zero FS, separate metadata, and writable nonaliasing embedded byte
slices. Coro/Emission and selected SSA regressions pass (129.172s / 8.127s).
The new image test is named `TestCoro*` so the existing CI selection includes it.

A separate compiler-only fast filter rejects ordinary allocations before
copying their use lists during synthetic-make recognition. Its zero-allocation
gate and slice/frame-retention regressions pass; an unchanged v48 compile.go
overlay makes the same gate fail at one allocation per query. The existing
make-slice identity/type checks are unchanged. This is not a runtime performance
claim.

Darwin CFData typed-pointer adapters and a static maphash type-ABI adapter remain
isolated experiments until affected executable gates pass. The minimal bool
maphash program passes the retained `afe9a540` main baseline; the v46 go/types
run crashes in TestHashers. The patch reuses existing typehashImpl rather than
reading a legacy map Hasher callback as a managed descriptor. Source-overlay
type checks pass on Darwin/arm64, Linux/amd64 and wasm32, and Go control tests
pass; actual patched package runs remain in progress. Do not count this as a
full go/types or maphash repair yet.

The fixed v48 full 1082-case GOROOT selection is running from the beginning.
Earlier missing-sysctl and scratch-scan interruptions are retained separately.
The new bounded runner retries only transient child ENOENT scan races; RSS,
disk, timeout and complete-scan checks remain enabled. A separate immutable r6
package runner fingerprints a configurable compiler Go soft memory limit below
the unchanged tree-RSS hard cap; generated-program limits are unchanged.

### Compatibility closure after PR #260

Immutable v52 closes the maphash/go-types crash: complete `test/std/hash/maphash`
passes 12 tests and complete `test/std/go/types` passes 18 tests, including the
original TestHashers failure. Source manifest SHA-256 is
`708f0357143fe4ccd023ca234f96a288a57498ad20b6237e8928f532ee517a56`;
compiler SHA-256 is
`58658b8bd0b0021b3b52e5101d0b87d0023e9d5373c7953a6cf0165956362e15`.
The retained generated-binary hashes are respectively
`d7b91d9122b41487f1def9c807e5a65a92febb9d99d14a02bc6208d7e52c1977` and
`54d3ed6b390ab1915358e2c3055e98929d724c0af3836a34344576c21709729e`.
The cohort joined all children, reclaimed private scratch and verified source
hashes. Its static typehashImpl boundary adds no coro directives or callback
representation, and preserves signed-zero, composite/interface value and
unhashable-interface panic semantics.

The combined v53 source passes complete embed and x509 packages. Its selected
compiler/SSA/build gates pass (363.752s / 50.128s / 663.289s), at a combined
8052.8 MiB tree-RSS peak. A stronger v55 cohort changes only one Darwin test;
compiler/runtime/registry sources match v53 exactly, so its frozen compiler
`03702a6431eda06fcbe1e5f752896010eb7ed12e45f55b345110d168abe9fc24`
is intentionally reused. All 41 x509 tests pass, including actual native
system-trust verification of an untrusted certificate at a nonzero explicit
time. This exercises CFDate's floating argument and CFData pointer copying;
the Go control also passes. Source manifest SHA-256 is
`a1a3f1941a8fb8268e77c9097d8b78dac212df29429d56f425025e38e54b0282`;
test binary SHA-256 is
`98d4211dd40eac26e41d3f29aa94f50aa228bb636015fac8582c7590bb2c230d`.
Source-after matches, with children joined and private scratch reclaimed. The
old v50 compiler attempt was deliberately interrupted and reclaimed for the
aggregate memory budget; it is not a semantic failure or a pass.

The original TLS/expvar failures then advance to an interface-phi panic-payload
check. A minimal gate reproduces two cases: an entirely dead predecessor and a
reachable predecessor whose edge is excluded by the frozen CFG. Lifetime
validation, phi incoming operands and branch emission now consume that same
projection. Evaluated opaque interface results remain rejected, and an opaque
call executed before the excluded edge remains emitted. Native64/wasm32 LLVM
verification passes before/after CoroSplit; restoring only the old audit fails
all four dead-edge positives while retaining all live-edge negatives. An early
intermediate implementation failed LLVM verification because it removed phi
operands but retained the old conditional edge; the final gate covers that
consistency requirement. No new frame/runtime state, annotation or effect graph
is added. Selected cl regressions pass (145.205s), SSA passes (0.718s), and the
architecture/source-binding gates pass (2.232s). Immutable v56 then compiles and
runs the complete original TLS package: 59 passed, zero skips/failures. Source
manifest SHA-256 is
`d2d451647bb88c13326f1c6480c1dd579bc82011e6ebbcc33db2b104e7297d7a`;
compiler SHA-256 is
`9ec7b275fc45849dde5ff60e4065f66e631fa30a98e8547ca8206109e1760f79`;
test binary SHA-256 is
`29ace0da4a7764a17a80f9d9f26292a9e45891651781d1fb673975b768f5a539`.
Build/run peaks are 12100.7/80.0 MiB under unchanged hard caps. The same cohort's
expvar build passes the old phi check but fails strict method ABI matching of
Go-identical `[]byte` and `[]uint8`; no executable was produced. The Go control
passes. The cohort joins children, reclaims scratch and matches source-after.

The follow-up canonicalizes predeclared Basic spellings by their existing
BasicKind, without changing defined-type identity, interface rules or runtime
dispatch. byte/rune and seven scalar/composite/signature forms pass across all
five type-key modes; native64/wasm32 real interface modules verify before/after
CoroSplit. Restoring only the old graph builder fails all four interface cases
and all fourteen alias-form cases. Different basic kinds and strict named-type
identity remain distinct. The focused type/IR gate passes (4.926s / 1218.4 MiB).
No new annotation, runtime field, scheduler or function version is added.

The same follow-up hardens the still-private borrowed-result declaration:
normal returns must freshly project the protected result tuple, and borrow
must precede source call/go/defer. Source facts reject stale result caches,
invented values and cleanup after result evaluation; ordinary Go wrappers are
unrestricted. All focused producer gates pass (1.712s / 1225.0 MiB); restoring
only the previous validator loses all ten new negative checks. Physical guards
still fail closed, so this is not public Open delivery acceptance.

Immutable v57 freezes source `15e8ddeb9`, manifest SHA-256
`d0f1bae1e244293eb1f5c84289af866cb82822087640363d7e9f7a15747a062e`,
compiler SHA-256
`120be9993e3748e6428f08c495c2d06a09d5113535088d7c4bfb3057a7b0bff6`.
The full `internal/coro` race/shuffle gate passes (38.649s / 1637.4 MiB).
Broad cl regressions pass (204.762s / 1239.3 MiB). The complete SMTP package
passes four tests, with test binary SHA-256
`989f67751fa0a7a0608fcb0d405306b127a0f121e61cf7e5e8cf549791c96ae3`.
The cohort finishes with matching source-after and reclaimed private scratch;
it is not a whole passing cohort: expvar's compilation exceeds the 16GiB cap
(16405.5 MiB peak / exit 137), and filepath reproduces a separate exact-plain
target validation failure. No executable pass is inferred from the expvar
resource failure. A higher-cap independent retry still needs memory admission.

The filepath failure has a closed, non-nil DirectPlain call occurrence, but its
target also has a descriptor consumer elsewhere. The per-value carrier was
already correct; validation incorrectly required a syntactic StaticCallee to
reuse that target's existing plain primary. Removing only that condition keeps
no-capture/no-method, effect, execution and foreign-contract checks intact.
Native64/wasm32 gates assert one direct primary call before/after CoroSplit,
a frame-free synchronous caller and no redundant target coroutine body or
dynamic capability branch. The focused gate passes (2.037s / 1255.9 MiB);
restoring only the previous validator rejects both target cases. No additional
lowering path, function version, annotation or runtime field is added.

Immutable v58 freezes this change as source `d5d6c5627`, manifest SHA-256
`4e19f44822bab94594cd031dccfa17ff0d8d9932a909d98ea6f692d1ccf11fe3`,
compiler SHA-256
`7e23b89ae2f218b4aaf3a2406fe76c3cb3427132b6713f005e20fb1dc8459ddc`.
Broad cl regressions pass (154.554s / 1208.0 MiB), and SSA passes
(10.479s / 762.6 MiB). The complete original filepath package now compiles and
executes successfully; test binary SHA-256 is
`6aac372b965541cd7e86677e25ca4e250550b6c06581a0ea980b90bac62b17e5`.
Children joined, private scratch was reclaimed, and source-after matches.
These follow-up changes are in PR #262 to llvm-coro; fresh head-specific CI
and review checks remain required. They do not inherit PR #261's green run.

The merged PR checkpoint also has a fresh v54 four-package executable pass:
68 tests plus one helper skip, tree peak 7192.5 MiB. Source manifest SHA-256
`95172eda35332a0d8f0f3518de5d40540ab66cd392afc63fed4941bb8a0cdf37`
matches after execution; compiler SHA-256 is
`15268f843bb4632bcdb3c953bc49bcf2d940f12d2d272a27e153e5f29c0f569b`.
These focused passes do not complete full stdlib/GOROOT or public resource
delivery/performance acceptance.

| Gate | Latest observed result | Scope |
| --- | --- | --- |
| `test` | 41 passed, 1 skipped | Fixed v54 compiler/source, uncached compile and execution |
| `test/cgo` | 3 passed | Fixed v54 compiler/source, uncached compile and execution |
| `test/syncpool` | 1 passed | Fixed v54 compiler/source, uncached compile and execution |
| `test/llgoext` | 23 passed | Fixed v54 compiler/source, including defer-head isolation and 130-G ID allocation |
| `test/std/runtime` | 5 passed | Fixed v35 compiler/source, uncached compile and execution |
| C fault recovery, foreign reentry, traceback | 4 passed | v7 compiler; probes compile, link and execute with LLGo |
| Full `test/go` | Complete v67 and v70: 268/268 passed each, zero failures/skips | v67 completed at 08:00:52 UTC, v70 at 08:41:59 UTC. Both match source-after, join children and reclaim scratch; v70 run peak is 6503.3MiB. The earlier v64 266-pass / 2-failure cohort is retained; both original caller-PC failures are covered by the later complete runs. These are post-PC-fix cohorts, not acceptance of every subsequent compiler/runtime change. |
| Top-level `_demo/go` | Fixed v70: all 73/73 passed, zero failures | Completed 2026-10-16 09:13:31 UTC. Source hashes match; private scratch reclaimed after child join and handle checks. Compiler caps are 8GiB except the recorded 12GiB importer override; generated programs retain a 2GiB cap. Every v70 package was freshly built and run; no earlier-version successes were adopted. The previous v46 complete pass remains separate evidence. |
| `test/std/os`, `test/std/net` | 85 passed; 129 passed / 5 skipped, respectively | Full packages compiled and executed with fixed source snapshot and v35, cache disabled |
| `test/std/reflect` | v25: 68 passed | Separate real LLGo compile and generated-binary execution; build peak 5768 MiB |
| `synctest` execution | Run/Wait, inheritance, channel/select/Cond, fake time, heap association and WaitGroup passed | Real LLGo programs also verify detached children, finalizers and AddCleanup outside the bubble; associated objects remain collectible. Host race tests are not LLGo race-instrumentation acceptance. |
| Collector weak handles | Native standard `weak`: 7 passed; WASI weak/frame-root fixture passed | Native disappearing links clear before finalization; tinygc clears after marking. Interior pointers, live strong ownership and expired weak identity are covered. |
| Ordinary immediate timers | Passed | Real LLGo NewTimer(0), active Reset(0), Stop/drain, Ticker and AfterFunc |
| Unreachable ordinary/fake-time channel timers | v25 compiler / v32 timer source: all six native + WASI gates passed | Ordinary GC, fake GC and standard timer semantics compile/link/run uncached on native and `wasip1-gc`. Each requires normal exit and an exact standard `syscall.Write` completion marker. Drops 32 one-hour timers/tickers and retires them before deadlines; retaining Timer or only C still delivers; discarded AfterFunc handle does not cancel callback. Includes the shared-state split and generic executor GC retry; total 187.50s / peak 1789.8 MiB, not a performance comparison. |
| Optional execution-domain specialization | Compiler and archive gates passed; uncached hello/channel passed | v16/v17: predicate folding precedes effect/ABI analysis, including dead backedges; archive v15 validates consumed true and false predicate constraints |
| Produced archive predicate consumption | Passed on 2026-10-15 | Real native object and compiler-owned records in indexed archive, then production importcfg consumption: matching false emits, reachable mismatch requires rebuild, unrelated entries remain valid. Not a final linked-runtime test. |
| GC notification and timer source islands | Passed on 2026-10-15 | Automatic foreign-thread BDWGC notification, fd detach/reuse, epoch and owner retry race gates; ordinary/fake timer observer tests. Native linked Timer/GC evidence is recorded separately above. |
| Optional GC observation specialization | v25 linked/run/symbol gate passed, 50.58s | Both no-import and `time.Now`-only executables have no optional GC observer/notification/completion service symbols. Go-only observer edges no longer manufacture C export stubs. Least-fixed-point discovery starts with optional services disabled and reuses existing whole-program analysis until monotone demand stabilizes; live-call certificates remain strict. PC-only observations do not enable services. True/false producer→archive→consumer tests pass for both predicates. Archive v16; plan digest v42. |
| Effect analysis and LLVM leaf proof | Passed | Host `go test ./internal/coro ./internal/llvmproof` |
| Compiler coroutine/EmissionUniverse gates | Fixed v44 selected cl and production IR passed; v43 full SSA/core gates passed | Coro/Emission/SSA, field-address/nil and descriptor/generic-patch tests pass. Native/wasm physical ABI, retention and CoroSplit gates remain enabled; instruction goldens are excluded. v44 additionally covers exact raw aggregate lowering, finalizer close callback admission, both preemptible/nonpreemptible allocation placement, and unchanged source fingerprints. |
| Analysis/runtime core race gates | Fixed v42: all selected packages passed | Host `-race`: internal/coro 16.876s, deadcode/dcepass/flags each pass; runtime/internal/coro 3.535s, coroalloc 1.779s and corodoorbell 1.346s. This is compiler/runtime-core validation, not race-instrumented LLGo application execution. |
| Darwin time.Now redundant frames | Passed on fixed v35 compiler / clock-r5 source | Final linked no-import/time.Now binaries contain none of five unnecessary clock/SynctestNow coroutine paths. Production plan/IR gates pass; two enabled synctest programs retain normal-task fallback and fake-time advancement. One exact C-clock contract plus the existing execution-domain predicate replaces no scheduler mechanism. Whole clock cohort 240.18s / peak 2906.2 MiB; not a performance measurement. |
| Coroutine frame zero-initialization | Native/wasm IR regressions and real optimized clearfat passed | Allocas still dominate the ramp, but memset now follows coro.begin and precedes publication/initial suspend. This avoids copying zeroed native-stack aggregates into the frame and subsequent element-wise SROA expansion. Full cl/SSA coro/Emission gates pass (103.52s / 0.39s). Fixed v36 plus the two-file frame-init overlay builds clearfat at default optimization with peak 2287.2 MiB and runs successfully; the original GOROOT case also passes (48.23s build). This is not a full-suite rerun of the newer compiler. |
| Aliased reflection-header pointer provenance | Original GOROOT and real GC/await fixture passed | Fixed v36 plus the reviewed two-file proof extension: `unsafe_string_data.go` passes unchanged; a 32 KiB string remains rooted across two GC rounds and channel suspension, for direct-alias and readonly-copy paths. The combined actual-program gate took 137.51s / peak 2789.2 MiB. Six positive and nine negative proof cases retain rejection of header/source mutation, fabricated headers, unknown callers and pointer arithmetic. No new runtime fields or post-emission analysis. |
| Go 1.27 generic methods | Original GOROOT, minimal program and testing regression passed | Fixed x/tools fork `f97edf9cf375` repairs receiver instantiation and avoids poisoning canonical ordinary signatures with method signatures. LLGo bound/thunk wrappers consume the exact instantiated signature and TypeArgs proof. Whole x/tools SSA/interpreter tests, 11 independent probes, full original `genmeth1.go`, compiler gates and the generated testing executable pass. The final testing gate took 218s / peak 5493.3 MiB. |
| Interface receiver helpers in `go` statements | Original GOROOT and call/go/defer execution passed | Fixed v37-bug130 snapshot: the existing ProgramIR helper classifier now derives receiver normalization once for all SSA call instructions. No emitter fallback or relaxed ABI gate. Original `fixedbugs/bug130.go` and the minimal interface call/go/defer program pass; full pipeline and architecture gates pass. |
| Darwin account-query worker boundaries | Actual `os/user` and `archive/tar` passed | Fixed v37 compiler plus the six exact C identity mappings for reentrant account/group queries, group-list lookup and sysconf: 16 user tests and 8 tar tests pass, with unchanged source fingerprints. The original Go wrappers retain result/errno interpretation. No additional coroutine directive, guessed code-pointer policy, or synchronous executor call. |
| High-fanout ordinary Sleep | Original 10,000-goroutine GOROOT case passed | Fixed v37 compiler with the timer-capacity correction: `fixedbugs/issue16016.go` passes with `GOMAXPROCS=1,2`; build peak 1343.2 MiB, sampled runtime peak 21.9 MiB. Removed the arbitrary 4096-slot timer quota, retaining the existing 64-slot inline page and demand-paged growth. No fixed storage, new scheduling path or operation-ID ABI change. The current 32,704-slot per-source encoding bound remains a high-concurrency limitation, not an unlimited-timer claim. |
| Empty line-directive filename | Original GOROOT case passed | Fixed v37-empty-filename compiler/source: `fixedbugs/issue22662.go` compiles and runs unchanged, build peak 2723.5 MiB. The shared location recorder accepts a valid line with an empty filename, allowing the existing formatter to produce `??`; invalid positions remain rejected. Unit gate reproduced the old failure before passing with the fix. |
| Source/physical named-type boundaries | Full SSA/compiler gates and actual archive/zip passed | Fixed v37-named-r6: archive/zip compiles (peak 6941.9 MiB) and all four tests execute successfully. Source replacement and physical lowering have separate domains, but share the single recursive replacement algorithm; exact source inverses survive ABI export/import. Generic instances are interned before the existing converter-cache lookup, while distinct named types remain distinct. Full SSA 7.67s, selected full Coro/Emission/descriptor compiler gates 143.83s, backend/architecture 10.26s pass; no relaxed identity checks or enlarged architecture budget. |
| Immediate reflection-address conversion | Original cases and actual GC/await regression passed | Fixed v37-reflect-pointer-r1: unchanged issue15329, issue35073a and issue35027 pass. Exact Pointer/UnsafeAddr method identity and typed owner provenance feed the existing retention proof; arithmetic, address escapes and open/mixed callees remain rejected. Actual GC/await fixtures cover five receiver forms and clearing the original global owner. Native64/wasm32 CoroSplit tests force an implicit preemption cut between the call and conversion and verify the correctly sized retained word reload. Current collectors are nonmoving conservative-or-none; this is not a moving-GC claim. |
| Pure integer uintptr call terminal | Original issue19078 passed | The existing integer-chain proof now accepts an exact closed ordinary Go callee consuming an integer. Foreign/worker calls and address-lifetime contracts remain excluded; no inverse pointer or root is fabricated. Positive/negative proof groups and the unchanged generated program pass. |
| Reflection callback source-signature identity | Original issue16331 and real plain/coro MakeFunc tests passed | Final v37-cha-signature-r3 compiler SHA-256 ea78ca8c9253f17924665b63c907f16e055433f466b1f9262e2cfbb49d8dc9ea. The single frozen metadata source-signature projection is shared by CHA indexing, closed certificates and dynamic consumers. It retains mixed plain/coroutine callbacks instead of incorrectly producing a coroutine-only entry. Original program passes at P=1,2,4 three times each; an earlier r2 passed 30 repeats. New direct/reflect.Call/defer callback tests compile and execute with LLGo (build peak 6284.5 MiB). Architecture budgets remain unchanged. |
| atomic.Value first publication | Original concurrency case and mixed-writer regression passed | Final v37-atomic-pin-r3 SHA-256 cc1dd46fec63ed2cbcd6e1d68e67abc5b89dff47a4606634321c69127a71b2bf. The stdlib pin/unpin pair uses the existing bounded critical-region proof, not a native mutex acquired on a worker and released on another thread. Original issue52612 passes 20 repeats at P=1,2 combined; the sync fixture with mixed Store/Swap/CompareAndSwap first writers passes ten repeats, internally testing both P settings. Shared nonnil inference removes only a strictly dominating, evaluated FieldAddr's repeated check; unevaluated generic Sizeof has a negative gate. Earlier r2: original case 100/100, native test/std/sync/atomic 46 passed/9 pre-existing skips, original WASI case 10/10. Those platform runs precede the final stricter nonnil refinement and are not a full final-snapshot suite pass. |
| Integrated critical-region safety | v38 original atomic case 20/20 native and one WASI run; mixed-writer fixture 10/10 | Atomic operands must be proven nonnil. Pointer/function/divisor/shift/loop-bound and retention proofs share one exact conditional-edge dominance query, including bypass predecessors and recover roots. Native/wasm negative gates retain necessary fault checks; no second effect analysis or new runtime lock. |
| Repeated allocation identity | v38 original issue48289 20/20 | A loop-local address cannot reuse a single borrowed frame slot when stored into cross-iteration scratch or carried through a loop phi. The existing borrowing proof remains valid for immediate synchronous consumers and same-iteration merges. SSA and physical-IR tests cover select-independent array and phi cases; no blanket disabling of borrowed allocations. |
| Logical Caller/Callers exhaustion | v38 generated regression 10/10 | Once a logical coroutine stack exists it is authoritative, including panic/recover views. Exhaustion no longer exposes physical executor frames. Normal, child and pre/post-recover calls check exact and large skips. Original issue5856 still assumes gc's private runtime.gopanic frame depth and is not claimed to pass; its source panic location is separately verified. |
| Integrated v38 lower-level gates | Full SSA and internal/coro race passed; 643 selected compiler tests passed with test-only overlay | SSA 7.099s, core race 22.059s, selected cl 109.954s. The original v38 cl run had two stale test expectations: the new pin intrinsic's operand-policy inventory and pure integer bitcast returns. Only three test files change; explicit pointer reconstruction, foreign address use and across-await negative gates remain. The unchanged original issue19078 also compiles and runs with v38. |
| SSA production-runtime test loader | Unfiltered SSA package passed | Test fixtures load the actual LLGo runtime through the production loader rather than host-importing LLGo-only declarations. No runtime `!llgo` stubs or IR golden changes; the complete package passes again with the final named-type changes. |
| Redundant coroutine and frame-size gates | 5 passed | Native/wasm codegen unit tests; not target runtime acceptance |
| Focused GOROOT | All 31 selected cases have passed; not yet a single-snapshot suite pass | v22: 22 closure/defer/recover/channel/select cases; v25: 7 more channel cases. Both sieve cases pass with v25 compiler/v31 shutdown source, then pass 80 repeated runs across `GOMAXPROCS=1,2`. Pending direct ingress/local completion returns to the existing reducer; stable main-return invokes the one shutdown owner even with remaining waits. Strict state validation and join ordering remain intact. |
| Broad `internal/build` coroutine gates | Integrated v35 passed, 365.48s / peak 6157.6 MiB | Entire `^Test(Coro\|GenMainModule.*Coro\|BuildCoroPlan\|NativeObject)` selection, including the unevaluated global-reader proof and native archive isolation regressions. Resolves the previous native runtime source-island closure, partial Time.Sleep fixture and inventory failures. Direct plan authority is 294 (was 312), raw opcode access 7 (was 8); original budgets unchanged. This does not establish executable stdlib compatibility. |
| Integrated v37 compiler/build gates | Passed | Fixed snapshot: cl 146.87s, SSA 0.44s and internal/build 518.50s; combined peak 6624.3 MiB. Selection is `^Test(Coro\|Emission\|GenMainModule.*Coro\|BuildCoroPlan\|NativeObject)`, not every test in those packages. Older SSA fixtures which host-import LLGo-only runtime still fail independently and require test-loader correction. |
| Native runtime ABI/fleet E2E | All 27 selected gates passed, 118.04s / peak 2086.7 MiB | Source islands now load the real shared execution-domain state, channel and GC observer dependencies. Existing ABI, cross-route select/cancel, callback reentry, locked-M and replacement-thread assertions are retained; no new fake runtime implementation. |
| Production time.Sleep plan/codegen | Passed, 11.58s / peak 1025.6 MiB | Uses complete production `Do`, default analysis, and actual synctest bridge instead of a partial mocked SSA universe. Preserves five physical bodies, direct-call effects, immediate nonpositive return, frame retention, direct tail-forward IR and LLVM coroutine pass verification. |
| Complete `test/std/...` and GOROOT | Full stdlib pending; v35 GOROOT CI selection completed with failures | All 1082 selected original Go 1.27 cases ran: 1059 runner-classified PASS and 23 unexpected FAIL. PASS includes one pre-existing xfail and eleven expected not-applicable failures, leaving 1047 actual successful checks. Four not-applicable cases actually succeeded. All four source fingerprints match. Twenty-one failures have later focused passing runs; four use extended compiler limits. Two depend on private gc behavior. After the x/tools repair, the original full rangegen-generated program compiles and executes on v42 at both O0 and default optimization (24GiB cap; default build peak 22,826.6MiB, run peak 30.5MiB). The generic chans finalization regression passes five unchanged executions on v44; its independent main-panic reporting error is also fixed. No new xfails added; no final-snapshot full-suite pass claimed. |
| Generic descriptor calls and closed spawn | v40 original absdiff3 10/10 and generated generic-spawn regression passed | Outcome-plain bodies can call descriptors independently of their own function representation; all-target outcome/native-result proofs remain strict. Core spawn analysis and physical ABI now share exact materialized-generic identity, including context-free literals inside instantiated functions/receiver methods. Native/wasm positive and malformed-identity/capture negative gates pass. The chans compile failure is fixed, but its finalizer-dependent runtime failure remains open. |
| Main panic with live peers | v40-derived runtime fix: 30/30 real executions and 41 host race tests passed | A committed dead main G with ready/parked peers uses the existing task panic reporter. The command reporter still requires fully retired executor/admission ownership. Last-G, parked-peer and ready-peer cases at P=1,2 preserve the original payload, user source location and exit status 2; no invariant check was relaxed. |
| WaitGroup semaphore storage | Fixed v40-sema actual sync, both synctest fixtures and cancellation/GC 10/10 passed | Ordinary and durable waits use one typed internal prepare entry, adjacent to the same park. Existing frame-borrow proof now applies without a broader compiler analysis; the generated resume no longer allocates a 256-byte GC object. A quiet seven-pair comparison reduces serial spawn/WaitGroup time 142.673 to 130.561 ms (8.49%), but Go is 16.837 ms: the remaining 7.754x gap is not solved by this allocation correction. |
| Fresh full stdlib package cohort | v35: all 9 selected packages passed, 306 tests passed / 7 skipped | time 6, sync 25, synctest 2, context 10, syscall 19, weak 7, unique 23, os 85, net 129; sync has two explicit skips and net five. Source fingerprints match before/after. Private per-build/per-recipe native objects fix concurrent clang/archive truncation; actual race regressions pass and the old-path overlay fails deterministically. The earlier failed cohort remains recorded separately. |
| WASI positional file operations | v25 actual compile/run passed | Shared HostOp protocol covers Pread/Pwrite, 64-bit offsets, short reads, zero length, invalid fd and unchanged cursor. Real C-reactor close-pending regression passes. Preview 1 synchronous-host limitations remain below. |
| WASI filesystem boundaries | v35: all eight actual target probes passed | Standard os.Root/OpenFile/io.WriteString/OffsetWriter/ReadAt, expired read/write deadlines and reset, EOF, Sync/Close, Rename/Stat/ReadDir/Remove and fmt completion marker pass. Raw filesystem, generic math, fmt alone, syscall.Exit(42) and os.Exit(42) pass; wasm-unknown positional I/O also passes with exact operation counts. Preserve Go's cwd/path/dirent logic and reuse one HostOp pending/poll/cancel path for raw WASI and internal/poll buffers. See [WASI filesystem design](coro-wasi-filesystem.md). |
| Node JavaScript standard filesystem | v36: actual compile/run passed, then independent execution repeated successfully | Production wasm_exec.js invokes asynchronous Node fs, preserving Go's fd/cwd/seek/dirent bodies. Covers OffsetWriter, ReadAt/EOF, closed/error cases and an exact fmt completion marker; fixture removes its temporary files. Build peak 2550.8 MiB. Source/typed-owner/producer-archive-consumer/protocol gates also pass. Post-publication cancellation ownership remains explicitly open. See [JavaScript filesystem boundary](coro-javascript-filesystem.md). |
| Freestanding dynamic reflection boundary | v35 explicit-unsupported negative probe passed | The target capability guard prevents unreachable libffi imports from breaking ordinary fmt/file programs. Actual reflect.Call/MakeFunc remain unsupported on WASM/baremetal; the probe checks the specific ABI/unsupported error. Native reflection remains separately covered. |
| Resource-producing operation cancellation | Borrowed-output execution passed; public delivery remains open | A real-fd LLGo fixture pre-registers ordinary defer cleanup around caller-owned output. Normal completion, early/late cancellation, pending GC, panic/recover and blocking close all pass, including other-G progress at P=1 and exact open/close counts. This reuses existing frame ownership and strong join. The unused optional custody experiment was removed after review; its isolated race tests are archived, not production acceptance. Public scalar-fd return and adoption by os.File still need platform integration; cleanup registered only after Open returns cannot cover the delivery window. See async-core contract §3.6. |
| Integrated borrowed-output/platform gates | v44 native 10 executions and Node 3 executions passed | Formal opt-in gates compile real LLGo programs, require exact FD close counts before completion, and exercise cancellation, GC and delayed close with other-G progress. The three WASI timer/GC programs and platform protocol/source gates also pass on v44. Earlier full runtime-core race evidence remains tied to its recorded snapshot. Node scheduler turns yield to host tasks so filesystem callbacks cannot be starved by an endless microtask chain. These results do not cover public Open result delivery or arbitrary fatal host termination. |
| Full target matrix | Pending | Historical target CI is not validation of the current integration |
| Baremetal standard timers | Passed | v17, LLVM 22 Cortex-M3 ELF executed on QEMU MPS2; host-return plus four alarm/continue transactions |
| Optimized compute/timer progress | v25 compiler / v32 timer source passed, then 10 repeated runs passed | Actual `-O3 -lto=full` binary, `GOMAXPROCS=1`, 100-million-iteration pure compute loop with an elapsed standard-library timer. This is a progress regression gate, not a hard latency bound for every possible path. Added to the stdlib-time CI lane. |
| Current performance, size and memory | v38 fixed-source comparison completed; performance gaps remain | Go 1.27 versus LLGo `-O3 -lto=full`, P=1, seven interleaved AB/BA pairs after warmup. LLGo/Go median elapsed ratios: static compute 0.943, buffered channel 0.987, ready select 0.657, batched spawn 1.974, serial spawn/WaitGroup 8.417, handoff 1.135, timers 1.077, file 3.494, raw file syscall 1.378, TCP 1.898. All 256 executions passed and output/input hashes match. At 5000 parked goroutines, process peak RSS is 12,353,536 versus Go's 17,989,632 bytes (31.3% lower), but the zero-goroutine baseline is higher (6,619,136 versus 3,948,544). Baseline-adjusted growth is about 1147 versus 2808 bytes/G, not an exact frame-size measurement. Equally native-stripped copies are 2.47x core / 3.52x I/O Go executable size; external shared libraries are excluded. These short local P=1 measurements do not establish dynamic-call or parallel throughput. |
| Existing semantic method-slot DCE | v42 production-default six-scene gate passed | Ordinary/O3 staged builds, full-LTO default and explicit disable, interface invoke and named reflect.Call all compile/run with expected method retention. Reuses the existing metadata/interface/reflection demand pass, not a new analysis graph. Isolated v38 core text shrinks 2,480,148 to 1,300,236 bytes (47.6%); equally stripped/signed file shrinks 3,359,344 to 2,024,784 bytes. Method names/counts/signatures remain, while unreachable callable slots no longer root parsing/formatting bodies. The earlier full reflection test-package LTO build reached its 12GiB cap; the standalone probe is not claimed as that full-package pass. |
| Integrated executable size | v42 same-source core/I/O builds and 33 checksum-checked smoke executions passed | Production full-LTO default, no explicit DCE flag. Equally stripped/signed core is 2,024,688 bytes versus v38 3,359,344 (-39.73%); I/O is 3,803,296 versus 6,225,632 (-38.91%). These remain 1.49x/2.15x Go 1.27. Text is 1,299,540/2,642,728 bytes (-47.60%/-43.97%). Seven core and four file/TCP/pipe modes pass on original LLGo, stripped LLGo and identical-source Go. Concurrent smoke timings are not performance samples. |
| Consumed pointer-result GC roots | v44 permanent live-caller regression 10/10 and unchanged GOROOT chans 5/5 passed | One frozen safepoint-interval allocation placement avoids spilling an unpublished pointer-cell address across await; consumed scalar pointer result fields are cleared only after loading all result fields. Aggregates, startup arguments and live environments remain rooted. v44 existing reflection/header plus new pointer regression, borrowed real FD, and three native timer gates pass together (386.627s / 2976.4 MiB including compilation). Earlier GC-only production cohort also passes three WASI TinyGC timer gates and MakeFunc startup/GC. No general prompt-finalizer guarantee or final whole-GOROOT rerun is claimed. |

LLGo and generated-program runs use process-tree RSS and time limits.
Current heavy jobs are serialized within each suite. A content-hash manifest
now tracks selected `LLGoFiles` inputs in both original and replacement
packages; tests cover unchanged size/mtime and dependency propagation.
Authoritative fresh runs still disable the cache because transitive C headers
and external pkg-config inputs are not yet fully tracked.

Local LLVM 22 cannot consume the newly installed macOS 27 SDK's
`arm64e.x1-macos` TAPI target. Native runs explicitly select the installed
macOS 26.5 SDK. LLGo now honors an explicit `SDKROOT`; path/named/default
resolver tests pass. No global Xcode or SDK symlinks were changed.
The resource guard now discovers process topology separately and queries RSS
only for the owned tree. Sampling failures pause the workload before retrying;
timeouts and RSS limits remain enforced. Full `test/go` compilation has an
8 GiB tree cap; its completed independent rerun allows 12 GiB because some tests
spawn nested compilers. Its original 6 GiB resource interruption is retained.
Individual builds generally allow 6 GiB and standalone
generated executable runs 2 GiB. Demo compilation may use 8 GiB when a normal
stdlib-heavy fixture exceeds 6 GiB; this is not a runtime-memory relaxation.

The residual directive inventory is reviewed at 8 contracts, 40 noblock and
37 sync declarations. The former baremetal Open wrapper contract is removed.
Five new sync boundaries are collector-owned weak-reference/notification C
leaves; they may acquire GC locks, but cannot migrate to a worker or suspend a
managed frame. The new noblock boundary is the exact non-returning WASI
proc_exit import. No Go caller-coloring directive class is reintroduced; the
owner manifest freezes these exact declarations. The eighth contract identifies
only Darwin's executor-safe C clock query; ordinary Go callers remain inferred.

The in-progress full GOROOT evaluation has found several distinct failures:
`genmeth1.go` reaches an x/tools SSA generic-method-expression assertion before
coro lowering; an independent x/tools reproduction confirms the dependency
failure. `clearfat.go` exceeds both the original 8 GiB and a diagnostic 11 GiB
compiler RSS cap. The identical generated clearfat source builds and runs with
v36 at `-O0` (32.26s / peak 2093.4 MiB); a standalone LLVM 22 optimization of the
presplit bitcode reproduces the default-optimization growth. `convinline.go` and
`index0.go` also exceed the per-case ten-minute compile limit. clearfat's
frame-initialization fix has subsequently passed at default optimization, as
recorded above; the unchanged v35 full-suite failure remains in its own log.
`noinit.go` inspects gc's private `main..inittask` binary layout, which LLGo does
not implement; this is not a Go interface/stdlib compatibility requirement.
`unsafe_string_data.go` instead exposed a real missing pointer-provenance proof
for an aliased reflect.StringHeader; the unchanged original now passes with the
fixed v36-header compiler, including a separate actual GC/await regression.
The original v35 full-suite result is not rewritten by this later focused pass.
`convinline.go` and `index0.go` still exceed ten minutes with the frame-initialization fix
(peak 7696.1 MiB), so the clearfat fix is not claimed to solve every generated
large-function case. `rangegen.go` also exceeds the original 8 GiB cap.
The identical convinline-generated program builds at `-O0` in 459.89s,
peak 6987.5 MiB, and executes successfully. Standalone LLVM CoroSplit confirms
a roughly 590,000-instruction cloned resume/destroy body. A bounded experiment
adding mem2reg/SimplifyCFG before splitting did not improve it (roughly 631,000
instructions and a four-minute stop); that experiment is not in production.
These failures
are not newly approved xfails. A separate unchanged-source convinline retry
with a 25-minute / 10 GiB compilation cap subsequently passes at default
optimization: 852.20s build, peak 9148.5 MiB, then normal executable exit.
Its executable SHA-256 is
`1e573b86ade386cf29832aa9ac20c0ed0f0845a1f5e0ac8e691caa4aac2e0c9a`.
This distinguishes a compilation-resource limit from a semantic failure;
it does not erase the ten-minute suite failure or establish acceptable
large-function compilation cost. The original `index0.go index.go` generated
program also passes unchanged at default optimization with the extended
25-minute / 10 GiB cap: 1005.91s build, peak 5512.0 MiB, then normal executable
exit. Its ten-minute suite timeout remains separately recorded. A subsequent
unchanged-source `rangegen.go` retry still exceeds a 12 GiB compile cap
(12,960.6 MiB sampled tree RSS after 124s). A further O0 retry with a 20GiB
cap compiles (17,824.8MiB peak) but fails during execution: nested iterator
`goto` incorrectly continues rather than restarting the outer iterator.
A 23-line reduction passes Go and local main but fails the old x/tools SSA
interpreter. The existing upstream fix was minimally backported to the fork;
its SSA/interpreter tests pass, and v42 is pinned to it. On v42, both the
minimal example and the unchanged full generated program execute successfully
at O0 with a 20GiB cap (18,120.1MiB peak); compiler and input hashes match
before/after. A separate 24GiB/25m default-optimization retry also compiles
and executes the unchanged full program: 22,826.6MiB compilation peak,
30.5MiB run peak, executable SHA-256
`5d7241711e86b038e0596d9d07bafa43b62731f81b63dddb11e192f221327423`.
This closes the discovered source-semantic failure, not the original compiler
resource-budget failure; neither earlier failure is a new approved xfail.
The original issue9604b-generated
program passes at default optimization under the extended 25-minute / 12 GiB
cap (1217s including execution, peak compilation RSS 8631.7 MiB). Its original
ten-minute timeout is retained; this result establishes semantics, not an
acceptable compiler-time cost.

The expanded evaluations have additionally found a source/physical named-type
cache collision (`archive/zip`), an unrecorded interface receiver helper for
`go` (`fixedbugs/bug130.go`), an unsupported fixed vector-add reduction in the
post-optimization atomic-cost verifier (`interface/bigdata.go`), and missing
provenance for immediate `reflect.Value.Pointer` conversion
(`fixedbugs/issue15329.go`). The receiver-helper fix and bounded vector proof
have passed their unchanged original programs independently. Named-type and
reflection-pointer integration are tested in later focused cohorts; no
permissive fallback was added. The named-type fix now passes actual archive/zip.
The unchanged original `fixedbugs/issue16016.go` initially failed at the
4096-active-timer profile quota. It passes after allowing demand-paged growth
to the existing encoded source capacity. Capacity failure is now diagnosed
separately from invalid owner/clock/deadline state; the underlying finite ID
bound is still explicit.

## Remaining work, in order

1. Finish current executable acceptance on immutable compiler/source pairs.
   Latest integrated v100 `test/go` passes271/271; immutable v101 is running
   the complete fresh-build cohort for PR277. The earlier v70 native Go demos
   pass73/73. The single-snapshot v70 180-package stdlib evaluation has124
   completed package passes and no failure so far; it remains active. The older
   fixed v65-source/v64-compiler eight-shard GOROOT evaluation has completed
   shards0–5 and is running shard6 (117/135 done at this observation).
   Preserve every older failure/resource stop;
   focused passes on later snapshots do not rewrite those journals or establish
   a final-snapshot whole-suite pass. Keep unchanged inputs, private scratch
   reclamation and child joins. Do not update instruction goldens or xfails
   to hide runtime failures.
2. Preserve the closed stale-GC-root regression while finishing the final
   GOROOT evaluation. The production correction passes the GC-only fixed cohort:
   placement is frozen, safepoint/critical cost and exact helper observation
   remain checked, and live-root retention/cancellation gates pass. Combining
   it with the raw-helper optimization exposed a separate static-outcome
   channel-close admission mismatch in v43. v44 fixes that mismatch and passes
   the permanent consumed-pointer regression 10/10 and unchanged `chans` 5/5;
   extended native GC/resource/timer integration gates also pass. No general prompt-finalizer
   guarantee follows. Of the original GOROOT cohort's 23 failures, 21 now have
   subsequent focused passing
   executions; four need extended compiler budgets and two remaining tests
   depend on private gc binary/trace behavior. Final integrated evaluation is
   still required, without silently adding xfails. The newly observed original
   reflection/GC case `issue27695b.go` passes on v82 after correcting the
   source-only closed-callable-domain assumption; retain its universal dispatch
   and unaffected exact/static-family gates through final integration.
3. Finish public resource-result cancellation handoff. Typed borrowed output
   plus pre-registered ordinary defer is already verified on native and Node;
   public `syscall.Open`/`os.Open` child-to-parent result delivery and platform
   wrapper adoption are not covered by that primitive. Reuse existing
   completion/cleanup ownership, without a second resource-capsule framework.
   Closed-static physical guards now pass all six actual native routes and
   60/60 executions on v81, including source defer, range-yield and nil-output
   roots; PR #267 is merged after all 18 CI jobs pass. Large fallbacks avoid a duplicate allocation,
   and resource-only cleanup avoids another result spill/copy. The shared
   result tuple has a source-expressible anonymous alias; retain exact types
   and use existing compiler-inserted helper demand where needed.
   Do not add a generic representation-cast or synthetic-SSA framework for
   helper field names. Typed recipe/model tests are passing, but no public
   seed may be enabled until static, dynamic and library consumption are wired.
4. Complete or explicitly delimit platform gaps. Native/WASI ordinary and
   fake-time timer collection, weak objects, standard WASI file operations and
   Node asynchronous filesystem paths have real execution gates. Remaining
   work includes explicit native-host signal admission, JS public callback
   handles and dynamic reflection on freestanding targets, and real-board
   clock/alarm/WFI wiring beyond the Cortex-M3 emulator gate. WASI Preview 1
   regular-file imports are synchronous: readiness alone cannot guarantee
   the host will never stall. A hard guarantee needs asynchronous host ABI or
   host offload. Do not silently restore the legacy libuv loop to fill gaps.
5. Close remaining measured performance gaps and remeasure on a quiet host.
   Static compute/channel fast paths are competitive; serial spawn/WaitGroup,
   file wrappers and TCP still lag Go. A shared raw-helper capability fix removes
   redundant synchronous-helper coroutine frames; its v43 channel-close
   integration failure is corrected and tested in v44. Same-work diagnostic
   persistent-worker comparisons identify the WaitGroup await/completion path,
   not per-G creation, as the remaining serial hot path. Actual counters show
   four child-frame allocations and five inline awaits per round, versus none
   in the channel control. The same-P keyed fast path is already being used;
   removing one wrapper barrier or forcing LLVM's global elision heuristic
   has not established a stable large speedup. These background diagnostics
   do not replace the quiet formal baseline or justify removing checks.
   Preserve atomic/preemption proofs;
   do not add stdlib-specific coloring labels or remove lifecycle checks.
   Keep size, parked-G memory slope and generated-code checks in the final
   comparison. Completed native/WASI timer progress gates do not prove every
   external source or physical instruction has a universal latency bound.
6. Review and commit the integration, open/update a PR to `llvm-coro`, inspect
   current CI and review comments, and merge only after the applicable gates
   pass. Final validation includes the full workflow set, with explicitly
   documented platform incompatibilities. Do not modify `cpunion/main` or
   push this development to upstream.

## Local result provenance

Current persistent logs are under `~/.cache/llgo-coro-acceptance/20261015/`:

- `root/compiler-build.log`
- `root/compiler-v20-build.log`
- `root/smoke-build-v20.log`, `root/smoke-run-v20.log`, `root/smoke-gc-symbols-v20.log`
- `root/time-now-build-v20.log` (unresolved elided-call conflict)
- `root/gc-link-v22.log` (no-import and time-only linked regressions pass)
- `root/gc-link-symbol-gate-v25.log` (disabled-service link/run and final-symbol gate)
- `root/goroot-focus-v22.log` (22 focused cases pass)
- `root/goroot-chan-rest-v25.log` (7 pass, 2 sieve runtime failures)
- `root/goroot-sieve-v28.log` (sieve1 fixed; sieve2 remains failing)
- `timer/sieve-v31-repeat80.log` (both original sieve cases pass 80 repeated executions)
- `root/build-core-v27.log` (broad build gates failed; outstanding fixes are tracked above)
- `root/build-core-v34.log` (all selected integrated build gates passed)
- `root/build-core-v35.log` (integrated build gates plus private-object and frozen-reader fixes passed)
- `root/js-patched-abi-{red,green}.log`, `root/cl-abi-v36.log` (canonical replacement-type ABI regression and full compiler selection)
- `root/frame-init-{red-r2,green-r2,cl-ssa}.log`, `root/clearfat-frame-init-{build,run}.log`, `root/goroot-frame-init.log` (frame-initialization correction and optimized original-case acceptance)
- `experiment-frame-init/overlay.json` (the fixed two-file v36 compiler experiment)
- `root/frame-init-cancel-e2e.log` (three actual defer/cancel/fleet regressions)
- `root/convinline-default-25m-{build,run}.log`, `root/convinline-default-25m-trace.json` (unchanged generated source, default optimization, extended compilation-resource cap)
- `root/index0-default-25m/` (original two-file generator, default optimization, extended compilation-resource cap)
- `root/empty-filename-v37/` (original issue22662, immutable source fingerprints and compiler hash)
- `root/stdlib-all-v37-interruption.md` (partial stdlib results and explicit disk-safety interruption)
- `metadata/reflect-header-v36-actual.log` (unchanged GOROOT plus GC/await acceptance)
- `timer/resource-capsule-experiment/` (removed optional experiment, recovery patch and historical isolated evidence)
- `timer/reflect-makefunc-callbacks-r3-{build,run}.log` (actual direct/reflect.Call/defer callbacks)
- `root/atomic-pin-v37-r3/` (final pin/nonnil compiler, fixed fingerprints, original case and mixed-writer execution)
- `root/atomic-pin-platforms/` (r2 native atomic package and original WASI case; existing skips retained)
- `root/snapshot-v38-source.sha256`, `root/v38-focused/` (integrated frozen source; original atomic and allocation cases, mixed atomic writers, Caller exhaustion and WASI atomic execution)
- `acceptance/v38-lower-integration/RESULT.md` (full SSA/core race, original cl failures and the test-only overlay's 643 passing compiler tests)
- `acceptance/REPEATED-ALLOCATION-IDENTITY-COHORT.md`, `acceptance/CALLER-EXHAUSTION-REVIEW.md`
- `timer/v38/README.md` (formal actual native/Node borrowed-output gates and runtime/platform checks)
- `perf-v38/README.md` (fresh same-source optimized build sizes; timing not yet performed)
- `acceptance/UINTPTR-GO-CALL-COHORT.md`, `acceptance/COMPLETED-FIELD-NONNIL-COHORT.md`
- `metadata/js-stdlib-file-v36-{build,run,fd-cleanup}.log` (actual Node filesystem, including 2 opens/2 closes and no outstanding fd)
- `root/stdlib-core-v35/` (new immutable nine-package stdlib cohort)
- `root/integrated-v37-gates.log` (fixed integrated compiler/build gate)
- `root/stdlib-all-v37-{0,1}/` (partial 180-package stdlib evaluation, stopped for disk safety)
- `root/stdlib-user-account-v37/` (exact C mapping correction; 24 actual tests pass)
- `root/timer-capacity-v37/` (original 10,000-goroutine Sleep case, fixed source and compiler, P=1 and P=2)
- `acceptance/GENMETH-COHORT.md`, `acceptance/genmeth-r4-{source,artifacts}.sha256`
- `acceptance/bug130-r1-journal.log` (original case and interface-call fixture)
- `root/goroot-ci-v35-{0,1,2,3}/` (four independent guarded GOROOT CI shards)
- `root/GOROOT-V35-RESULT.md` (completed 1082-case evaluation, raw classifications and every unexpected failure's later disposition)
- `metadata/v35-wasi-results.tsv` (eight actual target probes, including exact exit 42)
- `acceptance/demos-v25-r2/RESULT.md` (73/73 executable demos, fixed fingerprints)
- `acceptance/test-go-v35/` (fresh full executable test/go rerun)
- `timer/native-object-isolation-before.log`, `timer/native-object-isolation-race.log` (deterministic old-path red / private-path green)
- `timer/island-native-family.log` (all 27 selected native E2E/source gates pass)
- `root/sleep-production-v32-r4.log` (complete production plan and codegen fixture passes)
- `root/preempt-timer-v32.build.log`, `root/preempt-timer-v32.run.log` (optimized single-P progress gate)
- `root/preempt-timer-v32.repeat10.log` (ten more single-P progress passes)
- `root/stdlib-core-v32/` (unexpected native C-leaf link failure at the first package)
- `timer/std-time-v25-build-quiet.log`, `timer/std-time-v25-quiet-run.log` (unchanged isolated retry, six runtime tests passed)
- `timer/complete-acceptance-v32.log` (all six timer gates after the final shared-state/retry change)
- `root/compiler-core-v25.log` (complete selected compiler gates pass)
- `root/reflect-build-v25.log`, `root/reflect-run-v25.log` (68 pass)
- `root/goroot-focus-v20.log` (stopped after the same conflict; not a suite pass)
- `acceptance/test-go-v19/RESULT.md` (monitor failure, not semantic acceptance)
- `timer/ordinary-gc-v19-build.log`, `timer/ordinary-gc-v19-run.log`
- `timer/native-acceptance-v20.log` (all three linked native timer gates pass)
- `timer/native-acceptance-v24.log` (all three linked native timer gates pass)
- `timer/complete-acceptance-v27.log`, `timer/complete-acceptance-v27-sha256.txt` (six native/WASI strict gates pass)
- `timer/ordinary-gc-v25-cexit42-run.log` (ordinary WASI core reaches exact completion marker)
- `metadata/positional-v25-build.log`, `metadata/positional-v25-run.log`
- `metadata/archive-and-c-bridge.log`
- `metadata/gc-observation-race.log`
- `metadata/gc-observer-adapter-race.log`
- `timer/observer-race.log`
- `timer/fake-queue-race.log`
- `timer/source-gates.log`

The following historical paths identify the earlier runs, but their `/tmp`
artifacts were removed before resuming on 2026-10-15; they cannot be reopened
or treated as completed fresh-suite evidence:

- `/tmp/llgo-runtime-tests-v4.log`
- `/tmp/llgo-llgoext-tests-v7.log`
- `/tmp/llgo-foreign-fault-acceptance-v7.log`
- `/tmp/llgo-std-runtime-acceptance.log`
- `/tmp/llgo-test-go-all-v7.log`
- `/tmp/llgo-test-go-resume-v7-v11.log`
- `/tmp/llgo-coro-v16-hello-channel.log`
- `/tmp/llgo-nil-trace-unit.log`
- `/tmp/llgo-nil-trace-v17-e2e.log`
- `/tmp/llgo-test-go-snapshot-v17.log` (interrupted; final result unavailable)
- `/tmp/llgo-std-os-snapshot-v17.log`
- `/tmp/llgo-std-net-snapshot-v17.log`
- `/tmp/llgo-synctest-objects-v17-run.log`
- `/tmp/llgo-coro-weak-native.log`
- `/tmp/llgo-coro-weak-wasip1-run.log`
- `/tmp/llgo-coro-cl-cleanup-regression.log`
- `/tmp/llgo-coro-cl-focused-rerun.log`
- `/tmp/llgo-coro-demos-v7-core-summary.log`
- `/tmp/llgo-coro-core-host-20261013.log`
- `/tmp/llgo-coro-codegen-gates-20261013.log`
