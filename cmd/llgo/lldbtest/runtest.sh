#!/bin/bash

set -e

script_dir=$(cd "$(dirname "$0")" && pwd)

# Source common functions and variables
# shellcheck source=./common.sh
# shellcheck disable=SC1091
source "$script_dir/common.sh" || exit 1

# Parse command-line arguments
package_path="$DEFAULT_PACKAGE_PATH"
verbose=False
interactive=False
plugin_path=None

while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--verbose)
            verbose=True
            shift
            ;;
        -i|--interactive)
            interactive=True
            shift
            ;;
        -p|--plugin)
            plugin_path="\"$2\""
            shift 2
            ;;
        *)
            package_path="$1"
            shift
            ;;
    esac
done

# Build the project
build_project "$package_path" || exit 1

# Set up private paths for test results and auxiliary fixtures.
test_tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/llgo-lldbtest.XXXXXX")
trap 'rm -rf "$test_tmp_dir"' EXIT
result_file="$test_tmp_dir/exit-code"

# LLDB reports Python assertion failures in its output but can still exit with
# status 0 in batch mode. Treat a traceback as a test failure so compatibility
# fallback checks cannot pass silently.
run_checked_lldb() {
    local output
    if ! output=$("$@" 2>&1); then
        printf '%s\n' "$output"
        return 1
    fi
    printf '%s\n' "$output"
    if [[ "$output" == *"Traceback (most recent call last)"* ]]; then
        return 1
    fi
}

# Prepare LLDB commands
lldb_commands=(
    "command script import ./test.py"
    "script test.run_tests_with_result('./debug.out', ['main.go'], $verbose, $interactive, $plugin_path, '$result_file')"
    "quit"
)

# Prepare LLDB arguments without shell re-parsing.
lldb_args=()
for cmd in "${lldb_commands[@]}"; do
    lldb_args+=("-o" "$cmd")
done

cd "$package_path"
# Run LLDB with the embedded LLGo plugin and the test script.
llgo lldb -lldb "$LLDB_PATH" -- "${lldb_args[@]}"

# Read the exit code from the result file
if [ -f "$result_file" ]; then
    exit_code=$(cat "$result_file")
    rm "$result_file"
else
    echo "Error: Could not find exit code file"
    exit 1
fi

if [ "$exit_code" -ne 0 ]; then
    exit "$exit_code"
fi

run_checked_lldb llgo lldb -lldb "$LLDB_PATH" -- --batch "./debug.out" \
    -o 'script info = llgo_plugin.inspect_target(lldb.target); assert info.schema_version == 1 and info.runtime_layout_version == 1 and info.record_version == 1 and info.llgo_abi_version == 1 and info.cabi_mode == 2 and info.cabi_name == "allfunc" and info.pointer_size == lldb.target.GetAddressByteSize() and info.byte_order != "unknown"' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("llgo status", result); assert result.Succeeded() and "LLGo debugger schema v1 (runtime layout v1); LLGo ABI v1; C ABI mode 2 (allfunc)" in result.GetOutput()' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("llgo vars", result); assert not result.Succeeded() and "requires a stopped process" in result.GetError()' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("llgo print s", result); assert not result.Succeeded() and "requires a stopped process" in result.GetError()' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("llgo goroutines", result); assert not result.Succeeded() and "requires a stopped process" in result.GetError()'

# The LLGo formatter must not attach itself to an ordinary C target.
non_llgo_dir="$test_tmp_dir/non-llgo"
mkdir -p "$non_llgo_dir"
printf 'typedef struct { const char *data; unsigned long len; } string; string cstring = {"raw", 3}; int main(void) { return 0; }\n' | \
    "${CC:-cc}" -x c -g -o "$non_llgo_dir/non-llgo" -
run_checked_lldb llgo lldb -lldb "$LLDB_PATH" -- --batch "$non_llgo_dir/non-llgo" \
    -o 'script info = llgo_plugin.inspect_target(lldb.target); assert not info.marker_versions and not info.supported' \
    -o 'script value = lldb.target.FindFirstGlobalVariable("cstring"); assert value.IsValid() and value.GetSummary() is None and value.GetNumChildren() == 2' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("llgo status", result); assert result.Succeeded() and "Not an LLGo target" in result.GetOutput()' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("p 1+1", result); assert result.Succeeded() and "2" in result.GetOutput()'

# An unknown marker must disable only LLGo-specific presentation.
printf 'typedef struct { const char *data; unsigned long len; } string; string cstring = {"raw", 3}; __attribute__((used)) int __llgo_debugger_marker_v2 = 2; int main(void) { return 0; }\n' | \
    "${CC:-cc}" -x c -g -o "$non_llgo_dir/unsupported-llgo" -
run_checked_lldb llgo lldb -lldb "$LLDB_PATH" -- --batch "$non_llgo_dir/unsupported-llgo" \
    -o 'script info = llgo_plugin.inspect_target(lldb.target); assert info.marker_versions == (2,) and not info.supported' \
    -o 'script value = lldb.target.FindFirstGlobalVariable("cstring"); assert value.IsValid() and value.GetSummary() is None and value.GetNumChildren() == 2' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("llgo status", result); assert result.Succeeded() and "Unsupported LLGo debugger marker version(s): v2" in result.GetOutput()' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("llgo vars", result); assert not result.Succeeded() and "Unsupported LLGo debugger marker version(s): v2" in result.GetError()' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("llgo goroutines", result); assert not result.Succeeded() and "Unsupported LLGo debugger marker version(s): v2" in result.GetError()' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("p 1+1", result); assert result.Succeeded() and "2" in result.GetOutput()'

# Multiple marker versions are ambiguous even when one version is supported.
printf 'typedef struct { const char *data; unsigned long len; } string; string cstring = {"raw", 3}; __attribute__((used)) int __llgo_debugger_marker_v1 = 1; __attribute__((used)) int __llgo_debugger_marker_v2 = 2; int main(void) { return 0; }\n' | \
    "${CC:-cc}" -x c -g -o "$non_llgo_dir/ambiguous-llgo" -
run_checked_lldb llgo lldb -lldb "$LLDB_PATH" -- --batch "$non_llgo_dir/ambiguous-llgo" \
    -o 'script info = llgo_plugin.inspect_target(lldb.target); assert info.marker_versions == (1, 2) and not info.supported' \
    -o 'script value = lldb.target.FindFirstGlobalVariable("cstring"); assert value.IsValid() and value.GetSummary() is None and value.GetNumChildren() == 2' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("llgo status", result); assert result.Succeeded() and "Unsupported LLGo debugger marker version(s): v1, v2" in result.GetOutput()'

# A structured record is authoritative and must reject unsupported schemas or
# target-property mismatches without disabling ordinary LLDB commands.
printf 'typedef struct { const char *data; unsigned long len; } string; string cstring = {"raw", 3}; __attribute__((used)) int __llgo_debugger_marker_v1 = 1; __attribute__((used, visibility("hidden"))) unsigned char __llgo_debugger_abi_v1[16] = {0x4c,0x4c,0x47,0x4f,0x44,0x42,0x47,0,1,2,1,1,2,sizeof(void*),1,0}; int main(void) { return 0; }\n' | \
    "${CC:-cc}" -x c -g -o "$non_llgo_dir/unsupported-record" -
run_checked_lldb llgo lldb -lldb "$LLDB_PATH" -- --batch "$non_llgo_dir/unsupported-record" \
    -o 'script info = llgo_plugin.inspect_target(lldb.target); assert info.record_version == 1 and info.compatibility_error and not info.supported' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("llgo status", result); assert result.Succeeded() and "Unsupported LLGo debugger ABI" in result.GetOutput() and "unsupported record/schema/runtime/ABI versions" in result.GetOutput()' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("p 1+1", result); assert result.Succeeded() and "2" in result.GetOutput()'

printf 'typedef struct { const char *data; unsigned long len; } string; string cstring = {"raw", 3}; __attribute__((used)) int __llgo_debugger_marker_v1 = 1; __attribute__((used, visibility("hidden"))) unsigned char __llgo_debugger_abi_v1[16] = {0x4c,0x4c,0x47,0x4f,0x44,0x42,0x47,0,1,1,1,1,2,4,1,0}; int main(void) { return 0; }\n' | \
    "${CC:-cc}" -x c -g -o "$non_llgo_dir/mismatched-record" -
run_checked_lldb llgo lldb -lldb "$LLDB_PATH" -- --batch "$non_llgo_dir/mismatched-record" \
    -o 'script info = llgo_plugin.inspect_target(lldb.target); assert info.compatibility_error and "pointer size" in info.compatibility_error and not info.supported' \
    -o 'script value = lldb.target.FindFirstGlobalVariable("cstring"); assert value.IsValid() and value.GetSummary() is None and value.GetNumChildren() == 2' \
    -o 'script result = lldb.SBCommandReturnObject(); lldb.debugger.GetCommandInterpreter().HandleCommand("p 1+1", result); assert result.Succeeded() and "2" in result.GetOutput()'
