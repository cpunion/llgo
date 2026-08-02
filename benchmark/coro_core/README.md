# Coroutine core comparison workload

`testdata/workload` is one bounded, standard-Go fixture for comparing the
ordinary LLGo backend with the stackless coroutine backend. It deliberately
does not import an LLGo-private runtime package or read private scheduler
counters. A comparison therefore measures the same source-level contract on
both compilers.

The modes separate different costs:

- `idle`: process and scheduler fixed cost;
- `spawn`: bounded goroutine creation and completion throughput;
- `park`: peak resident memory with a known number of live goroutines;
- `handoff`: unbuffered channel scheduling throughput;
- `timers`: concurrent standard-library timer registration and wakeup.

The final output field is workload wall time in nanoseconds, measured inside
the process after argument parsing. It excludes process startup; an external
resource tool should still measure peak RSS for `park`.

Build the two native binaries from independent compiler caches. `LLGO_MAIN`
and `LLGO_CORO` below must name the exact compiler revisions being compared.
Run these commands from `benchmark/coro_core`:

```sh
GOCACHE=/tmp/llgo-main-cache "$LLGO_MAIN" build -o workload-main ./testdata/workload
GOCACHE=/tmp/llgo-coro-cache "$LLGO_CORO" build -o workload-coro ./testdata/workload
```

The fixtures are intentionally under `testdata`, so repository-wide package
patterns do not mistake measurement programs for libraries. Compile both with
the host Go tool as a lightweight source check:

```sh
go test ./testdata/workload ./testdata/wasm
```

Use process-start `GOMAXPROCS=1` for the single-executor comparison. The
runtime `GOMAXPROCS` setter is not used because xgo-dev/llgo#2261 currently
ignores its requested value. Parked-memory comparisons should run the same
binary at 0, 100, 500, and 1,000 goroutines and report both peak RSS and the
slope relative to `idle`; this keeps fixed native fleet storage separate from
the incremental stackless-frame cost.

For example, after building the same source with each compiler:

```sh
GOMAXPROCS=1 ./workload-main park 1000 1
GOMAXPROCS=1 ./workload-coro park 1000 1
```

Keep the native comparison at or below 1,000 simultaneously live goroutines:
the current main backend maps each goroutine to a pthread. Larger coroutine-only
runs are useful scalability evidence but are not a like-for-like ratio.

Run throughput modes repeatedly with fixed iteration counts rather than for a
fixed duration. The checked-in baseline uses five runs and reports the median:

```sh
GOMAXPROCS=1 ./workload-main spawn 100 100
GOMAXPROCS=1 ./workload-main handoff 5000 1
GOMAXPROCS=1 ./workload-main timers 100 10
```

`testdata/wasm` is the fixed portable subset used for a WASI command. It
combines goroutine, channel, two-event `select`, timer cancellation, and an
actual timer wake. The parameterized native fixture currently reaches a known
general interface-effect gap through `os.Args`; the WASI fixture intentionally
does not hide that gap by pretending its larger `os` dependency closure works.

Build and validate the portable artifact with a separate cache as well:

```sh
GOCACHE=/tmp/llgo-coro-wasi-cache "$LLGO_CORO" build \
  -target=wasip1 -o core-coro.wasm ./testdata/wasm
wasmtime core-coro.wasm
wasm-objdump -h core-coro.wasm
```

The measured results and revision identities live in
[`doc/coro-performance-baseline.md`](../../doc/coro-performance-baseline.md).
