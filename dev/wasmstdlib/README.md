# R4 standard-library behavior and reference hosts

## Full test audit (in progress)

`-full` discovers test packages throughout `test/`, independently of build tags,
and continues after individual package failures. It adds LLGo `GJS` and `GWASI`
source profiles to the five rows below; these are not the official Go reference
compiler and do not yet establish official-Go ABI compatibility.

```sh
go run ./dev/wasmstdlib -full -profile EC32 -llgo /path/to/llgo -report /tmp/full.json
```

The full audit requires GNU `timeout`. Each package has a five-minute build/run
budget and a 60-second test deadline. LLGo compilation cache is reused within a
job, but test execution is uncached. `-shard 0 -shards 2` partitions the sorted
inventory; `other-shard` is not a pass. CI leaves matrix parallelism to GitHub's
available concurrency and preserves JSON accounting and individual package logs
even after failures. Per-runner process and memory limits remain in place.
The normally pattern-excluded `_stress/runtime/timer` package is named
explicitly and runs with `LLGO_STRESS_PROFILE=quick` inside the same shards.

Unreviewed source-excluded packages remain unresolved and make the audit fail.
Reviewed exclusions are reported as `not-applicable` with a package- and
profile-specific reason: OS-only `plugin`, `syscall`, and `test/windows` tests;
native-only CPU-profiler, BDWGC-finalizer, and signal stress suites; plus
`test/cgo` and `test/std/runtime/cgo` on all wasm profiles, because Go's cgo
frontend does not support `GOARCH=wasm`. These exclusions do not waive LLGo's
Emscripten/WASI C interoperability or handle/host-boundary behavior: dedicated
target tests cover those contracts. JS excludes the heap-dump descriptor test because Go's
runtime fatally rejects heap-dump writes to file descriptors above stderr;
WASI and native profiles retain it. Stack reporting, crash-output error
contracts, and the remaining debug APIs still execute on JS. Binary-parser
tests use small, generated ELF, Mach-O, and PE inputs with real code and DWARF,
without requiring a subprocess compiler inside the wasm guest. The host-side
`test/goroot` package is reported as `separate-suite`: the same workflow
first executes two startup sentinels (`bom.go` and `helloworld.go`) on every
LLGo wasm profile and the original generated `fixedbugs/issue5162.go` on GWASI
to catch excessive pre-Asyncify inlining, then unlocks the four-shard GOROOT matrix only if all
sentinels pass. WC32 and GWASI also execute typed reflection bridge regressions
before unlocking the full matrix: calls, MakeFunc, deferred recovery, variadic
arguments, interface results, and GC-rooted callback lifetimes. The full matrix recursively discovers `run`, `runoutput`,
`buildrun`, `rundir`, `runindir`, `buildrundir`, and `errorcheckandrundir` cases.
The last category performs both diagnostic checks and program execution; a
diagnostic mismatch does not bypass the build/run comparison. It does not claim
coverage of compiler-diagnostic-only `compile` / `errorcheck` directives.
The earlier `ci` discovery mode omitted 128 runnable cases per profile in
Go 1.27; changing the discovery mode preserves the existing shard count.

The seven-directive inventory selects 1,152 cases per profile in Go 1.27.

Wasm host tests distinguish unsupported OS facilities from implemented APIs.
Signal tests cover registration, rejection of nil channels and context
cancellation, not native signal delivery. They do not require reproducing Go's
zero-length signal-table panic from `Ignored(os.Interrupt)`. CPU profiler tests
retain Start/Stop/restart and profile metadata checks on wasm, where official
Go has no CPU sampling timer; native profiles still require sampled symbols.
Default archive import resolution checks the unavailable host-compiler error,
and a separate source importer test type-checks real fixture files in the guest.

Each GOROOT shard uploads an incremental JSON report alongside its full log.
Actual `pass`, `fail`, and `not-run` counts remain distinct, and a timeout
leaves `complete: false`. Failed build/run diagnostics are retained per case.
Native host-safety skips and general native GC/known-failure expectations are
not automatically accepted as wasm exclusions: a selected wasm case failure
fails the shard even if it matches a general expectation. Any future wasm
exclusion needs an explicit profile-specific justification and separate
accounting; it must not be counted as a compatibility pass.

Browser acceptance runs the timer/process-state fixture on GJS, EC32, and EC64
in Chrome as an independent job. It copies `lib/wasm/wasm_exec.js` from the
selected Go toolchain and loads it before each LLGo module, so the `js/wasm`
standard library sees the same browser `fs`, `process`, and `path` host contract
as official Go instead of an LLGo-maintained approximation. It requires the
final `wasm timers ok` marker as well as successful module initialization.
Asyncify success exit signals are handled without ignoring aborts, nonzero
exits, or asynchronous failures. Harness self-tests include a delayed failure
after the marker and a blocked renderer with a wall-clock deadline. These
self-tests do not substitute for execution of the actual LLGo artifacts.

Interrupted audits retain an `incomplete` result. Passing every package and
GOROOT shard, reviewing exclusions, and separately covering browser execution
are prerequisites for R4 completion.

## GJS source reuse and host compatibility

Raw `GOOS=js GOARCH=wasm` builds retain the selected GOROOT's `syscall/js`
`js.go` and `func.go`. A source overlay replaces only bodyless host imports
and event registration; Value conversion, type checks, KeepAlive/finalizer
calls, the Func registry, and public error behavior are not forked. The host
bridge uses Go's NaN-boxed reference format and reference accounting, with an
LLGo-specific fixed-width call frame. Runtime event handling is adapted from
Go's `runtime/lock_js.go`: nested callbacks remain on the invoking G, drain
runnable work before returning to JS, and permit Go timer/channel waits.
Waiting inside a callback for an asynchronous JS event still deadlocks, as
documented by Go's `syscall/js.FuncOf` contract.

The GJS sentinel job runs the entire `internal/build/testdata/wasm-test` package
with both official Go and LLGo. It checks process status, the terminal PASS
marker, and fatal/failure output, and uploads both logs. Regressions include
Go's interleaved-callback case, Unicode/NUL strings, identity and NaN, JS
exceptions, typed byte copies, callback results, nested goroutine switching,
timer waits, and exactly-once JS side effects. The existing GJS browser fixture
also exercises nested blocking callbacks and NUL strings before its marker.
It does so during package initialization as well. Fiber storage reserves up
to 15 padding bytes to satisfy the C ABI's 16-byte stack alignment even when
the allocator returns an 8-byte-aligned block; only the original allocation
pointer is freed. Host-side tests cover every alignment residue, rollback,
and size overflow, with a separate storage coverage report in the GJS job.

This is **source/API compatibility work, not official-Go binary ABI parity**:
GJS still uses LLGo's wasm32 data layout and Emscripten/Asyncify module loader,
not Go's 64-bit Go value layout and `gojs` stack-call convention. Named EC32 and
EC64 profiles retain their Emscripten C-ABI `syscall/js` backend. Their passing
tests do not substitute for GJS validation.

## Fixed stack budgets

LLGo's single-worker wasm scheduler currently uses fixed-size C/Fiber and
Asyncify buffers, not Go's automatically growing goroutine stacks. Both default
to 128 KiB on the four wasm32 profiles and 256 KiB on EC64. Suspending a deep Go
call chain saves all active frames in Asyncify, not only the host-call boundary.
The `-pthread-stack-size` override initializes a read-only constant inside the
internal runtime's two-argument `NewProc(fn, arg)`. Larger explicit sizes increase
both buffers. Only the internal runtime package has a direct stack-size cache
input; callers retain their configuration-independent calling convention.

Executable `test/go` regressions retain a depth of 4096 and check ordinary
recursion as well as deep `Gosched`, GC, and caller symbolization. This tests the
default budget; it does not promise arbitrary recursion within a fixed buffer.

## Heap profiling

Single-worker linear-GC profiles implement sampled allocation stacks and weak
live-object accounting for `runtime.MemProfile` and Go's `runtime/pprof` readers.
Explicit frees and collection update previous samples even after sampling is
paused or `MemProfileRate` becomes zero. Snapshots are not limited to 64 buckets.
Compiler instrumentation is enabled by actual profiling consumers; executables
without them discard the sampler and its metadata rather than paying for it.

Focused tests cover sampling, cross-goroutine PC lifetime, finalizers, address
reuse, rate changes, nested pauses and large snapshots. Original Go 1.27
`heapsampling.go` passes locally on GJS, GWASI and EC64; final-head full-profile
CI remains required.

## GC policy

Single-worker linear-GC profiles read `GOGC` before user initialization and apply
`debug.SetGCPercent` to automatic collection. Negative settings and `off` disable
automatic GC; explicit `runtime.GC` remains available. With automatic collection
disabled, exhausted capacity grows or reports OOM instead of silently collecting.
`runtime.MemStats.NextGC` reports the current allocation goal.

Runtime-owned stacks and scheduler records share the collector's arena with Go
objects. Allocating or explicitly releasing these roots adjusts both the live
baseline and the goal, preserving the remaining Go-object allocation budget.
The roots remain fully traced and included in the arena's memory statistics;
collection still includes their scanning cost when computing the next goal.

This is a synchronous, non-moving collector, not Go's concurrent GC algorithm.
Its low-percentage policy guarantees at least 64 KiB of allocation progress
between collections; it does not implement Go's soft memory limit. Focused CI
tests cover startup settings, live changes, disabled growth, explicit collection,
low-percentage roots/finalizers and hard-limit OOM. Passing those tests does not
establish that the original high-goroutine-count GOROOT workloads meet their
execution limits.

## Initial standard-library slice

This acceptance slice runs the complete repository test packages for `errors`,
`sort`, `encoding/binary`, `fmt`, `strconv`, and `io`. It exercises error wrapping
and assertion, reflection-based sorting, byte-order interfaces, structured
encoding, varints, fixed-width and native-width integer boundaries, formatting
and scanning interfaces, readers/writers, and pipe goroutine/timer coordination.
No test-name filter or blanket skip is used; the driver clears inherited
`GOFLAGS` and sets `GOENV=off` so external or saved filters cannot narrow the suite.
Persistent `go env -w` settings are ignored; explicit process environment such as
`GOPROXY` is still available.

| Profile | Compiler and execution contract |
| --- | --- |
| EC32 | LLGo Emscripten wasm32, Node |
| EC64 | LLGo Emscripten Memory64, Node |
| WC32 | LLGo WASI C profile, Wasmtime |
| GJS-reference | Official Go compiler and the selected GOROOT's `go_js_wasm_exec` |
| GWASI-reference | Official Go compiler and the selected GOROOT's `go_wasip1_wasm_exec`, Wasmtime |

The reference rows run **Go output, not LLGo output**. Passing the same behavioral
tests on C profiles does not establish LLGo's official-Go data model, host ABI,
startup/import/export contract, or browser compatibility. The full audit above
therefore validates separate LLGo GJS and GWASI rows, while browser execution is
an independent required job. CI uses the repository-selected Go version and
records it in every report.

From the repository root:

```sh
go test ./dev/wasmstdlib
go run ./dev/wasmstdlib -profile EC64 -llgo /path/to/llgo -report /tmp/ec64.json
go run ./dev/wasmstdlib -profile GJS-reference -report /tmp/go-js.json
```

Each package must exit successfully, print exactly one terminal PASS and its
expected test witness, and contain no failed/skipped test records. A failure
stops that profile; earlier results remain in the JSON report and later packages
remain `not-run`. Test binaries have a 60-second test deadline; CI bounds
compilation and execution together to 25 minutes, or 40 minutes for WC32's six
Binaryen/Asyncify links. LLGo's compiler cache reuses shared dependencies within
the job, as in the full audit; every test package still links and executes with
`-count=1`. Official Go reference commands force `GOMAXPROCS=1` so a host's wider
setting cannot request unsupported OS threads from the Go wasm runtime.

The driver replaces any prior report before preparing the run. Preparation,
execution, or summary-writing errors set `slice_result` to `fail` with a top-level
`reason` when the report is writable. If Go environment or source selection fails,
discovered packages remain `not-run` with an incomplete-selection reason; they are
not classified as source-excluded. Completed package results survive later errors.

The inventory walks `test/std` independently of profile build constraints, then
checks the selected source files using `go list`. Its states are:

- `pass` / `fail`: actual results for this compiler/profile, not inferred from
  another profile or successful compilation alone;
- `not-run`: not yet validated, including packages outside this initial slice;
- `source-excluded`: no tests selected under this Go version and source context.
  This needs explicit classification or replacement tests, not an automatic
  "host-inapplicable" label or compatibility pass.

The source inventory is not an applicability whitelist. In particular, C profile
ABI and LLGo-specific runtime selection still require actual compilation and
execution beyond Go's initial `js/wasm` / `wasip1/wasm` source selection.

`.github/workflows/wasm-stdlib.yml` executes all five rows independently and
uploads their full inventories, including failures and all unvalidated entries.
