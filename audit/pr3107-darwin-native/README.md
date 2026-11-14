Native macOS arm64, Apple M4 Max, Go 1.27.3, LLVM 22.1.8.

PR head a582e400267716ae3b49ef97e63ecda503ad16d9 versus main 6a482f7f6ad94e9f2d0a0fea97dd4cabd46b1130; both use the main runtime. Frozen wasmvm source 5e5e6a44b2062d61bfa569f384232483763d3380, normal GC and bounds checks, O3 Full LTO, 20 interleaved blocks. Seed 2026111506. Exact binaries and hashes are recorded in results.json.gz. Go and main binaries are reused, checksum checked.

Protocol: https://github.com/cpunion/llgo/blob/88df6a0311636b8a42f29bd5dd01aaa6c9ca9cfe/.github/scripts/verify_lto_inline.py . Darwin adaptation removes Linux CPU affinity and records that limitation. Paired bootstrap intervals use 8000 resamples. Both relevant same-binary A/A controls must resolve; do not promote unresolved rows to a conclusion. No emulated throughput measurements.

`PR3107/main` is the actual PR. `old-ASM/main` is an earlier experimental entry-marker/defer-location variant, included only as a control. No performance claim is made for unresolved rows.
