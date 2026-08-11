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
    producer_count = 4,
    generation_count = 8 * LLGO_CORO_WORKER_QUEUE_CAPACITY_V1,
};

static _Atomic uint32_t seen[generation_count];
static _Atomic uint32_t completed;
static _Atomic uint32_t next_generation;

uint32_t __llgo_coro_native_worker_complete_v1(
    uint32_t source_slot,
    uint32_t generation,
    uintptr_t r1,
    uintptr_t r2,
    uintptr_t error,
    uintptr_t fault,
    uintptr_t fault_pc,
    uintptr_t fault_target) {
    (void)source_slot;
    (void)generation;
    (void)r1;
    (void)r2;
    (void)error;
    (void)fault;
    (void)fault_pc;
    (void)fault_target;
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
            job.function != 1 || job.trace_target != 1 ||
            job.argc != LLGO_CORO_WORKER_MAX_ARGS_V1) {
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

static int submit(size_t reservation, uint32_t generation) {
    struct llgo_coro_worker_job_v1 job;
    memset(&job, 0, sizeof(job));
    job.source_slot = UINT32_C(0x05000001) |
        ((generation & 1) != 0 ? UINT32_C(1) : UINT32_C(2)) << 15;
    job.generation = generation;
    job.function = 1;
    job.trace_target = 1;
    job.argc = LLGO_CORO_WORKER_MAX_ARGS_V1;
    for (uint32_t arg = 0; arg < LLGO_CORO_WORKER_MAX_ARGS_V1; ++arg) {
        job.args[arg] = ((uintptr_t)generation << 8) + arg;
    }
    return __llgo_coro_worker_queue_submit_reserved_v1(reservation, &job) ? 0 : 1;
}

static void *produce(void *unused) {
    (void)unused;
    for (;;) {
        uint32_t generation = atomic_fetch_add_explicit(
            &next_generation, 1, memory_order_relaxed);
        if (generation > generation_count) {
            return NULL;
        }
        size_t reservation;
        while (!__llgo_coro_worker_queue_reserve_v1(&reservation)) {
            sched_yield();
        }
        /* Exercise rollback after later producers can already own positions. */
        if ((generation & 31) == 0) {
            if (!__llgo_coro_worker_queue_cancel_reservation_v1(reservation)) {
                return (void *)(uintptr_t)5;
            }
            while (!__llgo_coro_worker_queue_reserve_v1(&reservation)) {
                sched_yield();
            }
        }
        if (submit(reservation, generation) != 0) {
            return (void *)(uintptr_t)6;
        }
    }
}

int main(void) {
    if (!__llgo_coro_worker_queue_can_release_v1() ||
        !__llgo_coro_worker_queue_init_v1() ||
        __llgo_coro_worker_queue_can_release_v1()) {
        return 10;
    }
    size_t reservation;
    if (__llgo_coro_worker_queue_reserve_v1(NULL)) {
        return 11;
    }

    size_t initial[LLGO_CORO_WORKER_QUEUE_CAPACITY_V1];
    for (size_t index = 0; index < LLGO_CORO_WORKER_QUEUE_CAPACITY_V1; ++index) {
        if (!__llgo_coro_worker_queue_reserve_v1(&initial[index])) {
            return 12;
        }
    }
    if (__llgo_coro_worker_queue_reserve_v1(&reservation)) {
        return 13;
    }
    /* Publish out of order to prove reservation identity is per producer. */
    for (uint32_t generation = LLGO_CORO_WORKER_QUEUE_CAPACITY_V1;
         generation != 0;
         --generation) {
        if (submit(initial[generation - 1], generation) != 0) {
            return 12;
        }
    }

    pthread_t workers[worker_count];
    for (uint32_t index = 0; index < worker_count; ++index) {
        if (pthread_create(&workers[index], NULL, consume, NULL) != 0) {
            return 14;
        }
    }
    atomic_store_explicit(
        &next_generation,
        LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 + 1,
        memory_order_relaxed);
    pthread_t producers[producer_count];
    for (uint32_t index = 0; index < producer_count; ++index) {
        if (pthread_create(&producers[index], NULL, produce, NULL) != 0) {
            return 15;
        }
    }
    for (uint32_t index = 0; index < producer_count; ++index) {
        void *result = NULL;
        if (pthread_join(producers[index], &result) != 0 || result != NULL) {
            return 22;
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
    pthread_t cleanup_worker;
    if (!__llgo_coro_worker_queue_init_v1() ||
        pthread_create(&cleanup_worker, NULL, consume, NULL) != 0 ||
        !__llgo_coro_worker_queue_reserve_v1(&reservation) ||
        __llgo_coro_worker_queue_stop_v1(1) ||
        !__llgo_coro_worker_queue_cancel_reservation_v1(reservation) ||
        !__llgo_coro_worker_queue_stop_v1(1)) {
        return 21;
    }
    void *cleanup_result = NULL;
    if (pthread_join(cleanup_worker, &cleanup_result) != 0 || cleanup_result != NULL) {
        return 23;
    }
    if (!__llgo_coro_worker_queue_destroy_after_join_v1() ||
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
