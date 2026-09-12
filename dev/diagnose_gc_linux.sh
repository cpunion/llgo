#!/usr/bin/env bash
set -euo pipefail

repo=$PWD
results="$repo/gc-linux-results"
mkdir -p "$results"
{
  uname -a
  lscpu
  free -h
  go version
  clang --version
  llvm-config --version
  pkg-config --modversion bdw-gc libffi
  dpkg-query -W libgc1 libgc-dev libc6
  git rev-parse HEAD
} >"$results/environment.txt"

printf '| Revision | Case | Exit | Wall seconds |\n| --- | --- | --- | --- |\n' >"$results/summary.md"

run_case() {
  local label=$1
  shift
  local started=$SECONDS
  local status=0
  "$@" >"$output/$label.log" 2>&1 || status=$?
  printf '%s: exit=%s wall=%ss\n' "$label" "$status" "$((SECONDS-started))"
  tail -n 45 "$output/$label.log"
  printf '| %s | %s | %s | %s |\n' "$subject" "$label" "$status" "$((SECONDS-started))" >>"$results/summary.md"
  # Preserve every failure. Each invocation is a measured trial, not a retry.
}

for subject in main panic-pr; do
  case "$subject" in
    main) revision=b07cd12ad1ace793047dcf1c12ece8c01b139565 ;;
    panic-pr) revision=7aa6c5f92f984f71a7232d7aecf3694a522715c8 ;;
  esac
  output="$results/$subject"
  source="$RUNNER_TEMP/gc-subject-$subject"
  mkdir -p "$output"
  git worktree add --detach "$source" "$revision"
  mkdir -p "$source/test/_stress/runtime/gc"
  cp "$repo/test/_stress/runtime/gc/gc_stress_test.go" "$source/test/_stress/runtime/gc/"
  (
    cd "$source"
    git rev-parse HEAD >"$output/revision.txt"
    go build -o "$output/llgo" ./cmd/llgo
    export LLGO_ROOT="$source"
    "$output/llgo" test -c -debug-trace="$output/build-go.json" -o "$output/go.test" ./test/go
    cd test/_stress
    "$output/llgo" test -c -debug-trace="$output/build-stress.json" -o "$output/stress.test" ./runtime/gc
    go test -c -o "$output/go-stress.test" ./runtime/gc
  )
  ldd "$output/stress.test" >"$output/libraries.txt"
  sha256sum "$output/stress.test" "$source/test/_stress/runtime/gc/gc_stress_test.go" >"$output/checksums.txt"
  run_case go-control timeout -k 5s 60s "$output/go-stress.test" -test.v -test.count=3 -test.timeout=50s
  for markers in default 1; do
    for trial in 1 2 3; do
      marker_env=(env -u GC_MARKERS)
      if [[ "$markers" != default ]]; then marker_env=(env "GC_MARKERS=$markers"); fi
      run_case "stress-$markers-$trial" timeout -k 5s 100s "${marker_env[@]}" "$output/stress.test" -test.v -test.run='^TestGCMakeFuncProgress$' -test.count=1 -test.timeout=95s
    done
  done
  run_case original-focused timeout -k 5s 100s env -u GC_MARKERS "$output/go.test" -test.v -test.run='^TestReflectMakeFuncGoroutine(Startup|GC)$' -test.count=3 -test.timeout=90s
  run_case original-full timeout -k 5s 120s env -u GC_MARKERS "$output/go.test" -test.v -test.count=1 -test.timeout=110s

  # Keep ptrace sampling separate from the measured trials: stopping threads
  # changes scheduling and must never be used to declare a timing test fixed.
  env -u GC_MARKERS "$output/go.test" -test.v -test.run='^TestReflectMakeFuncGoroutineStartup$' -test.count=1 -test.timeout=45s >"$output/sampled.log" 2>&1 &
  sample_pid=$!
  sleep 3
  if kill -0 "$sample_pid" 2>/dev/null; then
    ps -L -p "$sample_pid" -o pid,tid,pcpu,stat,wchan:32,comm >"$output/threads.txt"
    sudo timeout -k 2s 15s gdb --batch --nx -p "$sample_pid" -ex 'set pagination off' -ex 'thread apply all bt 20' -ex detach >"$output/stacks.txt" 2>&1 || true
    kill -TERM "$sample_pid" 2>/dev/null || true
  fi
  wait "$sample_pid" || true
done

cat "$results/summary.md" >>"$GITHUB_STEP_SUMMARY"
