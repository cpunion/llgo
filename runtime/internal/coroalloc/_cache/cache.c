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

#include <stdatomic.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

enum {
    llgo_coro_alloc_cache_small_min = 256,
    llgo_coro_alloc_cache_small_max = 1024,
    llgo_coro_alloc_cache_small_quantum = 32,
    llgo_coro_alloc_cache_small_count =
        (llgo_coro_alloc_cache_small_max - llgo_coro_alloc_cache_small_min) /
            llgo_coro_alloc_cache_small_quantum +
        1,
    llgo_coro_alloc_cache_large_min = 1152,
    llgo_coro_alloc_cache_large_max = 4096,
    llgo_coro_alloc_cache_large_quantum = 128,
    llgo_coro_alloc_cache_large_count =
        (llgo_coro_alloc_cache_large_max - llgo_coro_alloc_cache_large_min) /
            llgo_coro_alloc_cache_large_quantum +
        1,
    llgo_coro_alloc_cache_power_min_shift = 13,
    llgo_coro_alloc_cache_power_max_shift = 16,
    llgo_coro_alloc_cache_power_count =
        llgo_coro_alloc_cache_power_max_shift -
        llgo_coro_alloc_cache_power_min_shift + 1,
    llgo_coro_alloc_cache_bin_count =
        llgo_coro_alloc_cache_small_count +
        llgo_coro_alloc_cache_large_count +
        llgo_coro_alloc_cache_power_count,
    /* Small frames dominate high-rate spawn/retire loops, so preserve their
       prior reuse depth. The larger compact classes get a small budget: their
       main benefit is low live slack, not retaining a deep free list. Across
       all 53 bins the theoretical retention bound is below 3.6 MiB. */
    llgo_coro_alloc_cache_small_bytes_per_bin = 128 * 1024,
    llgo_coro_alloc_cache_large_bytes_per_bin = 16 * 1024,
};

struct llgo_coro_alloc_cache_bin_v1 {
    _Atomic(uint32_t) lock;
    void *head;
    uint32_t count;
    /* Zero is uninitialized; UINT32_MAX marks a class whose budget cannot
       retain even one object. On pointer-64 this occupies existing padding. */
    uint32_t capacity;
};

static struct llgo_coro_alloc_cache_bin_v1
    llgo_coro_alloc_cache_bins_v1[llgo_coro_alloc_cache_bin_count];

static int llgo_coro_alloc_cache_index_v1(uintptr_t size) {
    if (size >= llgo_coro_alloc_cache_small_min &&
        size <= llgo_coro_alloc_cache_small_max &&
        (size & (llgo_coro_alloc_cache_small_quantum - 1)) == 0) {
        return (int)((size - llgo_coro_alloc_cache_small_min) /
                     llgo_coro_alloc_cache_small_quantum);
    }
    if (size >= llgo_coro_alloc_cache_large_min &&
        size <= llgo_coro_alloc_cache_large_max &&
        (size & (llgo_coro_alloc_cache_large_quantum - 1)) == 0) {
        return llgo_coro_alloc_cache_small_count +
            (int)((size - llgo_coro_alloc_cache_large_min) /
                  llgo_coro_alloc_cache_large_quantum);
    }
    uintptr_t value =
        (uintptr_t)1 << llgo_coro_alloc_cache_power_min_shift;
    for (int index = llgo_coro_alloc_cache_small_count +
                     llgo_coro_alloc_cache_large_count;
         index < llgo_coro_alloc_cache_bin_count;
         ++index, value <<= 1) {
        if (size == value) {
            return index;
        }
    }
    return -1;
}

static void llgo_coro_alloc_cache_lock_v1(
    struct llgo_coro_alloc_cache_bin_v1 *bin) {
    while (atomic_exchange_explicit(&bin->lock, 1, memory_order_acquire) != 0) {
        atomic_signal_fence(memory_order_seq_cst);
    }
}

static void llgo_coro_alloc_cache_unlock_v1(
    struct llgo_coro_alloc_cache_bin_v1 *bin) {
    atomic_store_explicit(&bin->lock, 0, memory_order_release);
}

void *__llgo_coro_alloc_cache_take_v1(uintptr_t size) {
    int index = llgo_coro_alloc_cache_index_v1(size);
    if (index < 0) {
        return NULL;
    }
    struct llgo_coro_alloc_cache_bin_v1 *bin =
        &llgo_coro_alloc_cache_bins_v1[index];
    llgo_coro_alloc_cache_lock_v1(bin);
    void *result = bin->head;
    if (result != NULL) {
        bin->head = *(void **)result;
        --bin->count;
    }
    llgo_coro_alloc_cache_unlock_v1(bin);
    if (result != NULL) {
        *(void **)result = NULL;
    }
    return result;
}

bool __llgo_coro_alloc_cache_put_v1(void *pointer, uintptr_t size) {
    int index = llgo_coro_alloc_cache_index_v1(size);
    if (index < 0 || pointer == NULL) {
        return false;
    }

    /* Break every conservative GC edge before the process cache roots this
       allocation. The first word is then reused only as the private free link. */
    memset(pointer, 0, size);
    struct llgo_coro_alloc_cache_bin_v1 *bin =
        &llgo_coro_alloc_cache_bins_v1[index];
    llgo_coro_alloc_cache_lock_v1(bin);
    uint32_t capacity = bin->capacity;
    if (capacity == 0) {
        uintptr_t budget = size <= llgo_coro_alloc_cache_small_max
            ? llgo_coro_alloc_cache_small_bytes_per_bin
            : llgo_coro_alloc_cache_large_bytes_per_bin;
        uintptr_t computed = budget / size;
        capacity = computed == 0 ? UINT32_MAX : (uint32_t)computed;
        bin->capacity = capacity;
    }
    if (capacity == UINT32_MAX || bin->count >= capacity) {
        llgo_coro_alloc_cache_unlock_v1(bin);
        return false;
    }
    *(void **)pointer = bin->head;
    bin->head = pointer;
    ++bin->count;
    llgo_coro_alloc_cache_unlock_v1(bin);
    return true;
}
