# Native Go / LLGo comparison, frozen final S29 candidate cf54d9f6d

Candidate SDK source manifest: `c2ace95d379499905ea42bfad469595eb6962c8fc9839e3dc347ae077f728b70`; LLGo compiler SHA-256: `7b04ddcb13e50c9615f6b3a7c19c112582e5fa64a55d5fe1e4c7fe4fe1dbd778`. The compiler and SDK use the same PR #301 S29 freeze. This report is a shared-host checkpoint with raw ranges, not a general regression budget.

go version go1.27.0 darwin/arm64; LLVM 22.1.8; Apple M4 Max (Mac16,5), 16 logical CPUs, 128 GiB RAM. ProductName:		macOS; ProductVersion:		27.0.1; BuildVersion:		26A434.

Both compilers build the same ordinary Go sources with `-p=1 -trimpath -ldflags='-s -w'`. Each case has one prior warmup pair and seven alternating Go/LLGo pairs. P=1 is set at process start except the explicit P=4 row. Time is the common program's elapsed field; all checksums and exact completion records pass. No timed sample is discarded.

Observed one-minute load ranges: throughput 19.43–21.11; parked-memory 15.26–22.18. Per-run loads and process/power snapshots are retained.

| Workload | Go ms median [min, max] | LLGo ms median [min, max] | LLGo / Go |
|---|---:|---:|---:|
| static-compute | 82.139 [81.905, 82.584] | 76.860 [76.325, 78.794] | 0.936x |
| parallel-P1 | 65.861 [64.885, 70.612] | 63.743 [63.247, 64.128] | 0.968x |
| parallel-P4 | 16.547 [16.464, 16.585] | 15.739 [15.558, 15.912] | 0.951x |
| buffered | 81.359 [80.547, 82.849] | 81.698 [81.299, 84.659] | 1.004x |
| select-ready | 106.769 [105.925, 108.185] | 76.385 [74.019, 77.551] | 0.715x |
| spawn-batch | 8.390 [8.359, 8.485] | 21.644 [21.302, 21.777] | 2.580x |
| spawn-serial | 17.180 [16.814, 17.747] | 147.178 [146.138, 151.369] | 8.567x |
| handoff | 15.572 [15.495, 15.974] | 20.861 [20.673, 21.823] | 1.340x |
| timers | 127.102 [126.039, 127.604] | 140.160 [139.932, 140.974] | 1.103x |
| file | 10.414 [10.146, 10.531] | 47.804 [47.122, 50.919] | 4.590x |
| file-syscall | 10.281 [10.132, 10.578] | 14.618 [14.360, 14.946] | 1.422x |
| tcp | 92.349 [75.602, 95.103] | 207.660 [203.775, 210.120] | 2.249x |

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
| 0 | 4.078 [4.062, 4.094] | 7.047 [7.031, 7.047] |
| 1,000 | 6.906 [6.844, 6.938] | 8.625 [8.625, 8.641] |
| 10,000 | 30.609 [30.578, 30.641] | 22.328 [22.312, 22.344] |
| 50,000 | 137.156 [136.469, 137.297] | 83.031 [83.031, 83.047] |
| 100,000 | 267.828 [266.266, 268.281] | 159.016 [158.984, 159.016] |

go: zero-G steady baseline 4.078 MiB; baseline-adjusted 100,000-G slope 2765.62 bytes/G.
llgo: zero-G steady baseline 7.047 MiB; baseline-adjusted 100,000-G slope 1593.51 bytes/G.

Full-lifecycle peak RSS is a separate measurement:

| Parked G | Go peak MiB median [min, max] | LLGo peak MiB median [min, max] |
|---|---:|---:|
| 0 | 4.078 [4.062, 4.094] | 7.125 [7.109, 7.141] |
| 1,000 | 6.906 [6.844, 6.938] | 8.703 [8.688, 8.719] |
| 10,000 | 30.609 [30.578, 30.641] | 22.391 [22.375, 22.406] |
| 50,000 | 137.172 [136.484, 137.312] | 83.094 [83.078, 83.109] |
| 100,000 | 267.859 [266.297, 268.312] | 159.078 [159.047, 159.094] |

## Evidence and limits

All 272 throughput/progress/peak runs and all 80 separate steady-memory runs must complete before this report is generated. Each image, source, compiler and measurement script is hashed. Program runs keep the existing 2 GiB / 90 s guard; throughput and parked campaigns keep their 20/30-minute start budgets.

Raw samples are in `measurement-7pairs/samples.jsonl`, `samples.csv`, and `parked-measurement-7pairs/samples.jsonl`; summaries, logs, exact image hashes and source-after build proof remain beside this report. Performance tradeoffs and shared-host variance remain visible. No platform-wide, all-workload, or quiet-host performance claim follows from this checkpoint.
