# Native Go / LLGo comparison: 2026-10-19

Measured SDK head `f6d0f4348eb720bf8e7b40041c54005c88e5db5a`. Candidate SDK source manifest: `6d8a5ba89d39859caffd83f810ce36a15d19955ad865b21f421eadd56d6ae0f4`; LLGo compiler SHA-256: `3782471afe3378bb173e3fc79e9483a73c2744104d84a6599f8e1e80d437a7f8`. The native cache SDK integrated through [PR #299](https://github.com/cpunion/llgo/pull/299) as `5432ae604b4f9ab37d72832e1c6ea15f2f6cf904` uses the unchanged S6 compiler binary and its separately identified SDK source. This report is a shared-host checkpoint with raw ranges, not a general regression budget.

go version go1.27.0 darwin/arm64; LLVM 22.1.8; Apple M4 Max (Mac16,5), 16 logical CPUs, 128 GiB RAM. ProductName:		macOS; ProductVersion:		27.0.1; BuildVersion:		26A434.

Both compilers build the same ordinary Go sources with `-p=1 -trimpath -ldflags='-s -w'`. Each case has one prior warmup pair and seven alternating Go/LLGo pairs. P=1 is set at process start except the explicit P=4 row. Time is the common program's elapsed field; all checksums and exact completion records pass. No timed sample is discarded.

The linked external dependencies are BDWGC 8.2.12 and libffi 3.8.0; their library hashes are retained. Builds explicitly select the installed macOS 26.5 SDK.

Observed one-minute load ranges: throughput 9.40–10.02; parked-memory 9.62–27.41. Per-run loads and process/power snapshots are retained.

| Workload | Go ms median [min, max] | LLGo ms median [min, max] | LLGo / Go |
|---|---:|---:|---:|
| static-compute | 85.306 [84.969, 88.497] | 81.744 [79.222, 84.484] | 0.958x |
| parallel-P1 | 69.872 [66.954, 72.246] | 66.989 [65.226, 68.048] | 0.959x |
| parallel-P4 | 17.187 [16.763, 17.832] | 16.552 [16.153, 18.697] | 0.963x |
| buffered | 85.436 [83.501, 87.917] | 84.096 [82.407, 89.264] | 0.984x |
| select-ready | 115.646 [110.370, 116.650] | 78.397 [77.963, 80.472] | 0.678x |
| spawn-batch | 8.691 [8.327, 9.605] | 23.047 [22.379, 24.766] | 2.652x |
| spawn-serial | 18.706 [17.527, 19.907] | 153.778 [150.170, 158.036] | 8.221x |
| handoff | 16.705 [16.170, 18.325] | 21.846 [21.332, 23.274] | 1.308x |
| timers | 128.780 [126.905, 130.914] | 146.438 [140.959, 148.096] | 1.137x |
| file | 10.438 [10.243, 11.996] | 50.619 [49.311, 55.192] | 4.849x |
| file-syscall | 10.477 [10.187, 11.780] | 15.236 [14.197, 16.613] | 1.454x |
| tcp | 87.833 [81.196, 93.921] | 217.452 [208.547, 229.717] | 2.476x |

A ratio above 1 means LLGo took longer in this run. The timer case includes deliberate 1 ms waits. Pipe-block verifies progress and is not a throughput result; idle/park elapsed fields are excluded. Dynamic-call throughput is outside this matrix. TCP/file measurements use local cache/loopback behavior, not durability or remote-network latency.

## Stripped executable file sizes

| Sample | Go bytes | LLGo bytes | LLGo / Go |
|---|---:|---:|---:|
| core | 1,365,954 | 3,941,408 | 2.885x |
| io | 1,780,690 | 6,377,920 | 3.582x |
| parked | 1,382,594 | 3,994,064 | 2.889x |

Stripped executable file bytes; external shared libraries are excluded. Source, compiler, linked-library identities and raw measurement ranges are recorded separately. `toolchain-and-dependencies.log` records each image's linked dependencies. These bytes are not compressed packages or complete deployment closure sizes.

## Steady parked-goroutine memory

The same source parks N goroutines, waits for a readiness barrier, yields and runs GC, and holds them until an external stdin release. Each measured process has seven current OS RSS reads while held; each table cell is the median and range across seven measured processes. Full-lifecycle OS peak RSS is recorded separately. This measures process memory, including runtime/allocator costs; it is not an exact frame size.

| Parked G | Go steady MiB [min, max] | LLGo steady MiB [min, max] |
|---|---:|---:|
| 0 | 4.062 [4.047, 4.078] | 7.484 [7.484, 7.516] |
| 1,000 | 6.922 [6.812, 6.969] | 9.094 [9.094, 9.125] |
| 10,000 | 30.594 [30.594, 30.703] | 22.594 [22.578, 22.609] |
| 50,000 | 136.453 [136.422, 137.328] | 83.406 [83.391, 83.422] |
| 100,000 | 267.469 [267.000, 268.266] | 159.172 [159.156, 159.203] |

go: zero-G steady baseline 4.062 MiB; baseline-adjusted 100,000-G slope 2762.01 bytes/G.
llgo: zero-G steady baseline 7.484 MiB; baseline-adjusted 100,000-G slope 1590.56 bytes/G.

Full-lifecycle peak RSS is a separate measurement:

| Parked G | Go peak MiB median [min, max] | LLGo peak MiB median [min, max] |
|---|---:|---:|
| 0 | 4.062 [4.047, 4.078] | 7.562 [7.531, 7.594] |
| 1,000 | 6.922 [6.812, 6.969] | 9.156 [9.141, 9.188] |
| 10,000 | 30.594 [30.594, 30.703] | 22.656 [22.656, 22.672] |
| 50,000 | 136.469 [136.438, 137.344] | 83.484 [83.469, 83.500] |
| 100,000 | 267.500 [267.031, 268.297] | 159.250 [159.219, 159.281] |

## Evidence and limits

All 272 throughput/progress/peak runs and all 80 separate steady-memory runs must complete before this report is generated. Each image, source, compiler and measurement script is hashed. Program runs keep the existing 2 GiB / 90 s guard; throughput and parked campaigns keep their 20/30-minute start budgets.

Raw samples are in `measurement-7pairs/samples.jsonl`, `samples.csv`, and `parked-measurement-7pairs/samples.jsonl`; summaries, logs, exact image hashes and source-after build proof remain beside this report. Performance tradeoffs and shared-host variance remain visible. No platform-wide, all-workload, or quiet-host performance claim follows from this checkpoint.

All raw files are retained under `~/.cache/llgo-coro-acceptance/20261019/result-state/perf-cache-depth-s1/`. The refreshed measurement does not replace the historical R8 campaign with results from a different freeze. See [the follow-up acceptance ledger](coro-acceptance-20261019.md).
