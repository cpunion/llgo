#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <source-root> <llgo-output> <result-directory>" >&2
  exit 2
fi

harness_root="$(cd "$(dirname "$0")/../.." && pwd)"

source_root="$(cd "$1" && pwd)"
mkdir -p "$(dirname "$2")" "$3"
llgo_output="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
result_directory="$(cd "$3" && pwd)"

(
  cd "$source_root"
  LLGO_ROOT="$source_root" go build -p=1 -o "$llgo_output" ./cmd/llgo
)

(
  cd "$harness_root"
  # Keep one current-checkout harness for both source revisions. Benchmark
  # suite changes must therefore remain executable against the PR base.
  LLGO_ROOT="$source_root" go run ./benchmark/baseline \
    -root "$source_root" \
    -llgo "$llgo_output" \
    -out "$result_directory"
)

go_results="$result_directory/go.txt"
: > "$go_results"

compiler_benchmarks='^(BenchmarkMergeCompilerFlags|BenchmarkMergeLinkerFlags|BenchmarkLookupPCRandom)$'
core_benchmarks='^(BenchmarkRuntimeGetG|BenchmarkGlobal(Read|Write)|Benchmark(DirectCall|InterfaceCall|Defer|ChannelBuffered|ChannelHandoff))$'

drop_first_benchmark_sample() {
  awk '
    /^Benchmark/ {
      name = $1
      sub(/-[0-9]+$/, "", name)
      if (++seen[name] == 1) next
    }
    { print }
  '
}

# The first one-second sample warms each path and is discarded. The following
# seven samples use the same benchmark process and fixed single-CPU conditions.
(
  cd "$source_root"
  GOMAXPROCS=1 LLGO_ROOT="$source_root" go test \
    -run '^$' \
    -bench "$compiler_benchmarks" \
    -benchtime=1s \
    -count=8 \
    -cpu=1 \
    ./internal/clang ./internal/build/funcinfo
) | drop_first_benchmark_sample | tee -a "$go_results"

(
  cd "$source_root"
  GOMAXPROCS=1 LLGO_ROOT="$source_root" "$llgo_output" test \
    -run '^$' \
    -bench "$core_benchmarks" \
    -benchtime=1s \
    -count=8 \
    -cpu=1 \
    ./test/llgoext
) | drop_first_benchmark_sample | tee -a "$go_results"

# The current native backend creates one pthread per goroutine and intentionally
# has a bounded lifecycle stress limit. Keep creation monitoring deterministic
# instead of letting testing auto-calibrate to millions of host threads.
(
  cd "$source_root"
  GOMAXPROCS=1 LLGO_ROOT="$source_root" "$llgo_output" test \
    -run '^$' \
    -bench '^BenchmarkGoroutine$' \
    -benchtime=100x \
    -count=8 \
    -cpu=1 \
    ./test/llgoext
) | drop_first_benchmark_sample | tee -a "$go_results"

memprofile_noconsumer="$result_directory/bin/memprofile-noconsumer"
memprofile_enabled="$result_directory/bin/memprofile-enabled"
(
  cd "$harness_root"
  GOMAXPROCS=1 LLGO_ROOT="$source_root" LLGO_FULL_RPATH=true "$llgo_output" build \
    -o "$memprofile_noconsumer" \
    ./benchmark/memprofile/noconsumer
  GOMAXPROCS=1 LLGO_ROOT="$source_root" LLGO_FULL_RPATH=true "$llgo_output" build \
    -o "$memprofile_enabled" \
    ./benchmark/memprofile/enabled
)

# Each executable also warms its allocator internally before timing. These
# discarded processes additionally remove loader and first-execution effects.
GOMAXPROCS=1 "$memprofile_noconsumer" >/dev/null 2>&1
GOMAXPROCS=1 "$memprofile_enabled" rate0 >/dev/null 2>&1
GOMAXPROCS=1 "$memprofile_enabled" >/dev/null 2>&1

# Rotate the order so every mode occupies each scheduling position across the
# seven independent process samples.
for round in {0..6}; do
  case $((round % 3)) in
    0) modes=(noconsumer rate0 default) ;;
    1) modes=(rate0 default noconsumer) ;;
    2) modes=(default noconsumer rate0) ;;
  esac
  for mode in "${modes[@]}"; do
    case "$mode" in
      noconsumer) GOMAXPROCS=1 "$memprofile_noconsumer" 2>&1 ;;
      rate0) GOMAXPROCS=1 "$memprofile_enabled" rate0 2>&1 ;;
      default) GOMAXPROCS=1 "$memprofile_enabled" 2>&1 ;;
    esac
  done
done | tee -a "$go_results"

(
  cd "$harness_root"
  go run ./benchmark/baseline \
    -mode export \
    -out "$result_directory" \
    -benchmark-output "$result_directory/benchmark.txt"
)
