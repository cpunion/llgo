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

#ifndef LLGO_CORO_WORKER_V1_H
#define LLGO_CORO_WORKER_V1_H

#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>

enum {
    LLGO_CORO_WORKER_MAX_ARGS_V1 = 9,
    LLGO_CORO_WORKER_THREAD_COUNT_V1 = 4,
    LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 = 1024,
    LLGO_CORO_WORKER_QUEUE_TAKE_INVALID_V1 = 0,
    LLGO_CORO_WORKER_QUEUE_TAKE_JOB_V1 = 1,
    LLGO_CORO_WORKER_QUEUE_TAKE_STOP_V1 = 2,
};

/*
 * This is the complete C queue payload. It is POD and contains no G, LLVM
 * coroutine handle, ParkState, WaitSetRecord, or typed Go pointer. Arguments
 * are opaque syscall words; their Go storage remains retained by the parked
 * compiler frame until the operation generation is retired.
 */
struct llgo_coro_worker_job_v1 {
    uint32_t source_slot;
    uint32_t generation;
    uintptr_t function;
    uint32_t argc;
    uintptr_t args[LLGO_CORO_WORKER_MAX_ARGS_V1];
};

struct llgo_coro_worker_result_v1 {
    uintptr_t r1;
    uintptr_t r2;
    uintptr_t error;
};

int __llgo_coro_worker_create_v1(pthread_t *thread);

bool __llgo_coro_worker_queue_init_v1(void);
bool __llgo_coro_worker_queue_can_release_v1(void);
bool __llgo_coro_worker_queue_reserve_v1(void);
bool __llgo_coro_worker_queue_cancel_reservation_v1(void);
bool __llgo_coro_worker_queue_submit_reserved_v1(
    const struct llgo_coro_worker_job_v1 *job);
uint32_t __llgo_coro_worker_queue_wait_take_v1(
    struct llgo_coro_worker_job_v1 *job);
bool __llgo_coro_worker_queue_stop_v1(uint32_t worker_count);
bool __llgo_coro_worker_queue_destroy_after_join_v1(void);

bool __llgo_coro_worker_call_v1(
    uintptr_t function,
    uint32_t argc,
    const uintptr_t args[LLGO_CORO_WORKER_MAX_ARGS_V1],
    struct llgo_coro_worker_result_v1 *result);

#endif
