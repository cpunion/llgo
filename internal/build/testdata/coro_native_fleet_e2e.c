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

#include <pthread.h>
#include <stdatomic.h>
#include <stdint.h>

uintptr_t __llgo_coro_native_fleet_e2e_thread_id_v1(void) {
    return (uintptr_t)pthread_self();
}

static _Atomic uint32_t llgo_coro_native_fleet_e2e_active_v1;
static _Atomic uint32_t llgo_coro_native_fleet_e2e_maximum_v1;

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
