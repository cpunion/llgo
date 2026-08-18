# LLGo baseline benchmarks

This suite is the lightweight performance gate for ordinary LLGo changes. It
uses fixed workloads and calibrated benchmarks on Linux and macOS so it
can run on every `main` push and pull request. Branch-only series can be run
explicitly with `workflow_dispatch`, avoiding duplicate push and pull-request
jobs for the same commit. The two native jobs record normalized artifacts; a
trusted `workflow_run` publisher validates and merges both platforms into one
commit, branch, or pull-request series.

The program workloads reuse:

- `benchmark/binary_size/cprintf`: only `lib/c.Printf`;
- `benchmark/binary_size/println`: only the built-in `println`;
- `benchmark/binary_size/fmtprintf`: `fmt.Printf`.

For each workload, the collector performs an unmeasured warm build, then records
the median of five builds and fifteen process runs, file size, executable-code
bytes, allocated non-executable data, and zero-filled data. On ELF, read-only
constants are included in the data bucket; on Mach-O, `__TEXT` constants are
included in the text bucket. The Go benchmark stream discards the first
one-second sample as warmup, then records seven one-second samples of compiler
helpers and LLGo-generated core-language operations: direct/interface calls,
defer, channels, `getg`, and global access.
Goroutine creation keeps its bounded 100-iteration sample and likewise discards
the first of eight runs.

The memory-profile allocation group uses standalone LLGo executables so the
whole-program no-consumer path is measurable independently of the retained
profile paths. Every process disables BDWGC, warms with two million allocations,
then measures forty million escaping 16-byte allocations. Seven independent
processes are recorded per mode in rotated order; an additional discarded
process warms loader state. The reported modes are `NoConsumer`, `Rate0`, and
`Default`.

For pull requests, each platform job checks out the recorded base and current
commits into the same source path, then runs both suites sequentially on the same
runner. The pull request comment compares that pair, avoiding differences from
runner machines and embedded source paths. Dependency setup is shared, and Go's
build cache can be reused by unchanged packages; main pushes still run the suite
only once. Very small changes can still be scheduler, frequency, or thermal
noise and should be confirmed by repeated workflow runs. If a workflow does not
provide a paired result, the publisher falls back to the latest matching `main`
data.

The trusted publisher commits the current result history and generated site to
the `pages` branch of the configured data repository. Every LLGo repository
defaults to `<owner>/llgo-benchmark-data`:

```text
llgo/baseline/series/main/main
llgo/baseline/series/branch/<safe branch identifier>
llgo/baseline/series/pull/<number>
```

The publisher never executes code from the measured revision and pull request
jobs never receive the benchmark repository token. Pull requests receive one
updated summary comment linking to their long-term trend page. If no matching
`main` history exists yet, the pull-request report is still published and
marks every metric as `new`.

Run the complete local collection with the same script as CI:

```sh
benchmark/baseline/run.sh \
  "$PWD" \
  "$PWD/.benchmark/llgo" \
  "$PWD/.benchmark/results"
```

The normalized artifact is `.benchmark/results/benchmark.txt`.
