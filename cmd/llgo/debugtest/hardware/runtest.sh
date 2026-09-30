#!/bin/bash

set -euo pipefail

if [[ "${LLGO_HARDWARE_CONFIRM:-}" != "flash" ]]; then
	echo "physical debugger test disabled; set LLGO_HARDWARE_CONFIRM=flash to allow halt/load" >&2
	exit 2
fi

target=${LLGO_HARDWARE_TARGET:?set LLGO_HARDWARE_TARGET to the checked-in target name}
gdb=${LLGO_HARDWARE_GDB:?set LLGO_HARDWARE_GDB to the target-aware GDB executable}
if ! command -v "$gdb" >/dev/null 2>&1; then
	echo "GDB executable not found: $gdb" >&2
	exit 1
fi
gdb=$(command -v "$gdb")

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_root=$(cd "$script_dir/../../../.." && pwd)
fixture_dir="$script_dir/../embedded"
source_file="$fixture_dir/C/c.go"
break_line=$(awk '/LLGO_EMBEDDED_DEBUG_BREAK/ { print NR; exit }' "$source_file")
if [[ -z "$break_line" ]]; then
	echo "embedded hardware breakpoint marker not found" >&2
	exit 1
fi

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/llgo-hardware-debug.XXXXXX")
cleanup() {
	rm -rf "$tmp_dir"
}
trap cleanup EXIT

llgo=${LLGO:-llgo}
if ! command -v "$llgo" >/dev/null 2>&1; then
	echo "llgo executable not found: $llgo" >&2
	exit 1
fi
llgo=$(command -v "$llgo")

remote=${LLGO_HARDWARE_REMOTE:-}
server=${LLGO_HARDWARE_SERVER:-}
if [[ -n "$remote" && -n "$server" ]]; then
	echo "LLGO_HARDWARE_REMOTE and LLGO_HARDWARE_SERVER are mutually exclusive" >&2
	exit 2
fi

debug_flags=(
	-backend=gdb
	-gdb "$gdb"
	-target "$target"
	-o "$tmp_dir/hardware-debug.elf"
)
if [[ -n "$remote" ]]; then
	debug_flags+=(-remote "$remote")
elif [[ -n "$server" ]]; then
	debug_flags+=(-server "$server")
fi

gdb_commands=(
	--nx --batch
	-ex "set pagination off"
	-ex "set confirm off"
)
if [[ ( -n "$remote" || -n "$server" ) && "${LLGO_HARDWARE_LOAD:-0}" == "1" ]]; then
	gdb_commands+=(
		-ex "monitor reset halt"
		-ex "load"
		-ex "monitor reset halt"
	)
fi
gdb_commands+=(
	-ex "break $source_file:$break_line"
	-ex "continue"
	-ex 'printf "LLGO_HARDWARE_SEED=%d\n", seed'
	-ex 'printf "LLGO_HARDWARE_PAIR=%d,%d\n", pair.Left, pair.Right'
	-ex 'printf "LLGO_HARDWARE_VALUES=%d,%d,%d\n", values[0], values[1], values[2]'
	-ex 'printf "LLGO_HARDWARE_RESULT=%d\n", result'
	-ex 'printf "LLGO_HARDWARE_SINK=%d\n", DebugSink'
	-ex "backtrace"
	-ex "detach"
)

if ! output=$(cd "$fixture_dir" && LLGO_ROOT="$repo_root" "$llgo" debug \
	"${debug_flags[@]}" . -- "${gdb_commands[@]}" 2>&1); then
	printf '%s\n' "$output" >&2
	exit 1
fi
printf '%s\n' "$output"

for expected in \
	"LLGO_HARDWARE_SEED=7" \
	"LLGO_HARDWARE_PAIR=7,8" \
	"LLGO_HARDWARE_VALUES=9,10,11" \
	"LLGO_HARDWARE_RESULT=33" \
	"LLGO_HARDWARE_SINK=33" \
	"C/c.go:$break_line"
do
	if [[ "$output" != *"$expected"* ]]; then
		echo "missing expected hardware debugger output: $expected" >&2
		exit 1
	fi
done

echo "LLGo physical probe source-debug acceptance passed for $target"
