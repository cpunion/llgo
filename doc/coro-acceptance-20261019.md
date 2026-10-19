# Selected LLVM coroutine follow-up: 2026-10-19

[PR #298](https://github.com/cpunion/llgo/pull/298) merged into `cpunion/llgo:llvm-coro` as `be66eadefba7fd4c7d25fca100b8c6f46d2bb728`. Exact-head [CI 39169187618](https://github.com/cpunion/llgo/actions/runs/39169187618) passes **20/20** at `36e576d15`; fresh paginated reviews, comments and review threads were empty. The merged tree equals the tested head. The cache follow-up [PR #299](https://github.com/cpunion/llgo/pull/299) merged as `5432ae604b4f9ab37d72832e1c6ea15f2f6cf904` after [new-base exact-head CI 39174841112](https://github.com/cpunion/llgo/actions/runs/39174841112) passes **20/20**, the full native test/go gate passes, and fresh paginated reviews/comments/threads are checked. Its merged file tree equals tested head `03458a98a`. This is an affected-package follow-up, not a fresh full Go/all-platform acceptance campaign.

## Version and scope

Code head `36e576d15d30ba55a7f805f567585a0846268a07`; SDK source manifest `d0d8386eef8878dfad49faf98d8359a90fba5abc9738712ed13c4b9e03367db8`; compiler SHA-256 `3782471afe3378bb173e3fc79e9483a73c2744104d84a6599f8e1e80d437a7f8`. These identities belong to the S6 follow-up. The prior [R8 full checkpoint](coro-acceptance-20261018.md) retains its own production freeze and outcomes.

Twelve fresh native package commands pass: **293 top-level tests / 342 including subtests, zero failures or skips**. They cover `sync/atomic`, `hash`, `hash/adler32`, `io/ioutil`, `debug/buildinfo`, `runtime/debug`, `sync`, `math`, `net`, `go/build`, `go/importer`, and `go/types`. Both isolated workers have joined and their source/compiler/image identities match after execution. Their sampled aggregate peak is 18,313.6 MiB under the unchanged 40 GiB controller cap. Per-package builds retain the 24 GiB/60m limit, 4 GiB Go soft target, private cold caches and 8 GiB scratch bound.

Receipt: `20261019/result-state/stdlib-s6-final-receipt.json`, SHA-256 `48f44331c867e50fd72aa3ce862f2a2f3f7faa369a5afd87350954fc3c791c24`. Every package binary and verbose test log is retained. These results do not relabel R8's 180-package or 1,082-case campaign as an S6 pass.

## Full native test/go on the cache candidate

The fresh native `test/go` image passes **276/276 top-level tests (614 including subtests)**, with no failures or skips. All **45 nested compiler process domains** join successfully and reclaim their private scratch. Expected negative compiler diagnostics retain their nonzero exits and are asserted by their passing parent tests. The original 10 GiB process-group RSS, 35m build, 80m outer run and 75m test deadlines, private cache/scratch bounds and nested launcher remain unchanged. Source, compiler, launcher and retained executable hashes match after execution.

Receipt: `20261019/result-state/fulltestgo-final-receipt-cache-s2.json`, SHA-256 `ad57a3eb0908187cad739a4d13ba3a9897d4b31503064f174df96b48a19dbefc`. This result uses the cache SDK manifest identified in the bounded-cache section, with the S6 compiler binary; it is separate from the S6 twelve-package cohort. The previous full 180-package/73-demo/1,082-GOROOT campaign remains R8 evidence. Those historical totals are not combined into a full current-version pass.

## Implementation and executable assertions

Native ELF, Mach-O and PE executables now carry a retained, aligned, pointer-free Go build-info record. `runtime/debug.ReadBuildInfo` reads the same entry-owned immutable module/package/settings data through an ordinary analyzable Go getter. The frontend patches `runtime.Compiler` before constant folding; the runtime replacement alone could not change constants already folded in callers. `go/build.Default` and `go/importer.Default` keep using the companion Go toolchain's gc package/export metadata. This does not implement an LLVM native-archive importer. Paths, versions and replacements use loader-supplied metadata; unavailable module sums and VCS attestations are omitted.

The public readers must succeed and agree with runtime/debug, including compiler/GOOS/GOARCH/version settings. The old compiler fails the newly strict file readers. The first getter prototype correctly fails coroutine analysis as an unknown external managed call; the final implementation retains a defined Go function rather than changing that policy. A separate frontend regression fails with `runtime.Compiler = "gc"` before the source patch and passes afterward.

The old 22 stdlib skips are addressed explicitly:

| Previous skipped assertions | Follow-up |
|---|---|
| Eight atomic And/Or tests and concurrent CAS | Execute old-value/stored-value checks and bounded concurrent increments |
| Adler-32 binary state | Restore into nonempty state, continue hashing and reject truncated state |
| Directory reading | Sorted entries, batched reads, metadata, EOF and closed-directory errors |
| Two native build-info readers | Require successful parsing and runtime/file agreement |
| Five localhost DNS record cases | Private local UDP responder with exact TXT/MX/NS/SRV/address assertions |
| Hash/Pool/Cond/future-math placeholders (four) | Remove obsolete placeholders; retain existing real Cloner/XOF/Pool/Cond assertions |

Thus 18 former skipped tests now execute assertions, four placeholders are removed, and a new batched directory test is added. This is not 22 newly implemented APIs.

Native unit tests link ELF amd64/386/ppc64, PE amd64 and Mach-O arm64 images with section collection enabled; the Go standard reader parses the final images. The complete standalone native reader probe passes under a 6 GiB/5m build cap and a 30s run cap, with its source/image identities retained. CI includes this probe and the original noinit.go case.

## noinit classification correction

The previous R8 raw `noinit.go` FAIL remains unchanged in its frozen receipt. Its original unsupported-ABI explanation was inaccurate: LLGo already emitted a compatibility init-task header. Staged emission releases `pkg.Types` before entry generation; relying only on `Types.Name()` emitted `command-line-arguments..inittask` instead of `main..inittask`. The entry generator now uses the retained loader Name.

The original, unmodified case passes on S6 with the original 25m compilation, 1m execution and 10 GiB process-group RSS limits (compile 35.361s; run 0.304s). This establishes correct symbol identity. LLGo still runs guarded package initializers rather than gc's runtime-dispatched init task, so it does not establish gc's no-op-initializer optimization.

## Remaining boundaries and measurements

R8's other raw failures remain recorded: rangegen exceeds its 10 GiB compilation cap; issue9604b reaches the 25m generated-compilation cap; issue78081 has a load/startup-sensitive one-minute execution cost. Separately identified S6 and cache-candidate runs of the unmodified recursive case pass under the original minute, but a single diagnostic pass does not rewrite the R8 full-campaign outcome or establish a robust performance fix.

The existing allocation-stack heap-profile gap and conservative-GC/gc-specific stack cases remain classified. The extra 1,316 GOROOT diagnostic/multifile cases are outside the user's selected 1,082 runtime-case scope. Native ELF/Mach-O/PE file metadata tests do not establish file-reader support for Wasm/bare-metal formats, and the established WASI synchronous-host/reflection boundaries remain explicit.

## Bounded native frame cache and refreshed measurements

[PR #299](https://github.com/cpunion/llgo/pull/299) increases the medium/large native frame-cache budget from 16 KiB to 128 KiB per size class. Small-bin capacity is unchanged. Across 53 bins the theoretical global retention bound is below **6.625 MiB**, independent of scheduler worker count, compared with below 3.6 MiB previously. Readiness, ownership, locking and full clearing before publication are unchanged. The C regression fails the old budget at the thirteenth insertion and verifies all 65 frames in a 1280-byte recursive working set can be reused with zeroed contents. Five race-enabled allocator suite runs pass, including the existing small-bin bound and eight-thread stress.

The measured SDK head is `f6d0f4348eb720bf8e7b40041c54005c88e5db5a`, source manifest `6d8a5ba89d39859caffd83f810ce36a15d19955ad865b21f421eadd56d6ae0f4`. It uses the unchanged S6 compiler binary. After the parent merge, PR head `03458a98a44a6b54d679c833b3e2382704f6ae80` has the identical file tree (`8cf4de6e386e161e4c02f896cdea9d189ad57962`); only the parent graph changes.

The unmodified recursive issue78081 case uses 1240-byte frames rounded to 1280 bytes. The old budget retains only 12 frames of its 65-frame chain; the candidate retains 102. After all owned compilation domains joined, three alternating measured pairs and one prior warmup pair give:

| Startup | Old median [min, max], seconds | Candidate median [min, max], seconds |
|---|---:|---:|
| Process-start P=2 | 28.590 [28.516, 31.927] | 25.909 [25.003, 27.158] |
| Default startup; program then sets P=2 | 33.083 [32.538, 37.163] | 31.530 [29.227, 31.575] |

These limited shared-host samples show a working-set benefit, with about 9.4% and 4.7% lower medians. Both arms pass. The retained original R8 image also passes unchanged in **35.650s** under its original one-minute guard after owned compilers have joined. Thus the cache change does **not** establish a fix for the historical timeout; its raw matrix FAIL is retained. Longer diagnostic guards preserve timings without changing the selected one-minute acceptance limit.

The separate candidate/Go comparison completes **272/272 throughput/progress/peak runs** and **80/80 steady parked-memory runs** with source/compiler/image/library hashes matching before and after. Serial spawn/WaitGroup, public File and TCP cost **8.221x / 4.849x / 2.476x** Go time in this sample. At 100,000 parked goroutines steady RSS is **159.172 MiB** versus Go **267.469 MiB**. Stripped core/I/O/parked files are **2.885x / 3.582x / 2.889x** Go, excluding external shared libraries. Own compiler domains had joined for measurements; the host remained loaded. These results do not establish a quiet-host or cross-campaign improvement. See [the full measured ranges](coro-performance-20261019.md).

## Generated-compilation diagnostics

The retained issue9604b generated input is 669,627 bytes, SHA-256 `c575171e56cd5a2f42a510924c7b43e343ce8627fbd65551a8f899d7f3d8fe5c`. Two separately identified diagnostics keep the original 25m/10 GiB compilation and one-minute execution bounds. Explicit `-O0` compiles in **207.394s** and runs in **0.449s**. A diagnostic compiler with only native MachineSink disabled compiles in **409.916s** and runs in **0.536s**; its compiler SHA-256 is `70ff2e1aa66413b426606d3f7755858f241894865921513b7413c85c039c9b5f`. The latter SDK source-after check matches S6.

Previous default-compiler samples at 11 and 24 minutes repeatedly locate `MachineSinking::run` / `MachineRegisterInfo::clearKillFlags`; the no-MachineSink sample instead locates GVN load-PRE/dominator updates at 4m18s. This narrows the optimization cost. Neither diagnostic changes production optimization, establishes a default-compiler semantic pass, or removes the original failure. No global pass disable or raised acceptance limit is shipped. Receipt: `issue9604b-optimization-diagnostic-receipt.json`.

The final ledger commit changes documentation only relative to the tested merged code tree. Frozen source manifests retain their original whole-tree identities; documentation updates do not rewrite those manifests or their historical receipts.
