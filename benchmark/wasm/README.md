# WebAssembly build and size benchmarks

The harness builds the existing `benchmark/binary_size` examples `cprintf`,
`println`, and `fmtprintf` with LLGo for all five current profiles: `js`,
`wasip1`, EC32, EC64, and WC32. Each example/profile records the Wasm module
size, generated JavaScript glue size (zero when absent), and build time.
These are build/size measurements, not runtime or official-Go ABI acceptance.

The JS glue measurement includes every required browser host sidecar, including
`wasm_fs.js`; a source revision that provides that host file must publish it or
the benchmark fails. Official Go size references cover `println` and `fmtprintf`
on `js/wasm` and `wasip1/wasm`. There is no official-Go `cprintf` reference:
that example calls C `printf` through LLGo's C interop, which official Go does
not provide on wasm.

The existing unqualified metric names continue to mean `println`, preserving
its benchmark history. New `cprintf/` and `fmtprintf/` metric prefixes identify
the other examples. Artifacts are stored separately for every example,
profile, and compiler.

From the repository root, with the usual LLVM, Emscripten and Binaryen tools:

```sh
benchmark/wasm/run.sh "$PWD" /tmp/llgo-wasm-bench /tmp/llgo-wasm-bench-results
```

The result directory is replaced on each run; use a dedicated output directory.
To reuse a compiler already built for the source revision:

```sh
go run ./benchmark/wasm -root "$PWD" -llgo /path/to/llgo \
  -out /tmp/llgo-wasm-bench-results -build-runs 1
```

Each LLGo example/profile has one warm-up build, followed by measured builds.
The CLI defaults to three measured builds and reports their median. The CI
wrapper uses `LLGO_WASM_BENCH_BUILD_RUNS=1`: one warm-up plus one measured build
for each of 15 cases (30 LLGo builds per revision, versus the previous 20 for
five println cases). Exact output-size coverage expands without adding CI jobs
or repeating dependency installation. A single build-time sample is noisier
than the default three-sample median; base and head use the same harness and
setting, sequentially on the same runner. Go references are built once each.

The existing WebAssembly benchmark job tests this harness and measures both PR
base and head, publishing their results through `.github/llgo-wasm-benchmark.yml`.
