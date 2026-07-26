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
#include <stdatomic.h>
#include <stdint.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

uintptr_t __llgo_coro_native_fleet_e2e_thread_id_v1(void) {
    return (uintptr_t)pthread_self();
}

static _Atomic uint32_t llgo_coro_native_fleet_e2e_active_v1;
static _Atomic uint32_t llgo_coro_native_fleet_e2e_maximum_v1;
static _Atomic uint32_t llgo_coro_native_fleet_e2e_blocked_state_v1;
static _Atomic uint32_t llgo_coro_native_fleet_e2e_release_state_v1;
static _Atomic uint32_t llgo_coro_native_fleet_e2e_nested_blocked_state_v1;
static int llgo_coro_native_fleet_e2e_stream_v1[2] = {-1, -1};

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
