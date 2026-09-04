#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/dev/wasm_ci_report.sh"
llgo_cmd="${LLGO:-llgo}"
node_cmd="${NODE:-node}"
wasmtime_cmd="${WASMTIME:-wasmtime}"
scheduler_fixture="${repo_root}/internal/build/testdata/wasm-scheduler"
timer_fixture="${repo_root}/internal/build/testdata/wasm-timers"
callback_fixture="${repo_root}/internal/build/testdata/wasm-callback"
gc_fixture="${repo_root}/internal/build/testdata/wasm-gc"
lifecycle_fixture="${repo_root}/internal/build/testdata/wasm-lifecycle"
test_fixture="${repo_root}/internal/build/testdata/wasm-test"
secondary_test_fixture="${repo_root}/internal/build/testdata/wasm-test-secondary"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/llgo-wasm-single-worker.XXXXXX")"
report_file="${work_dir}/coverage.tsv"
wasm_ci_report_init "${report_file}"

finish() {
	local status=$?
	trap - EXIT
	set +e
	wasm_ci_publish_report "${report_file}" "${status}"
	rm -rf "${work_dir}"
	exit "${status}"
}
trap finish EXIT
export LLGO_WASM_TEST_ENV=wasm-env-ok

run_with_timeout() {
	run_with_timeout_limit 180s "$@"
}

run_with_timeout_limit() {
	local limit="$1"
	shift
	if command -v timeout >/dev/null 2>&1; then
		timeout "${limit}" "$@"
	else
		"$@"
	fi
}

expect_failure() {
	local expected="$1"
	shift
	local output exit_code
	set +e
	output="$(run_with_timeout "$@" 2>&1)"
	exit_code=$?
	set -e
	printf '%s\n' "${output}"
	if [[ ${exit_code} -ne 2 ]]; then
		echo "expected exit status 2, got ${exit_code}: $*" >&2
		exit 1
	fi
	grep -Fq "${expected}" <<<"${output}"
}

assert_no_implicit_wasm_artifacts() {
	local temp_dir="$1"
	local artifact
	artifact="$(find "${temp_dir}" -type f \( -name '*.mjs' -o -name '*.wasm' \) -print -quit)"
	if [[ -n "${artifact}" ]]; then
		echo "implicit WebAssembly artifact was not removed: ${artifact}" >&2
		exit 1
	fi
}

expect_llgo_runner_failure() {
	local target="$1"
	local profile="$2"
	local runner="$3"
	local fixture="$4"
	local output exit_code
	local temp_dir="${work_dir}/runner-failure-${target}-tmp"
	mkdir -p "${temp_dir}"

	set +e
	output="$(run_with_timeout env TMPDIR="${temp_dir}" LLGO_WASM_SCHEDULER_DEADLOCK=1 \
		"${llgo_cmd}" run -target "${target}" -emulator "${fixture}" 2>&1)"
	exit_code=$?
	set -e
	printf '%s\n' "${output}"
	if [[ ${exit_code} -ne 1 ]]; then
		echo "expected llgo runner failure status 1, got ${exit_code}" >&2
		exit 1
	fi
	for expected in \
		"phase=run" \
		"target=${target}" \
		"profile=${profile}" \
		'artifact="' \
		"runner=\"${runner}\"" \
		'package="github.com/xgo-dev/llgo/internal/build/testdata/wasm-scheduler"' \
		"status=exit" \
		"exit_code=2"; do
		grep -Fq "${expected}" <<<"${output}"
	done
	assert_no_implicit_wasm_artifacts "${temp_dir}"
}

expect_llgo_runner_timeout() {
	local target="$1"
	local profile="$2"
	local runner="$3"
	local fixture="$4"
	local output exit_code
	local temp_dir="${work_dir}/runner-timeout-${target}-tmp"
	mkdir -p "${temp_dir}"

	set +e
	output="$(run_with_timeout env TMPDIR="${temp_dir}" LLGO_WASM_SCHEDULER_HANG=1 \
		"${llgo_cmd}" run -timeout=1s -target "${target}" -emulator "${fixture}" 2>&1)"
	exit_code=$?
	set -e
	printf '%s\n' "${output}"
	if [[ ${exit_code} -ne 1 ]]; then
		echo "expected llgo runner timeout status 1, got ${exit_code}" >&2
		exit 1
	fi
	for expected in \
		"phase=run" \
		"target=${target}" \
		"profile=${profile}" \
		'artifact="' \
		"runner=\"${runner}\"" \
		'package="github.com/xgo-dev/llgo/internal/build/testdata/wasm-scheduler"' \
		"status=timeout" \
		"timeout=1s"; do
		grep -Fq "${expected}" <<<"${output}"
	done
	assert_no_implicit_wasm_artifacts "${temp_dir}"
}

run_emscripten() {
	local target="$1"
	local runner="$2"
	local fixture="$3"
	local expected="$4"
	local name="$5"
	local module="${work_dir}/${name}.mjs"

	"${llgo_cmd}" build -target "${target}" -o "${module}" "${fixture}"
	wasm-tools validate --features all "${work_dir}/${name}.wasm"
	run_with_timeout "${node_cmd}" "${repo_root}/targets/${runner}" "${module}" 2>&1 | tee "${work_dir}/${name}.out"
	grep -Fq "${expected}" "${work_dir}/${name}.out"
}

run_wasi() {
	local target="$1"
	local fixture="$2"
	local expected="$3"
	local name="$4"
	local module="${work_dir}/${name}.wasm"

	"${llgo_cmd}" build -target "${target}" -o "${module}" "${fixture}"
	wasm-tools validate --features all "${module}"
	run_with_timeout "${wasmtime_cmd}" run -W exceptions=y \
		--env LLGO_WASM_TEST_ENV="${LLGO_WASM_TEST_ENV}" "${module}" 2>&1 | tee "${work_dir}/${name}.out"
	grep -Fq "${expected}" "${work_dir}/${name}.out"
}

run_llgo_run() {
	local target="$1"
	local fixture="$2"
	local expected="$3"
	local name="$4"
	local output="${work_dir}/${name}.out"
	local temp_dir="${work_dir}/${name}-tmp"
	mkdir -p "${temp_dir}"

	# Exercise the public command and its target-owned runner. Other fixtures in
	# this script retain explicit artifacts for wasm-tools validation, while this
	# path verifies that users do not need to assemble Node or Wasmtime commands.
	echo "testing public llgo run command for ${target}"
	run_with_timeout env TMPDIR="${temp_dir}" "${llgo_cmd}" run -target "${target}" -emulator "${fixture}" 2>&1 | tee "${output}"
	grep -Fq "${expected}" "${output}"
	assert_no_implicit_wasm_artifacts "${temp_dir}"
}

run_llgo_test() {
	local target="$1"
	local name="$2"
	local output="${work_dir}/${name}.out"
	local temp_dir="${work_dir}/${name}-tmp"
	mkdir -p "${temp_dir}"

	# Binaryen's post-Asyncify processing of this standard-library test takes
	# about 165 seconds on a local arm64 host and exceeded 180 seconds on the
	# shared x86-64 runner. Keep execution bounded without treating normal
	# compiler variance as a scheduler failure.
	echo "testing public llgo test command for ${target}"
	run_with_timeout_limit 300s env TMPDIR="${temp_dir}" "${llgo_cmd}" test -target "${target}" -emulator \
		-v -count=1 -timeout=30s "${test_fixture}" "${secondary_test_fixture}" 2>&1 | tee "${output}"
	grep -Fq "PASS" "${output}"
	grep -Fq "TestScheduler" "${output}"
	grep -Fq "wasm secondary package ok" "${output}"
	assert_no_implicit_wasm_artifacts "${temp_dir}"
}

run_llgo_test_compile_only() {
	local target="$1"
	local name="$2"
	local module
	case "${target}" in
	emscripten | emscripten-memory64 | wasm)
		module="${work_dir}/${name}.mjs"
		;;
	*)
		module="${work_dir}/${name}.wasm"
		;;
	esac

	echo "testing public llgo test -c command for ${target}"
	"${llgo_cmd}" test -target "${target}" -c -o "${module}" "${test_fixture}"
	case "${module}" in
	*.mjs)
		test -s "${module}"
		wasm-tools validate --features all "${module%.mjs}.wasm"
		;;
	*.wasm)
		wasm-tools validate --features all "${module}"
		;;
	esac
}

# Canonical C-ecosystem profiles exercise the same scheduler semantics under
# Emscripten wasm32, Emscripten Memory64/LP64, and WASI Preview 1.
wasm_ci_run_case EC32/emscripten scheduler 1 0 0 0 0 \
	run_emscripten emscripten emscripten-runner.mjs "${scheduler_fixture}" "wasm scheduler ok" "scheduler-emscripten"
wasm_ci_run_case EC64/emscripten-memory64 scheduler 1 0 0 0 0 \
	run_emscripten emscripten-memory64 emscripten-memory64-runner.mjs "${scheduler_fixture}" "wasm scheduler ok" "scheduler-memory64"
wasm_ci_run_case WC32/wasi scheduler 1 0 0 0 0 \
	run_wasi wasi "${scheduler_fixture}" "wasm scheduler ok" "scheduler-wasi"

wasm_ci_run_case EC32/emscripten scheduler-deadlock 1 1 0 0 0 \
	expect_failure "fatal error: all goroutines are asleep - deadlock!" \
	env LLGO_WASM_SCHEDULER_DEADLOCK=1 "${node_cmd}" "${repo_root}/targets/emscripten-runner.mjs" "${work_dir}/scheduler-emscripten.mjs"
wasm_ci_run_case EC32/emscripten scheduler-main-goexit 1 1 0 0 0 \
	expect_failure "fatal error: no goroutines (main called runtime.Goexit) - deadlock!" \
	env LLGO_WASM_SCHEDULER_MAIN_GOEXIT=1 "${node_cmd}" "${repo_root}/targets/emscripten-runner.mjs" "${work_dir}/scheduler-emscripten.mjs"
wasm_ci_run_case EC64/emscripten-memory64 scheduler-deadlock 1 1 0 0 0 \
	expect_failure "fatal error: all goroutines are asleep - deadlock!" \
	env LLGO_WASM_SCHEDULER_DEADLOCK=1 "${node_cmd}" "${repo_root}/targets/emscripten-memory64-runner.mjs" "${work_dir}/scheduler-memory64.mjs"
wasm_ci_run_case EC64/emscripten-memory64 scheduler-main-goexit 1 1 0 0 0 \
	expect_failure "fatal error: no goroutines (main called runtime.Goexit) - deadlock!" \
	env LLGO_WASM_SCHEDULER_MAIN_GOEXIT=1 "${node_cmd}" "${repo_root}/targets/emscripten-memory64-runner.mjs" "${work_dir}/scheduler-memory64.mjs"
wasm_ci_run_case WC32/wasi scheduler-deadlock 1 1 0 0 0 \
	expect_failure "fatal error: all goroutines are asleep - deadlock!" \
	"${wasmtime_cmd}" run -W exceptions=y --env LLGO_WASM_SCHEDULER_DEADLOCK=1 "${work_dir}/scheduler-wasi.wasm"
wasm_ci_run_case WC32/wasi scheduler-main-goexit 1 1 0 0 0 \
	expect_failure "fatal error: no goroutines (main called runtime.Goexit) - deadlock!" \
	"${wasmtime_cmd}" run -W exceptions=y --env LLGO_WASM_SCHEDULER_MAIN_GOEXIT=1 "${work_dir}/scheduler-wasi.wasm"

# Exercise the CLI-level failure boundary in CI, not only the runners in
# isolation. The other public run calls below cover successful EC32, EC64,
# WC32, and alias execution through the same path.
wasm_ci_run_case EC32/emscripten public-runner-exit 1 1 0 0 0 \
	expect_llgo_runner_failure emscripten emscripten node "${scheduler_fixture}"
wasm_ci_run_case EC32/emscripten public-runner-timeout 1 0 1 0 0 \
	expect_llgo_runner_timeout emscripten emscripten node "${scheduler_fixture}"

# Timers share the Go-derived heap but use different host-wait backends.
wasm_ci_run_case EC32/emscripten timers 1 0 0 0 0 \
	run_emscripten emscripten emscripten-runner.mjs "${timer_fixture}" "wasm timers ok" "timers-emscripten"
wasm_ci_run_case EC64/emscripten-memory64 timers 1 0 0 0 0 \
	run_emscripten emscripten-memory64 emscripten-memory64-runner.mjs "${timer_fixture}" "wasm timers ok" "timers-memory64"
wasm_ci_run_case WC32/wasi timers 1 0 0 0 0 \
	run_wasi wasi "${timer_fixture}" "wasm timers ok" "timers-wasi"

# R2 enables the non-moving collector by default for each canonical
# single-worker C profile. This fixture covers active and suspended G roots,
# closures/interfaces/aggregates, panic/recover unwinding, pure-Go loop
# safepoints, reclamation, aligned allocation, and memory growth.
wasm_ci_run_case EC32/emscripten gc 1 0 0 0 0 \
	run_emscripten emscripten emscripten-runner.mjs "${gc_fixture}" "wasm gc ok" "gc-emscripten"
wasm_ci_run_case EC64/emscripten-memory64 gc 1 0 0 0 0 \
	run_emscripten emscripten-memory64 emscripten-memory64-runner.mjs "${gc_fixture}" "wasm gc ok" "gc-memory64"
wasm_ci_run_case WC32/wasi gc 1 0 0 0 0 \
	run_wasi wasi "${gc_fixture}" "wasm gc ok" "gc-wasi"

# Finalizers, cleanups, and weak references share the collector lifecycle but
# have additional ordering, cancellation, and dynamic-call ABI requirements.
wasm_ci_run_case EC32/emscripten lifecycle 1 0 0 0 0 \
	run_llgo_run emscripten "${lifecycle_fixture}" "wasm lifecycle ok" "lifecycle-emscripten"
wasm_ci_run_case EC64/emscripten-memory64 lifecycle 1 0 0 0 0 \
	run_llgo_run emscripten-memory64 "${lifecycle_fixture}" "wasm lifecycle ok" "lifecycle-memory64"
wasm_ci_run_case WC32/wasi lifecycle 1 0 0 0 0 \
	run_llgo_run wasi "${lifecycle_fixture}" "wasm lifecycle ok" "lifecycle-wasi"

# A registered JS callback is a host wake source even when no Go timer exists.
# This catches treating an empty timer heap as an immediate deadlock.
wasm_ci_run_case EC32/emscripten callback 1 0 0 0 0 \
	run_emscripten emscripten emscripten-runner.mjs "${callback_fixture}" "wasm callback-only wake ok" "callback-emscripten"
wasm_ci_run_case EC64/emscripten-memory64 callback 1 0 0 0 0 \
	run_emscripten emscripten-memory64 emscripten-memory64-runner.mjs "${callback_fixture}" "wasm callback-only wake ok" "callback-memory64"

# Keep the legacy named aliases executable while raw js/wasm remains the
# browser/worker-only compatibility path defined by R0.
wasm_ci_run_case L32/wasm-alias scheduler 1 0 0 0 0 \
	run_llgo_run wasm "${scheduler_fixture}" "wasm scheduler ok" "scheduler-legacy-wasm"
wasm_ci_run_case LW32/wasip1-alias scheduler 1 0 0 0 0 \
	run_llgo_run wasip1 "${scheduler_fixture}" "wasm scheduler ok" "scheduler-legacy-wasip1"

# Exercise test-main generation, process exit, verbose output, and host runners
# through the public test command. The JS-specific callback case also verifies
# that host readiness interrupts a longer Go timer wait without re-entering an
# arbitrary parked G.
wasm_ci_run_case EC32/emscripten public-test 2 0 0 0 0 \
	run_llgo_test emscripten "test-emscripten"
wasm_ci_run_case EC64/emscripten-memory64 public-test 2 0 0 0 0 \
	run_llgo_test emscripten-memory64 "test-memory64"
wasm_ci_run_case WC32/wasi public-test 2 0 0 0 0 \
	run_llgo_test wasi "test-wasi"
wasm_ci_run_case EC32/emscripten compile-only 0 0 0 0 0 \
	run_llgo_test_compile_only emscripten "test-compile-only-emscripten"
wasm_ci_run_case EC64/emscripten-memory64 compile-only 0 0 0 0 0 \
	run_llgo_test_compile_only emscripten-memory64 "test-compile-only-memory64"
wasm_ci_run_case WC32/wasi compile-only 0 0 0 0 0 \
	run_llgo_test_compile_only wasi "test-compile-only-wasi"

echo "single-worker WebAssembly scheduler and timer checks passed"
