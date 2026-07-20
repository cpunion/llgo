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

#include "worker.h"

#include <errno.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>

#if defined(LLGO_CORO_WORKER_BDWGC)
#include <gc/gc.h>
#endif

#if defined(__APPLE__)
#include <mach/mach.h>
#include <mach/semaphore.h>
#include <mach/sync_policy.h>

typedef semaphore_t llgo_coro_worker_wake_v1;

static bool llgo_coro_worker_wake_init_v1(llgo_coro_worker_wake_v1 *wake) {
    if (wake == NULL) {
        return false;
    }
    *wake = SEMAPHORE_NULL;
    return semaphore_create(mach_task_self(), wake, SYNC_POLICY_FIFO, 0) == KERN_SUCCESS;
}

static bool llgo_coro_worker_wake_signal_v1(llgo_coro_worker_wake_v1 *wake) {
    return wake != NULL && *wake != SEMAPHORE_NULL &&
        semaphore_signal(*wake) == KERN_SUCCESS;
}

static bool llgo_coro_worker_wake_wait_v1(llgo_coro_worker_wake_v1 *wake) {
    if (wake == NULL || *wake == SEMAPHORE_NULL) {
        return false;
    }
    kern_return_t result;
    do {
        result = semaphore_wait(*wake);
    } while (result == KERN_ABORTED);
    return result == KERN_SUCCESS;
}

static bool llgo_coro_worker_wake_destroy_v1(llgo_coro_worker_wake_v1 *wake) {
    if (wake == NULL || *wake == SEMAPHORE_NULL) {
        return false;
    }
    semaphore_t value = *wake;
    *wake = SEMAPHORE_NULL;
    return semaphore_destroy(mach_task_self(), value) == KERN_SUCCESS;
}
#else
#include <semaphore.h>

typedef sem_t llgo_coro_worker_wake_v1;

static bool llgo_coro_worker_wake_init_v1(llgo_coro_worker_wake_v1 *wake) {
    return wake != NULL && sem_init(wake, 0, 0) == 0;
}

static bool llgo_coro_worker_wake_signal_v1(llgo_coro_worker_wake_v1 *wake) {
    return wake != NULL && sem_post(wake) == 0;
}

static bool llgo_coro_worker_wake_wait_v1(llgo_coro_worker_wake_v1 *wake) {
    if (wake == NULL) {
        return false;
    }
    int result;
    do {
        result = sem_wait(wake);
    } while (result != 0 && errno == EINTR);
    return result == 0;
}

static bool llgo_coro_worker_wake_destroy_v1(llgo_coro_worker_wake_v1 *wake) {
    return wake != NULL && sem_destroy(wake) == 0;
}
#endif

typedef uintptr_t (*llgo_coro_worker_fn0_v1)(void);
typedef uintptr_t (*llgo_coro_worker_fn1_v1)(uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn2_v1)(uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn3_v1)(uintptr_t, uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn4_v1)(uintptr_t, uintptr_t, uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn5_v1)(uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn6_v1)(uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn7_v1)(uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn8_v1)(uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn9_v1)(uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t);

struct llgo_coro_worker_queue_slot_v1 {
    _Atomic size_t sequence;
    struct llgo_coro_worker_job_v1 job;
};

/*
 * There is exactly one native coroutine executor in one process. The owner P
 * is the only producer and reservation holder; fixed raw pthreads are the
 * consumers. Sequence numbers make each slot independently reusable, so no
 * consumer and producer ever contend on a mutex or retain a G/frame identity.
 */
struct llgo_coro_worker_queue_v1 {
    _Atomic bool initialized;
    _Atomic bool stopping;
    _Atomic bool reserved;
    _Atomic size_t enqueue_position;
    _Atomic size_t dequeue_position;
    _Atomic size_t reserved_position;
    llgo_coro_worker_wake_v1 wake;
    struct llgo_coro_worker_queue_slot_v1 slots[LLGO_CORO_WORKER_QUEUE_CAPACITY_V1];
};

static struct llgo_coro_worker_queue_v1 llgo_coro_worker_queue_v1;

extern uint32_t __llgo_coro_native_worker_complete_v1(
    uint32_t source_slot,
    uint32_t generation,
    uintptr_t r1,
    uintptr_t r2,
    uintptr_t error);

static void *llgo_coro_worker_main_v1(void *unused);

static int llgo_coro_worker_thread_create_v1(
    pthread_t *thread,
    void *(*routine)(void *)) {
#if defined(LLGO_CORO_WORKER_BDWGC)
    return GC_pthread_create(thread, NULL, routine, NULL);
#else
    return pthread_create(thread, NULL, routine, NULL);
#endif
}

#define LLGO_CORO_WORKER_ALIGN_UP_V1(value, alignment) \
    (((value) + (alignment) - 1) / (alignment) * (alignment))

_Static_assert((LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 &
    (LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 - 1)) == 0,
    "worker queue capacity must remain a power of two");
_Static_assert(offsetof(struct llgo_coro_worker_job_v1, source_slot) == 0,
    "worker job source ABI changed");
_Static_assert(offsetof(struct llgo_coro_worker_job_v1, generation) == sizeof(uint32_t),
    "worker job generation ABI changed");
_Static_assert(offsetof(struct llgo_coro_worker_job_v1, function) == 2 * sizeof(uint32_t),
    "worker job function ABI changed");
_Static_assert(offsetof(struct llgo_coro_worker_job_v1, argc) ==
    2 * sizeof(uint32_t) + sizeof(uintptr_t),
    "worker job argc ABI changed");
_Static_assert(offsetof(struct llgo_coro_worker_job_v1, args) ==
    LLGO_CORO_WORKER_ALIGN_UP_V1(
        3 * sizeof(uint32_t) + sizeof(uintptr_t), _Alignof(uintptr_t)),
    "worker job args ABI changed");
_Static_assert(sizeof(struct llgo_coro_worker_job_v1) ==
    LLGO_CORO_WORKER_ALIGN_UP_V1(
        offsetof(struct llgo_coro_worker_job_v1, args) +
            LLGO_CORO_WORKER_MAX_ARGS_V1 * sizeof(uintptr_t),
        _Alignof(uintptr_t)),
    "worker job size ABI changed");

static bool llgo_coro_worker_job_valid_v1(
    const struct llgo_coro_worker_job_v1 *job) {
    return job != NULL && job->source_slot != 0 && job->generation != 0 &&
        job->function != 0 && job->argc <= LLGO_CORO_WORKER_MAX_ARGS_V1;
}

bool __llgo_coro_worker_queue_can_release_v1(void) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    return !atomic_load_explicit(&queue->initialized, memory_order_acquire) &&
        !atomic_load_explicit(&queue->stopping, memory_order_relaxed) &&
        !atomic_load_explicit(&queue->reserved, memory_order_relaxed) &&
        atomic_load_explicit(&queue->enqueue_position, memory_order_relaxed) == 0 &&
        atomic_load_explicit(&queue->dequeue_position, memory_order_relaxed) == 0;
}

bool __llgo_coro_worker_queue_init_v1(void) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (!__llgo_coro_worker_queue_can_release_v1()) {
        return false;
    }

    atomic_init(&queue->stopping, false);
    atomic_init(&queue->reserved, false);
    atomic_init(&queue->enqueue_position, 0);
    atomic_init(&queue->dequeue_position, 0);
    atomic_init(&queue->reserved_position, 0);
    for (size_t index = 0; index < LLGO_CORO_WORKER_QUEUE_CAPACITY_V1; ++index) {
        atomic_init(&queue->slots[index].sequence, index);
        memset(&queue->slots[index].job, 0, sizeof(queue->slots[index].job));
    }

    /* Refuse a target on which the supposedly nonblocking ingress atomics lock. */
    if (!atomic_is_lock_free(&queue->initialized) ||
        !atomic_is_lock_free(&queue->stopping) ||
        !atomic_is_lock_free(&queue->reserved) ||
        !atomic_is_lock_free(&queue->enqueue_position) ||
        !atomic_is_lock_free(&queue->dequeue_position) ||
        !atomic_is_lock_free(&queue->slots[0].sequence) ||
        !llgo_coro_worker_wake_init_v1(&queue->wake)) {
        return false;
    }
    atomic_store_explicit(&queue->initialized, true, memory_order_release);
    return true;
}

bool __llgo_coro_worker_queue_reserve_v1(void) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (!atomic_load_explicit(&queue->initialized, memory_order_acquire) ||
        atomic_load_explicit(&queue->stopping, memory_order_relaxed)) {
        return false;
    }
    bool expected = false;
    if (!atomic_compare_exchange_strong_explicit(
            &queue->reserved, &expected, true,
            memory_order_acquire, memory_order_relaxed)) {
        return false;
    }

    size_t position = atomic_load_explicit(&queue->enqueue_position, memory_order_relaxed);
    struct llgo_coro_worker_queue_slot_v1 *slot =
        &queue->slots[position & (LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 - 1)];
    if (atomic_load_explicit(&slot->sequence, memory_order_acquire) != position) {
        atomic_store_explicit(&queue->reserved, false, memory_order_release);
        return false;
    }
    atomic_store_explicit(&queue->reserved_position, position, memory_order_relaxed);
    return true;
}

bool __llgo_coro_worker_queue_cancel_reservation_v1(void) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (!atomic_load_explicit(&queue->initialized, memory_order_acquire) ||
        atomic_load_explicit(&queue->stopping, memory_order_relaxed)) {
        return false;
    }
    bool expected = true;
    return atomic_compare_exchange_strong_explicit(
        &queue->reserved, &expected, false,
        memory_order_release, memory_order_relaxed);
}

bool __llgo_coro_worker_queue_submit_reserved_v1(
    const struct llgo_coro_worker_job_v1 *job) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (!llgo_coro_worker_job_valid_v1(job) ||
        !atomic_load_explicit(&queue->initialized, memory_order_acquire) ||
        atomic_load_explicit(&queue->stopping, memory_order_relaxed) ||
        !atomic_load_explicit(&queue->reserved, memory_order_acquire)) {
        return false;
    }

    size_t position = atomic_load_explicit(&queue->reserved_position, memory_order_relaxed);
    if (atomic_load_explicit(&queue->enqueue_position, memory_order_relaxed) != position) {
        return false;
    }
    struct llgo_coro_worker_queue_slot_v1 *slot =
        &queue->slots[position & (LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 - 1)];
    if (atomic_load_explicit(&slot->sequence, memory_order_acquire) != position) {
        return false;
    }

    slot->job = *job;
    atomic_store_explicit(&queue->enqueue_position, position + 1, memory_order_relaxed);
    atomic_store_explicit(&slot->sequence, position + 1, memory_order_release);
    /*
     * Keep the reservation set through wake publication. A concurrent misuse
     * of Stop therefore fails instead of sealing between the durable job and
     * its matching wake token.
     */
    if (!llgo_coro_worker_wake_signal_v1(&queue->wake)) {
        return false;
    }
    atomic_store_explicit(&queue->reserved, false, memory_order_release);
    return true;
}

uint32_t __llgo_coro_worker_queue_wait_take_v1(
    struct llgo_coro_worker_job_v1 *job) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (job == NULL ||
        !atomic_load_explicit(&queue->initialized, memory_order_acquire) ||
        !llgo_coro_worker_wake_wait_v1(&queue->wake)) {
        return LLGO_CORO_WORKER_QUEUE_TAKE_INVALID_V1;
    }
    memset(job, 0, sizeof(*job));

    size_t position = atomic_load_explicit(&queue->dequeue_position, memory_order_relaxed);
    for (;;) {
        struct llgo_coro_worker_queue_slot_v1 *slot =
            &queue->slots[position & (LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 - 1)];
        size_t sequence = atomic_load_explicit(&slot->sequence, memory_order_acquire);
        if (sequence == position + 1) {
            if (atomic_compare_exchange_weak_explicit(
                    &queue->dequeue_position, &position, position + 1,
                    memory_order_relaxed, memory_order_relaxed)) {
                *job = slot->job;
                memset(&slot->job, 0, sizeof(slot->job));
                atomic_store_explicit(
                    &slot->sequence,
                    position + LLGO_CORO_WORKER_QUEUE_CAPACITY_V1,
                    memory_order_release);
                return llgo_coro_worker_job_valid_v1(job)
                    ? LLGO_CORO_WORKER_QUEUE_TAKE_JOB_V1
                    : LLGO_CORO_WORKER_QUEUE_TAKE_INVALID_V1;
            }
            continue;
        }

        position = atomic_load_explicit(&queue->dequeue_position, memory_order_relaxed);
        if (atomic_load_explicit(&queue->stopping, memory_order_acquire)) {
            return LLGO_CORO_WORKER_QUEUE_TAKE_STOP_V1;
        }
    }
}

bool __llgo_coro_worker_queue_stop_v1(uint32_t worker_count) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (worker_count > LLGO_CORO_WORKER_THREAD_COUNT_V1 ||
        !atomic_load_explicit(&queue->initialized, memory_order_acquire) ||
        atomic_load_explicit(&queue->reserved, memory_order_acquire)) {
        return false;
    }
    bool expected = false;
    if (!atomic_compare_exchange_strong_explicit(
            &queue->stopping, &expected, true,
            memory_order_release, memory_order_relaxed)) {
        return false;
    }
    for (uint32_t index = 0; index < worker_count; ++index) {
        if (!llgo_coro_worker_wake_signal_v1(&queue->wake)) {
            return false;
        }
    }
    return true;
}

bool __llgo_coro_worker_queue_destroy_after_join_v1(void) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (!atomic_load_explicit(&queue->initialized, memory_order_acquire) ||
        !atomic_load_explicit(&queue->stopping, memory_order_acquire) ||
        atomic_load_explicit(&queue->reserved, memory_order_relaxed) ||
        atomic_load_explicit(&queue->enqueue_position, memory_order_relaxed) !=
            atomic_load_explicit(&queue->dequeue_position, memory_order_relaxed) ||
        !llgo_coro_worker_wake_destroy_v1(&queue->wake)) {
        return false;
    }

    for (size_t index = 0; index < LLGO_CORO_WORKER_QUEUE_CAPACITY_V1; ++index) {
        memset(&queue->slots[index].job, 0, sizeof(queue->slots[index].job));
        atomic_store_explicit(&queue->slots[index].sequence, 0, memory_order_relaxed);
    }
    atomic_store_explicit(&queue->enqueue_position, 0, memory_order_relaxed);
    atomic_store_explicit(&queue->dequeue_position, 0, memory_order_relaxed);
    atomic_store_explicit(&queue->reserved_position, 0, memory_order_relaxed);
    atomic_store_explicit(&queue->stopping, false, memory_order_relaxed);
    atomic_store_explicit(&queue->initialized, false, memory_order_release);
    return true;
}

int __llgo_coro_worker_create_v1(pthread_t *thread) {
    if (thread == NULL) {
        return EINVAL;
    }
    return llgo_coro_worker_thread_create_v1(thread, llgo_coro_worker_main_v1);
}

bool __llgo_coro_worker_call_v1(
    uintptr_t function,
    uint32_t argc,
    const uintptr_t args[LLGO_CORO_WORKER_MAX_ARGS_V1],
    struct llgo_coro_worker_result_v1 *result) {
    if (function == 0 || argc > LLGO_CORO_WORKER_MAX_ARGS_V1 || args == NULL || result == NULL) {
        return false;
    }

    errno = 0;
    uintptr_t r1;
    switch (argc) {
    case 0:
        r1 = ((llgo_coro_worker_fn0_v1)function)();
        break;
    case 1:
        r1 = ((llgo_coro_worker_fn1_v1)function)(args[0]);
        break;
    case 2:
        r1 = ((llgo_coro_worker_fn2_v1)function)(args[0], args[1]);
        break;
    case 3:
        r1 = ((llgo_coro_worker_fn3_v1)function)(args[0], args[1], args[2]);
        break;
    case 4:
        r1 = ((llgo_coro_worker_fn4_v1)function)(args[0], args[1], args[2], args[3]);
        break;
    case 5:
        r1 = ((llgo_coro_worker_fn5_v1)function)(args[0], args[1], args[2], args[3], args[4]);
        break;
    case 6:
        r1 = ((llgo_coro_worker_fn6_v1)function)(args[0], args[1], args[2], args[3], args[4], args[5]);
        break;
    case 7:
        r1 = ((llgo_coro_worker_fn7_v1)function)(args[0], args[1], args[2], args[3], args[4], args[5], args[6]);
        break;
    case 8:
        r1 = ((llgo_coro_worker_fn8_v1)function)(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7]);
        break;
    case 9:
        r1 = ((llgo_coro_worker_fn9_v1)function)(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8]);
        break;
    default:
        return false;
    }

    /*
     * This leaf returns raw worker-local errno. The compiler-owned
     * llgo.syscall/llgo.syscall32/llgo.syscallPtr resume lowering applies the
     * exact word, int32, or NULL failure predicate before exposing the tuple.
     */
    result->r1 = r1;
    result->r2 = 0;
    result->error = (uintptr_t)errno;
    return true;
}

/*
 * This is the complete blocking worker island. Neither the queue wait nor the
 * uintptr-shaped foreign call can enter a managed LLVM coroutine. Only the
 * pointer-free completion tuple crosses back into Go, after the syscall has
 * returned and before this fixed native thread waits for its next job.
 */
static void *llgo_coro_worker_main_v1(void *unused) {
    (void)unused;
    for (;;) {
        struct llgo_coro_worker_job_v1 job;
        uint32_t status = __llgo_coro_worker_queue_wait_take_v1(&job);
        if (status == LLGO_CORO_WORKER_QUEUE_TAKE_STOP_V1) {
            return NULL;
        }
        if (status != LLGO_CORO_WORKER_QUEUE_TAKE_JOB_V1) {
            abort();
        }

        struct llgo_coro_worker_result_v1 result;
        if (!__llgo_coro_worker_call_v1(
                job.function, job.argc, job.args, &result) ||
            __llgo_coro_native_worker_complete_v1(
                job.source_slot,
                job.generation,
                result.r1,
                result.r2,
                result.error) != UINT32_C(1)) {
            abort();
        }
    }
}
