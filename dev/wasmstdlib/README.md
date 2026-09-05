# R4 standard-library behavior and reference hosts

This first acceptance slice runs the complete repository test packages for
`errors`, `sort`, and `encoding/binary`. It exercises error wrapping/assertion,
reflection-based sorting, byte-order interfaces, structured encoding, varints,
and fixed-width integer boundaries. No test-name filter or blanket skip is used;
the driver clears inherited `GOFLAGS` and sets `GOENV=off` so external or saved
filters cannot narrow the suite. Persistent `go env -w` settings are ignored;
explicit process environment such as `GOPROXY` is still available.

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
remain `not-run`. Test binaries have a 60-second test deadline; the CI job bounds
compilation and execution together to 25 minutes.

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
