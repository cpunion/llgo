# LLVM coroutine performance baseline

This document records a reproducible local comparison between the current
stackless coroutine prototype and LLGo `main`. It is a directional engineering
baseline, not a cross-machine performance claim.

## Compared revisions and environment

- original LLGo `main` baseline: `ab2fe9c81523`
- original coroutine baseline: `15feb5690677`, plus exact-interface devirtualization,
  CFG-based preemption safepoints, and the `Tfn`/`Ifn` ABI correction
- incremental emission baseline: `b372ef10d024` (`cpunion/llgo:llvm-coro`)
- Go 1.26.5, LLVM 22.1.8, Darwin arm64, Apple M4 Max, 16 logical CPUs
- cold compiler processes were limited to 4 GiB; generated programs were
  limited to 1 or 2 GiB depending on the workload
- timings below are medians unless a range is shown

The same source fixtures were compiled by both compilers. Native executable
footprints use Mach-O section sizes. A benchmark loop is itself a coroutine
preemption boundary, so the single-call and 16-call batch results are reported
separately.

The runtime currently ignores the requested argument to `GOMAXPROCS`; this is
tracked independently as
[xgo-dev/llgo#2261](https://github.com/xgo-dev/llgo/issues/2261) with a minimal
reproducer. Therefore the original throughput table is a 16-P directional
comparison, not a strict single-P scheduler measurement. Emission size and
structural lifecycle counts are unaffected by that issue.

When comparing uncommitted compiler variants, give each variant an independent
empty `GOCACHE`. The development compiler identity is revision-based, so `-a`
alone can still reuse package artifacts produced by a different dirty-tree
variant and invalidate a code-size comparison.

## Same-source Go gc checkpoint

The first direct Go comparison uses coroutine base `56154d44a` and the exact
fixtures committed as `6edd888bc`. Go gc and LLGo compile the same ordinary Go
sources, including the standard `os.File` and loopback `net.TCPConn` paths;
there is no LLGo-only timing or scheduler hook. Both builds use `-trimpath` and
`-ldflags='-s -w'` with independent caches. The host is Darwin arm64 on an
Apple M4 Max with Go 1.26.5 and LLVM 19.1.7.

This is deliberately a directional checkpoint. The host load average remained
between roughly 26 and 32, with unrelated media analysis, filesystem indexing,
and a VM active throughout the final run. Each throughput row is nevertheless
an AB/BA-interleaved median of seven process runs; the complete min/max range is
shown so the noise is visible. Process-start `GOMAXPROCS=1` fixes the
single-executor rows, and the elapsed time is measured inside the common
fixture after argument parsing. A quiet paired runner must reproduce the
ratios before they become regression budgets.

### Single-executor throughput

| Same-source workload | Go gc median [range] | LLGo coroutine median [range] | LLGo / Go |
| --- | ---: | ---: | ---: |
| 5,000,000 sequential arithmetic calls | 8.374 ms [7.718, 8.702] | 185.602 ms [182.896, 187.887] | 22.17x |
| 1,000,000 buffered channel round trips | 16.359 ms [15.967, 16.736] | 54.780 ms [54.132, 55.501] | 3.35x |
| 500,000 two-ready-case selects | 26.338 ms [25.799, 27.301] | 40.755 ms [39.729, 41.796] | 1.55x |
| 10,000 created and joined goroutines | 0.961 ms [0.908, 1.051] | 143.423 ms [140.872, 145.706] | 149.32x |
| 5,000 unbuffered request/ack handoffs | 0.771 ms [0.743, 0.828] | 82.289 ms [80.768, 83.903] | 106.67x |
| 100 concurrent 1 ms timers, 10 rounds | 12.276 ms [12.011, 12.589] | 45.826 ms [44.801, 46.551] | 3.73x |
| 500 cache-hot 4 KiB file round trips | 1.047 ms [0.927, 1.123] | 59.962 ms [57.768, 62.621] | 57.25x |
| 500 loopback 4 KiB TCP echo round trips | 9.131 ms [6.461, 20.257] | 63.585 ms [60.441, 64.855] | 6.96x |

The file round trip is seek/write/seek/read on one persistent temporary file,
so it intentionally measures four standard-library transitions per iteration
without storage durability latency. The timer row contains real 1 ms waits and
is not a synthetic per-timer latency. The TCP row keeps one connection and one
server goroutine alive; it excludes listen/dial setup from each operation but
includes the normal standard-library poll path.

The relatively small buffered-channel and ready-select ratios establish that
their non-parking mechanics are already viable. The dominant measured costs
are instead task creation, runnable return/handoff, and the regular-file worker
transition. Sequential compute also exposes the current backedge
safepoint/requeue tax. These are runtime-core or whole-program emission targets,
not reasons to add library-specific lowering.

### Parallel execution

The parallel fixture runs four goroutines, each performing 1,000,000 instances
of the same arithmetic kernel and producing the same checksum on both
compilers:

| Process-start quota | Go gc median [range] | LLGo coroutine median [range] | LLGo / Go |
| --- | ---: | ---: | ---: |
| `GOMAXPROCS=1` | 6.784 ms [6.728, 6.927] | 348.080 ms [342.898, 360.002] | 51.31x |
| `GOMAXPROCS=4` | 1.713 ms [1.685, 1.786] | 48.183 ms [44.158, 50.049] | 28.12x |

Go improves by 3.96x and LLGo by 7.22x. The LLGo value greater than four is
not a claim of superlinear CPU execution: changing the quota also removes much
of the single-route runnable/poll traffic between four managed tasks. It does
show that the native fleet performs real parallel progress, while identifying
single-P task switching as a separate large overhead that a pure arithmetic
speedup cannot explain.

### Parked-goroutine RSS

Peak RSS is the median of five AB/BA-interleaved `/usr/bin/time -l` process
runs. Ranges are bytes, and the fixed process cost is deliberately shown rather
than hidden in a per-G number:

| Live parked G | Go gc median [range] | LLGo coroutine median [range] |
| ---: | ---: | ---: |
| 0 | 3,571,712 [3,522,560, 3,653,632] | 8,470,528 [8,437,760, 8,470,528] |
| 1,000 | 6,455,296 [6,389,760, 6,488,064] | 12,632,064 [12,238,848, 13,320,192] |
| 5,000 | 17,596,416 [17,563,648, 17,629,184] | 37,634,048 [37,224,448, 37,650,432] |

The 0-to-1,000 slopes are about 2,884 bytes/G for Go and 4,162 bytes/G for
LLGo. At 5,000 the endpoint slopes are about 2,805 and 5,833 bytes/G. Thus the
current stackless implementation does **not** yet have an incremental resident
memory advantage over Go's growable stacks; it uses about 1.44x more per G at
1,000 and 2.08x more at 5,000, in addition to a 4.90 MB higher fixed cost. Its
demonstrated memory advantage remains relative to LLGo's thread-per-goroutine
backend, while fixed-stack independence and target portability remain separate
architectural benefits. Frame-size census, allocation reuse, and parked wait
state are required before making a memory-efficiency claim against Go.

### Native artifact footprint

Both executables are stripped through their compiler's `-s -w` path. Mach-O
`__TEXT` below includes all allocated text/constant/pclntab sections; data and
zero-fill use the same accounting as the repository baseline collector.

| Fixture / metric | Go gc | LLGo coroutine | Ratio |
| --- | ---: | ---: | ---: |
| core file bytes | 1,488,034 | 4,859,920 | 3.27x |
| core `__TEXT` bytes | 1,231,198 | 2,981,851 | 2.42x |
| core data bytes | 195,156 | 359,424 | 1.84x |
| core zero-fill bytes | 158,552 | 974,935 | 6.15x |
| file/TCP file bytes | 1,797,730 | 7,346,704 | 4.09x |
| file/TCP `__TEXT` bytes | 1,454,370 | 4,405,943 | 3.03x |
| file/TCP data bytes | 264,928 | 584,896 | 2.21x |
| file/TCP zero-fill bytes | 159,064 | 977,127 | 6.14x |

This comparison confirms compatibility and exposes a useful optimization
order, but it does not yet demonstrate a general performance win over Go:

1. reduce safepoint/runnable-return traffic and make channel handoff transfer a
   continuation without repeated global scheduling;
2. pool or otherwise reduce spawn/frame lifecycle allocation and measure the
   exact parked-frame layout;
3. reduce regular-file worker submit/completion round trips in the common
   fast-completion case while retaining the unified blocking contract;
4. continue whole-program outcome/plain emission work to reduce both sequential
   call overhead and the 2.4x--3.0x allocated `__TEXT` gap;
5. rerun this exact source on a quiet pinned native host, then add embedded/WASM
   artifact and linear-memory measurements rather than projecting native RSS
   onto those targets.

## Exact same-source checkpoint before the main sync

The current comparison uses the bounded standard-Go fixtures under
`benchmark/coro_core`, exact upstream `xgo-dev/llgo` main `60c30f2ff`, and
`cpunion/llgo:llvm-coro` `b573dad03`. Both compiler binaries were rebuilt from
their clean worktrees and used independent caches. The host was Darwin arm64
on an Apple M4 Max with Go 1.26.5 and LLVM 19.1.7. Both native binaries compile
and run the same source. Compiler RSS is deliberately not a decision metric in
this checkpoint.

### Native artifact cost

| Metric | exact `main` | coroutine | Ratio |
| --- | ---: | ---: | ---: |
| File bytes | 1,363,680 | 5,020,960 | 3.68x |
| `__text` bytes | 344,376 | 2,159,512 | 6.27x |
| zero-fill bytes | 284,971 | 11,380,814 | 39.94x |
| linked resume entries | 0 | 1,498 | — |

The coroutine zero-fill total is `__common + __bss`; the main total also
contains 32 bytes of `__thread_bss`. The coroutine binary's 11.27 MB
`__common` is primarily fixed native fleet/event storage. It is not stored in
the executable and is not a stackless frame cost, but it is a real native
process and embedded-profile deficit.

### Fixed-iteration native behavior

All runs started with `GOMAXPROCS=1`. Each cell below is the median of five
complete process runs, using the workload's internal elapsed time.

| Workload | exact `main` | coroutine | Result |
| --- | ---: | ---: | ---: |
| spawn 100 x 100 rounds (10,000 G) | 8.336 s | 0.776 s | coroutine 10.74x faster |
| 5,000 unbuffered handoff round trips | 235.4 ms | 1.213 s | coroutine 5.15x slower |
| 100 concurrent 1 ms timers x 10 rounds | 92.79 ms | 136.09 ms | coroutine 1.47x slower |

The spawn case exposes the main backend's thread-per-goroutine cost and the
stackless backend's intended scaling advantage. The handoff result identifies
channel wake/schedule transfer as the largest measured runtime hotspot. Timer
registration and wakeup are already within the same order of magnitude; the
batch contains concurrent 1 ms lower bounds, so dividing the total into a
synthetic per-timer latency would be misleading.

### Parked-frame memory slope

Peak RSS is the median of three `/usr/bin/time -l` process runs. Bytes are
reported directly so the fixed-cost subtraction is reproducible.

| Live parked G | exact `main` RSS | coroutine RSS |
| ---: | ---: | ---: |
| 0 | 3,801,088 | 22,822,912 |
| 100 | 6,766,592 | 22,921,216 |
| 500 | 15,663,104 | 24,608,768 |
| 1,000 | 26,345,472 | 26,853,376 |

Using the 0-to-1,000 endpoint, main grows by about 22,544 bytes/G and the
coroutine runtime by about 4,030 bytes/G: stackless frames use 5.59x less
incremental resident memory. The approximately 19 MB higher fixed native cost
puts the observed crossover near 1,027 live G. These two facts must remain
separate: stackless storage is working, while the default native fleet is too
large for low-concurrency and embedded deployments.

### WASI portability and current limits

The fixed portable fixture builds and exits successfully under Wasmtime using
the coroutine compiler without libuv or BDWGC. Its artifact has:

- 1,626,053 file bytes;
- 13 imports and 3,173 functions;
- a 1,193,025-byte code section and 423,208-byte data section;
- 1,024 initial memory pages, or 64 MiB.

The measured Wasmtime process peak RSS was 131,907,584 bytes. That number is
dominated by the Wasmtime process and the compiler-selected 64 MiB linear
memory reservation, so it is not evidence of application-frame memory usage.
Target-configurable initial memory and stack sizing is tracked by
[xgo-dev/llgo#2262](https://github.com/xgo-dev/llgo/issues/2262).

Exact main currently cannot provide a valid like-for-like WASI artifact:
`llgo build -target=wasip1` silently emits native arm64, tracked by
[xgo-dev/llgo#2263](https://github.com/xgo-dev/llgo/issues/2263); forcing
`GOOS=wasip1 GOARCH=wasm` reaches WebAssembly lowering but LLVM 19 crashes
during instruction selection of a standard-library generic map method.

The parameterized native fixture is intentionally not claimed as WASI-clean.
Its `os.Args` closure reaches an exact managed-interface rejection around
`(*io.OffsetWriter).WriteAt` and `(*os.fileWithoutReadFrom).WriteAt`. This is a
general interface/effect transport gap, not a dependency on libuv or BDWGC.

### Promoted-wrapper emission audit

The native coroutine artifact links 440 compiler-generated promoted-method
resume entries, 29.4% of its 1,498 total resume entries. A temporary whole-plan
observer over the same clean build found 457 emitted promoted wrappers:

- 7 are already `NoSuspend`, plain, and have no coroutine ramp;
- 450 carry structured outcome/await, preemption yield, park, or foreign-wait
  semantics and are emitted as coroutines;
- link-time reachability retains 440 of those coroutine wrappers.

ABI type materialization records promoted wrappers because method tables need
stable physical definitions. For an async receiver method the ordinary itab
word is only a validated discriminator; the managed entry performs the actual
structured call. Consequently, treating every method-table reference as a
synchronous function address or dropping its demand would be incorrect.

There is no safe one-line emission relaxation here: the existing planner
already keeps the only seven no-suspend wrappers plain. Reducing the remaining
cohort requires descriptor/archive entry-kind metadata plus complete adapters
for closed and open interface dispatch, raw method tokens, reflection, panic /
Goexit outcomes, and cross-package consumers. Until those gates close, these
440 ramps remain a quantified next architecture optimization rather than an
unsafe partial change.

### Checkpoint conclusion

The stackless design now has concrete value in two places: 5.59x lower parked-G
incremental RSS and 10.74x faster bounded goroutine creation than exact main.
It also runs the portable goroutine/channel/select/timer fixture on WASI without
the old native runtime libraries. The value is not yet sufficient for a default
replacement: native file/text/fixed-memory costs remain high, unbuffered
handoff is 5.15x slower, general interface effect transport is incomplete, and
WASI reserves 64 MiB by default. The next runtime optimization target is
channel handoff; the next compiler emission target is descriptor-aware managed
entry selection, not another local SSA pattern.

## Post-main-sync verification

The coroutine branch was subsequently synchronized with exact upstream main
`2310ff87f` and merged as `cpunion/llgo:llvm-coro` `48c3119ea`. The coroutine
compiler used for the measurements below was built from the PR tree
`d5d98c75e`; that tree is identical to the merge commit. The same
`benchmark/coro_core` sources and fresh, revision-specific compiler caches were
used again.

The native artifact structure did not grow during the sync:

| Metric | main `2310ff87f` | coroutine `d5d98c75e` | Ratio |
| --- | ---: | ---: | ---: |
| File bytes | 1,363,680 | 5,020,960 | 3.68x |
| `__text` bytes | 344,376 | 2,159,512 | 6.27x |
| zero-fill bytes | 284,971 | 11,380,814 | 39.94x |
| linked resume entries | 0 | 1,498 | — |

Every value is identical to the pre-sync artifact. This is expected: the sync
added method-DCE integration and coroutine correctness adapters, but changed no
runtime source, and method dropping remains a development opt-in.

Peak RSS was remeasured with three complete process runs per point; the table
reports the median:

| Live parked G | main RSS | coroutine RSS |
| ---: | ---: | ---: |
| 0 | 3,817,472 | 22,904,832 |
| 100 | 6,995,968 | 22,937,600 |
| 500 | 15,466,496 | 25,001,984 |
| 1,000 | 25,788,416 | 26,918,912 |

The 0-to-1,000 slopes are about 21,971 bytes/G for main and 4,014 bytes/G for
the coroutine runtime, so stackless frames retain a 5.47x incremental RSS
advantage. The approximately 19.1 MB fixed native scheduler cost moves the
observed crossover to roughly 1,063 live G. This small movement is ordinary
process-RSS noise rather than an architecture change.

The post-sync host had unrelated OrbStack and QEMU processes continuously using
more than six CPU cores. Absolute throughput samples from that interval were
therefore rejected rather than replacing the quiet-host checkpoint above. A
bounded five-run smoke test still preserved all three directions—coroutine
spawn faster, unbuffered handoff slower, and concurrent timers slower—but a new
absolute timing table requires a quiet-host paired run.

The fixed WASI fixture still builds and exits successfully. Its post-sync
artifact has 13 imports, 3,173 functions, a 1,193,025-byte code section, a
422,964-byte data section, and 1,024 initial memory pages. The file is
1,625,809 bytes, 244 bytes smaller than the previous artifact. Exact main still
emits an arm64 Mach-O executable for `-target=wasip1`, so xgo-dev/llgo#2263
remains reproducible.

## Demand-paged native operation catalogs

The next fixed-cost checkpoint is based on `cpunion/llgo:llvm-coro`
`d4e96df25` plus the demand-page change. It uses the same
`benchmark/coro_core/testdata/workload` source, Go 1.26.5, LLVM 19.1.7 and a
fresh compiler cache on Darwin arm64. The pre-change values are the exact
post-main-sync artifact above; both executables run successfully.

| Metric | eager native catalogs | demand-paged catalogs | Delta |
| --- | ---: | ---: | ---: |
| File bytes | 5,020,960 | 5,021,648 | +688 (+0.014%) |
| `__text` bytes | 2,159,512 | 2,159,492 | -20 |
| zero-fill bytes | 11,380,814 | 2,070,270 | -9,310,544 (-81.81%) |
| `coroNativeFleetV1State` | 8,632,560 | 356,528 | -8,276,032 (-95.87%) |
| `coroNativeMDirectoryV1State` | 1,120,040 | 1,120,040 | unchanged |

Zero-fill remains `__common + __bss`. The reduction does not shrink logical
capacity or change the two-word `OperationID` producer ABI. Every P retains one
allocation-free 64-slot inline page. Timer, poll, manual, worker and channel
catalogs then attach stable pages immediately before an irreversible park.
Each source embeds eight directory root pointers rather than all 510 possible
page pointers; one 64-pointer directory block is allocated only when a source
crosses another 64 dynamic pages. Native profile limits remain 4,096 timers,
1,024 poll operations, 2,048 manual operations, 1,024 worker operations and
1,024 channel operations. Embedded, WASM and bare-metal profiles may keep only
the inline page or supply their own stable allocation policy.

The 600-way standard-library timer and independent `sync.WaitGroup` fixtures
deterministically exceed the aggregate eight-P inline capacity and both pass a
fresh compile, link and run. The latter exposed an older keyed-registry defect:
its linear scan could be preempted while holding a required-plain spin gate,
leaving another owner spinning forever. The registry now uses one 32-bit
generation/state CAS word per slot and atomic immutable snapshots. Scans may be
preempted safely, no new coroutine annotation is required, and the protocol
does not depend on 64-bit atomics on WASM32 or bare-metal targets. Host race and
JS/WASM tests cover 600 concurrent claims, FIFO sequence wrap, generation
exhaustion, cancellation and producer publication races.

The actual coroutine compiler also builds the portable WASI fixture with this
runtime, not merely its host-Go test adapter. The 1,631,004-byte module has 13
imports, 3,189 functions, a 1,195,693-byte code section and a 425,471-byte data
section, and exits successfully under Wasmtime. This directly covers the LLGo
32-bit atomic lowering selected by the lock-free registry.

Direct runs of the linked 600-way sync and timer fixtures completed in 0.35 s
at 14.7 MB maximum RSS and 0.57 s at 12.9 MB maximum RSS respectively. A noisy
but paired five-run smoke comparison between the demand-page candidate before
and after the lock-free registry gave these medians with `GOMAXPROCS=1`:

| Workload | before lock-free registry | final candidate |
| --- | ---: | ---: |
| spawn 100 x 100 rounds | 188.43 ms | 176.57 ms |
| 5,000 unbuffered handoffs | 147.49 ms | 138.08 ms |
| 100 timers x 10 rounds | 63.06 ms | 61.06 ms |

These samples establish no observed throughput regression; the host had heavy
unrelated VM load, so the apparent improvements are not treated as a portable
performance claim. Fresh standard-library `file` and full TCP/UDP/deadline/DNS
fixtures also pass. At that checkpoint the remaining fixed-memory target was
the unchanged 1.12 MB native M directory; it is independent of
operation-catalog paging and is measured separately below.

## Demand-paged native M ownership

The paired checkpoint uses the same standard-library `sync` acceptance source,
Go 1.26.5 and LLVM 19.1.7 on Darwin arm64. Both artifacts include the managed
keyed-ingress shutdown fix and pass a fresh LLGo compile, link and run. The only
candidate differences are demand-paged Go logical-M storage and demand-allocated
C physical-thread records.

| Metric | fixed 10,000-record storage | demand-allocated storage | Delta |
| --- | ---: | ---: | ---: |
| File bytes | 7,698,896 | 7,256,064 | -442,832 (-5.752%) |
| `__text` bytes | 3,584,424 | 3,585,472 | +1,048 (+0.029%) |
| zero-fill bytes | 2,076,278 | 958,438 | -1,117,840 (-53.839%) |
| `coroNativeMDirectoryV1State` | 1,120,040 | 2,192 | -1,117,848 (-99.804%) |
| C `llgo_coro_fleet_factory_v1` span | 440,160 | 176 | -439,984 (-99.960%) |
| median maximum RSS, 12 interleaved runs | 14,794,752 | 13,262,848 | -1,531,904 (-10.354%) |

The logical limit remains 10,000. The eight initial fleet owners stay inline
and allocation-free. Slots above eight use immutable, CAS-published pages of 64
owners; the static directory holds only 157 page roots, and a reader never
allocates. A one-byte marker claims the page root before allocation, so
concurrent first users allocate exactly one page even in `nogc` builds. Each
attached 64-owner page is 7,168 bytes and remains stable for the process
lifetime, so memory follows the high-water logical replacement depth rather
than the theoretical limit. C still receives only the scalar slot ABI.

The C factory now allocates one 48-byte record only for an actual pthread,
keeps actual records in a mutex-owned intrusive list, and frees a record after
join or self-retirement. The bounded eight-thread standby cache retains its
records while parked. A monotonic nonzero 32-bit physical token is never reused;
exhaustion fails closed. Linear scans are restricted to exceptional physical-M
lifecycle operations and are bounded by the number of actual threads, not the
10,000 logical slots.

Twelve AB/BA-interleaved direct runs at `GOMAXPROCS=2` measured median wall time
0.16 s and median user time 0.24 s for both, retired instructions -0.029%, and
cycles +0.130%. These small deltas establish no observed throughput regression
and are not treated as a portable speedup claim. The independently built
candidate also completed 250 consecutive `GOMAXPROCS=8` sync acceptance runs.
Native replacement, nested replacement, timer, poll and retirement E2E,
plus repeated C factory lifecycle tests, cover the allocation and reclamation
paths.

## Compiler analysis and emission

The final native `syncbench` executable has:

| Metric | `main` | coroutine | Ratio |
| --- | ---: | ---: | ---: |
| File bytes | 5,348,272 | 18,344,416 | 3.43x |
| `__text` bytes | 1,700,732 | 10,494,276 | 6.17x |
| linked resume entries | 0 | 5,169 | — |
| linked destroy entries | 4 unrelated suffix matches | 5,169 | — |
| cold-build peak RSS | 1,151.6 MiB | 3,008.1 MiB | 2.61x |

The minimal concurrent program is a harsher fixed-cost measurement:

| Metric | `main` | coroutine | Ratio |
| --- | ---: | ---: | ---: |
| File bytes | 128,224 | 1,189,008 | 9.27x |
| `__text` bytes | 24,796 | 429,268 | 17.31x |
| linked coroutine ramps | 0 | 47 | — |

This establishes that native code size is still a primary deficit. The large
native zero-fill region is mostly the fixed fleet/M directory and is not stored
in the executable, but it is also unsuitable as the default embedded profile.

An exact guarded slice/string-index experiment was deliberately removed after
measurement. Its complete analysis, digest, and lowering plumbing removed only
three coroutine bodies, reduced text by 8,356 bytes, and reduced the file by
768 bytes. That result did not justify roughly 470 lines of additional
cross-layer machinery.

## Core operation throughput

| Benchmark | `main` | coroutine | Coroutine / `main` |
| --- | ---: | ---: | ---: |
| Direct call, one per poll loop | 1.30 ns/op | 70.42 ns/op | 54.2x |
| Exact interface call, one per poll loop | 3.90 ns/op | 58.20 ns/op | 14.9x |
| Direct call, batch of 16 | 9.52 ns/batch | 43.23 ns/batch | 4.54x |
| Exact interface call, batch of 16 | 49.80 ns/batch | 43.43 ns/batch | 0.87x |
| Buffered channel round trip | 15.71 ns/op | 52.42 ns/op | 3.34x |
| Two-ready-case select | 27.69 ns/op | 63.35 ns/op | 2.29x |
| Unbuffered two-channel handoff | 8.67 µs/op | 61.27 µs/op | 7.07x |

The batch result is the useful call-path measurement: one backedge poll is
amortized across 16 calls. Exact interface devirtualization makes the coroutine
direct and interface batches equivalent, while the single-call result exposes
the current poll/requeue tax. The handoff path remains the largest runtime
hotspot and also reports about 29 KiB/op of allocator traffic versus 384 B/op
on `main`.

Relative to the unoptimized coroutine snapshot, the two compiler changes are
material:

- exact-interface lowering reduced the fixture's interface call from roughly
  6–16 µs/op to about 70 ns/op;
- the CFG safepoint plan reduced direct and exact-interface single-call loops
  from about 70 ns/op to about 35 ns/op in the initial 16-P measurement.

## Stackless concurrency and target portability

For 10,000 joined goroutines, the stackless binary measured
6.35–6.99 µs/op with 65.1 MiB peak RSS. The `main` backend measured
65.7–268.4 µs/op with 498.8 MiB peak RSS in the same run. This workload is
favorable to stackless frames and unfavorable to the current native
thread-per-goroutine backend, but it demonstrates the intended scaling
property.

For parked goroutines:

| Backend | Idle RSS | Parked goroutines | Peak RSS | Approx. incremental RSS |
| --- | ---: | ---: | ---: | ---: |
| `main` | 5.4 MiB | 1,000 | 25.0 MiB | 20.1 KiB/G |
| coroutine | 24.1 MiB | 10,000 | 47.2 MiB | 2.4 KiB/G |

The coroutine runtime has a much higher fixed native scheduler cost but a much
lower incremental cost per parked goroutine. Embedded profiles must remove the
native fleet tax rather than treating this native measurement as target
neutral.

The same minimal concurrent source builds for `wasip1` only on the coroutine
branch:

- output size: 270,709 bytes;
- code section: 257,753 bytes;
- data section: 11,060 bytes;
- 566 functions and 13 WASI imports;
- execution succeeds under Wasmtime at about 18.4 MiB host RSS.

The `main` build attempts to link host libuv and BDWGC archives into WebAssembly
and fails. This is evidence that the coroutine runtime core is independent of
those native libraries; it is not yet evidence of bare-metal completion.
RP2040 still lacks board hooks plus required atomic/compiler-rt/libc support in
both compared configurations.

## Outcome-plain V0 emission result

The full-program plan exposed a higher-leverage opportunity than more local
no-unwind pattern matching. After the narrow index experiment, 571 emitted
coroutines still had exactly `OutcomeStructured` and no real wait, park, or
yield effect. Reverting the three locally proven functions restores that count
to 574 in the final baseline.

These functions must not be reclassified as logically `NoSuspend`: they may
still panic, recover, run defers, or propagate Goexit. The first
`outcome-plain` replacement cohort now implements the separate physical entry
without changing those logical facts:

1. Preserve logical `OutcomeStructured` and `MayUnwind` facts.
2. Select `outcome-plain` only when the function has no `YieldOnly`,
   `AwaitStructured`, `MayPark`, platform/host/foreign wait, recursion, or
   `NeedsPreempt`.
3. V0 accepts only source-call-free, acyclic leaves whose frozen semantic
   recipes are `Debug/Phi/Jump/If/Return/Panic` and whose instruction cost is
   within `MaxPlainInstructions`. Roots, spawn/address/dynamic uses, recursion,
   raw or lowered incoming calls, and all external operations fail closed.
4. The hidden V0 ABI is `(g, out, completion, args...) -> void`.
   `completion` is `{status uint32, typeWord unsafe.Pointer, dataWord
   unsafe.Pointer}`; normal return and panic publish exactly one terminal
   outcome and return synchronously. The status space reserves Goexit for the
   next capability cohort. No LLVM frame, ramp, resume, or destroy entry is
   created.
5. An exact coroutine caller reconciles the immediate return/panic/Goexit
   status inline. `ManagedEntryOutcomePlain` is an explicit frozen plan and
   archive-summary dimension; consumers never infer it from a code pointer.
   An active importer also rejects a producer cost above its own finite
   `MaxPlainInstructions` budget instead of silently extending the no-poll gap.
6. Keep one source body. A boundary adapter may translate the logical outcome,
   but it must not clone the body or fall back to native unwind across a
   stackless boundary.

The implementation is issued by `ProgramIR` from frozen semantic recipes and
uses the same exclusive physical-emission transaction as a full coroutine.
The architecture debt gate records no second plan lookup, body session, or
body-state field. It fixes the legacy coroutine-only lifecycle entry counts at
zero, the shared managed begin/bind/complete consumers at exactly two, and the
exclusive capability arms at exactly `coroutine/outcome`; a renamed or second
session therefore cannot bypass the gate. Plan digest, plan summary and
library-effect schemas were advanced together, and an imported exact call
consumes the producer's published `$outcome` entry.

This cohort also crossed the design document's approximately 1,000-line
production review stop. Relative to `cpunion/llgo:llvm-coro` after PR #110
(`09ecca3bd`), the audited diff is production `+1,173/-125` (net `+1,048`),
tests `+806/-32` (net `+774`) and documentation `+131/-44` (net `+87`). The
review accepted the narrow overage
because the new volume closes one physical capability through graph planning,
canonical digest/summary, archive metadata, owner preflight, ABI emission and
caller reconciliation; it does not add another raw-SSA scan, fixed point,
source-body clone, LLVM-coroutine CFG builder, or emission session. The next
direct-call-DAG cohort must extend these same dimensions and transaction. A
second analysis/emission path is a failed architecture gate, not justified by
another 1,000-line allowance.

The exact native/wasm fixture compares V0 with the previous all-coroutine
emission by setting the leaf budget to `-1` for the baseline and `64` for V0.
Both variants compile the same source and run the same CoroSplit and LLVM O2
pipelines:

| Target | Post-split IR baseline | Post-split IR V0 | O2 object baseline | O2 object V0 |
| --- | ---: | ---: | ---: | ---: |
| Darwin arm64 | 80,833 B | 39,326 B | 4,600 B | 3,008 B (-34.6%) |
| wasm32/WASI | 80,797 B | 39,308 B | 3,485 B | 2,390 B (-31.4%) |

For this one-leaf fixture V0 also removes one frame allocation, one resume
entry, one destroy entry and three parent await-hook references. This is a
deterministic physical-cost gate, not yet a wall-clock throughput claim. The
native return-and-panic E2E additionally compiles, links and executes both
terminal payload/control-flow paths; it does not yet claim traceback-frame
fidelity for the removed leaf. Native and wasm structural tests verify that
CoroSplit cannot manufacture `$outcome.resume` or `$outcome.destroy`.

### Direct-call DAG cohort

The next cohort extends the same ProgramIR, SSA call plan, fixed point,
physical-emission session, completion ABI and archive metadata; it does not add
a second SSA scanner or coroutine CFG builder. A local candidate may add only
ordinary `Call` and `Extract` recipes to the V0 leaf language. Bottom-up
selection requires every counted call to be one exact, closed, static managed
edge whose target already publishes a proven local or imported outcome entry.
`AtomicCostDAG` is the longest path over the frozen ProgramIR CFG, with the
complete transitive callee cost added at each exact source call occurrence. Roots, references,
compiler-lowered incoming calls, open/dynamic targets, recursion, CFG cycles,
cost overflow and budget excess retain the full coroutine.

An outcome DAG forwards the scheduler-owned G, uses caller-owned result and
completion storage, and republishes child Panic or Goexit without scheduling.
A target-layout check rejects the optimization before plan selection when a
call result would exceed the native-stack single-object bound; the full
coroutine fallback continues to use managed result storage. The same proof
class and its content-addressed certificate are carried by plan digest v33,
diagnostic summary v7 and library-effect summary v5, so an imported DAG proof
can close a local DAG and contributes its full producer path certificate.

For the exact `Parent -> Middle -> Leaf` native/wasm fixture, disabling the
budget retains three coroutines while the DAG cohort retains only the root:

| Target | Post-split IR baseline | Post-split IR DAG | O2 object baseline | O2 object DAG |
| --- | ---: | ---: | ---: | ---: |
| Darwin arm64 | 125,975 B | 41,778 B | 6,240 B | 3,104 B (-50.3%) |
| wasm32/WASI | 125,921 B | 41,760 B | 4,720 B | 2,487 B (-47.3%) |

This removes two frames, two resume entries and two destroy entries. Native
and wasm32 verify before and after CoroSplit; the middle outcome body directly
calls the leaf outcome body, contains no coroutine intrinsic or scheduler
await hook, and republishes Panic/Goexit statuses to its parent.

### Path and post-LLVM certificate cohort

ProgramIR now freezes one minimal block projection per defined function:
canonical block/successor indexes, non-debug semantic work per block, and each
ordinary call at its exact source-instruction coordinate. Outcome selection
computes the longest entry-to-terminal path, takes the maximum rather than the
sum across mutually exclusive branches, rejects CFG/call cycles and overflow,
and hashes the projection together with FunctionID, proof class, cost, and the
complete transitive callee certificates. Physical planning reconstructs that
same certificate from the frozen direct-outcome recipes; any logical/physical
edge disagreement fails before emission.

Each emitted local outcome body publishes compiler-only
`llgo.coro.atomic_cost` metadata. Imported outcome callees publish dependency
rows containing their already ABI-checked producer certificate. A fail-closed
LLVM verifier runs both before and after CoroSplit, walks the actual LLVM CFG
and certified direct-call DAG, and rejects cycles, indirect calls, coroutine
intrinsics, unknown helpers, dynamic allocas, unsupported EH/control, and
variable-length memory intrinsics. Constant-length `memset`, `memcpy`, and
`memmove` are accepted with their byte count included in the abstract work
bound. LLVM's scalar integer `umin`, `umax`, `smin`, and `smax` intrinsics are
also accepted as one work unit only when the intrinsic identity, canonical
name, two equal operands, equal result, and at-most-64-bit width all match;
this covers InstCombine's compare/select folding without admitting arbitrary
`llvm.*` declarations. The final funcinfo/pclntab data-only inline-assembly anchor is admitted
only through compiler-injected identity plus a digest of its complete assembly
payload; unmarked inline assembly remains rejected. The deterministic
`llgo.coro.post-llvm-atomic-cost.v1` report binds each semantic certificate to
the observed LLVM maximum before/after CoroSplit and after final optimization
and compiler-owned site insertion.

This closes the structural no-cut certificate used by outcome-plain selection;
it is not yet a target-cycle or wall-clock preemption-latency proof. LLVM work
units are deliberately reported separately from semantic work units. Machine
instruction latency, target-specific library calls/assembly, final machine
stack bytes, interrupts, and scheduler overhead still require the target cost
model and final-code certificate described in the runtime design.

### Scalar and memory recipe cohort

The next incremental cohort broadens the same frozen semantic recipe and
outcome-plain physical transaction; it does not introduce another SSA scan or
emitter. The sole raw-SSA classifier now records whether an instruction can be
executed as bounded, helper-free outcome work. It accepts integer/floating
arithmetic and comparisons, representation-preserving conversions, plus
`FieldAddr`, typed load and store. Integer division/remainder requires the existing dominance
proof that the divisor is non-zero, and a signed shift count must either be an
unsigned value or a non-negative constant. Strings, interfaces, allocations,
dynamic indexing and helper-backed operations remain fail-closed.

Pointer memory access retains Go's recoverable nil-panic semantics without
calling a runtime helper from the certified outcome body. A nil access
publishes a dedicated allocation-free completion status through synchronous
outcome parents; the first full coroutine parent constructs the existing V1
fault payload and runs its normal cleanup/recover path. The completion record
remains three words. A full `fmt.Printf` build initially rejected a direct
fault-payload helper as uncertified, demonstrating that the pre/post-LLVM atomic
cost gate catches an accidental helper edge rather than silently accepting it.
Because this adds a terminal status to the hidden outcome vocabulary, the
archive producer/importer schema is hard-cut from v5 to v6; old libraries fail
schema validation instead of being reinterpreted.

The same physical ABI is now selected when a consumer module first declares an
outcome entry owned by another package. A cross-package pointer-receiver method
with narrow integer input/output gates the exact
`(g, out, completion, receiver, args...) -> void` declaration; this prevents an
ordinary Go declaration from being cached under the outcome symbol before its
call is emitted.

An exact cold-cache A/B used the same compiler toolchain and source
(`fmt.Printf("Hello, world\\n")`) on Darwin arm64 with LLVM 22.1.8. The baseline
was rebuilt from `b372ef10d024`; both executables run successfully.

| Metric | `b372ef10d024` | scalar/memory cohort | Delta |
| --- | ---: | ---: | ---: |
| file bytes | 7,735,328 | 7,714,240 | -21,088 |
| `__text` bytes | 3,669,376 | 3,656,680 | -12,696 |
| read-only const bytes | 1,135,864 | 1,134,200 | -1,664 |
| linked resume entries | 2,318 | 2,307 | -11 |
| linked destroy entries | 2,318 | 2,307 | -11 |

The full plan contains 7,291 functions. This cohort selects seven local leaves
as outcome-plain and leaves 2,531 full coroutines. Six selected leaves were
linked as coroutine bodies in the baseline; their lifecycle pairs disappear.
The changed reachability also lets optimization remove five poll-related
coroutine bodies, for eleven fewer linked lifecycle pairs in total. The seven
selected leaves are `reflect.mapiterelem`, `reflect.mapiterkey`,
`reflect.overflowFloat32`, `io.Size`, `os.sameFile`, `syscall.SetControllen` and
`time.rest`.

There are 116 remaining pure-outcome candidates that fail the current proof.
Their common blockers include 26 allocations, 26 interface constructions, 92
calls, 81 index-address operations and 21 index operations across the candidate
set; only two index-address occurrences have a statically fixed in-range index.
The next useful cohort should therefore freeze target-dependent direct-interface
representation and bounded native-stack allocation facts in ProgramIR. It
should not speculate a larger completion ABI or add a second lowering path.

### Synchronous completion cost: `ken/modconst.go`

Go 1.26.5 `GOROOT/test/ken/modconst.go` is a useful whole-program stress case
for managed calls that almost always complete synchronously. Its integer
remainder loops repeatedly call small test functions, and those functions in
turn used the compiler-inserted `runtime.AssertDivideByZero` helper whenever
the divisor was not statically proven non-zero. Under the original coroutine
lowering, both the source function and the helper were initial-suspended child
coroutines, even though the normal path never suspended.

The first measured replacement cohort keeps the logical helper edge for effect
analysis but freezes an instruction-owned physical recipe. It emits an integer
zero comparison, routes the zero edge through the existing allocation-free V1
explicit-fault payload, and lowers the normal edge with
`BinOpWithNonZeroDivisor`. No helper call or child-await remains at the physical
site. Cleanup and recover reuse the existing fault-payload transaction; a
dominating non-zero proof emits neither helper nor fault edge. Native and
wasm32 pre-/post-CoroSplit IR tests are the regression gate.

The same Darwin arm64 machine, Go 1.26.5 toolchain, source case, 4 GiB hard
process-group RSS limit, and three-minute step timeout produced. The final
column is a fresh semantic run of the general eager-child cohort:

| Metric | `3a57f9a8a` | structured divide fault | + eager child completion |
| --- | ---: | ---: | ---: |
| LLGo build | 22.064 s | 19.947 s | 19.536 s |
| LLGo run | 84.440 s | 43.864 s | 4.636 s |
| peak process-group RSS | not recorded | not recorded | 1,046.0 MiB |
| semantic result | pass | pass | pass |

The general cohort reduces the post-divide-fault run time by 39.228 seconds
(-89.4%), or 79.804 seconds (-94.5%) relative to the original checkpoint. It
preserves one logical function version: each managed child still starts as an
LLVM coroutine, but runs immediately on the current executor until final
suspend or the first real yield/park/await. Synchronous completion uses the
existing completion/destroy protocol. A real suspension conditionally suspends
every generated parent while the temporary native resume chain unwinds, then
the outer scheduler dispatches the deepest pending frame. Native nesting is
bounded at 16 and falls back without mutating the ordinary pending await.

The runtime-core transaction, nested-yield and wrong-child fail-closed tests,
native and wasm32 pre-/post-CoroSplit IR, explicit panic, channel/static-spawn,
production `time.Sleep`, same-M foreign and locked replacement E2Es all pass.
The GOROOT case also exercised the worker/event slow path; this found and fixed
an adapter assumption that `P.action.Handle` must equal the current leaf. Event
adapters now accept only exact completion-owned inline ancestry. This is strong
evidence that mandatory initial suspend no longer imposes a scheduler round
trip on the common synchronous path, but it is not a full GOROOT or platform
acceptance result.

### Compact emission type-graph cohort

Whole-program `go:linkname` pairing previously expanded named/private-mirror
types as recursive strings. A type DAG with one shared child used twice per
level therefore became a complete binary tree even though the source contained
only a linear number of unique type nodes. The resulting multi-megabyte keys
were immediately hashed, but only after the exponential allocation and copy
cost had already occurred; standard-library `mapzero` and `abimethod` builds
could spend minutes in emission-universe preparation before LLVM emission.

The pairing key now freezes a compact ordered type graph. Non-recursive SCCs
contribute Merkle child digests, eliminating irrelevant pointer sharing;
recursive SCCs use a deterministic root-local DFS numbering, retaining exact
cycle topology instead of merging a one-node self-cycle with a two-node cycle.
Named identity and struct-field source metadata remain erased exactly as the
private-mirror ABI requires, while field order, scalar attributes, interface
method identity and all child types remain bound by the digest. Other emission
and C ABI type keys retain their existing representation.

On the deterministic depth-18 shared-child fixture (19 unique type nodes), one
key changed from about 106.5 ms, 405 MB and 2.31 million allocations to about
21 us, 52 KB and 457 allocations on an Apple M4 Max. This microbenchmark is
an algorithmic regression gate, not a cross-machine performance promise. The
previously blocked `mapzero` and `abimethod` package builds now reach FileCheck
in about 54 seconds; their remaining golden-IR differences belong to the
separate coroutine LIT compatibility audit rather than emission analysis.

### Remaining outcome-plain expansion

The original 574-body opportunity is not yet claimed. The current cohorts do
not accept defer/recover bodies, a Goexit producer, function values/interfaces,
or recursive/open-world paths. The next cohorts are:

1. add a target-machine cost/stack model if the structural bound is to become
   a strict wall-clock preemption-latency claim;
2. add exact panic source/trace ownership, Goexit, cleanup/defer and recover
   outcome transactions without cloning source bodies;
3. add descriptor/interface dispatch only after entry-kind metadata and every
   boundary adapter are closed across archives;
4. rerun representative standard-library executable size, direct/interface/
   channel throughput, and parked-frame memory baselines before broadening the
   default cohort.

### Causal channel return-route checkpoint

The next native scheduling checkpoint uses the tree shared by
`cpunion/llgo:llvm-coro` `f32f1195d` and its parent `be1b2daec` as the exact
baseline. The candidate changes only runtime scheduling metadata and policy;
the compiler binary and the standard-Go source in
`benchmark/coro_core/testdata/workload` are unchanged. Both artifacts were
built on Darwin arm64 with Go 1.26.5, LLVM 22.1.8, fresh runtime inputs and the
same LLGo compiler. Throughput values are medians of five interleaved complete
process runs with process-start `GOMAXPROCS=1`.

| Metric | `be1b2daec` | return-route candidate | Delta |
| --- | ---: | ---: | ---: |
| 5,000 unbuffered channel handoffs | 144.722 ms | 81.718 ms | -43.53% |
| spawn 100 x 100 rounds | 147.531 ms | 138.898 ms | -5.85% |
| 100 timers x 10 rounds | 45.560 ms | 44.899 ms | -1.45% |
| file bytes | 4,818,208 | 4,836,496 | +18,288 (+0.38%) |
| Mach-O `__TEXT` | 2,965,504 | 2,981,888 | +16,384 (+0.55%) |
| Mach-O `__DATA` | 1,032,192 | 1,032,192 | unchanged |

Three interleaved 1,000-parked-G runs gave median peak RSS 12,042,240 bytes
before and 12,009,472 bytes after. This is noise-sized evidence of no observed
memory regression, not a claim that the candidate reduces parked-G storage.
The permanent scheduler layouts do not grow: the producer route phase-overlays
the high bits of the existing channel physical word and then the materialized
phase of `ParkState.seed`; current logical-task identity uses the runtime
sidecar's otherwise-idle startup-argument field. Independent foreign-thread
callbacks, timers and I/O
producers retain route zero; synchronous same-G foreign reentry may inherit the
managed resume's route.

The policy is deliberately narrower than work stealing. A materialized channel
continuation can return only to the exact producer route, only after that route
has published runnable demand, and only after source cleanup has removed every
old-P pointer. No alternative route is scanned. A producer which continues
computing therefore cannot capture its peer; failure or contention retains the
ordinary local FIFO. The durable transfer has an explicit core commit which
settles source `readyDebt` only when no local runnable remains.

Core/race tests, the production typed-channel adapter on native and JS/WASM,
the host native-fleet/program adapters, five repeated native spawn/park/
handoff/timer workload passes, and three 100,000-round handoff stress runs all
completed. The standard-Go WASI fixture also compiled to a 1,747,245-byte
module (3,225 functions, 13 imports, 1,304,987-byte code section and
432,327-byte data section) and exited successfully under Wasmtime. These tests
validate the route-zero portable fallback and the native optimization; they do
not replace the final full repository and GOROOT acceptance matrix.

### Bounded preemption checkpoint cohort

The next core optimization separates a compiler safepoint from a full runtime
poll. Previously every selected loop feedback edge called the runtime, checked
multiple atomic scheduler gates, and remained a potential LLVM suspension.
With another runnable G, `BeginRunG` also requested an immediate cut, so a hot
loop could spill its live values and return to the scheduler after every source
iteration.

The candidate retains the same physical safepoint plan but uses the active
frame header's existing `StateID` word as a compiler-private 64-safepoint
countdown. `StateID` is reset on activation and overwritten with the real
resume state before every scheduler-visible suspension, so no frame or G field
is added. The hot edge is a decrement/branch with no runtime call. A full poll
occurs at the stride boundary; a known runnable competitor receives one
64-safepoint slice, while a sole G returns for event-source service after 64
full polls (4,096 source safepoints). Critical exit remains an immediate full
poll and charges one source safepoint. Both bounds are deterministic and need
no clock, signal, TLS, or host thread facility.

The compiler ABI hash now includes the stride. Native and wasm32 regression
tests compare a gated poll against an ungated poll with the same suspend and
require identical CoroSplit frame sizes. On the native workload, representative
frame sizes are unchanged: compute 144 bytes, parked closure 504 bytes, and
spawn 184 bytes. The stripped workload executable grew from 4,859,920 to
4,876,192 bytes (+16,272, +0.33%).

On the same Darwin arm64 host, the old and candidate binaries were retained and
run in the same session with process-start `GOMAXPROCS=1`. The old column is a
five-run median; the candidate compute result is the seven-run AB-paired median
used below, and the remaining candidate results are five-run medians.

| Workload | old coroutine | checkpoint candidate | Change |
| --- | ---: | ---: | ---: |
| 5,000,000 arithmetic calls | 213.920 ms | 9.293 ms | -95.66% (23.02x faster) |
| four x 1,000,000 arithmetic calls, one P | 543.964 ms | 27.040 ms | -95.03% (20.12x faster) |
| spawn 100 x 100 rounds | 190.997 ms | 59.348 ms | -68.93% (3.22x faster) |
| 5,000 unbuffered request/ack handoffs | 104.319 ms | 72.638 ms | -30.37% (1.44x faster) |

Seven interleaved same-source Go comparisons give:

| Workload | Go gc median [range] | checkpoint candidate [range] | LLGo / Go |
| --- | ---: | ---: | ---: |
| 5,000,000 arithmetic calls | 8.251 ms [7.995, 8.498] | 9.293 ms [9.148, 10.256] | 1.13x |
| spawn 100 x 100 rounds | 0.946 ms [0.918, 1.050] | 59.348 ms [57.251, 62.353] | 62.73x |
| 5,000 unbuffered request/ack handoffs | 0.751 ms [0.701, 0.775] | 72.638 ms [71.772, 73.137] | 96.69x |

Thus the basic preemption tax is no longer the dominant sequential-compute
problem. Task/frame lifecycle and channel park/wake remain the dominant common
substrate and are the next optimization gates.

Peak RSS from one bounded run at each parked-G count was:

| parked Gs | Go gc RSS | candidate RSS | Go incremental / G | candidate incremental / G |
| ---: | ---: | ---: | ---: | ---: |
| 0 | 3,555,328 | 7,700,480 | - | - |
| 1,000 | 6,471,680 | 10,600,448 | 2,916 B | 2,900 B |
| 5,000 | 17,580,032 | 25,493,504 | 2,805 B | 3,559 B |
| 10,000 | 31,539,200 | 39,813,120 | 2,798 B | 3,211 B |

The candidate therefore has near-Go incremental storage at 1,000 parked Gs
and narrows the 10,000-G slope to about 15% above Go, but its fixed runtime
footprint is still about 4.1 MB higher and the larger-count slope is not yet an
advantage. The 10,000-G LLGo run also took 3.75 seconds versus about 7 ms for
Go, exposing nonlinear lifecycle/queue work that must be removed before this
prototype can claim a practical large-goroutine advantage.

Two native E2E gates were made independent of the speedup. The foreign-worker
test now runs its compute peer until an explicit stop instead of assuming that
one million atomic increments must outlast a 30 ms worker; the worker event
must therefore preempt genuinely unbounded computation. The channel smoke
accepts sender stage 4 or 5 after an unbuffered rendezvous: the send orders all
work through stage 4, while Go does not require the sender's post-send
continuation to run again before a receiving `main` returns.

### Constant-time spawn-owner checkpoint

The next checkpoint uses `19712185a` as its exact parent. The spawn transaction
gate previously ran the complete ready-queue and scheduler-wait audits from
`CanBeginSpawn`, `BeginSpawn`, and `CommitSpawn`. A burst which accumulated N
children therefore revisited unrelated tasks and wait records for every `go`
statement and made the transaction-validation component O(N^2).

The hot owner gate now proves only O(1) facts: the current resume owns the P,
the ready/park/affected queue endpoints are coherent, and the OS-thread owner
header is valid. Enqueue, park, wake, and affinity operations maintain those
headers while holding that same ownership. Full payload audits remain at
lifecycle, shutdown, transfer, test, and diagnostic boundaries. A regression
test deliberately corrupts distant queue payloads and separately corrupts
each local header, proving that the hot gate does not scan the former and still
fails closed on the latter.

On a concurrently loaded Darwin arm64 host, three interleaved 10,000-parked-G
runs had medians of 7.077 seconds for the parent and 3.184 seconds for the
candidate (2.22x faster). Five interleaved spawn-100-by-100 runs had medians of
124.932 ms and 114.161 ms (8.6% faster); this workload keeps its queues much
shorter. A paired `/usr/bin/time -l` sample reported 40,796,160 versus
40,779,776 bytes peak RSS. The executable changed by only 16 bytes and its
Mach-O text/data sizes were unchanged. These noisy-host measurements establish
the removed complexity and absence of a storage regression, not a stable
throughput promise. The remaining large-G cost is linear channel close/wakeup,
task/frame allocation and runtime-context initialization.

### Lazy panic-snapshot checkpoint

The next memory checkpoint uses `c773ea52e` as its exact parent. Every runtime
G previously embedded a 544-byte `panicPCStore`, including its 64-PC array,
even though an ordinary parked logical G never panics. Replacing that payload
with one pointer removes 536 bytes from every 64-bit runtime context. The
bounded store is allocated and explicitly released only if a managed panic
snapshot is actually requested. Stackless language panics continue to use
their scheduler-owned logical frame trace and therefore do not allocate it.

The legacy native fault callback cannot allocate in signal context. It now
uses an already-owned G store when one exists and otherwise publishes into one
static emergency store. This preserves the pre-existing process-global,
best-effort concurrent-fault scope of the legacy fault source. A normal panic
replaces that emergency attachment with an owned store. Source gates require
the signal callback to use the allocation-free entry, while complete coroutine
profiles retain their signal-stack-free compiler outcome path.

Peak RSS from one bounded, process-start `GOMAXPROCS=1` run was:

| parked Gs | Go gc RSS | `c773ea52e` RSS | lazy snapshot RSS | lazy incremental / G |
| ---: | ---: | ---: | ---: | ---: |
| 0 | 3,555,328 | 7,716,864 | 7,733,248 | - |
| 1,000 | 6,471,680 | 10,665,984 | 9,748,480 | 2,015 B |
| 5,000 | 17,580,032 | 25,526,272 | 20,987,904 | 2,651 B |
| 10,000 | 31,539,200 | 40,779,776 | 32,751,616 | 2,502 B |

At 10,000 parked Gs the candidate removes 8,028,160 bytes (19.7%) from the
parent and is only 1,212,416 bytes (3.8%) above Go in total RSS. Its slope from
idle is about 10.6% below Go's 2,798 bytes/G, so the stackless implementation
now demonstrates a per-goroutine memory advantage even though its larger fixed
runtime still narrowly loses the total-footprint comparison at this scale.

The stripped executable shrank by 416 bytes; Mach-O `__TEXT`, `__DATA_CONST`,
and `__DATA` segment reservations were unchanged. Runtime tests, native/wasm
panic and recover IR tests, and the linked native ExplicitStatus panic E2E
completed. The legacy PCLN signal integration command remains blocked before
its fault subtest by the same unrelated `pclntab_external.go` uintptr-retention
audit on both this candidate and the exact parent.

### Bounded hot-queue audit checkpoint

The next scheduler checkpoint uses `a3e1a09e2` as its exact parent. Three
ordinary paths still revisited an entire owner queue: every completed unlocked
Yield/Park passed the physical-owner handoff gate through full ready and wait
audits, surplus distribution audited the complete source ready queue before
selecting at most eight entries, and destination import audited all existing
local runnables before draining at most eight frozen mailbox entries. A burst
of N parked goroutines therefore retained quadratic validation work after the
spawn-owner fix.

The unlocked handoff now validates the O(1) scheduler endpoints plus the exact
completed continuation. A Yield must be the newly appended ready tail; a Park
must have an exact locally valid active wait record. Full queue audits remain
mandatory for the exceptional locked-M handoff. Fleet distribution validates
only the selected frame chain or a mailbox-bounded prefix, and fleet drain
validates only the frozen incoming entries while rechecking the destination
endpoints under its Try gate. Public exact import, physical-owner transitions,
lifecycle, shutdown, tests, and diagnostics retain complete audits.

Regression fixtures independently corrupt a distant source runnable, a
distant destination runnable, unrelated active waits, ready tail links, wait
endpoints, and the ready-count overflow sentinel. They prove that hot paths do
not walk unrelated payloads while every local ownership header and selected
continuation still fails closed.

The same standard-Go workload was rebuilt on Darwin arm64 with Go 1.26.5 and
LLVM 19.1.7. Five process-start `GOMAXPROCS=1` runs of each 10,000-parked-G
artifact were interleaved with reversed order. Medians were:

| Metric | `a3e1a09e2` parent | bounded-audit candidate | Change |
| --- | ---: | ---: | ---: |
| workload time | 1,904.885 ms | 157.035 ms | -91.76% (12.13x faster) |
| retired instructions | 26.449 billion | 6.297 billion | -76.19% (4.20x fewer) |
| peak RSS | 32,735,232 | 30,326,784 | -2,408,448 (-7.36%) |
| stripped file size | 4,875,776 | 4,875,600 | -176 bytes |

Segment reservations for `__TEXT`, `__DATA_CONST`, and `__DATA` are unchanged.
Seven interleaved short-workload runs also showed no displaced regression:
spawn 100 x 100 moved from a 56.690 ms median to 56.087 ms, and 5,000
unbuffered request/ack handoffs moved from 72.557 ms to 71.484 ms.

Five-run peak-RSS medians against the same-source Go build were:

| parked Gs | Go gc RSS | candidate RSS | Go incremental / G | candidate incremental / G |
| ---: | ---: | ---: | ---: | ---: |
| 0 | 3,522,560 | 7,716,864 | - | - |
| 1,000 | 6,471,680 | 9,535,488 | 2,949 B | 1,819 B |
| 5,000 | 17,596,416 | 18,989,056 | 2,815 B | 2,254 B |
| 10,000 | 31,539,200 | 30,326,784 | 2,802 B | 2,261 B |

At 10,000 parked goroutines the stackless candidate is 1,212,416 bytes (3.84%)
below Go in total RSS, not only in slope. Its incremental resident cost is
about 19.3% lower than Go's; the larger fixed LLGo runtime still makes the
candidate larger at 5,000, so the observed total-footprint crossover lies
between those scales. This checkpoint changes no logical frame or G layout;
the extra RSS reduction comes from avoiding repeated touches of cold queue and
frame metadata, and should be treated as a working-set result rather than a
second object-size reduction.

The remaining 5,000-to-10,000 instruction growth is about 2.46x rather than
the former near-4x queue-scan signature. A larger diagnostic profile contains
no `validReadyQueue` under normal handoff, distribution, or drain. Its active
cost is now dominated by channel registration/cleanup, fixed source and fleet
dispatch, frame allocation/free, and BDWGC marking/locking. Those are the next
performance gates before broadening capability coverage.

### Fused task envelope and physical M/P checkpoint

The next allocation checkpoint uses merge `2a3eb6884` as its exact parent. A
spawned logical G previously owned three independently released native ranges:
the 264-byte scheduler G, a 160-byte runtime G/M/P/local sidecar, and its LLVM
coroutine-frame allocation. The first two have identical lifetimes and root
requirements, but simply concatenating them was not sufficient. The local
BDWGC `GC_size` boundary rounded the old pair to 272 + 160 = 432 bytes, while
both a 424-byte and a 416-byte combined object occupied the 448-byte size
class. Five-run gates observed the expected RSS regression and rejected both
intermediate layouts.

The retained design removes physical M/P state from the logical sidecar. A
`coroRuntimeContext` now contains only the runtime G and its `LocalContext`;
the independently allocated native/executor placeholder keeps the full
`runtimeContext { logical core, M, P }`. Immediately around one physical
`llvm.coro.resume`, the logical G borrows the executor placeholder's actual M/P
and becomes `M.curg`; leave restores the exact placeholder and detaches the G.
Synchronous same-G C-to-Go reentry borrows the already-installed attachment.
Suspended and runnable logical Gs therefore retain no M/P, matching the
scheduler model instead of carrying one synthetic M/P pair per goroutine.

The scheduler G remains at allocation offset zero, so the compiler-facing
spawn ABI is unchanged. The native64 104-byte logical runtime context follows
it in one 368-byte scanned/root allocation, and a compile-time offset equality
fixes the G-at-base invariant. Spawn clears that envelope once and retirement releases
it once with the exact backend size. The ordinary dynamic lifecycle therefore
drops from three allocator objects to two without a pool, retained cache,
target callback, or GC-specific pointer recovery. Native BDWGC reports the
368-byte request as an exact 368-byte object; WASI malloc and tinygogc consume
the same target-neutral layout.

Five process-start `GOMAXPROCS=1` runs of the final candidate gave the following
peak-RSS medians. The Go column is the same five-run same-source build used by
the bounded-audit checkpoint.

| parked Gs | Go gc RSS | task-envelope RSS | Go incremental / G | candidate incremental / G |
| ---: | ---: | ---: | ---: | ---: |
| 0 | 3,522,560 | 7,749,632 | - | - |
| 1,000 | 6,471,680 | 9,486,336 | 2,949 B | 1,737 B |
| 5,000 | 17,596,416 | 17,235,968 | 2,815 B | 1,897 B |
| 10,000 | 31,539,200 | 29,409,280 | 2,802 B | 2,166 B |

At 10,000 parked goroutines the candidate is 2,129,920 bytes (6.75%) below Go
in total RSS and its incremental resident cost is about 22.7% lower. It is also
360,448 bytes below Go at 5,000, while the higher fixed runtime still loses at
1,000; the observed total-footprint crossover is therefore between 1,000 and
5,000 live Gs.

Five final 10,000-G runs interleaved with the exact parent measured 30,375,936
versus 29,442,048 bytes peak RSS (-933,888, -3.07%), 6.294 versus 6.277 billion
retired instructions (-0.28%), and 156.354 versus 156.989 ms workload time
(+0.41%, within the loaded-host range). Seven interleaved short-workload runs
showed compute +0.8%, spawn -1.3%, and unbuffered handoff -0.2%; their ranges
overlap, so only absence of a displaced common-path regression is claimed.
The stripped executable grew by 992 bytes (+0.02%); `__TEXT`, `__DATA_CONST`,
and `__DATA` segment reservations are unchanged.

The full runtime suite, all 20 native-fleet E2Es (including foreign reentry,
blocking compensation, locked G/M replacement, cross-route channel/select,
quota changes, and shutdown), the three linked defer/panic/channel-spawn E2Es,
JS/WASM, WASI malloc, WASI tinygogc, Linux ARM/RISC-V, and Cortex-M baremetal
target builds passed. Both WASI profiles also executed successfully under
Wasmtime. These gates freeze two architectural requirements for later work:
logical G storage may not regain permanent M/P fields, and task/frame pooling
must demonstrate a benefit beyond this allocation fusion without retaining an
unbounded embedded-target cache.

### Constant-time source-owner gates checkpoint

The next scheduler checkpoint uses merge `d2eff9bcc` as its exact parent. A
single bounded source reduction previously re-entered the complete driver
validator from several nested layers. That validator re-read every configured
source capacity and walked the complete in-progress poll-resolution cursor.
The work was valuable at bind, shutdown, compatibility, and diagnostic
boundaries, but redundant while the same owner P was advancing one already
selected source slot.

The retained split has three explicit levels. The hot driver/source header
checks the immutable driver-to-P binding, the channel source back-pointer, and
the local run cursor in O(1). Every selected source then validates its own scan
limit, owner, route, slot, generation, and operation state immediately before
mutation. Complete audits remain at lifecycle and diagnostic boundaries. The
bounded runner also calls a private already-bound poll reducer after validating
the owner once; exported and compatibility poll entries retain the checked
wrapper. Its managed-resume permit probe validates only the active P
back-pointer and issued-action gate because `NextExecutorRunStep` performs the
complete hot-header proof before opening the no-return action interval.

Regression tests corrupt a distant manual-source scan tail and an in-progress
poll cursor: an unrelated observational owner probe remains O(1), while the
complete audit rejects both. Independent corruption of source-set identity, P
back-pointer, and run cursor fails at the hot header. Finally, a selected
manual operation with a damaged exact owner is rejected without changing its
park or source state, then succeeds after the owner is restored. These are
architecture gates, not only output tests.

The host changed frequency substantially between two AB/BA-interleaved
campaigns, so only within-campaign ratios are combined. In the first nine-run
campaign, replacing complete catalog/cursor audits moved 5,000 unbuffered
request/ack handoffs from 138.628 ms to 93.204 ms (-32.77%) and spawn 100 by
100 from 102.044 ms to 99.249 ms (-2.74%). In the second eleven-run campaign,
removing the nested owner recheck moved handoff from 47.830 ms to 43.712 ms
(-8.61%) and spawn from 51.389 ms to 50.841 ms (-1.07%). The compounded
handoff reduction is about 38.6%. The final handoff range in that stable
campaign was 43.509--44.121 ms.

The same pure-compute function has the same address and byte-for-byte identical
machine code in parent and candidate binaries. Its noisy timing movement is
therefore not attributed to this runtime-only change. Against the earlier
stable same-source Go median of 0.751 ms, the final handoff sample is a
directional cross-session ratio of about 58.2x, down from about 96.7x at the
previous checkpoint. It is not promoted to a paired regression budget: the
next attempted all-workload run coincided with unrelated media rendering and a
host load average near 35, producing more than 10x ranges, and was rejected.

The stripped workload executable grows from 4,876,592 to 4,877,056 bytes
(+464, +0.010%). Mach-O `__TEXT`, `__DATA_CONST`, and `__DATA` segment
reservations are unchanged; `__text` grows by 3,068 bytes. No runtime object or
coroutine-frame layout changes. The host race/shuffle coroutine suite, complete
runtime suite, all 20 native-fleet E2Es, and the linked native
defer/panic/channel-spawn E2Es pass. The remaining handoff gap is no longer a
catalog-scan problem: an immediately matched channel operation still suspends
both endpoints and runs the durable A/ack/B plus typed-cleanup transaction.
Avoiding that suspension on the exact local ready path is the next performance
gate.

### Closed-program worker demand and pure static checkpoint

The 2026-08-13 local candidate based on `914379fbd` separates target support
from closed-program demand. After the final physical ProgramIR transaction is
sealed, the compiler projects a worker bit only from reachable
`worker-syscall`, `worker-foreign`, `worker-cgo`, and `worker-cgo-errno`
recipes. The bit is included in both startup-table and manifest hashes. The
runtime validates it before binding executor sources and starts the native
worker pool only when the program requests it.

This exposed one real false demand in `g_pthread.go`: an unrecoverable TLS-key
failure tried to call `fprintf` before a scheduler existed. That diagnostic
could never suspend and resume correctly. It now delegates directly to the
existing terminal synchronous abort path, while ordinary stdio remains a
potentially blocking worker operation.

Two no-import, no-output fixtures provide a closed negative gate. Both were
built with LLVM 22.1.8 using `-a -trimpath -O3 -lto=full -ldflags='-s -w'` and
an independent cache. Their linked V2 bootstrap has flags zero, the generated
worker foreign-call thunk is absent, and the four-second compute run has one
OS thread. The existing general workload retains flags `1`, providing the
positive worker-demand side of the gate.

The table reports medians of seven AB/BA-interleaved `/usr/bin/time -lp` runs.
Retired instructions and cycles are authoritative because process wall time
was heavily affected by unrelated host scheduling.

| Pure fixture / metric | Go 1.26.5 | LLGo coroutine | Difference |
| --- | ---: | ---: | ---: |
| idle file bytes | 1,161,922 | 897,968 | -22.72% |
| idle retired instructions | 23,433,796 | 40,681,350 | +73.60% |
| idle cycles | 19,374,249 | 22,141,783 | +14.29% |
| idle peak RSS | 3,293,184 | 3,768,320 | +475,136 (+14.43%) |
| compute file bytes | 1,161,922 | 898,608 | -22.66% |
| one-billion-call instructions | 25,235,748,943 | 23,219,390,643 | -7.99% |
| one-billion-call cycles | 6,876,063,156 | 5,779,240,893 | -15.95% |
| compute peak RSS | 3,276,800 | 3,768,320 | +491,520 (+15.00%) |

The full seven-run instruction ranges were 25.172--25.507 billion for Go and
23.192--23.275 billion for LLGo. Cycle ranges were 6.709--7.295 billion and
5.696--5.861 billion respectively. Thus the direct static-call plus bounded
safepoint path now beats Go on both stable hardware counters; it is no longer
the general performance blocker. The LLGo compute payload adds only 640 bytes
to its pure-idle artifact.

The fixed path is not finished. Pure LLGo startup still retires about 17.25
million more instructions than Go and holds about 464 KiB more resident
memory. Also, 64 worker-named runtime symbols remain linked even when the
capability bit is zero: the change avoids their threads and operation source,
but the runtime's dynamic source-binding references prevent complete LTO code
elimination. The next fixed-cost work should profile scheduler/bootstrap
initialization and then make optional service code itself link-time removable;
it should not perturb the now-competitive static kernel.

A same-source full-LTO `-tags=nogc` control isolates most of that fixed gap.
Seven runs gave 27,229,137 median retired instructions and 2,424,832-byte
median peak RSS; the stripped artifact was 897,552 bytes and had no `libgc`
dependency. Compared with the default coroutine build, removing BDWGC startup
saves about 13.45 million instructions and 1.34 MiB RSS. That accounts for
roughly 78% of LLGo's 17.25-million-instruction fixed deficit to Go. The
remaining `nogc` gap is about 3.80 million instructions (+16.2%), while its
RSS is about 0.83 MiB below Go. Default-GC lazy initialization/linkage is
therefore the primary fixed-cost target; scheduler bootstrap is a smaller,
separately measurable remainder.

### Compact structural emission type graph checkpoint

The same 2026-08-13 candidate removes a compiler-side exponential path that
was exposed by the complete cmd/cgo fixture. The previous structural emission
keys recursively serialized every occurrence of a type child. A shared
go/types DAG was therefore expanded as a tree, even though repeated acyclic
children carry no additional identity. Deep generic C and managed-interface
signatures could spend more time and memory constructing key strings than
emitting their LLVM body.

One parameterized graph now serves all three required equivalence policies:
strict emission retains named identity, tuple names, and struct metadata;
strict ABI omits only tuple names; identity-free ABI also expands named types
and erases struct field metadata. Tarjan SCCs preserve recursive topology,
ordered root-local references encode cycles, and acyclic children contribute a
Merkle digest. Each public key is a schema-separated SHA-256 digest, so
incidental go/types pointer sharing no longer changes equality or output size.
This replaces the separate recursive builder rather than adding a second type
authority; the implementation and tests have a net growth of 71 lines.

At depth 18, the adversarial shared-DAG benchmark completes in about 61 us with
76,880 bytes and 509 allocations per operation, while returning a fixed-length
key. Before the change, `cgofull` had not reached its IR check after more than
720 seconds. The compact graph reached that check in about 374 seconds; a
subsequent complete build-and-run gate passed in 616 seconds. The latter also
includes compilation, linking, and execution, so it is recorded as a
correctness gate rather than compared directly with the IR-only cutoff.

A follow-up retains root digests in the owning `EmissionUniverse`. The cache is
mode-separated, canonicalizes aliases, and dies with that one compiler
session; it cannot pin go/types graphs from earlier requests in a process-wide
map. All production structural-key consumers use this boundary, including
interface descriptors, worker/cgo thunks, callable shadows, frame-retention
proofs, dispatch layouts, and archive effect summaries. A cache hit takes
about 318--560 ns with zero bytes and zero allocations on the loaded validation
host, versus about 0.8--1.1 ms, 76,880 bytes, and 509 allocations when rebuilding
the depth-18 graph. The affected semantic suite and the complete `cgobasic`
build/link/run fixture pass.

The large `cgofull` fixture was not promoted as a post-cache timing result. Both
its IR-only and full paths entered the existing LLVM emission peak above the
1.6-GiB local validation ceiling and were terminated cleanly. The earlier
post-graph 616-second pass remains the correctness result; no claim is made
from incomplete, heavily host-contended wall-clock samples.

### Closed direct-channel transaction checkpoint

The next 2026-08-13 local candidate, still based on `914379fbd`, isolates the
remaining unbuffered handoff cost with `testdata/pure_handoff`. The fixture has
no imports or output and performs 100,000 request/ack round trips, hence 200,000
successful unbuffered rendezvous. Go 1.26.5 and the LLVM 22.1.8 coroutine
compiler build the exact same source. Every LLGo variant uses an independent
cache plus `-a -trimpath -O3 -lto=full -ldflags='-s -w'`.

The retained direct path is a closed transaction rather than a second channel
implementation:

1. compiler lowering passes its exact hidden `*G` task to channel helpers;
2. zero-filled coroutine-frame allocation replaces redundant caller and
   prepare aggregate stores;
3. `PrepareCurrentChannelParkCleanup` authenticates the compiler park window,
   reserves and exposes one source operation, publishes the parked frame, and
   binds typed cleanup in one transaction; catalog exhaustion returns an exact
   zero-effect `NeedsCapacity` result;
4. after an hchan has detached an ordinary one-case waiter under its lock, the
   same-P matcher acquires the private select claim without first taking the
   generic producer lifetime admission;
5. an already published Ready fact upgrades that transaction to the complete
   admission/mailbox path. After a successful physical commit, the source is
   sealed before any frame detach. Producers admitted just before the seal are
   joined through the owner-local FIFO; otherwise the common closed Apply tail
   resolves, detaches, materializes, recycles, and promotes the peer inline.

Multi-case select, cross-P matching, buffered channels, close, cancellation,
and every uncertain shape retain the durable source transaction. The fast path
therefore removes proof work only where hchan serialization plus the exact
current-P capability already supplies that proof; it does not weaken the
general producer ABI.

Each intermediate change had its own seven-run instruction gate. Removing the
caller zero saved 0.382%, removing the duplicate prepare zero saved 0.139%, the
narrow compiler-task capability saved 1.027%, and the closed park preparation
saved 1.264% in their respective AB/BA campaigns. A shared struct-return park
helper was rejected after increasing instructions by 0.359%; LLVM did not
inline its three boundaries. These independent campaign ratios are not
compounded below.

The final measurement rotates Go, the retained closed-park parent, and the
sealed same-P candidate through seven process runs. Retired instructions are
the primary metric because unrelated host load moved wall time by more than an
order of magnitude. Complete hardware-counter ranges are included.

| Pure handoff metric | Go 1.26.5 | closed-park parent | sealed same-P candidate |
| --- | ---: | ---: | ---: |
| file bytes | 1,161,970 | 916,256 | 916,544 |
| retired instructions, median | 312,088,337 | 2,332,891,820 | 2,270,337,552 |
| retired instructions, range | 311,143,041--319,408,760 | 2,328,641,148--2,337,837,686 | 2,266,369,974--2,273,049,208 |
| cycles, median | 94,773,661 | 531,823,719 | 524,401,481 |
| cycles, range | 88,386,522--104,969,862 | 516,014,589--614,000,723 | 499,670,027--554,399,515 |
| peak RSS, median | 3,309,568 | 3,817,472 | 3,801,088 |

Against the exact parent, the candidate retires 2.681% fewer instructions and
uses 1.396% fewer cycles. Its instruction ratio to Go narrows from 7.48x to
7.27x, while the cycle ratio narrows from 5.61x to 5.53x. Peak RSS drops one
16-KiB page versus the parent but remains 14.85% above Go. The stripped LLGo
artifact remains 21.12% smaller than Go; versus its parent it grows 288 bytes
and its Mach-O `__text` grows 1,524 bytes. No compiler-spilled channel-frame
layout changes.

Correctness gates cover the capability boundaries, not only the benchmark
output. The ordinary direct transaction must leave admission count and linear
lease zero. A Ready race must upgrade to the generic admission and complete
source/claim/packet/frame recycling. A producer paused after admission but
before its physical store must observe the sealed source, release its lease,
and defer inline detach until quiescence. Capacity failure must leave all frame
and scheduler state untouched. The complete `runtime/internal/coro` suite, the
CI-shaped race/shuffle typed-hchan and owner-lock suite, and compiler native plus
WASM channel-lowering tests pass.

This checkpoint is a real reduction, not completion of channel optimization.
About 11,350 instructions are still retired per successful rendezvous versus
about 1,560 for Go. The next gate should collapse the remaining ordinary
park/resolve/materialize metadata transitions under the same closed capability
or avoid suspending the immediately matched endpoint. General select and
cross-owner machinery should not be cloned into another fast runtime.

#### Closed inline state transaction follow-up

The retained follow-up executes that first next gate without introducing a
second channel state machine. The exact same-P, uncanceled, one-case capability
now performs one complete read-only preflight over the source slot, frozen park
descriptor, active/affected queue edges, result packet, and ready-queue header.
Only after every fallible observation succeeds does one no-fail write half
publish the canonical end states: source slot `Free`, claim `Open`, packet and
park `Materialized`, wait record detached, and peer `Runnable`. The transient
`Detaching`, `Ready`, `Consumed`, `Detached`, `Quiesced`, and `Taken` states are
not externally observable while producer ingress is sealed, its admission is
quiesced, the claim excludes another resolver, and the current P excludes
owner mutation. The general state machine remains authoritative for select,
cancellation, cross-P work, an admitted producer, and every failed fast-path
shape.

This replaces repeated construction and validation of the same state; it does
not remove a lifecycle gate. A deterministic corruption test changes the
bound packet after the physical effect and verifies that failed preflight has
not detached, recycled, or promoted any owner state. Restoring the descriptor
then lets the same closed transaction reach its canonical terminal state. The
existing Ready-race admission upgrade and producer-before-seal join tests also
remain passing.

The comparison below is a fresh seven-run rotation of Go, the immediately
preceding source-proof-tail candidate, and the closed inline candidate. It is
not mixed with the earlier checkpoint's host-counter session.

| Pure handoff metric | Go 1.26.5 | source-proof-tail parent | closed inline candidate |
| --- | ---: | ---: | ---: |
| file bytes | 1,161,970 | 916,784 | 933,104 |
| retired instructions, median | 321,618,389 | 2,190,867,063 | 1,814,602,002 |
| retired instructions, range | 319,044,013--323,486,769 | 2,187,207,643--2,194,970,866 | 1,813,240,660--1,816,758,591 |
| cycles, median | 90,885,434 | 488,143,750 | 387,515,097 |
| cycles, range | 84,637,855--101,931,134 | 447,473,831--517,061,273 | 367,009,332--419,624,483 |
| peak RSS, median | 3,260,416 | 3,801,088 | 3,817,472 |

Against the exact parent, the closed transaction retires 17.174% fewer
instructions and 20.615% fewer cycles. The instruction ratio to Go narrows
from 6.81x to 5.64x, and the cycle ratio narrows from 5.37x to 4.26x. The
candidate's median RSS is one 16-KiB page above its parent. Its `__text` grows
only 672 bytes, but an additional 256 bytes in the on-disk data/metadata
section crosses the next Mach-O 16-KiB segment-file alignment boundary; that
accounts for the apparent 16,320-byte file increase. The artifact remains
19.70% smaller than Go, with no G, wait-record, source-slot, or coroutine-frame
layout changes.

The complete target-neutral runtime suite, the CI-shaped race/shuffle typed
hchan and owner-lock suite, and LLVM 22 native plus WASM32 compiler channel
lowering tests pass. The remaining roughly 9,073 instructions per rendezvous
versus Go's roughly 1,608 are no longer dominated by resolve/apply/materialize
validation. The next profile gate should separate unavoidable stackless
suspend/resume and scheduler handoff cost from hchan locking, compiler-emitted
park preparation, and the still-general producer-side transaction. An
immediately matched endpoint may avoid more work, but only if its no-suspend
hchan critical section can retain the same close/claim lifetime proof.

#### Fused direct park preparation follow-up

The next retained checkpoint applies the same closed-transaction rule to the
ordinary compiler-generated park half. `PrepareCurrentChannelParkCleanup`
first authenticates the exact G/P/frame/source relation, fresh frame storage,
reusable ParkState, source capacity, operation identity, and private direct
reservation. It then seals the selected producer generation before changing
owner state. Once that atomic begin succeeds, one no-fail write suffix builds
the final single-link `Parked` graph, committed wait record, cleanup packet,
frame transition, source cursor, and reserved external endpoint. Release
publication of the initialized generation and then the hchan endpoint are the
only visibility boundaries.

This removes the hot chain through generic operation prepare, commit-mode
declaration, link attachment, post-build graph audit, and external-exposure
revalidation. Those APIs remain authoritative for multi-case select, pending
task cancellation, compatibility callers, and any non-private reservation.
The generation/admission protocol is unchanged. A deterministic stale-
reservation gate forces the generation begin to fail and verifies that the
ParkState, pending transition, frame link, wait/claim/packet/cleanup storage,
source scan limit, and reserve cursor all remain untouched.

The table is a fresh seven-run candidate/parent/Go rotation using the same
LLVM 22.1.8 full-LTO build and `/usr/bin/time -lp` hardware counters as the
preceding checkpoint.

| Pure handoff metric | Go 1.26.5 | closed inline parent | fused park candidate |
| --- | ---: | ---: | ---: |
| file bytes | 1,161,970 | 933,104 | 933,104 |
| retired instructions, median | 311,240,084 | 1,802,711,153 | 1,620,832,698 |
| retired instructions, range | 309,587,998--316,837,619 | 1,800,541,881--1,812,707,051 | 1,618,790,552--1,624,955,168 |
| cycles, median | 82,996,555 | 381,292,958 | 365,235,840 |
| cycles, range | 77,067,945--91,384,658 | 352,567,507--448,725,174 | 357,100,740--376,275,824 |
| peak RSS, median | 3,227,648 | 3,817,472 | 3,833,856 |

Against the exact parent, the candidate retires 10.089% fewer instructions
and uses 4.211% fewer cycles. Its instruction ratio to Go narrows from 5.79x
to 5.21x and its cycle ratio from 4.59x to 4.40x. That is about 8,104 retired
instructions per rendezvous versus Go's 1,556 in this campaign. The stripped
file size is unchanged, Mach-O `__text` grows by 400 bytes, and median RSS
grows by one 16-KiB page. The artifact remains 19.70% smaller than Go and no
runtime or coroutine-frame layout changes.

The target-neutral core suite, full race/shuffle core gate, native and WASM
typed-hchan adapter gates, and LLVM 22 native/WASM32 compiler lowering gate
pass. The remaining gap now needs a new profile: the old park-preparation
stack no longer describes the emitted transaction, so further optimization
must target measured hchan, scheduler, or suspend/resume cost rather than
guessing at another metadata layer.

#### Hchan direct-shape certificate follow-up

The typed hchan matcher already validates the dequeued coroutine operation,
waiter back-pointers, source route, payload size, direction, and terminal
status before it acquires the select claim. For the same open, unbuffered,
direct waiter, the hchan mutex then keeps queue linkage and channel state
stable across the existing no-suspend begin/effect/commit span. The retained
follow-up records that proof in the transient commit context and consumes it
at the later phase boundaries instead of calling the complete operation
validator and direct-shape recognizer again. Select, buffered, close, and every
caller without that certificate still perform the ordinary checks. No bit is
stored in the waiter, operation source, coroutine frame, or producer ABI.

A fresh seven-run parent/candidate rotation measured:

| Pure handoff metric | fused park parent | hchan certificate candidate | Change |
| --- | ---: | ---: | ---: |
| file bytes | 933,104 | 933,104 | 0 |
| retired instructions, median | 1,621,999,749 | 1,587,608,197 | -2.120% |
| retired instructions, range | 1,621,031,474--1,625,614,230 | 1,585,121,525--1,588,455,666 | |
| cycles, median | 366,395,409 | 362,085,650 | -1.176% |
| cycles, range | 354,485,472--371,605,891 | 352,947,743--379,396,740 | |
| peak RSS, median | 3,833,856 | 3,817,472 | -16,384 |

Mach-O `__text` grows by 176 bytes. A separate fresh candidate/Go rotation
gave 1,586,785,281 versus 312,146,532 median retired instructions (5.08x) and
343,619,997 versus 81,370,142 median cycles (4.22x), or about 7,934 versus
1,561 retired instructions per rendezvous. The complete core and race/shuffle
gates, native/WASM typed-hchan gates, and LLVM 22 native/WASM32 lowering gate
remain passing.

#### Issued-action scheduler and scalar-resume checkpoint

The final 2026-08-14 optimization pass follows one exact same-owner channel
handoff from hchan commit through coroutine resume. The root cause was no
longer one expensive function. It was duplicated interpretation at each layer:
the scheduler rebuilt ready headers and compatibility context, the runtime
reopened execution ownership around each action, the completion path decoded
the same driver/route/source relation after hchan had already proved it, and
the compiler/runtime resume ABI returned an aggregate whose fields represented
one small finite outcome.

The retained implementation keeps one managed execution lease across adjacent
actions, dispatches the already validated ready FIFO directly, and enters the
same-owner issued-completion branch before reconstructing compatibility
context. The exact V2 resume ABI consumes a single `uint32` word containing
outcome class and payload; the typed aggregate wrapper remains only as an
internal test/compatibility helper. OS-thread suspend bookkeeping is skipped
unless the task has actually acquired thread-lock depth. These are projections
of the existing scheduler, completion, and cancellation state machines, not a
second protocol. Multi-case select, cross-owner completion, cancellation, and
uncertain shapes still take the durable path.

A review follow-up then restored the intended architecture boundaries without
changing the hot transaction. The shared park emitter now owns static state-ID
allocation and accepts one typed prepare callback, so fused channel try-or-park
does not expose `nextState`, instruction counters, or a `parkPrepared` mode in
the feature lowerer. Structural type-key memoization is a standalone
compilation-lifetime component rather than twelve new direct consumers of
`EmissionUniverse`. The architecture gate therefore remains at 296 direct
plan-authority observations, legacy Park steps remain zero, raw helper
physical-type observations remain zero, and physical-body capability access
drops from 35 to 33. The unconsumed external `frame_publish_v2` ABI was removed;
V3 is the sole current metadata-bearing publication ABI.

The same no-import `pure_handoff` source performs 100,000 request/ack round
trips, or 200,000 successful unbuffered rendezvous. Both binaries are stripped;
LLGo uses LLVM 22.1.8 with `-O3 -lto=full`. The table is a fresh nine-run
same-host measurement after the architecture review. The first LLGo run and
first Go run show ordinary cold-start counter elevation; medians and complete
ranges are reported.

| Pure handoff metric | Go 1.26.5 | LLGo coroutine | LLGo / Go |
| --- | ---: | ---: | ---: |
| file bytes | 1,161,970 | 932,752 | 0.803x |
| retired instructions, median | 308,400,621 | 347,676,747 | 1.127x |
| retired instructions, range | 308,281,359--313,822,784 | 347,611,493--350,025,044 | |
| cycles, median | 68,603,883 | 58,946,249 | 0.859x |
| cycles, range | 66,893,098--74,009,098 | 57,796,591--62,107,540 | |
| peak footprint, median | 2,245,136 | 2,769,208 | 1.233x |
| maximum RSS, median | 3,194,880 | 3,801,088 | 1.190x |

LLGo still retires 12.7% more instructions because each rendezvous explicitly
advances a stackless continuation and its scheduler state. On this CPU that is
not a remaining throughput deficit: its median cycle count is 14.1% below Go,
and the stripped artifact is 19.7% smaller. Relative to the roughly
1.59-billion-instruction hchan-certificate checkpoint, the cumulative issued
action, lease, scheduler, completion, and resume work removes about 78% of the
remaining instructions. The previously reported broad “LLGo is slower than
Go” handoff gap is therefore closed for this single-executor core case.

Build configuration is material. Rebuilding the same source with full LTO but
the default optimization level retired about 350.4 million instructions; with
both full LTO and `-O3`, it retired about 347.7 million. Disabling LTO produced
an 810,512-byte artifact but retired about 394.2 million instructions, roughly
13.4% more than the matched `-O3 -lto=full` binary. Cross-package inlining of
the compiler/runtime transaction is consequently a required performance
profile today, not merely a benchmark decoration. Making equivalent
optimization available without full LTO is future build-pipeline work; it is
not evidence of a missing scheduler mechanism.

#### Stable-boundary scheduler review follow-up

A final commit review found that the remaining retired-instruction gap was not
the LLVM coroutine operation itself. Four already-linearized facts were being
re-observed on every channel action:

1. `ExecutionQuota.TryAcquire` was already the exact live concurrency gate,
   but ready placement reloaded the GOMAXPROCS limit after every action.
2. The private issued-action interval authenticated P, G, driver and action
   before `llvm.coro.resume`, but its adjacent return replayed the immutable
   executor binding.
3. Direct-channel materialization published one complete runnable certificate,
   but dequeue and continuation entry rechecked fields written and consumed by
   that same owner-only transaction.
4. `readyDebt` semantically requires one already-materialized runnable to run
   before any lower-priority source, yet the selector still traversed all idle-P
   and producer probes before making that forced choice.

The retained fix keeps those proofs at their actual stable boundaries. A
successful public `runtime.GOMAXPROCS` change performs one compiler scheduler
yield; the next bounded slice refreshes its cached placement epoch, while
`TryAcquire` continues to enforce shrink immediately. Issued resume consumes
the P-owned action instead of round-tripping a duplicate runtime value. The
direct continuation consumes the private materialized/dequeued certificate,
and an ordinary same-owner `readyDebt` takes a priority-preserving selector
shortcut. General commits, LockOSThread islands, source work, cancellation,
cross-owner completion and compatibility entries retain their complete gates.
An additional dispatch-capability parameter measured only 0.06% and was
discarded rather than retained as architectural surface.

The final race review also found one producer-side read of the owner-only
direct-channel inbox tail. The MPSC publisher now observes only its atomic head
and immutable retained-ingress binding; the single consumer remains the sole
tail owner. A source-shape gate prevents that cursor ownership from leaking
back into the producer, and the complete target-neutral core race suite passes.
All measurements below use the corrected publisher.

The following table is a fresh nine-run same-host rotation of the same stripped
100,000-round-trip binaries. It supersedes the preceding short-run checkpoint;
the build remains LLVM 22.1.8, `-O3 -lto=full`.

| Pure handoff metric | Go 1.26.5 | LLGo coroutine | LLGo / Go |
| --- | ---: | ---: | ---: |
| file bytes | 1,161,970 | 916,784 | 0.789x |
| retired instructions, median | 316,755,514 | 334,224,188 | 1.055x |
| retired instructions, range | 316,415,548--318,241,479 | 333,610,648--336,123,375 | |
| cycles, median | 71,281,738 | 64,066,897 | 0.899x |
| cycles, range | 69,396,625--73,644,923 | 62,764,022--79,084,562 | |
| peak footprint, median | 2,228,752 | 2,785,592 | 1.250x |
| maximum RSS, median | 3,162,112 | 3,801,088 | 1.202x |

The short process still includes LLGo's roughly 13.45-million-instruction GC
startup cost measured above. A 100,000,000-round-trip steady-state run therefore
provides the more useful transaction comparison:

| Long pure handoff metric | Go 1.26.5 | LLGo coroutine | LLGo / Go |
| --- | ---: | ---: | ---: |
| retired instructions | 293,936,307,783 | 296,022,059,612 | 1.007x |
| cycles | 60,430,528,016 | 50,224,458,184 | 0.831x |
| peak footprint | 2,327,056 | 2,785,592 | 1.197x |
| maximum RSS | 3,342,336 | 3,801,088 | 1.137x |

Thus steady retired instructions are within 0.7% of Go, while LLGo uses 16.9%
fewer cycles and its stripped artifact is 21.1% smaller. The remaining fixed
memory difference in these runs is about 0.45--0.61 MiB; parked-G slopes and
broader workloads remain separate measurements. The target-neutral runtime suites, coroutine
`cl` subset, full `internal/build` `TestCoro*`, architecture/directive gates,
bootstrap/runtime-link tests, cross-route channel/select, dynamic GOMAXPROCS,
LockOSThread suspension, native/WASM lowering and timer gates pass. The complete
`cl` command reached its explicit 15-minute limit in the unrelated testgo
`cursor` case; no full-suite pass is claimed. A full `ssa` run likewise has no
result beyond its previously reported explicit timeout.
