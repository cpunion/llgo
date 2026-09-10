#!/usr/bin/env bash
set -euo pipefail

typed_sha="${1:?usage: compare_wasm_reflect_ffi.sh <typed-sha>}"
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
result_root="${RUNNER_TEMP:-/tmp}/llgo-wasm-ffi-comparison"
typed_root="$result_root/typed-source"
artifact_root="$result_root/artifacts"
mkdir -p "$result_root" "$artifact_root"
git -C "$repo_root" worktree add --detach "$typed_root" "$typed_sha"
cleanup() {
	git -C "$repo_root" worktree remove --force "$typed_root"
}
trap cleanup EXIT

declare -A roots=( [libffi]="$repo_root" [typed]="$typed_root" )
declare -A compilers=( [libffi]="$result_root/llgo-libffi" [typed]="$result_root/llgo-typed" )
for implementation in libffi typed; do
	(
		cd "${roots[$implementation]}"
		LLGO_ROOT="$PWD" go build -p=2 -o "${compilers[$implementation]}" ./cmd/llgo
	)
done

printf 'implementation\tprofile\texample\tseconds\tmax_rss_kb\twasm_bytes\tglue_bytes\n' > "$result_root/build.tsv"
printf 'implementation\tprofile\trepetition\texit_code\toutput\n' > "$result_root/runtime.tsv"
for profile in ec32 ec64; do
	case "$profile" in
		ec32) target=emscripten; runner=emscripten-runner.mjs ;;
		ec64) target=emscripten-memory64; runner=emscripten-memory64-runner.mjs ;;
	esac
	for example in cprintf println fmtprintf reflectcall; do
		for implementation in libffi typed; do
			source_root="${roots[$implementation]}"
			compiler="${compilers[$implementation]}"
			if [[ "$example" == reflectcall ]]; then
				fixture="$repo_root/internal/build/testdata/wasm-reflect-benchmark/main.go"
			else
				fixture="$source_root/benchmark/binary_size/$example/main.go"
			fi
			output_dir="$artifact_root/$implementation/$profile/$example"
			output="$output_dir/program.mjs"
			timing="$output_dir/time.txt"
			link_map="$output_dir/link.map"
			mkdir -p "$output_dir"
			(
				cd "$source_root"
				env LLGO_ROOT="$source_root" LLGO_BUILD_CACHE=off "$compiler" build -target "$target" -o "$output" "$fixture" >/dev/null
				rm -f "$output" "${output%.mjs}.wasm"
				/usr/bin/time -f '%e\t%M' -o "$timing" env LLGO_ROOT="$source_root" LLGO_BUILD_CACHE=off LDFLAGS="-Wl,--Map=$link_map" "$compiler" build -target "$target" -o "$output" "$fixture" >/dev/null
			)
			read -r seconds max_rss_kb < "$timing"
			wasm_bytes="$(stat -c %s "${output%.mjs}.wasm")"
			glue_bytes="$(stat -c %s "$output")"
			printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$implementation" "$profile" "$example" "$seconds" "$max_rss_kb" "$wasm_bytes" "$glue_bytes" >> "$result_root/build.tsv"
			if [[ "$example" == reflectcall ]]; then
				for repetition in 1 2 3 4 5; do
					if runtime_output="$(node "$typed_root/targets/$runner" "$output" 2>&1)"; then
						runtime_status=0
					else
						runtime_status=$?
					fi
					printf '%s\t%s\t%s\t%s\t' "$implementation" "$profile" "$repetition" "$runtime_status" >> "$result_root/runtime.tsv"
					printf '%s' "$runtime_output" | tr '\n' '\t' >> "$result_root/runtime.tsv"
					printf '\n' >> "$result_root/runtime.tsv"
				done
			fi
		done
	done
done

cat "$result_root/build.tsv"
cat "$result_root/runtime.tsv"
for implementation in libffi typed; do
	for profile in ec32 ec64; do
		module="$artifact_root/$implementation/$profile/reflectcall/program.wasm"
		printf '\n%s %s sections\n' "$implementation" "$profile" >> "$result_root/sections.txt"
		llvm-objdump -h "$module" >> "$result_root/sections.txt"
	done
done
cat "$result_root/sections.txt"
