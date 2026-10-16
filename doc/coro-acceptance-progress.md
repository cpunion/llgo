# LLVM coroutine acceptance status

This ledger records current acceptance, separately from the historical phase
notes in the design documents. Update a row only with a test of the relevant
compiler and generated program. Host-Go unit tests do not establish LLGo
source compatibility, and one platform's result does not establish another's.

## Current checkpoint: 2026-10-16 (Asia/Shanghai)

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
architecture/source-binding gates pass (2.232s). Original TLS/expvar executable
validation is still in progress on immutable v56, not yet a closed failure.

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
| Full `test/go` | Fixed v46: all 266 top-level tests passed, zero failures/skips | Completed 2026-10-16 01:27 UTC, run peak 6454.0 MiB including nested compilers. Exact retained binary and source hashes match; scratch reclaimed after child join. This entire rerun starts from the beginning rather than adopting 233 interrupted passes. Includes main-panic peers, generic/static spawn, rangefunc, C faults, foreign reentry, logical stacks and reflection Call/MakeFunc. Later v47 cleanup and source-front-end changes have separate affected gates, not an implicit full-v46 acceptance transfer. |
| Top-level `_demo/go` | Fixed v46: all 73/73 passed, zero semantic failures | Completed 2026-10-16 01:14 UTC. Source hashes match; private scratch reclaimed after child join and handle checks. Compiler caps are 8GiB except the recorded 12GiB importer override; generated programs retain a 2GiB cap. Disk/monitor interruptions remain in the journal; only completed exact-input package passes were reused. |
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
   Fresh v46 `test/go` passes 266/266 and `_demo/go` passes 73/73; v47 basic
   executable packages pass 68 tests with one helper skip. The v46 stdlib
   evaluation has 60 completed package passes across fixed cohorts after 15
   initial passes; two remaining lanes are active and two are joined/stopped.
   The full v48 GOROOT selection is active, not a complete pass. Keep private
   cache reclamation, unchanged source/compiler
   inputs and every interrupted receipt. A fresh final GOROOT selection is
   still required; focused fixes do not replace the whole selection.
   Rerun affected executable gates after later fixes;
   do not update instruction goldens to hide runtime failures.
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
   still required, without silently adding xfails.
3. Finish public resource-result cancellation handoff. Typed borrowed output
   plus pre-registered ordinary defer is already verified on native and Node;
   public `syscall.Open`/`os.Open` child-to-parent result delivery and platform
   wrapper adoption are not covered by that primitive. Reuse existing
   completion/cleanup ownership, without a second resource-capsule framework.
   The shared result tuple now has a source-expressible anonymous alias; retain
   exact types and use existing compiler-inserted helper demand where needed.
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
