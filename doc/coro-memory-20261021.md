# Native baseline memory follow-up: 2026-10-21

[PR #302](https://github.com/cpunion/llgo/pull/302) reduces two independent fixed costs. This is an affected-runtime follow-up to the completed coroutine acceptance campaign, not a new full-suite acceptance result or a throughput-parity claim.

## Collector startup

Creating the runtime TLS key called `GC_malloc` before the allocator bootstrap could configure BDWGC. With initial `GOMAXPROCS=1` on the 16-CPU host, this started 15 unnecessary parallel marker helpers. The allocator now initializes the collector before creating TLS and bounds its initial default marker team by a valid positive execution quota and online CPUs.

Explicit `GC_MARKERS` / `GC_NPROCS` settings and a collector already initialized by an embedder take precedence. An unset or invalid initial `GOMAXPROCS` keeps the collector default. BDWGC before 8.2 has no public marker-count setter and retains its existing policy. Later `runtime.GOMAXPROCS` calls resize managed execution independently of this startup-only collector setting.

The preceding seven-triplet cohort measured zero-parked RSS falling from 7.047 to 6.578 MiB, and 1,000-parked RSS from 8.641 to 8.094 MiB. Those numbers retain their own frozen source and samples; the fresh cache cohort below is separate.

## Unused symbol caches

Two 4,096-entry symbol caches occupied 128 KiB and 32 KiB of static roots on arm64. A read watchpoint on the old fallback cache stops in `GC_mark_from`, reached through `GC_gcollect`, before any symbol lookup. Conservative root scanning touched the zero-filled storage even for this parked workload. Each cache is now allocated on its first actual use, with atomic publication of one shared table. Cache capacities and symbol-table warmup remain unchanged. There is no allocation when reading an empty fallback cache.

A native mapping diagnostic confirms the main image's private zero-filled data falls from 1,008 to 848 KiB. The measured RSS change also includes ordinary page/thread variation; it is not an exact heap-object-size measurement. The stripped parked executable remains 4,543,760 bytes because its former zero-filled reservation did not occupy file bytes.

The main thread is not the source of the MiB-scale gap: both Go and LLGo reserve about 8 MiB of virtual stack, with only 32 and 48 KiB resident in the zero-parked diagnostic. Shrinking that reservation alone would not recover the remaining RSS difference.

## Fresh paired memory measurements

Darwin/arm64, Apple M4 Max (16 logical CPUs, 128 GiB), macOS 27.0.1; Go 1.27.0, LLVM 22.1.8, BDWGC 8.2.12, libffi 3.8.0. Both toolchains use the same ordinary Go source and `-p=1 -trimpath -ldflags='-s -w'`, with initial P=1. The source waits for all goroutines, yields, requests GC, and holds them at an external stdin release barrier. Each process has seven OS RSS reads; each cell is the median across seven measured processes after one warmup. Go / collector-fix baseline / cache candidate order rotates. All 120 runs completed, including release and joining their goroutines; every sample is retained.

| Parked goroutines | Go MiB | Collector fix MiB | Plus lazy caches MiB |
|---|---:|---:|---:|
| 0 | 4.094 | 6.578 | 6.406 |
| 1,000 | 6.938 | 8.109 | 7.938 |
| 10,000 | 30.672 | 21.734 | 21.562 |
| 50,000 | 135.797 | 82.359 | 82.188 |
| 100,000 | 266.797 | 158.125 | 157.953 |

The cache change saves 176 KiB in this cohort at zero and 1,000 parked goroutines. Baseline LLGo RSS remains above Go at these populations; parity is still open. The shared host was under substantial unrelated load. These are P1 observations, not all-platform or quiet-host guarantees.

Evidence directory: `20261021/performance-parity-r1` under the retained acceptance artifact root. `memory-symbol-cache-r4-7pairs` contains raw logs, all RSS samples and input hashes; `memory-maps-symbol-cache-r4` contains diagnostic mappings. The production runtime is identified separately from its unchanged S29 compiler:

- Compiler SHA-256: `7b04ddcb13e50c9615f6b3a7c19c112582e5fa64a55d5fe1e4c7fe4fe1dbd778`.
- Parked candidate SHA-256: `fc48f60583edd0248d299c704ea6e9b26aab1f4ea0c1e52ebf45027a675458d5`.
- `runtime/internal/lib/runtime/symtab.go` SHA-256: `386e0e71efdea2e6dc0585d1908077de81013820b5737c17ea5e20b89c3385a8`.

## Validation

The complete runtime host suite and the closed native build-plan test pass. Three actual native probes pass: stack and statement metadata including recovered panic stacks, concurrent first use of prebuilt function metadata, and a new 64-goroutine fallback-cache publication/GC-retention probe. The fallback probe is also part of the native fault CI lane; the compatibility lane runs the real collector startup and legacy-header fixtures.

The five requested throughput targets remain separate work after this memory PR is validated and merged. Previous throughput, full native, GOROOT, fixture and demo receipts retain their own version identities.
