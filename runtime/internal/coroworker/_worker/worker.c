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

#ifndef _XOPEN_SOURCE
#define _XOPEN_SOURCE 700
#endif
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE
#endif
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif

#include "worker.h"

#include <errno.h>
#include <limits.h>
#include <sched.h>
#include <setjmp.h>
#include <signal.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <ucontext.h>
#include <unistd.h>

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
 * There is exactly one physical coroutine worker pool in one process. Exact P
 * owners reserve independent sequence cells and publish or cancel them before
 * leaving their no-suspend hooks. Fixed raw pthreads are the consumers, and
 * each job's source_slot carries its executor route. Sequence numbers make
 * every slot independently reusable, so no consumer and producer ever contend
 * on a mutex or retain a G/frame identity.
 */
struct llgo_coro_worker_queue_v1 {
    _Atomic bool initialized;
    /* High bit seals ingress; low bits count unpublished reservations. */
    _Atomic uint32_t producer_state;
    _Atomic size_t enqueue_position;
    _Atomic size_t dequeue_position;
    /* Exactly one between-job worker may advertise a kernel-free handoff. */
    _Atomic bool handoff_poller;
    llgo_coro_worker_wake_v1 wake;
    struct llgo_coro_worker_queue_slot_v1 slots[LLGO_CORO_WORKER_QUEUE_CAPACITY_V1];
};

static struct llgo_coro_worker_queue_v1 llgo_coro_worker_queue_v1;

extern uint32_t __llgo_coro_native_worker_complete_v1(
    uint32_t source_slot,
    uint32_t generation,
    uintptr_t r1,
    uintptr_t r2,
    uintptr_t error,
    uintptr_t fault,
    uintptr_t fault_pc,
    uintptr_t fault_target);

static void *llgo_coro_worker_main_v1(void *unused);

#if defined(__aarch64__) || defined(__arm__)
#define LLGO_CORO_WORKER_HANDOFF_POLLS_V1 UINT32_C(262144)
#else
#define LLGO_CORO_WORKER_HANDOFF_POLLS_V1 UINT32_C(32768)
#endif

static inline void llgo_coro_worker_cpu_relax_v1(void) {
#if defined(__aarch64__) || defined(__arm__)
    __asm__ volatile("yield" ::: "memory");
#elif defined(__x86_64__) || defined(__i386__)
    __asm__ volatile("pause" ::: "memory");
#else
    atomic_signal_fence(memory_order_seq_cst);
#endif
}

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
_Static_assert(offsetof(struct llgo_coro_worker_job_v1, trace_target) ==
    2 * sizeof(uint32_t) + sizeof(uintptr_t),
    "worker job trace target ABI changed");
_Static_assert(offsetof(struct llgo_coro_worker_job_v1, argc) ==
    2 * sizeof(uint32_t) + 2 * sizeof(uintptr_t),
    "worker job argc ABI changed");
_Static_assert(offsetof(struct llgo_coro_worker_job_v1, args) ==
    LLGO_CORO_WORKER_ALIGN_UP_V1(
        3 * sizeof(uint32_t) + 2 * sizeof(uintptr_t), _Alignof(uintptr_t)),
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
        job->function != 0 && job->trace_target != 0 &&
        job->argc <= LLGO_CORO_WORKER_MAX_ARGS_V1;
}

static bool llgo_coro_worker_job_canceled_v1(
    const struct llgo_coro_worker_job_v1 *job) {
    if (job == NULL || job->source_slot != 0 || job->generation != 0 ||
        job->function != 0 || job->trace_target != 0 || job->argc != 0) {
        return false;
    }
    for (uint32_t index = 0; index < LLGO_CORO_WORKER_MAX_ARGS_V1; ++index) {
        if (job->args[index] != 0) {
            return false;
        }
    }
    return true;
}

#define LLGO_CORO_WORKER_PRODUCER_STOPPING_V1 UINT32_C(0x80000000)
#define LLGO_CORO_WORKER_PRODUCER_COUNT_V1 UINT32_C(0x7fffffff)

static bool llgo_coro_worker_queue_stopping_v1(
    const struct llgo_coro_worker_queue_v1 *queue) {
    return (atomic_load_explicit(&queue->producer_state, memory_order_acquire) &
        LLGO_CORO_WORKER_PRODUCER_STOPPING_V1) != 0;
}

/*
 * Admission and shutdown share one atomic word. Stop can seal only zero active
 * reservations; once admitted, a producer can therefore publish its sequence
 * cell and matching wake token without racing queue destruction.
 */
static bool llgo_coro_worker_queue_enter_producer_v1(
    struct llgo_coro_worker_queue_v1 *queue) {
    if (!atomic_load_explicit(&queue->initialized, memory_order_acquire)) {
        return false;
    }
    uint32_t state = atomic_load_explicit(&queue->producer_state, memory_order_acquire);
    for (;;) {
        if ((state & LLGO_CORO_WORKER_PRODUCER_STOPPING_V1) != 0 ||
            (state & LLGO_CORO_WORKER_PRODUCER_COUNT_V1) ==
                LLGO_CORO_WORKER_PRODUCER_COUNT_V1) {
            return false;
        }
        if (atomic_compare_exchange_weak_explicit(
                &queue->producer_state, &state, state + 1,
                memory_order_acquire, memory_order_acquire)) {
            return true;
        }
    }
}

static bool llgo_coro_worker_queue_leave_producer_v1(
    struct llgo_coro_worker_queue_v1 *queue) {
    uint32_t previous = atomic_fetch_sub_explicit(
        &queue->producer_state, 1, memory_order_release);
    return (previous & LLGO_CORO_WORKER_PRODUCER_STOPPING_V1) == 0 &&
        (previous & LLGO_CORO_WORKER_PRODUCER_COUNT_V1) != 0;
}

static bool llgo_coro_worker_queue_reservation_slot_v1(
    struct llgo_coro_worker_queue_v1 *queue,
    size_t reservation,
    struct llgo_coro_worker_queue_slot_v1 **slot) {
    if (slot == NULL ||
        !atomic_load_explicit(&queue->initialized, memory_order_acquire) ||
        llgo_coro_worker_queue_stopping_v1(queue)) {
        return false;
    }
    size_t enqueue = atomic_load_explicit(&queue->enqueue_position, memory_order_acquire);
    if (reservation >= enqueue || enqueue - reservation > LLGO_CORO_WORKER_QUEUE_CAPACITY_V1) {
        return false;
    }
    struct llgo_coro_worker_queue_slot_v1 *candidate =
        &queue->slots[reservation & (LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 - 1)];
    if (atomic_load_explicit(&candidate->sequence, memory_order_acquire) != reservation) {
        return false;
    }
    *slot = candidate;
    return true;
}

static bool llgo_coro_worker_queue_publish_reserved_v1(
    struct llgo_coro_worker_queue_v1 *queue,
    size_t reservation,
    const struct llgo_coro_worker_job_v1 *job) {
    struct llgo_coro_worker_queue_slot_v1 *slot = NULL;
    if (!llgo_coro_worker_queue_reservation_slot_v1(
            queue, reservation, &slot)) {
        return false;
    }
    slot->job = *job;
    atomic_store_explicit(&slot->sequence, reservation + 1, memory_order_release);
    /*
     * Only a successful exchange of the exact handoff token may suppress the
     * wake. A poller clears that token before sleeping and rechecks after a
     * producer claims it, so no stale population count can lose a wake.
     * Backlog always emits another token and retains full pool parallelism.
     * Keep producer admission through this decision: Stop cannot seal between
     * the durable cell and its required wake.
     */
    size_t dequeue = atomic_load_explicit(&queue->dequeue_position, memory_order_acquire);
    bool direct_handoff = reservation == dequeue &&
        atomic_exchange_explicit(&queue->handoff_poller, false, memory_order_acq_rel);
    if ((!direct_handoff && !llgo_coro_worker_wake_signal_v1(&queue->wake)) ||
        !llgo_coro_worker_queue_leave_producer_v1(queue)) {
        return false;
    }
    return true;
}

bool __llgo_coro_worker_queue_can_release_v1(void) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    return !atomic_load_explicit(&queue->initialized, memory_order_acquire) &&
        atomic_load_explicit(&queue->producer_state, memory_order_relaxed) == 0 &&
        atomic_load_explicit(&queue->enqueue_position, memory_order_relaxed) == 0 &&
        atomic_load_explicit(&queue->dequeue_position, memory_order_relaxed) == 0 &&
        !atomic_load_explicit(&queue->handoff_poller, memory_order_relaxed);
}

bool __llgo_coro_worker_queue_init_v1(void) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (!__llgo_coro_worker_queue_can_release_v1()) {
        return false;
    }

    atomic_init(&queue->producer_state, 0);
    atomic_init(&queue->enqueue_position, 0);
    atomic_init(&queue->dequeue_position, 0);
    atomic_init(&queue->handoff_poller, false);
    for (size_t index = 0; index < LLGO_CORO_WORKER_QUEUE_CAPACITY_V1; ++index) {
        atomic_init(&queue->slots[index].sequence, index);
        memset(&queue->slots[index].job, 0, sizeof(queue->slots[index].job));
    }

    /* Refuse a target on which the supposedly nonblocking ingress atomics lock. */
    if (!atomic_is_lock_free(&queue->initialized) ||
        !atomic_is_lock_free(&queue->producer_state) ||
        !atomic_is_lock_free(&queue->enqueue_position) ||
        !atomic_is_lock_free(&queue->dequeue_position) ||
        !atomic_is_lock_free(&queue->handoff_poller) ||
        !atomic_is_lock_free(&queue->slots[0].sequence) ||
        !llgo_coro_worker_wake_init_v1(&queue->wake)) {
        return false;
    }
    atomic_store_explicit(&queue->initialized, true, memory_order_release);
    return true;
}

size_t __llgo_coro_worker_queue_reserve_v2(void) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (!llgo_coro_worker_queue_enter_producer_v1(queue)) {
        return 0;
    }

    size_t position = atomic_load_explicit(&queue->enqueue_position, memory_order_relaxed);
    for (;;) {
        /* Keep all sequence arithmetic defined instead of depending on wrap. */
        if (position > SIZE_MAX - LLGO_CORO_WORKER_QUEUE_CAPACITY_V1) {
            (void)llgo_coro_worker_queue_leave_producer_v1(queue);
            return 0;
        }
        struct llgo_coro_worker_queue_slot_v1 *slot =
            &queue->slots[position & (LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 - 1)];
        size_t sequence = atomic_load_explicit(&slot->sequence, memory_order_acquire);
        if (sequence == position) {
            size_t expected = position;
            if (atomic_compare_exchange_weak_explicit(
                    &queue->enqueue_position, &expected, position + 1,
                    memory_order_relaxed, memory_order_relaxed)) {
                /* Zero is the failure sentinel; the opaque public token is
                 * decoded by cancel/submit before sequence arithmetic. */
                return position + 1;
            }
            position = expected;
            continue;
        }
        if (sequence < position) {
            (void)llgo_coro_worker_queue_leave_producer_v1(queue);
            return 0;
        }
        position = atomic_load_explicit(&queue->enqueue_position, memory_order_relaxed);
    }
}

static bool llgo_coro_worker_queue_decode_reservation_v2(
    size_t reservation,
    size_t *position) {
    if (reservation == 0 || position == NULL) {
        return false;
    }
    *position = reservation - 1;
    return true;
}

bool __llgo_coro_worker_queue_cancel_reservation_v2(size_t reservation) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    size_t position;
    if (!llgo_coro_worker_queue_decode_reservation_v2(reservation, &position)) {
        return false;
    }
    struct llgo_coro_worker_job_v1 canceled;
    memset(&canceled, 0, sizeof(canceled));
    return llgo_coro_worker_queue_publish_reserved_v1(queue, position, &canceled);
}

bool __llgo_coro_worker_queue_submit_reserved_v4(
    size_t reservation,
    uint32_t source_slot,
    uint32_t generation,
    uintptr_t function,
    uintptr_t trace_target,
    uint32_t argc,
    uintptr_t a0,
    uintptr_t a1,
    uintptr_t a2,
    uintptr_t a3,
    uintptr_t a4,
    uintptr_t a5,
    uintptr_t a6,
    uintptr_t a7,
    uintptr_t a8) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    size_t position;
    if (!llgo_coro_worker_queue_decode_reservation_v2(reservation, &position)) {
        return false;
    }
    struct llgo_coro_worker_job_v1 job;
    memset(&job, 0, sizeof(job));
    job.source_slot = source_slot;
    job.generation = generation;
    job.function = function;
    job.trace_target = trace_target;
    job.argc = argc;
    job.args[0] = a0;
    job.args[1] = a1;
    job.args[2] = a2;
    job.args[3] = a3;
    job.args[4] = a4;
    job.args[5] = a5;
    job.args[6] = a6;
    job.args[7] = a7;
    job.args[8] = a8;
    if (!llgo_coro_worker_job_valid_v1(&job)) {
        return false;
    }
    return llgo_coro_worker_queue_publish_reserved_v1(queue, position, &job);
}

enum {
    LLGO_CORO_WORKER_QUEUE_TAKE_EMPTY_V1 = 3,
    LLGO_CORO_WORKER_QUEUE_TAKE_CANCELED_V1 = 4,
};

static uint32_t llgo_coro_worker_queue_try_take_v1(
    struct llgo_coro_worker_queue_v1 *queue,
    struct llgo_coro_worker_job_v1 *job) {
    for (;;) {
        size_t position = atomic_load_explicit(&queue->dequeue_position, memory_order_relaxed);
        struct llgo_coro_worker_queue_slot_v1 *slot =
            &queue->slots[position & (LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 - 1)];
        size_t sequence = atomic_load_explicit(&slot->sequence, memory_order_acquire);
        if (sequence == position + 1) {
            if (!atomic_compare_exchange_weak_explicit(
                    &queue->dequeue_position, &position, position + 1,
                    memory_order_relaxed, memory_order_relaxed)) {
                continue;
            }
            *job = slot->job;
            memset(&slot->job, 0, sizeof(slot->job));
            atomic_store_explicit(
                &slot->sequence,
                position + LLGO_CORO_WORKER_QUEUE_CAPACITY_V1,
                memory_order_release);
            if (llgo_coro_worker_job_valid_v1(job)) {
                return LLGO_CORO_WORKER_QUEUE_TAKE_JOB_V1;
            }
            return llgo_coro_worker_job_canceled_v1(job) ?
                LLGO_CORO_WORKER_QUEUE_TAKE_CANCELED_V1 :
                LLGO_CORO_WORKER_QUEUE_TAKE_INVALID_V1;
        }
        if (position != atomic_load_explicit(
                &queue->dequeue_position, memory_order_relaxed)) {
            continue;
        }
        if (llgo_coro_worker_queue_stopping_v1(queue) &&
            position == atomic_load_explicit(&queue->enqueue_position, memory_order_acquire)) {
            return LLGO_CORO_WORKER_QUEUE_TAKE_STOP_V1;
        }
        /* Empty also covers an earlier reservation which is not published yet. */
        return LLGO_CORO_WORKER_QUEUE_TAKE_EMPTY_V1;
    }
}

uint32_t __llgo_coro_worker_queue_wait_take_v1(
    struct llgo_coro_worker_job_v1 *job) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (job == NULL ||
        !atomic_load_explicit(&queue->initialized, memory_order_acquire)) {
        return LLGO_CORO_WORKER_QUEUE_TAKE_INVALID_V1;
    }
    for (;;) {
        /* A consumed semaphore token always earns one queue/Stop observation. */
        uint32_t status = llgo_coro_worker_queue_try_take_v1(queue, job);
        bool expected = false;
        if (status == LLGO_CORO_WORKER_QUEUE_TAKE_EMPTY_V1 &&
            atomic_compare_exchange_strong_explicit(
                &queue->handoff_poller, &expected, true,
                memory_order_acq_rel, memory_order_acquire)) {
            /*
             * Only this one idle worker owns the exact next dequeue cell.
             * A producer may consume the token only while publishing that
             * cell, so polling avoids a kernel wake without weakening queue
             * correctness. The bounded architecture-specific budget is only
             * a latency/power tradeoff; expiry falls back to the semaphore.
             */
            size_t handoff_position = atomic_load_explicit(
                &queue->dequeue_position, memory_order_relaxed);
            struct llgo_coro_worker_queue_slot_v1 *handoff_slot =
                &queue->slots[handoff_position &
                    (LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 - 1)];
            for (uint32_t attempt = 0;
                 attempt < LLGO_CORO_WORKER_HANDOFF_POLLS_V1 &&
                     atomic_load_explicit(
                         &handoff_slot->sequence, memory_order_acquire) !=
                         handoff_position + 1 &&
                     atomic_load_explicit(
                         &queue->handoff_poller, memory_order_acquire);
                 ++attempt) {
                llgo_coro_worker_cpu_relax_v1();
            }
            (void)atomic_exchange_explicit(
                &queue->handoff_poller, false, memory_order_acq_rel);
            status = llgo_coro_worker_queue_try_take_v1(queue, job);
        }
        if (status != LLGO_CORO_WORKER_QUEUE_TAKE_EMPTY_V1) {
            if (status == LLGO_CORO_WORKER_QUEUE_TAKE_CANCELED_V1) {
                continue;
            }
            return status;
        }
        if (!llgo_coro_worker_wake_wait_v1(&queue->wake)) {
            return LLGO_CORO_WORKER_QUEUE_TAKE_INVALID_V1;
        }
    }
}

bool __llgo_coro_worker_queue_stop_v1(uint32_t worker_count) {
    struct llgo_coro_worker_queue_v1 *queue = &llgo_coro_worker_queue_v1;
    if (worker_count > LLGO_CORO_WORKER_THREAD_COUNT_V1 ||
        !atomic_load_explicit(&queue->initialized, memory_order_acquire)) {
        return false;
    }
    uint32_t expected = 0;
    if (!atomic_compare_exchange_strong_explicit(
            &queue->producer_state, &expected,
            LLGO_CORO_WORKER_PRODUCER_STOPPING_V1,
            memory_order_acq_rel, memory_order_acquire)) {
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
        atomic_load_explicit(&queue->producer_state, memory_order_acquire) !=
            LLGO_CORO_WORKER_PRODUCER_STOPPING_V1 ||
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
    atomic_store_explicit(&queue->handoff_poller, false, memory_order_relaxed);
    atomic_store_explicit(&queue->producer_state, 0, memory_order_relaxed);
    atomic_store_explicit(&queue->initialized, false, memory_order_release);
    return true;
}

int __llgo_coro_worker_create_v1(pthread_t *thread) {
    if (thread == NULL) {
        return EINVAL;
    }
    return llgo_coro_worker_thread_create_v1(thread, llgo_coro_worker_main_v1);
}

/*
 * A potentially blocking C call runs outside the managed executor, but a
 * synchronous hardware fault must still complete the parked Go operation
 * instead of killing the process. The process handler therefore does only a
 * thread-local scalar capture and siglongjmp to this exact native call frame.
 * It never enters Go, allocates, locks, or retains the coroutine frame.
 */
struct llgo_coro_worker_fault_tls_v1 {
    sigjmp_buf *landing;
    uintptr_t trace_target;
    volatile uintptr_t fault_pc;
    volatile sig_atomic_t fault;
    volatile sig_atomic_t signal_number;
    volatile sig_atomic_t active;
    volatile sig_atomic_t handling;
};

static _Thread_local struct llgo_coro_worker_fault_tls_v1
    llgo_coro_worker_fault_tls_v1;
static _Thread_local int llgo_coro_worker_fault_signals_ready_v1;

struct llgo_coro_worker_fault_action_v1 {
    int signal;
    struct sigaction previous;
};

static struct llgo_coro_worker_fault_action_v1 llgo_coro_worker_fault_actions_v1[] = {
    {SIGSEGV, {0}},
    {SIGBUS, {0}},
    {SIGFPE, {0}},
};
static pthread_once_t llgo_coro_worker_fault_once_v1 = PTHREAD_ONCE_INIT;
static int llgo_coro_worker_fault_ready_v1;

static uintptr_t llgo_coro_worker_fault_pc_v1(void *opaque) {
    ucontext_t *context = (ucontext_t *)opaque;
#if defined(__APPLE__) && defined(__aarch64__)
    return (uintptr_t)context->uc_mcontext->__ss.__pc;
#elif defined(__APPLE__) && defined(__x86_64__)
    return (uintptr_t)context->uc_mcontext->__ss.__rip;
#elif defined(__linux__) && defined(__aarch64__)
    return (uintptr_t)context->uc_mcontext.pc;
#elif defined(__linux__) && defined(__x86_64__)
    return (uintptr_t)context->uc_mcontext.gregs[REG_RIP];
#else
    (void)context;
    return 0;
#endif
}

static struct llgo_coro_worker_fault_action_v1 *
llgo_coro_worker_fault_action_v1(int signal) {
    size_t count = sizeof(llgo_coro_worker_fault_actions_v1) /
        sizeof(llgo_coro_worker_fault_actions_v1[0]);
    for (size_t index = 0; index < count; ++index) {
        if (llgo_coro_worker_fault_actions_v1[index].signal == signal) {
            return &llgo_coro_worker_fault_actions_v1[index];
        }
    }
    return NULL;
}

static void llgo_coro_worker_forward_fault_v1(
    int signum,
    siginfo_t *info,
    void *context) {
    struct llgo_coro_worker_fault_action_v1 *saved =
        llgo_coro_worker_fault_action_v1(signum);
    if (saved != NULL && saved->previous.sa_handler == SIG_IGN) {
        return;
    }
    if (saved != NULL && saved->previous.sa_handler != SIG_DFL &&
        saved->previous.sa_handler != NULL) {
        if ((saved->previous.sa_flags & SA_SIGINFO) != 0) {
            saved->previous.sa_sigaction(signum, info, context);
        } else {
            saved->previous.sa_handler(signum);
        }
        return;
    }
    if (saved != NULL) {
        (void)sigaction(signum, &saved->previous, NULL);
    } else {
        (void)signal(signum, SIG_DFL);
    }
    (void)raise(signum);
    _exit(128 + signum);
}

static void llgo_coro_worker_fault_handler_v1(
    int signum,
    siginfo_t *info,
    void *context) {
    struct llgo_coro_worker_fault_tls_v1 *state =
        &llgo_coro_worker_fault_tls_v1;
    if (state->active != 0 && state->handling == 0 && state->landing != NULL) {
        state->handling = 1;
        state->fault = signum == SIGFPE ?
            LLGO_CORO_WORKER_FAULT_DIVIDE_V1 :
            LLGO_CORO_WORKER_FAULT_MEMORY_V1;
        state->signal_number = signum;
        state->fault_pc = llgo_coro_worker_fault_pc_v1(context);
        siglongjmp(*state->landing, 1);
    }
    llgo_coro_worker_forward_fault_v1(signum, info, context);
}

static void llgo_coro_worker_fault_install_v1(void) {
    struct sigaction action;
    size_t count = sizeof(llgo_coro_worker_fault_actions_v1) /
        sizeof(llgo_coro_worker_fault_actions_v1[0]);
    memset(&action, 0, sizeof(action));
    action.sa_sigaction = llgo_coro_worker_fault_handler_v1;
    sigemptyset(&action.sa_mask);
    action.sa_flags = SA_SIGINFO;
    for (size_t index = 0; index < count; ++index) {
        struct llgo_coro_worker_fault_action_v1 *slot =
            &llgo_coro_worker_fault_actions_v1[index];
        if (sigaction(slot->signal, NULL, &slot->previous) != 0 ||
            sigaction(slot->signal, &action, NULL) != 0) {
            while (index > 0) {
                --index;
                slot = &llgo_coro_worker_fault_actions_v1[index];
                (void)sigaction(slot->signal, &slot->previous, NULL);
            }
            return;
        }
    }
    llgo_coro_worker_fault_ready_v1 = 1;
}

/*
 * Internal workers must be able to receive the three synchronous faults that
 * the process handler translates. pthreads inherit their creator's mask, so
 * normalize it once per physical worker. After a cold siglongjmp, unblock the
 * delivered signal again because sigsetjmp deliberately leaves the hot path's
 * complete signal-mask snapshot unsaved.
 */
static bool llgo_coro_worker_unblock_fault_signals_v1(int only_signal) {
    sigset_t signals;
    if (sigemptyset(&signals) != 0) {
        return false;
    }
    if (only_signal != 0) {
        if (llgo_coro_worker_fault_action_v1(only_signal) == NULL) {
            return false;
        }
        if (sigaddset(&signals, only_signal) != 0) {
            return false;
        }
    } else {
        size_t count = sizeof(llgo_coro_worker_fault_actions_v1) /
            sizeof(llgo_coro_worker_fault_actions_v1[0]);
        for (size_t index = 0; index < count; ++index) {
            if (sigaddset(&signals, llgo_coro_worker_fault_actions_v1[index].signal) != 0) {
                return false;
            }
        }
    }
    return pthread_sigmask(SIG_UNBLOCK, &signals, NULL) == 0;
}

static bool llgo_coro_worker_prepare_fault_signals_v1(void) {
    if (llgo_coro_worker_fault_signals_ready_v1 != 0) {
        return true;
    }
    if (!llgo_coro_worker_unblock_fault_signals_v1(0)) {
        return false;
    }
    llgo_coro_worker_fault_signals_ready_v1 = 1;
    return true;
}

bool __llgo_coro_worker_call_v1(
    uintptr_t function,
    uintptr_t trace_target,
    uint32_t argc,
    const uintptr_t args[LLGO_CORO_WORKER_MAX_ARGS_V1],
    struct llgo_coro_worker_result_v1 *result) {
    if (function == 0 || argc > LLGO_CORO_WORKER_MAX_ARGS_V1 || args == NULL || result == NULL) {
        return false;
    }

    memset(result, 0, sizeof(*result));
    sigjmp_buf landing;
    struct llgo_coro_worker_fault_tls_v1 *fault_state =
        &llgo_coro_worker_fault_tls_v1;
    if (trace_target != 0) {
        if (fault_state->active != 0) {
            return false;
        }
        /*
         * fault_signals_ready is a thread-local certificate produced only
         * after pthread_once has installed the process handlers and this
         * physical thread has unblocked them. Rechecking pthread_once on
         * every foreign call adds an atomic/library boundary to the hottest
         * worker path without strengthening that certificate.
         */
        if (llgo_coro_worker_fault_signals_ready_v1 == 0 &&
            (pthread_once(
                 &llgo_coro_worker_fault_once_v1,
                 llgo_coro_worker_fault_install_v1) != 0 ||
             !llgo_coro_worker_fault_ready_v1 ||
             !llgo_coro_worker_prepare_fault_signals_v1())) {
            return false;
        }
        fault_state->landing = &landing;
        fault_state->trace_target = trace_target;
        fault_state->fault_pc = 0;
        fault_state->fault = LLGO_CORO_WORKER_FAULT_NONE_V1;
        fault_state->signal_number = 0;
        fault_state->handling = 0;
        if (sigsetjmp(landing, 0) != 0) {
            int fault_signal = (int)fault_state->signal_number;
            fault_state->active = 0;
            fault_state->landing = NULL;
            result->fault = (uintptr_t)fault_state->fault;
            result->fault_pc = (uintptr_t)fault_state->fault_pc;
            result->fault_target = fault_state->trace_target;
            fault_state->trace_target = 0;
            fault_state->fault_pc = 0;
            fault_state->fault = LLGO_CORO_WORKER_FAULT_NONE_V1;
            fault_state->signal_number = 0;
            fault_state->handling = 0;
            return llgo_coro_worker_unblock_fault_signals_v1(fault_signal) &&
                (result->fault == LLGO_CORO_WORKER_FAULT_MEMORY_V1 ||
                    result->fault == LLGO_CORO_WORKER_FAULT_DIVIDE_V1);
        }
        fault_state->active = 1;
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
        fault_state->active = 0;
        fault_state->landing = NULL;
        return false;
    }

    fault_state->active = 0;
    fault_state->landing = NULL;
    fault_state->trace_target = 0;
    fault_state->signal_number = 0;

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
                job.function, job.trace_target, job.argc, job.args, &result) ||
            __llgo_coro_native_worker_complete_v1(
                job.source_slot,
                job.generation,
                result.r1,
                result.r2,
                result.error,
                result.fault,
                result.fault_pc,
                result.fault_target) != UINT32_C(1)) {
            abort();
        }
    }
}
