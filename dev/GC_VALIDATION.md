# Independent tinygogc validation

This is a focused gate for the collector shared by `baremetal && !nogc` and
Wasm linear-GC targets. It does not validate native BDWGC, and does not replace
R4's final Wasm acceptance, patch coverage or executable-size checks.

## Supported entry contract

The collector currently supports **one mutator**: only one execution context
may mutate its heap and roots at a time. Its `mutex` is a reentry guard, not an
atomic lock or a stop-the-world protocol. Entering allocation, collection or
statistics while the guard is active traps without allocating a diagnostic.
Finalizers/callbacks scheduled after the outer operation releases ownership may
allocate normally.

Concurrent mutators and allocations from interrupt handlers are unsupported.
Wasm linear-GC configurations with worker/thread build tags are explicitly
rejected. Embedded target metadata mentioning multiple cores or RTOS support
does not establish concurrent-GC support. Serializing allocator calls alone is
insufficient: another core must not mutate roots or payloads during collection.

## Verification layers

| Layer | What runs | What it does not establish |
| --- | --- | --- |
| Core arenas | Production allocator, mark/sweep, metadata, statistics, pacing and guard; fixed and growable memory; graph cycles/interior roots/worklist overflow; OOM; realloc/free; busy-entry rejection | Platform stack/register roots, real finalizers or LLGo code generation |
| Host CPU matrix | Linux x86-64/ARM64, macOS ARM64, Windows x86-64; also Linux 386; each arena test with `GOMAXPROCS=1,4`; serialized requests from eight goroutines | Concurrent GC, a race-detector result, or 16-bit AVR coverage |
| Wasm profiles | EC32, EC64, WC32, GJS, GWASI; actual LLGo root/liveness/finalizer fixtures, `GOGC=100,1,0,off`, negative reentry/configuration tests, original Go GC workloads | Full standard-library compatibility or final R4 acceptance |
| Embedded ISA | Cortex-M and RISC-V firmware built by LLGo and run by QEMU, one mutator; explicit collections and surviving interior roots | Physical-board, interrupt-allocation, RTOS/multi-hart or multicore GC support |

The native arena harness extracts declarations from the current production
files, rather than maintaining another collector. Only platform memory/root
discovery and profiler/finalizer integration are substituted. Its coverage
percentage describes that extracted core, **not** whole-runtime or PR coverage.

Embedded firmware passes only after emitting its exact checked completion line.
An emulator that merely starts, exits without the line, or times out fails.
After that witness the runner stops only the emulator process group it started;
firmware need not implement an operating-system process exit. RISC-V uses one
hart even when the inherited target configuration mentions four cores.

## Running the gate

The `Independent GC validation` workflow accepts `all`, `core`, `wasm` or
`embedded`. It needs no unrelated standard-library/benchmark jobs. Before this
new workflow is present on the fork's default branch, the diagnostic branch's
existing `GOROOT` dispatch can invoke it as a reusable workflow:

```sh
gh workflow run goroot.yml --repo cpunion/llgo \
  --ref codex/gc-validation-20260909 -f mode=gc-validation
```

Fast local core checks (Go only, no LLVM or device toolchain):

```sh
cd internal/build
go test -v -count=1 -timeout=180s gc_arena_test.go \
  wasm_gc_metadata_test.go wasm_gc_head_cache_test.go wasm_gc_pacing_test.go
```

The CI uses LLVM 22 and preserves logs, helper coverage, generated Wasm/firmware
and per-case resource reports. Missing required tools are errors, not skips.
The original Go pressure cases retain their 60-second execution and 4-GiB RSS
limits. A failure in these limits stays a failure; it is not converted to a
pass by relaxing a timeout or reducing the workload.

Once this gate converges, promote only validated production changes and tests
to R4, then rerun the final-head five-profile integration suites, native/patch
coverage and `cprintf`, `println`, `fmtprintf` size benchmarks. Canceled earlier
acceptance runs do not count as completed validation.
