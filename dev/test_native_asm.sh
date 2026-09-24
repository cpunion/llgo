#!/usr/bin/env bash

# Exercise the installed compiler on the existing four release-artifact hosts.
set -euo pipefail

llgo_cmd="${LLGO:-llgo}"
if [[ "${llgo_cmd}" != */* ]]; then
	llgo_cmd="$(command -v "${llgo_cmd}")"
elif [[ "${llgo_cmd}" != /* ]]; then
	llgo_cmd="$(cd "$(dirname "${llgo_cmd}")" && pwd)/$(basename "${llgo_cmd}")"
fi

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/llgo-native-asm.XXXXXX")"
work_dir="$(cd "${work_dir}" && pwd)"
trap 'rm -rf "${work_dir}"' EXIT
lib_dir="${work_dir}/library with spaces"
app_dir="${work_dir}/app"
mkdir -p "${lib_dir}" "${app_dir}"
export GOWORK=off

cat >"${lib_dir}/probe.c" <<'EOF'
long long answer(long long x) { return x + 7; }
long long invoke(long long (*fn)(long long), long long x) { return fn(x); }
EOF
case "$(uname -s)" in
	Darwin)
		library="${lib_dir}/libprobe.dylib"
		cc_flags=(-dynamiclib -fPIC "-Wl,-install_name,${library}")
		;;
	Linux)
		library="${lib_dir}/libprobe.so.1"
		cc_flags=(-shared -fPIC -Wl,-soname,libprobe.so.1)
		;;
	*) echo "unsupported native assembly test host" >&2; exit 1 ;;
esac
clang "${cc_flags[@]}" "${lib_dir}/probe.c" -o "${library}"

cat >"${app_dir}/go.mod" <<'EOF'
module example.com/native-shared-library

go 1.20
EOF
cat >"${app_dir}/bridge.s" <<'EOF'
#include "textflag.h"
TEXT bridge<>(SB), NOSPLIT, $0
 JMP imported(SB)
GLOBL ·entry(SB), RODATA, $8
DATA ·entry(SB)/8, $bridge<>(SB)
EOF

libraries=("${library}")
if [[ "$(uname -s)" == Linux ]]; then
	libraries+=(libprobe.so.1)
fi
for imported_library in "${libraries[@]}"; do
	cat >"${app_dir}/main.go" <<EOF
package main
import _ "unsafe"
//go:cgo_import_dynamic imported answer "${imported_library}"
var entry uintptr
//go:linkname invoke C.invoke
func invoke(fn uintptr, x int64) int64
func main() {
 if invoke(entry, 35) != 42 || invoke(entry, -9) != -2 { panic("native argument/result") }
 println("ok")
}
EOF
	for mode in off thin full; do
		lto_flags=()
		if [[ "${mode}" != off ]]; then
			lto_flags=("-lto=${mode}")
		fi
		echo "==> native assembly: ${imported_library}, LTO ${mode}"
		(
			cd "${app_dir}"
			# Only the source directive supplies the library; no extra -l flag.
			LIBRARY_PATH="${lib_dir}" "${llgo_cmd}" build -O2 ${lto_flags[@]+"${lto_flags[@]}"} -o probe .
			output="$(LD_LIBRARY_PATH="${lib_dir}" ./probe 2>&1)"
			if [[ "${output}" != ok ]]; then
				printf 'unexpected native callback output: %s\n' "${output}" >&2
				exit 1
			fi
		)
	done
done
