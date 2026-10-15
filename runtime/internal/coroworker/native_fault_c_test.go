//go:build darwin || linux

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package coroworker

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeFaultLandingReentryAndEscape(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc is unavailable")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "fault.c")
	const program = `
#define _XOPEN_SOURCE 700
#include "worker.h"
#include <setjmp.h>
#include <signal.h>
#include <string.h>

uint32_t __llgo_coro_native_worker_complete_v1(
    uint32_t slot, uint32_t generation, uintptr_t r1, uintptr_t r2,
    uintptr_t error, uintptr_t fault, uintptr_t pc, uintptr_t target) {
    (void)slot; (void)generation; (void)r1; (void)r2;
    (void)error; (void)fault; (void)pc; (void)target;
    return 0;
}

static sigjmp_buf managed_landing, escape_landing;
static int *volatile null_pointer;

static void managed_fault(int signal) {
    (void)signal;
    siglongjmp(managed_landing, 1);
}

int llgo_cpu_profile_fault_recover(void) { return 0; }
void llgo_dynunwind_init(void) {}
void llgo_dynunwind_capture(void *context) { (void)context; }
void llgo_fault_capture_done(void);
void llgo_install_coro_fault_handler(
    void (*callback)(uintptr_t, uintptr_t, uintptr_t, int, uint32_t));

static void managed_capture(uintptr_t pc, uintptr_t fp, uintptr_t address,
                            int signal, uint32_t policy) {
    (void)pc; (void)fp; (void)address; (void)policy;
    llgo_fault_capture_done();
    managed_fault(signal);
}

static uintptr_t leaf(void) {
    *null_pointer = 42;
    return 0;
}

static uintptr_t value(void) { return 7; }

static int invoke(uintptr_t function, uintptr_t target,
                  struct llgo_coro_worker_result_v1 *result) {
    return __llgo_coro_worker_call_words_v2(
        function, target, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
        (uintptr_t)result);
}

static int fault(void) {
    struct llgo_coro_worker_result_v1 result;
    return invoke((uintptr_t)leaf, (uintptr_t)leaf, &result) &&
        result.fault == LLGO_CORO_WORKER_FAULT_MEMORY_V1 &&
        result.fault_pc != 0 && result.fault_target == (uintptr_t)leaf;
}

static uintptr_t callback(void) {
    uintptr_t scope = __llgo_coro_worker_fault_suspend_v1();
    if (scope == 0 || !fault() || !fault()) return 100;
    /* A Go callback's fault must reach its own managed landing, not the C
       caller's landing; nested foreign calls use independent native scopes. */
    if (sigsetjmp(managed_landing, 1) == 0) leaf();
    if (!__llgo_coro_worker_fault_resume_v1(scope)) return 101;
    return 7;
}

static uintptr_t escape(void) {
    if (__llgo_coro_worker_fault_suspend_v1() == 0) return 102;
    /* Mirrors a completed callback panic escaping the abandoned C stack. */
    siglongjmp(escape_landing, 1);
}

static uintptr_t untrapped_helper(void) {
    struct llgo_coro_worker_result_v1 result;
    if (!invoke((uintptr_t)value, 0, &result) || result.r1 != 7) return 103;
    return leaf();
}

int main(int argc, char **argv) {
    (void)argv;
    struct sigaction action;
    memset(&action, 0, sizeof(action));
    action.sa_handler = managed_fault;
    sigemptyset(&action.sa_mask);
    if (sigaction(SIGSEGV, &action, NULL) != 0 ||
        sigaction(SIGBUS, &action, NULL) != 0) return 1;
    if (argc > 1 && !fault()) return 7;
    llgo_install_coro_fault_handler(managed_capture);
    for (int i = 0; i < 10; ++i) {
        if (!fault()) return 2;
        struct llgo_coro_worker_result_v1 result;
        if (!invoke((uintptr_t)callback, (uintptr_t)callback, &result) ||
            result.fault != 0 || result.r1 != 7) return 3;
        if (!invoke((uintptr_t)untrapped_helper, (uintptr_t)leaf, &result) ||
            result.fault != LLGO_CORO_WORKER_FAULT_MEMORY_V1) return 4;
        if (sigsetjmp(escape_landing, 1) == 0) {
            invoke((uintptr_t)escape, (uintptr_t)escape, &result);
            return 5;
        }
        if (!fault()) return 6;
    }
    return 0;
}
`
	if err := os.WriteFile(harness, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	worker, err := filepath.Abs(filepath.Join("_worker", "worker.c"))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "fault")
	managed, err := filepath.Abs(filepath.Join("..", "lib", "runtime", "_wrap", "fault.c"))
	if err != nil {
		t.Fatal(err)
	}
	compile := exec.Command(cc,
		"-std=c11", "-O2", "-Wall", "-Wextra", "-Werror", "-pthread",
		"-I", filepath.Dir(worker), worker, managed, harness, "-o", executable,
	)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile native fault test: %v\n%s", err, output)
	}
	for _, args := range [][]string{nil, {"late-install"}} {
		if output, err := exec.Command(executable, args...).CombinedOutput(); err != nil {
			t.Fatalf("native fault/reentry lifecycle %v: %v\n%s", args, err, output)
		}
	}
}
