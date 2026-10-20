# Native Go / LLGo comparison: PR #300, 2026-10-20

Candidate SDK source manifest: `ecbdef8fb6295d52e73d76687aee2fda1d4cbe701a98890094a7292ca82b5083`; LLGo compiler SHA-256: `0b0fafc808dc3dbe4cb7a520481cc130a7725a001c5a987c29cec108f42637f6`. The compiler and SDK use the same final PR #300 freeze. This report is a shared-host checkpoint with raw ranges, not a general regression budget.

Production commit: `3728ce895472195cd5b98a941b985e16d6465aa6`.
See the [acceptance report](coro-acceptance-20261019-pr300.md) for the complete
runtime scope, retained failures and platform boundaries. Documentation commits
after this freeze do not change its tested production identity.

go version go1.27.0 darwin/arm64; LLVM 22.1.8; Apple M4 Max (Mac16,5), 16 logical CPUs, 128 GiB RAM. ProductName:		macOS; ProductVersion:		27.0.1; BuildVersion:		26A434.

Both compilers build the same ordinary Go sources with `-p=1 -trimpath -ldflags='-s -w'`. Each case has one prior warmup pair and seven alternating Go/LLGo pairs. P=1 is set at process start except the explicit P=4 row. Time is the common program's elapsed field; all checksums and exact completion records pass. No timed sample is discarded.

Observed one-minute load ranges: throughput 16.75–20.91; parked-memory 9.61–16.75. Per-run loads and process/power snapshots are retained.

| Workload | Go ms median [min, max] | LLGo ms median [min, max] | LLGo / Go |
|---|---:|---:|---:|
| static-compute | 82.148 [81.634, 82.625] | 77.000 [76.179, 77.752] | 0.937x |
| parallel-P1 | 66.067 [65.484, 66.381] | 63.757 [63.033, 64.185] | 0.965x |
| parallel-P4 | 16.578 [16.541, 16.759] | 15.816 [15.588, 15.847] | 0.954x |
| buffered | 81.243 [80.345, 81.957] | 81.874 [80.721, 82.774] | 1.008x |
| select-ready | 106.711 [105.790, 107.925] | 74.990 [73.507, 76.022] | 0.703x |
| spawn-batch | 8.395 [8.310, 8.583] | 22.069 [21.781, 22.390] | 2.629x |
| spawn-serial | 17.139 [16.866, 17.814] | 147.412 [145.984, 148.999] | 8.601x |
| handoff | 15.523 [15.446, 16.032] | 21.006 [20.691, 21.530] | 1.353x |
| timers | 115.265 [114.881, 115.533] | 127.728 [127.183, 142.617] | 1.108x |
| file | 10.317 [10.246, 10.389] | 48.075 [47.175, 50.971] | 4.660x |
| file-syscall | 10.221 [10.123, 10.406] | 14.578 [14.203, 16.034] | 1.426x |
| tcp | 94.493 [93.526, 95.440] | 198.799 [196.250, 203.406] | 2.104x |

A ratio above 1 means LLGo took longer in this run. The timer case includes deliberate 1 ms waits. Pipe-block verifies progress and is not a throughput result; idle/park elapsed fields are excluded. Dynamic-call throughput is outside this matrix. TCP/file measurements use local cache/loopback behavior, not durability or remote-network latency.

## Stripped executable file sizes

| Sample | Go bytes | LLGo bytes | LLGo / Go |
|---|---:|---:|---:|
| core | 1,365,954 | 4,505,488 | 3.298x |
| io | 1,780,690 | 7,283,696 | 4.090x |
| parked | 1,382,594 | 4,543,552 | 3.286x |

Stripped executable file bytes; external shared libraries are excluded. Source, compiler, linked-library identities and raw measurement ranges are recorded separately. `toolchain-and-dependencies.log` records each image's linked dependencies. These bytes are not compressed packages or complete deployment closure sizes.

## Steady parked-goroutine memory

The same source parks N goroutines, waits for a readiness barrier, yields and runs GC, and holds them until an external stdin release. Each measured process has seven current OS RSS reads while held; each table cell is the median and range across seven measured processes. Full-lifecycle OS peak RSS is recorded separately. This measures process memory, including runtime/allocator costs; it is not an exact frame size.

| Parked G | Go steady MiB [min, max] | LLGo steady MiB [min, max] |
|---|---:|---:|
| 0 | 4.109 [4.094, 4.219] | 7.078 [7.062, 7.094] |
| 1,000 | 6.906 [6.859, 6.953] | 8.656 [8.641, 8.672] |
| 10,000 | 30.672 [30.656, 30.703] | 22.328 [22.312, 22.344] |
| 50,000 | 136.531 [136.484, 136.953] | 83.062 [83.031, 83.062] |
| 100,000 | 267.812 [267.438, 268.250] | 159.016 [159.000, 159.062] |

go: zero-G steady baseline 4.109 MiB; baseline-adjusted 100,000-G slope 2765.13 bytes/G.
llgo: zero-G steady baseline 7.078 MiB; baseline-adjusted 100,000-G slope 1593.18 bytes/G.

At 100,000 parked goroutines, LLGo's median steady process RSS is 40.6% lower
than Go's in this checkpoint. LLGo has a larger zero-goroutine baseline and
stripped executables. Spawn-serial takes 8.601 times Go's elapsed time; file
I/O takes 4.660 times. These costs remain visible alongside the faster compute
and ready-select rows; this measurement does not establish universal improvement.

Full-lifecycle peak RSS is a separate measurement:

| Parked G | Go peak MiB median [min, max] | LLGo peak MiB median [min, max] |
|---|---:|---:|
| 0 | 4.109 [4.094, 4.219] | 7.141 [7.125, 7.156] |
| 1,000 | 6.906 [6.859, 6.953] | 8.734 [8.703, 8.750] |
| 10,000 | 30.672 [30.656, 30.703] | 22.406 [22.391, 22.422] |
| 50,000 | 136.547 [136.500, 136.969] | 83.141 [83.094, 83.141] |
| 100,000 | 267.844 [267.469, 268.281] | 159.094 [159.078, 159.125] |

## Evidence and limits

All 272 throughput/progress/peak runs and all 80 separate steady-memory runs must complete before this report is generated. Each image, source, compiler and measurement script is hashed. Program runs keep the existing 2 GiB / 90 s guard; throughput and parked campaigns keep their 20/30-minute start budgets.

Raw samples are in `measurement-7pairs/samples.jsonl`, `samples.csv`, and `parked-measurement-7pairs/samples.jsonl`; summaries, logs, exact image hashes and source-after build proof remain beside this report. Performance tradeoffs and shared-host variance remain visible. No platform-wide, all-workload, or quiet-host performance claim follows from this checkpoint.

Local evidence directory:
`~/.cache/llgo-coro-acceptance/20261019/result-state/codegen-cost-s1/perf-final-s28/`.
`measurement-preflight.json` and `measurement-after.json` verify all executable,
source, measurement-script, compiler and external-library identities. The timed
interval is 2026-10-20 01:55:03–01:56:47 UTC. Initial task-owned cache maintenance
completed before preflight; no compatibility compiler campaign ran during the
comparison. External host load remains part of this observation.
The later historical-file COW deduplication occurred after the comparison and
before the supplemental expected-failure continuation.
The subsequent power-event audit confirms no host sleep during this measurement
interval; its receipt is retained in `demo-awake-s28-controller-r5/result.json`
beside the acceptance artifacts.
