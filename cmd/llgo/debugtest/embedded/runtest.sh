#!/bin/bash

set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_root=$(cd "$script_dir/../../../.." && pwd)
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/llgo-embedded-debug.XXXXXX")
qemu_pid=

cleanup() {
	if [[ -n "$qemu_pid" ]]; then
		kill "$qemu_pid" 2>/dev/null || true
		wait "$qemu_pid" 2>/dev/null || true
	fi
	rm -rf "$tmp_dir"
}
trap cleanup EXIT

find_tool() {
	local candidate
	for candidate in "$@"; do
		if [[ -n "$candidate" ]] && command -v "$candidate" >/dev/null 2>&1; then
			command -v "$candidate"
			return
		fi
	done
	return 1
}

require_tool() {
	local description=$1
	shift
	local path
	if ! path=$(find_tool "$@"); then
		echo "missing $description (tried: $*)" >&2
		exit 1
	fi
	printf '%s\n' "$path"
}

assert_contains() {
	local output=$1
	local expected=$2
	if [[ "$output" != *"$expected"* ]]; then
		echo "missing expected debugger output: $expected" >&2
		printf '%s\n' "$output" >&2
		exit 1
	fi
}

free_port() {
	python3 - <<'PY'
import socket

with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

start_qemu() {
	local port=$1
	"$qemu" -machine lm3s6965evb -nographic -kernel "$debug_elf" \
		-S -gdb "tcp::$port" >"$tmp_dir/qemu-$port.log" 2>&1 &
	qemu_pid=$!
	sleep 1
	if ! kill -0 "$qemu_pid" 2>/dev/null; then
		cat "$tmp_dir/qemu-$port.log" >&2
		exit 1
	fi
}

stop_qemu() {
	if [[ -n "$qemu_pid" ]]; then
		kill "$qemu_pid" 2>/dev/null || true
		wait "$qemu_pid" 2>/dev/null || true
		qemu_pid=
	fi
}

llgo=$(require_tool llgo "${LLGO:-}" llgo)
objcopy=$(require_tool llvm-objcopy "${LLVM_OBJCOPY:-}" llvm-objcopy llvm-objcopy-19)
dwarfutil=$(require_tool llvm-dwarfutil "${LLVM_DWARFUTIL:-}" llvm-dwarfutil llvm-dwarfutil-19)
dwarfdump=$(require_tool llvm-dwarfdump "${LLVM_DWARFDUMP:-}" llvm-dwarfdump llvm-dwarfdump-19)
gdb=$(require_tool GDB "${LLGO_GDB:-}" gdb-multiarch arm-none-eabi-gdb gdb)
lldb=$(require_tool LLDB "${LLGO_LLDB:-}" lldb-19 lldb)
qemu=$(require_tool qemu-system-arm "${LLGO_QEMU_SYSTEM_ARM:-}" qemu-system-arm)

debug_elf="$tmp_dir/embedded-debug.elf"
stripped_elf="$tmp_dir/embedded-stripped.elf"
debug_bin="$tmp_dir/embedded-debug.bin"
stripped_bin="$tmp_dir/embedded-stripped.bin"
verified_elf="$tmp_dir/embedded-verified.elf"
source_file="$script_dir/C/c.go"
break_line=$(awk '/LLGO_EMBEDDED_DEBUG_BREAK/ { print NR; exit }' "$source_file")
if [[ -z "$break_line" ]]; then
	echo "embedded debug breakpoint marker not found" >&2
	exit 1
fi

(
	cd "$script_dir"
	LLGO_ROOT="$repo_root" "$llgo" build -O0 -target=cortex-m-qemu \
		-debug-artifact=host -ldflags=-w=false -o "$debug_elf" .
)

# Debug sections are host-only. Stripping them from the same final ELF must not
# alter the bytes that are loaded into target flash.
"$objcopy" --strip-debug "$debug_elf" "$stripped_elf"
"$objcopy" -O binary "$debug_elf" "$debug_bin"
"$objcopy" -O binary "$stripped_elf" "$stripped_bin"
cmp "$debug_bin" "$stripped_bin"

# LLD section GC leaves tombstone DIEs for discarded functions. Verify a
# garbage-collected copy because LLVM 19 dwarfutil drops live global-variable
# DIEs as well; the original artifact below remains the debugger input.
"$dwarfutil" --garbage-collection --verify "$debug_elf" "$verified_elf"
"$dwarfdump" --verify "$verified_elf"

gdb_port=$(free_port)
start_qemu "$gdb_port"
if ! gdb_output=$("$gdb" --nx --quiet --batch "$debug_elf" \
	-ex "set pagination off" \
	-ex "set confirm off" \
	-ex "target remote 127.0.0.1:$gdb_port" \
	-ex "break $source_file:$break_line" \
	-ex "continue" \
	-ex 'printf "LLGO_SEED=%d\\n", seed' \
	-ex 'printf "LLGO_PAIR=%d,%d\\n", pair.Left, pair.Right' \
	-ex 'printf "LLGO_VALUES=%d,%d,%d\\n", values[0], values[1], values[2]' \
	-ex 'printf "LLGO_TEXT_LEN=%u\\n", text.len' \
	-ex 'printf "LLGO_RESULT=%d\\n", result' \
	-ex 'printf "LLGO_SINK=%d\\n", DebugSink' \
	-ex "backtrace" 2>&1); then
	printf '%s\n' "$gdb_output" >&2
	exit 1
fi
stop_qemu
assert_contains "$gdb_output" "LLGO_SEED=7"
assert_contains "$gdb_output" "LLGO_PAIR=7,8"
assert_contains "$gdb_output" "LLGO_VALUES=9,10,11"
assert_contains "$gdb_output" "LLGO_TEXT_LEN=8"
assert_contains "$gdb_output" "LLGO_RESULT=33"
assert_contains "$gdb_output" "LLGO_SINK=33"
assert_contains "$gdb_output" "Reset_Handler"
assert_contains "$gdb_output" "C/c.go:$break_line"

lldb_port=$(free_port)
start_qemu "$lldb_port"
if ! lldb_output=$("$lldb" --batch "$debug_elf" \
	-o "gdb-remote 127.0.0.1:$lldb_port" \
	-o "target modules load --file $debug_elf --slide 0" \
	-o "breakpoint set --file c.go --line $break_line" \
	-o "continue" \
	-o "frame variable seed pair values text result" \
	-o "target variable DebugSink" \
	-o "thread backtrace" 2>&1); then
	printf '%s\n' "$lldb_output" >&2
	exit 1
fi
stop_qemu
if [[ "$lldb_output" == *"Traceback (most recent call last)"* ]]; then
	printf '%s\n' "$lldb_output" >&2
	exit 1
fi
assert_contains "$lldb_output" "seed = 7"
assert_contains "$lldb_output" "Left = 7"
assert_contains "$lldb_output" "Right = 8"
assert_contains "$lldb_output" "result = 33"
assert_contains "$lldb_output" "DebugSink = 33"
assert_contains "$lldb_output" "Reset_Handler"
assert_contains "$lldb_output" "c.go:$break_line"

echo "embedded GDB Remote and LLDB gdb-remote checks passed"
