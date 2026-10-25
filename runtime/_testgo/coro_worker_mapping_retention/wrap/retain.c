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

#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif
#define GC_THREADS 1
#include <gc/gc.h>
#include <pthread.h>
#include <sched.h>
#include <stdatomic.h>
#include <stdint.h>
#include <string.h>

static pthread_t collector;
static _Atomic uint32_t *gate;
static _Atomic uint32_t held;
static _Atomic uint32_t released;
static _Atomic uint32_t collected;

static void *collect(void *unused) {
    (void)unused;
    while (atomic_load_explicit(gate, memory_order_acquire) == 0 ||
           atomic_load_explicit(&held, memory_order_acquire) < 4) {
        sched_yield();
    }
    GC_word before = GC_get_gc_no();
    for (unsigned cycle = 0; cycle < 4; ++cycle) {
        GC_gcollect();
        for (unsigned churn = 0; churn < 64; ++churn) {
            void *allocation = GC_malloc(64 * 1024);
            if (allocation == NULL) {
                atomic_store_explicit(&collected, UINT32_MAX, memory_order_release);
                atomic_store_explicit(&released, 1, memory_order_release);
                return NULL;
            }
            memset(allocation, 0xa5, 64 * 1024);
            GC_reachable_here(allocation);
        }
    }
    atomic_store_explicit(&collected, GC_get_gc_no() - before >= 4 ? 1 : 0, memory_order_release);
    atomic_store_explicit(&released, 1, memory_order_release);
    return NULL;
}

uintptr_t llgo_worker_mapping_collector_start(uint32_t *value) {
    gate = (_Atomic uint32_t *)value;
    return (uintptr_t)GC_pthread_create(&collector, NULL, collect, NULL);
}

uintptr_t llgo_worker_mapping_hold(void *pointer, uintptr_t size, uintptr_t index) {
    atomic_fetch_add_explicit(&held, 1, memory_order_release);
    while (atomic_load_explicit(&released, memory_order_acquire) == 0) {
        sched_yield();
    }
    const unsigned char *bytes = pointer;
    for (uintptr_t offset = 0; offset < size; ++offset) {
        if (bytes[offset] != (unsigned char)(index * 17 + offset)) return 1;
    }
    return 0;
}

uintptr_t llgo_worker_mapping_collector_join(void) {
    return GC_pthread_join(collector, NULL) == 0 &&
        atomic_load_explicit(&collected, memory_order_acquire) == 1 ? 0 : 1;
}
