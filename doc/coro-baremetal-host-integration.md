# Baremetal coroutine timer integration

## Execution boundary

Baremetal uses the existing host-owned coroutine executor, timer table and
monotonic clock. It has no separate timer scheduler and no native worker pool.
The standard synchronous `time.Sleep`, `Timer`, `Ticker` and `AfterFunc`
interfaces select the same managed implementations as other coroutine targets.

The initial command entry runs a bounded slice and returns. An embedding drains
the versioned host actions, arranges the requested schedule/alarm, publishes a
monotonic nanosecond sample, and calls `continue_slice` **after** the previous
slice has returned. Slot, generation and epoch identify each action; cancellation
acknowledgements use the existing host protocol. An actual board supplies its
clock, interrupt/compare alarm and WFI/event-loop glue. The generic startup does
not invent a clock, busy-wait inside Sleep, or add another scheduler.

A board without an RTC gets civil time `Unix epoch + monotonic elapsed time`;
this keeps `time.Since(time.Now())` correct even when the first monotonic sample
is zero. A published RTC sample takes precedence. Explicit host mode on a native
OS also uses this clock coordinate, not the unrelated OS nanotime clock.

## Executable gate

`internal/build/_testgo/coro_baremetal_timer` is a Go program using ordinary
standard-library APIs and a deterministic **test-only** C embedding. The C
embedding advances its clock only when consuming an alarm. Success requires:

- Nonpositive Sleep returns immediately, positive Sleep does not return early.
- Timer Stop/Reset, channel delivery, Ticker and AfterFunc complete correctly.
- Main returns to the embedding before completion, and at least four alarm /
  continuation transactions occur.
- The final C observer exits zero through QEMU semihosting. HardFault exits
  nonzero. Missing console output is never interpreted as success.

Run the explicit acceptance gate with an independently built compiler and a
process-tree memory guard appropriate to the machine:

```sh
LLGO_TEST_LLGO=/absolute/path/to/llgo \
LLGO_CORO_BAREMETAL_ACCEPTANCE=1 \
go test ./internal/build -run '^TestCoroBaremetalTimerAcceptance$' -count=1 -v
```

The test limits compilation to ten minutes and target execution to twenty
seconds. It requires LLVM 22 and `qemu-system-arm`. The focused CI job runs only
for PRs into `cpunion/llgo:llvm-coro`.

## Verified result and resource limits

On 2026-10-13, the v17 integration compiler (Go 1.27, LLVM 22.1.8) compiled and
ran the fixture on QEMU MPS2-AN385 Cortex-M3 successfully. Source/LLVM layout
regressions cover native ARM, Cortex-M3, RV32 and wasm32, including nested
runtime-context shapes. ARM's 8-byte LLVM alignment for uint64 must be used by
the frontend: the old gc/arm 4-byte alignment underallocated runtime objects.

The measured ELF contains 1,199,408 bytes of text, 2,640 bytes of initialized
data, and 181,532 bytes of BSS. The separate MPS2 target reserves 16 KiB for its
single physical host stack (not one stack per G); this is a validation budget,
not a measured minimum. It exposes 4 MiB code memory and 4 MiB RAM. The existing
LM3S6965 target remains unchanged at 256 KiB Flash / 64 KiB RAM and does not fit
the current standard-library timer image. No small-board footprint claim is
made by the larger-memory semantic gate.

Largest BSS contributors in this image:

| Symbol | Bytes |
| --- | ---: |
| `runtime.frameSymbolCache` | 65,536 |
| `coroProgramKeyedRegistryV2State` | 57,352 |
| `runtime.funcForPCCache` | 16,384 |
| `coroProgramWorkerSourceV1State` | 9,544 |
| `coroProgramPollSourceV1State` | 6,720 |
| `coroProgramTimerTableV1State` | 6,232 |

Text also retains time parsing/formatting/timezone and reflect-visible code.
The clear next resource work is demand-driven omission or lazy storage for
unused symbol caches/source registries, followed by exact reachability review
of retained library methods. These must preserve capabilities and reflection;
simply deleting code from this fixture would not be a valid optimization.

### Small baremetal FuncForPC cache

The cache now selects 32 four-way sets on baremetal, while native and WASM keep
1024 sets. The lookup, atomic immutable-pointer publication and replacement
algorithm are unchanged. A miss still uses the same symbol tables; this is not
a limit on functions, goroutines or reflection. No allocator or new coroutine
annotation is introduced.

On 2026-10-16, the same v36 compiler built both variants from the same temporary
source path, using immutable before/after snapshots as inputs. Both Cortex-M3
timer images passed QEMU execution:

| `llvm-size` measurement | Before | After | Change |
| --- | ---: | ---: | ---: |
| Text | 1,213,176 | 1,213,072 | −104 |
| Data | 2,640 | 2,640 | 0 |
| BSS | 181,808 | 165,936 | −15,872 (8.73%) |

The retained `runtime.funcForPCCache` symbol shrank from 16,384 to 512 bytes.
The replacement-counter array is already eliminated from this timer fixture;
when retained, its smaller policy saves another 3,968 bytes. The frame-symbol
cache and keyed registry are unchanged. The image still does **not** fit the
64 KiB RAM target, and this does not claim a measured runtime speed improvement.

The gate executes the actual production cache declarations with both capacities,
checking exact hits, collision misses, eviction, counter wrap and concurrent
replacement under the race detector. Target-selection tests preserve other
platforms' capacity. The actual Cortex acceptance checks any retained cache
symbol is at most 512 bytes before running the timer image.

## Explicit remaining platform work

- Real board monotonic clock, alarm interrupt and WFI glue is not supplied by
  the deterministic test host. RISC-V source/layout gates are not a RISC-V
  timer execution result.
- Raw Linux syscalls are rejected with ENOSYS on baremetal, even though the Go
  frontend uses Linux/ARM source selection. File/network I/O requires an
  embedding or platform adapter; there is no default POSIX filesystem/console.
- Dynamic libffi-based reflection calls are explicitly unsupported on this
  baremetal profile; the target must not link the build host's libffi.
- Generic ARM/RISC-V use the validated LLVM 22 compiler/linker. Xtensa and
  ESP-specific prebuilt ABI combinations remain explicitly unsupported until
  validated with LLVM 22; no LLVM 19 toolchain is silently downloaded. Existing
  compiler-rt C/assembly source assets have a historical release name but are
  freshly compiled to this target with LLVM 22, without old LLVM bitcode reuse.
