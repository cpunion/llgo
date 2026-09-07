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
inventory; `other-shard` is not a pass. CI runs at most two shards concurrently
and preserves JSON accounting and individual package logs even after failures.
The normally pattern-excluded `_stress/runtime/timer` package is named
explicitly and runs with `LLGO_STRESS_PROFILE=quick` inside the same shards.

Unreviewed source-excluded packages remain unresolved and make the audit fail.
Reviewed exclusions are reported as `not-applicable` with a package- and
profile-specific reason: OS-only `plugin`, `syscall`, and `test/windows` tests;
native-only CPU-profiler, BDWGC-finalizer, and signal stress suites; plus
`test/cgo` on raw profiles where C interop is absent by contract. The same
`test/cgo` suite remains mandatory on EC32, EC64, and WC32. The host-side
`test/goroot` package is reported as `separate-suite`: the same workflow
first executes two startup sentinels (`bom.go` and `helloworld.go`) on every
LLGo wasm profile, then unlocks the four-shard GOROOT matrix only if all
sentinels pass. The full matrix recursively discovers `run`, `runoutput`,
`buildrun`, `rundir`, `runindir`, `buildrundir`, and `errorcheckandrundir` cases.
The last category performs both diagnostic checks and program execution; a
diagnostic mismatch does not bypass the build/run comparison. It does not claim
coverage of compiler-diagnostic-only `compile` / `errorcheck` directives.
The earlier `ci` discovery mode omitted 128 runnable cases per profile in
Go 1.27; changing the discovery mode preserves the existing shard count and
concurrency limit.

The seven-directive inventory selects 1,152 cases per profile in Go 1.27.

Each GOROOT shard uploads an incremental JSON report alongside its full log.
Actual `pass`, `fail`, and `not-run` counts remain distinct, and a timeout
leaves `complete: false`. Failed build/run diagnostics are retained per case.
Native host-safety skips and general native GC/known-failure expectations are
not automatically accepted as wasm exclusions: a selected wasm case failure
fails the shard even if it matches a general expectation. Any future wasm
exclusion needs an explicit profile-specific justification and separate
accounting; it must not be counted as a compatibility pass.

Browser acceptance runs the timer/process-state fixture on raw GJS, EC32, and
EC64 in Chrome within the existing GJS sentinel job. It requires the final
`wasm timers ok` marker as well as successful module initialization. Asyncify
success exit signals are handled without ignoring aborts, nonzero exits, or
asynchronous failures. Harness self-tests include a delayed failure after the
marker and a blocked renderer with a wall-clock deadline. These self-tests do
not substitute for execution of the actual LLGo artifacts.

Interrupted audits retain an `incomplete` result. Passing every package and
GOROOT shard, reviewing exclusions, and separately covering browser execution
are prerequisites for R4 completion.

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
startup/import/export contract, or browser compatibility. Those remain R4 work.
CI uses the repository-selected Go version and records it in every report.

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
uncached Binaryen/Asyncify builds.

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
