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

func TestNativeDemandCountsReservedCanceledAndCompletingCalls(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc is unavailable")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "demand.c")
	program := `
#include "worker.h"
#include <pthread.h>
#include <sched.h>
#include <stdatomic.h>
#include <stdint.h>

static _Atomic uint32_t entered;
static _Atomic uint32_t callbacks;
static _Atomic bool release_calls;
static _Atomic bool release_callbacks;

static bool wait_count(_Atomic uint32_t *value, uint32_t wanted) {
    for (unsigned attempt = 0; attempt < 2000000; ++attempt) {
        if (atomic_load_explicit(value, memory_order_acquire) == wanted) return true;
        sched_yield();
    }
    return false;
}

static uintptr_t blocked_call(void) {
    atomic_fetch_add_explicit(&entered, 1, memory_order_release);
    while (!atomic_load_explicit(&release_calls, memory_order_acquire)) sched_yield();
    return 123;
}

uint32_t __llgo_coro_native_worker_complete_v1(
    uint32_t source_slot, uint32_t generation, uintptr_t r1, uintptr_t r2,
    uintptr_t error, uintptr_t fault, uintptr_t fault_pc, uintptr_t fault_target) {
    (void)r2;
    (void)error;
    (void)fault_pc;
    (void)fault_target;
    if (source_slot != UINT32_C(0x05008001) || generation < 1 || generation > 4 ||
        r1 != 123 || fault != 0) return 0;
    atomic_fetch_add_explicit(&callbacks, 1, memory_order_release);
    while (!atomic_load_explicit(&release_callbacks, memory_order_acquire)) sched_yield();
    return 1;
}

int main(void) {
    if (__llgo_coro_worker_queue_demand_v1() != 0 ||
        !__llgo_coro_worker_queue_init_v1()) return 1;
    size_t token = __llgo_coro_worker_queue_reserve_v2();
    if (token == 0 || __llgo_coro_worker_queue_demand_v1() != 1 ||
        __llgo_coro_worker_queue_stop_v1(0)) return 2;
    if (!__llgo_coro_worker_queue_cancel_reservation_v2(token) ||
        __llgo_coro_worker_queue_demand_v1() != 1 ||
        !__llgo_coro_worker_queue_stop_v1(0)) return 3;
    struct llgo_coro_worker_job_v1 canceled;
    if (__llgo_coro_worker_queue_wait_take_v1(&canceled) !=
            LLGO_CORO_WORKER_QUEUE_TAKE_STOP_V1 ||
        __llgo_coro_worker_queue_demand_v1() != 0 ||
        !__llgo_coro_worker_queue_destroy_after_join_v1() ||
        !__llgo_coro_worker_queue_can_release_v1()) return 4;

    if (!__llgo_coro_worker_queue_init_v1()) return 5;
    for (uint32_t index = 1; index <= 4; ++index) {
        token = __llgo_coro_worker_queue_reserve_v2();
        if (token == 0 || !__llgo_coro_worker_queue_submit_reserved_v4(
                token, UINT32_C(0x05008001), index,
                (uintptr_t)blocked_call, (uintptr_t)blocked_call, 0,
                0, 0, 0, 0, 0, 0, 0, 0, 0)) return 6;
    }
    pthread_t workers[4];
    for (uint32_t index = 0; index < 4; ++index) {
        if (__llgo_coro_worker_queue_demand_v1() != 4 ||
            __llgo_coro_worker_create_v1(&workers[index]) != 0 ||
            !wait_count(&entered, index + 1)) return 7;
    }
    if (!__llgo_coro_worker_queue_stop_v1(4)) return 8;
    atomic_store_explicit(&release_calls, true, memory_order_release);
    if (!wait_count(&callbacks, 4) ||
        __llgo_coro_worker_queue_demand_v1() != 4) return 9;
    atomic_store_explicit(&release_callbacks, true, memory_order_release);
    for (uint32_t index = 0; index < 4; ++index) {
        void *result = (void *)(uintptr_t)1;
        if (pthread_join(workers[index], &result) != 0 || result != NULL) return 10;
    }
    if (__llgo_coro_worker_queue_demand_v1() != 0 ||
        !__llgo_coro_worker_queue_destroy_after_join_v1() ||
        !__llgo_coro_worker_queue_can_release_v1()) return 11;
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
	executable := filepath.Join(dir, "demand")
	compile := exec.Command(cc, "-std=c11", "-Wall", "-Wextra", "-Werror", "-pthread",
		"-I", filepath.Dir(worker), worker, harness, "-o", executable)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile worker demand control: %v\n%s", err, output)
	}
	if output, err := exec.Command(executable).CombinedOutput(); err != nil {
		t.Fatalf("worker demand lifecycle failed: %v\n%s", err, output)
	}
}
