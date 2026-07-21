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

func TestNativeQueueC11CapacityConcurrentWrapAndStop(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc is unavailable")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "native_queue_test.c")
	program := `
#include "worker.h"

#include <pthread.h>
#include <sched.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

enum {
    worker_count = 4,
    generation_count = 4 * LLGO_CORO_WORKER_QUEUE_CAPACITY_V1,
};

static _Atomic uint32_t seen[generation_count];
static _Atomic uint32_t completed;

uint32_t __llgo_coro_native_worker_complete_v1(
    uint32_t source_slot,
    uint32_t generation,
    uintptr_t r1,
    uintptr_t r2,
    uintptr_t error) {
    (void)source_slot;
    (void)generation;
    (void)r1;
    (void)r2;
    (void)error;
    return 0;
}

static void *consume(void *unused) {
    (void)unused;
    for (;;) {
        struct llgo_coro_worker_job_v1 job;
        uint32_t status = __llgo_coro_worker_queue_wait_take_v1(&job);
        if (status == LLGO_CORO_WORKER_QUEUE_TAKE_STOP_V1) {
            return NULL;
        }
        if (status != LLGO_CORO_WORKER_QUEUE_TAKE_JOB_V1 ||
            job.generation == 0 || job.generation > generation_count ||
            job.function != 1 || job.argc != LLGO_CORO_WORKER_MAX_ARGS_V1) {
            return (void *)(uintptr_t)1;
        }
        /* OperationSourceWorker=5, route=1/2, local slot=1. */
        uint32_t want_source_slot = UINT32_C(0x05000001) |
            ((job.generation & 1) != 0 ? UINT32_C(1) : UINT32_C(2)) << 15;
        if (job.source_slot != want_source_slot) {
            return (void *)(uintptr_t)4;
        }
        uint32_t index = job.generation - 1;
        for (uint32_t arg = 0; arg < LLGO_CORO_WORKER_MAX_ARGS_V1; ++arg) {
            if (job.args[arg] != ((uintptr_t)job.generation << 8) + arg) {
                return (void *)(uintptr_t)2;
            }
        }
        if (atomic_fetch_add_explicit(&seen[index], 1, memory_order_relaxed) != 0) {
            return (void *)(uintptr_t)3;
        }
        atomic_fetch_add_explicit(&completed, 1, memory_order_relaxed);
    }
}

static int submit(uint32_t generation) {
    struct llgo_coro_worker_job_v1 job;
    memset(&job, 0, sizeof(job));
    job.source_slot = UINT32_C(0x05000001) |
        ((generation & 1) != 0 ? UINT32_C(1) : UINT32_C(2)) << 15;
    job.generation = generation;
    job.function = 1;
    job.argc = LLGO_CORO_WORKER_MAX_ARGS_V1;
    for (uint32_t arg = 0; arg < LLGO_CORO_WORKER_MAX_ARGS_V1; ++arg) {
        job.args[arg] = ((uintptr_t)generation << 8) + arg;
    }
    return __llgo_coro_worker_queue_submit_reserved_v1(&job) ? 0 : 1;
}

int main(void) {
    if (!__llgo_coro_worker_queue_can_release_v1() ||
        !__llgo_coro_worker_queue_init_v1() ||
        __llgo_coro_worker_queue_can_release_v1()) {
        return 10;
    }
    if (!__llgo_coro_worker_queue_reserve_v1() ||
        !__llgo_coro_worker_queue_cancel_reservation_v1() ||
        __llgo_coro_worker_queue_cancel_reservation_v1()) {
        return 11;
    }

    for (uint32_t generation = 1;
         generation <= LLGO_CORO_WORKER_QUEUE_CAPACITY_V1;
         ++generation) {
        if (!__llgo_coro_worker_queue_reserve_v1() || submit(generation) != 0) {
            return 12;
        }
    }
    if (__llgo_coro_worker_queue_reserve_v1()) {
        return 13;
    }

    pthread_t workers[worker_count];
    for (uint32_t index = 0; index < worker_count; ++index) {
        if (pthread_create(&workers[index], NULL, consume, NULL) != 0) {
            return 14;
        }
    }
    for (uint32_t generation = LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 + 1;
         generation <= generation_count;
         ++generation) {
        while (!__llgo_coro_worker_queue_reserve_v1()) {
            sched_yield();
        }
        if (submit(generation) != 0) {
            return 15;
        }
    }
    if (!__llgo_coro_worker_queue_stop_v1(worker_count)) {
        return 16;
    }
    for (uint32_t index = 0; index < worker_count; ++index) {
        void *result = NULL;
        if (pthread_join(workers[index], &result) != 0 || result != NULL) {
            return 17;
        }
    }
    if (atomic_load_explicit(&completed, memory_order_relaxed) != generation_count) {
        return 18;
    }
    for (uint32_t index = 0; index < generation_count; ++index) {
        if (atomic_load_explicit(&seen[index], memory_order_relaxed) != 1) {
            return 19;
        }
    }
    if (!__llgo_coro_worker_queue_destroy_after_join_v1() ||
        !__llgo_coro_worker_queue_can_release_v1()) {
        return 20;
    }

    /* Stop must reject an unpublished reservation and restart must be clean. */
    if (!__llgo_coro_worker_queue_init_v1() ||
        !__llgo_coro_worker_queue_reserve_v1() ||
        __llgo_coro_worker_queue_stop_v1(0) ||
        !__llgo_coro_worker_queue_cancel_reservation_v1() ||
        !__llgo_coro_worker_queue_stop_v1(0) ||
        !__llgo_coro_worker_queue_destroy_after_join_v1() ||
        !__llgo_coro_worker_queue_can_release_v1()) {
        return 21;
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
	include := filepath.Dir(worker)
	executable := filepath.Join(dir, "native_queue_test")
	compile := exec.Command(cc,
		"-std=c11", "-Wall", "-Wextra", "-Werror", "-pthread",
		"-I", include, worker, harness, "-o", executable,
	)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile native worker queue test: %v\n%s", err, output)
	}
	if output, err := exec.Command(executable).CombinedOutput(); err != nil {
		t.Fatalf("native worker queue lifecycle failed: %v\n%s", err, output)
	}
}
