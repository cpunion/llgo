# Coroutine core comparison workloads

The fixtures in this directory are bounded standard-Go programs. Go gc, the
ordinary LLGo backend, and the stackless coroutine backend compile the exact
same source; the programs do not import an LLGo-private runtime package or read
private scheduler counters. A comparison therefore measures the same
source-level contract rather than two benchmark implementations.

`testdata/workload` keeps its dependency closure small and separates core
runtime costs:

- `idle`: process and scheduler fixed cost;
- `compute`: sequential non-inlined integer calls and loop safepoints;
- `parallel`: the same compute kernel split across a requested number of
  goroutines;
- `buffered`: capacity-one channel send/receive fast paths;
- `select`: a two-ready-receive `select` plus draining the other channel;
- `spawn`: bounded goroutine creation and completion throughput;
- `park`: peak resident memory with a known number of live goroutines;
- `handoff`: unbuffered channel scheduling throughput;
- `timers`: concurrent standard-library timer registration and wakeup.

`testdata/io_workload` is separate so importing `os`, `io`, and `net` does not
pollute the core artifact. Its modes are:

- `file`: one persistent temporary file, with cache-hot 4 KiB
  seek/write/seek/read round trips;
- `tcp`: one persistent loopback TCP connection, with 4 KiB request/echo round
  trips between two goroutines.

`testdata/preempt_timer` is a bounded progress gate: one goroutine sleeps on a
standard-library timer while the sole runnable goroutine executes a pure
compute loop. The timer must wake by compiler safepoint preemption before the
100-million-iteration guard is exhausted. This catches executor-service
optimizations which accidentally rely only on runnable peers or callbacks and
therefore starve elapsed timer/poll sources.

The final output field is workload wall time in nanoseconds, measured inside
the process after argument parsing. It excludes process startup; an external
resource tool should still measure peak RSS for `park`.

Build native binaries from independent compiler caches. For a Go comparison,
use `-s -w` on both commands so the file-size result does not give either
toolchain a debug-information advantage. `LLGO_CORO` must name the exact
compiler revision being measured. Run these commands from the repository root:

```sh
GOCACHE=/tmp/coro-go-cache go build -trimpath -ldflags='-s -w' \
  -o /tmp/core-go ./benchmark/coro_core/testdata/workload
GOCACHE=/tmp/coro-llgo-cache "$LLGO_CORO" build -trimpath -ldflags='-s -w' \
  -o /tmp/core-llgo ./benchmark/coro_core/testdata/workload

GOCACHE=/tmp/coro-go-cache go build -trimpath -ldflags='-s -w' \
  -o /tmp/io-go ./benchmark/coro_core/testdata/io_workload
GOCACHE=/tmp/coro-llgo-cache "$LLGO_CORO" build -trimpath -ldflags='-s -w' \
  -o /tmp/io-llgo ./benchmark/coro_core/testdata/io_workload
```

The fixtures are intentionally under `testdata`, so repository-wide package
patterns do not mistake measurement programs for libraries. Compile both with
the host Go tool as a lightweight source check:

```sh
go test \
  ./benchmark/coro_core/testdata/workload \
  ./benchmark/coro_core/testdata/io_workload \
  ./benchmark/coro_core/testdata/preempt_timer \
  ./benchmark/coro_core/testdata/wasm
```

Use process-start `GOMAXPROCS=1` for the single-executor comparison. Measure
`parallel` separately with process-start values 1 and 4; do not mix its
parallel speedup with the single-executor latency ratios. Parked-memory
comparisons should run the same binary at 0, 1,000, and 5,000 goroutines and
report both peak RSS and the slope relative to `idle`; this keeps fixed native
fleet storage separate from incremental goroutine/frame storage.

For example, after building the same source with each compiler:

```sh
GOMAXPROCS=1 /tmp/core-go park 1000 1
GOMAXPROCS=1 /tmp/core-llgo park 1000 1

GOMAXPROCS=1 /tmp/core-go parallel 4 1000000
GOMAXPROCS=4 /tmp/core-go parallel 4 1000000
GOMAXPROCS=1 /tmp/core-llgo parallel 4 1000000
GOMAXPROCS=4 /tmp/core-llgo parallel 4 1000000
```

Run throughput modes repeatedly with fixed iteration counts rather than for a
fixed duration. Interleave compilers, reverse their order every sample, and
report the median and complete range of at least seven runs. The checked-in Go
comparison uses these bounded arguments:

```sh
GOMAXPROCS=1 /tmp/core-go compute 5000000 1
GOMAXPROCS=1 /tmp/core-go buffered 1000000 1
GOMAXPROCS=1 /tmp/core-go select 500000 1
GOMAXPROCS=1 /tmp/core-go spawn 100 100
GOMAXPROCS=1 /tmp/core-go handoff 5000 1
GOMAXPROCS=1 /tmp/core-go timers 100 10
GOMAXPROCS=1 /tmp/io-go file 500 1
GOMAXPROCS=1 /tmp/io-go tcp 500 1
```

Repeat the same commands with the matching LLGo binaries. Use a process
timeout and an external process-group RSS limit when automating them. The
fixtures also reject a `count * rounds` product above 100 million; that source
bound and the 10,000-live-goroutine cap are not substitutes for the external
hard limit.

`testdata/wasm` is the fixed portable subset used for a WASI command. It
combines goroutine, channel, two-event `select`, timer cancellation, and an
actual timer wake. The parameterized native fixture currently reaches a known
general interface-effect gap through `os.Args`; the WASI fixture intentionally
does not hide that gap by pretending its larger `os` dependency closure works.

Build and validate the portable artifact with a separate cache as well:

```sh
GOCACHE=/tmp/llgo-coro-wasi-cache "$LLGO_CORO" build \
  -target=wasip1 -o /tmp/core-coro.wasm \
  ./benchmark/coro_core/testdata/wasm
wasmtime /tmp/core-coro.wasm
wasm-objdump -h /tmp/core-coro.wasm
```

The measured results and revision identities live in
[`doc/coro-performance-baseline.md`](../../doc/coro-performance-baseline.md).
