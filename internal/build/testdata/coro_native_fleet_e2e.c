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

#include <errno.h>
#include <pthread.h>
#include <sched.h>
#include <stdatomic.h>
#include <stdint.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

uintptr_t __llgo_coro_native_fleet_e2e_thread_id_v1(void) {
    return (uintptr_t)pthread_self();
}

typedef int32_t (*llgo_coro_native_fleet_e2e_callback_v1)(int32_t);

int32_t __llgo_coro_native_fleet_e2e_reentry_v1(
    llgo_coro_native_fleet_e2e_callback_v1 callback,
    int32_t value) {
    if (callback == NULL) {
        return INT32_MIN;
    }
    return callback(value) + callback(value + 1);
}

static _Atomic uint32_t llgo_coro_native_fleet_e2e_reentry_after_v2;

void __llgo_coro_native_fleet_e2e_reentry_escape_reset_v2(void) {
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_reentry_after_v2,
        0,
        memory_order_seq_cst);
}

uintptr_t __llgo_coro_native_fleet_e2e_reentry_escape_after_v2(void) {
    return (uintptr_t)atomic_load_explicit(
        &llgo_coro_native_fleet_e2e_reentry_after_v2,
        memory_order_seq_cst);
}

int32_t __llgo_coro_native_fleet_e2e_reentry_escape_v2(
    llgo_coro_native_fleet_e2e_callback_v1 callback,
    int32_t value) {
    if (callback == NULL) {
        return INT32_MIN;
    }
    int32_t result = callback(value);
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_reentry_after_v2,
        1,
        memory_order_seq_cst);
    return result;
}

extern int32_t __llgo_coro_native_fleet_e2e_export_v1(int32_t value);

int32_t __llgo_coro_native_fleet_e2e_call_export_v1(int32_t value) {
    return __llgo_coro_native_fleet_e2e_export_v1(value) +
           __llgo_coro_native_fleet_e2e_export_v1(value + 1);
}

struct llgo_coro_native_fleet_e2e_export_thread_v1 {
    int32_t value;
    int32_t result;
    _Atomic uint32_t *start;
};

static void *__llgo_coro_native_fleet_e2e_export_thread_main_v1(void *opaque) {
    struct llgo_coro_native_fleet_e2e_export_thread_v1 *call =
        (struct llgo_coro_native_fleet_e2e_export_thread_v1 *)opaque;
    if (call->start != NULL) {
        while (atomic_load_explicit(call->start, memory_order_seq_cst) == 0) {
            (void)sched_yield();
        }
    }
    call->result = __llgo_coro_native_fleet_e2e_export_v1(call->value);
    return NULL;
}

int32_t __llgo_coro_native_fleet_e2e_call_export_thread_v1(int32_t value) {
    struct llgo_coro_native_fleet_e2e_export_thread_v1 call = {
        .value = value,
        .result = INT32_MIN,
        .start = NULL,
    };
    pthread_t thread;
    if (pthread_create(
            &thread,
            NULL,
            __llgo_coro_native_fleet_e2e_export_thread_main_v1,
            &call) != 0 ||
        pthread_join(thread, NULL) != 0) {
        return INT32_MIN;
    }
    return call.result;
}

int32_t __llgo_coro_native_fleet_e2e_call_export_threads_v1(int32_t value) {
    /* Deliberately exceeds the eight-slot ingress rendezvous. The second
       wave must wait for and reuse slots instead of requiring unbounded
       process-global callback storage. */
    enum { thread_count = 16 };
    struct llgo_coro_native_fleet_e2e_export_thread_v1 calls[thread_count];
    pthread_t threads[thread_count];
    _Atomic uint32_t start = 0;
    uint32_t created = 0;
    for (; created < thread_count; created++) {
        calls[created].value = value + (int32_t)created;
        calls[created].result = INT32_MIN;
        calls[created].start = &start;
        if (pthread_create(
                &threads[created],
                NULL,
                __llgo_coro_native_fleet_e2e_export_thread_main_v1,
                &calls[created]) != 0) {
            break;
        }
    }
    atomic_store_explicit(&start, 1, memory_order_seq_cst);
    int32_t result = created == thread_count ? 0 : INT32_MIN;
    for (uint32_t index = 0; index < created; index++) {
        if (pthread_join(threads[index], NULL) != 0) {
            result = INT32_MIN;
        } else if (result != INT32_MIN) {
            result += calls[index].result;
        }
    }
    return result;
}

static _Atomic uint32_t llgo_coro_native_fleet_e2e_active_v1;
static _Atomic uint32_t llgo_coro_native_fleet_e2e_maximum_v1;
static _Atomic uint32_t llgo_coro_native_fleet_e2e_blocked_state_v1;
static _Atomic uint32_t llgo_coro_native_fleet_e2e_release_state_v1;
static _Atomic uint32_t llgo_coro_native_fleet_e2e_nested_blocked_state_v1;
static _Atomic uint32_t llgo_coro_native_fleet_e2e_thread_exit_count_v1;
static int llgo_coro_native_fleet_e2e_stream_v1[2] = {-1, -1};
static pthread_key_t llgo_coro_native_fleet_e2e_thread_exit_key_v1;
static pthread_once_t llgo_coro_native_fleet_e2e_thread_exit_once_v1 =
    PTHREAD_ONCE_INIT;

static void llgo_coro_native_fleet_e2e_thread_exit_destructor_v1(void *value) {
    if (value != NULL) {
        atomic_fetch_add_explicit(
            &llgo_coro_native_fleet_e2e_thread_exit_count_v1,
            1,
            memory_order_seq_cst);
    }
}

static void llgo_coro_native_fleet_e2e_thread_exit_init_v1(void) {
    (void)pthread_key_create(
        &llgo_coro_native_fleet_e2e_thread_exit_key_v1,
        llgo_coro_native_fleet_e2e_thread_exit_destructor_v1);
}

uintptr_t __llgo_coro_native_fleet_e2e_arm_thread_exit_v1(void) {
    if (pthread_once(
            &llgo_coro_native_fleet_e2e_thread_exit_once_v1,
            llgo_coro_native_fleet_e2e_thread_exit_init_v1) != 0 ||
        pthread_setspecific(
            llgo_coro_native_fleet_e2e_thread_exit_key_v1,
            (void *)(uintptr_t)1) != 0) {
        return 0;
    }
    return 1;
}

uintptr_t __llgo_coro_native_fleet_e2e_thread_exit_count_v1(void) {
    return (uintptr_t)atomic_load_explicit(
        &llgo_coro_native_fleet_e2e_thread_exit_count_v1,
        memory_order_seq_cst);
}

void __llgo_coro_native_fleet_e2e_wait_exit_and_release_v1(void) {
    while (atomic_load_explicit(
               &llgo_coro_native_fleet_e2e_thread_exit_count_v1,
               memory_order_seq_cst) == 0) {
        (void)sched_yield();
    }
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_release_state_v1,
        1,
        memory_order_seq_cst);
}

void __llgo_coro_native_fleet_e2e_quota_reset_v1(void) {
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_active_v1, 0, memory_order_seq_cst);
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_maximum_v1, 0, memory_order_seq_cst);
}

void __llgo_coro_native_fleet_e2e_quota_run_v1(uint32_t spins) {
    uint32_t current = atomic_fetch_add_explicit(
        &llgo_coro_native_fleet_e2e_active_v1, 1, memory_order_seq_cst) + 1;
    uint32_t maximum = atomic_load_explicit(
        &llgo_coro_native_fleet_e2e_maximum_v1, memory_order_seq_cst);
    while (maximum < current &&
           !atomic_compare_exchange_weak_explicit(
               &llgo_coro_native_fleet_e2e_maximum_v1,
               &maximum,
               current,
               memory_order_seq_cst,
               memory_order_seq_cst)) {
    }

    volatile uint32_t sink = current;
    for (uint32_t index = 0; index < spins; index++) {
        sink = sink * 1664525u + index + 1013904223u;
    }
    (void)sink;
    atomic_fetch_sub_explicit(
        &llgo_coro_native_fleet_e2e_active_v1, 1, memory_order_seq_cst);
}

uint32_t __llgo_coro_native_fleet_e2e_quota_maximum_v1(void) {
    return atomic_load_explicit(
        &llgo_coro_native_fleet_e2e_maximum_v1, memory_order_seq_cst);
}

void __llgo_coro_native_fleet_e2e_block_reset_v1(void) {
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_blocked_state_v1, 0, memory_order_seq_cst);
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_release_state_v1, 0, memory_order_seq_cst);
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_nested_blocked_state_v1,
        0,
        memory_order_seq_cst);
}

uintptr_t __llgo_coro_native_fleet_e2e_blocked_v1(void) {
    return (uintptr_t)atomic_load_explicit(
        &llgo_coro_native_fleet_e2e_blocked_state_v1, memory_order_seq_cst);
}

void __llgo_coro_native_fleet_e2e_release_v1(void) {
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_release_state_v1, 1, memory_order_seq_cst);
}

void __llgo_coro_native_fleet_e2e_block_v1(void) {
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_blocked_state_v1, 1, memory_order_seq_cst);
    while (atomic_load_explicit(
               &llgo_coro_native_fleet_e2e_release_state_v1,
               memory_order_seq_cst) == 0) {
    }
}

uintptr_t __llgo_coro_native_fleet_e2e_nested_blocked_v1(void) {
    return (uintptr_t)atomic_load_explicit(
        &llgo_coro_native_fleet_e2e_nested_blocked_state_v1,
        memory_order_seq_cst);
}

void __llgo_coro_native_fleet_e2e_nested_block_v1(void) {
    atomic_store_explicit(
        &llgo_coro_native_fleet_e2e_nested_blocked_state_v1,
        1,
        memory_order_seq_cst);
    struct timespec remaining = {
        .tv_sec = 0,
        .tv_nsec = 30 * 1000 * 1000,
    };
    while (nanosleep(&remaining, &remaining) != 0 && errno == EINTR) {
    }
}

uintptr_t __llgo_coro_native_fleet_e2e_stream_reset_v1(void) {
    for (uint32_t index = 0; index < 2; index++) {
        if (llgo_coro_native_fleet_e2e_stream_v1[index] >= 0) {
            (void)close(llgo_coro_native_fleet_e2e_stream_v1[index]);
            llgo_coro_native_fleet_e2e_stream_v1[index] = -1;
        }
    }
    return socketpair(
               AF_UNIX,
               SOCK_STREAM,
               0,
               llgo_coro_native_fleet_e2e_stream_v1) == 0;
}

int32_t __llgo_coro_native_fleet_e2e_stream_read_fd_v1(void) {
    return (int32_t)llgo_coro_native_fleet_e2e_stream_v1[0];
}

uintptr_t __llgo_coro_native_fleet_e2e_stream_write_v1(void) {
    const uint8_t value = UINT8_C(0x5a);
    return send(
               llgo_coro_native_fleet_e2e_stream_v1[1],
               &value,
               sizeof(value),
               MSG_DONTWAIT) == (ssize_t)sizeof(value);
}

uintptr_t __llgo_coro_native_fleet_e2e_stream_read_v1(void) {
    uint8_t value = 0;
    ssize_t size = recv(
        llgo_coro_native_fleet_e2e_stream_v1[0],
        &value,
        sizeof(value),
        MSG_DONTWAIT);
    return size == (ssize_t)sizeof(value) ? (uintptr_t)value : UINTPTR_MAX;
}

void __llgo_coro_native_fleet_e2e_stream_close_v1(void) {
    for (uint32_t index = 0; index < 2; index++) {
        if (llgo_coro_native_fleet_e2e_stream_v1[index] >= 0) {
            (void)close(llgo_coro_native_fleet_e2e_stream_v1[index]);
            llgo_coro_native_fleet_e2e_stream_v1[index] = -1;
        }
    }
}
