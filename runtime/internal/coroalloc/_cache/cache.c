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
    /* Reuse a bounded working set of hot coroutine frames and recursive
       chains without holding unbounded per-class memory. Empty and live-held
       processes populate zero cached bytes, while bursty spawn/retire traffic
       reuses recent allocations without overflowing the L1/L2 working set. */
    llgo_coro_alloc_cache_bytes_per_bin = 128 * 1024,
    /* Per-thread magazine for the hot 256..1024B frame/task classes. Batching
       32 objects per global lock acquisition cuts multi-P spinlock traffic by
       32x without touching the shared global bin cache line on thread-local
       hit paths. Magazines live in a fixed static pool keyed via pthread_key_t
       rather than C11 _Thread_local so (1) thread-exit destructors never race
       with platform TLV teardown (e.g. Darwin dyld freeing _Thread_local
       before dynamic pthread_key destructors), (2) BDWGC always scans cached
       pointers in static storage, and (3) zero dynamic allocations are
       required. */
    llgo_coro_alloc_tls_batch = 32,
    llgo_coro_alloc_tls_max = 64,
    /* Cover 1152..8192B bins in per-thread magazines too (bounded to small
       counts) so hot medium/large frames (e.g. JSON struct arshalers and
       recursive decoders) avoid global spinlock contention across Ps. */
    llgo_coro_alloc_tls_bin_count =
        llgo_coro_alloc_cache_small_count +
        llgo_coro_alloc_cache_large_count + 1,
    llgo_coro_alloc_tls_large_batch = 4,
    llgo_coro_alloc_tls_large_max = 8,
    llgo_coro_alloc_tls_8k_batch = 2,
    llgo_coro_alloc_tls_8k_max = 4,
    llgo_coro_alloc_tls_pool_size = 64,
};

struct llgo_coro_alloc_cache_bin_v1 {
    _Alignas(128) _Atomic(uint32_t) lock;
    _Atomic(uint32_t) count;
    /* Zero is uninitialized; UINT32_MAX marks a class whose budget cannot
       retain even one object. */
    _Atomic(uint32_t) capacity;
    void *head;
    /* Stack of pre-linked full llgo_coro_alloc_tls_batch-node magazines.
       Each batch head stores the next batch pointer in word 1 (((void**)b)[1]),
       which is zeroed before returning the object to callers. */
    void *batch_head;
};

struct llgo_coro_alloc_tls_bin_v1 {
    void *head;
    uint32_t count;
};

struct llgo_coro_alloc_tls_cache_v1 {
    _Alignas(128) _Atomic(uint32_t) in_use;
    struct llgo_coro_alloc_tls_bin_v1 bins[llgo_coro_alloc_tls_bin_count];
};

static struct llgo_coro_alloc_cache_bin_v1
    llgo_coro_alloc_cache_bins_v1[llgo_coro_alloc_cache_bin_count];

static struct llgo_coro_alloc_tls_cache_v1
    llgo_coro_alloc_tls_pool_v1[llgo_coro_alloc_tls_pool_size];
static pthread_once_t llgo_coro_alloc_tls_key_once_v1 = PTHREAD_ONCE_INIT;
static pthread_key_t llgo_coro_alloc_tls_key_v1;
static _Atomic(bool) llgo_coro_alloc_tls_key_ready_v1;
/* Fast thread-local cache of the static pool slot owned by pthread_key_t.
   llgo_coro_alloc_tls_destroy_v1 receives the slot directly from pthread_key_t
   and NEVER accesses this _Thread_local variable, avoiding Darwin dyld TLV
   teardown races while eliminating pthread_getspecific calls on hot paths. */
static _Thread_local struct llgo_coro_alloc_tls_cache_v1 *
    llgo_coro_alloc_tls_fast_v1;

static inline int llgo_coro_alloc_cache_index_v1(uintptr_t size) {
    uintptr_t off = size - llgo_coro_alloc_cache_small_min;
    if (off <= (llgo_coro_alloc_cache_small_max - llgo_coro_alloc_cache_small_min) &&
        (off & (llgo_coro_alloc_cache_small_quantum - 1)) == 0) {
        return (int)(off / llgo_coro_alloc_cache_small_quantum);
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
    for (;;) {
        if (atomic_exchange_explicit(&bin->lock, 1, memory_order_acquire) == 0) {
            return;
        }
        while (atomic_load_explicit(&bin->lock, memory_order_relaxed) != 0) {
#if defined(__aarch64__)
            __asm__ __volatile__("yield" ::: "memory");
#elif defined(__x86_64__) || defined(__i386__)
            __builtin_ia32_pause();
#else
            atomic_signal_fence(memory_order_seq_cst);
#endif
        }
    }
}

static void llgo_coro_alloc_cache_unlock_v1(
    struct llgo_coro_alloc_cache_bin_v1 *bin) {
    atomic_store_explicit(&bin->lock, 0, memory_order_release);
}

static uint32_t llgo_coro_alloc_bin_capacity_v1(
    struct llgo_coro_alloc_cache_bin_v1 *bin,
    uintptr_t size) {
    uint32_t cap = atomic_load_explicit(&bin->capacity, memory_order_relaxed);
    if (cap != 0) {
        return cap;
    }
    uintptr_t computed = (uintptr_t)llgo_coro_alloc_cache_bytes_per_bin / size;
    cap = computed == 0 ? UINT32_MAX : (uint32_t)computed;
    atomic_store_explicit(&bin->capacity, cap, memory_order_relaxed);
    return cap;
}

static void llgo_coro_alloc_tls_destroy_v1(void *raw) {
    struct llgo_coro_alloc_tls_cache_v1 *cache =
        (struct llgo_coro_alloc_tls_cache_v1 *)raw;
    if (cache == NULL) {
        return;
    }
    for (int index = 0; index < llgo_coro_alloc_tls_bin_count; ++index) {
        struct llgo_coro_alloc_tls_bin_v1 *tbin = &cache->bins[index];
        if (tbin->head == NULL || tbin->count == 0) {
            tbin->head = NULL;
            tbin->count = 0;
            continue;
        }
        void *head = tbin->head;
        void *tail = head;
        uint32_t n = 1;
        while (*(void **)tail != NULL) {
            tail = *(void **)tail;
            ++n;
        }
        tbin->head = NULL;
        tbin->count = 0;
        struct llgo_coro_alloc_cache_bin_v1 *bin =
            &llgo_coro_alloc_cache_bins_v1[index];
        llgo_coro_alloc_cache_lock_v1(bin);
        *(void **)tail = bin->head;
        bin->head = head;
        uint32_t c = atomic_load_explicit(&bin->count, memory_order_relaxed);
        atomic_store_explicit(&bin->count, c + n, memory_order_relaxed);
        llgo_coro_alloc_cache_unlock_v1(bin);
    }
    atomic_store_explicit(&cache->in_use, 0, memory_order_release);
}

static void llgo_coro_alloc_tls_init_key_v1(void) {
    if (pthread_key_create(
            &llgo_coro_alloc_tls_key_v1,
            llgo_coro_alloc_tls_destroy_v1) == 0) {
        atomic_store_explicit(
            &llgo_coro_alloc_tls_key_ready_v1,
            true,
            memory_order_release);
    }
}

static struct llgo_coro_alloc_tls_cache_v1 *llgo_coro_alloc_tls_acquire_v1(
    void) {
    for (int i = 0; i < llgo_coro_alloc_tls_pool_size; ++i) {
        struct llgo_coro_alloc_tls_cache_v1 *slot =
            &llgo_coro_alloc_tls_pool_v1[i];
        uint32_t expected = 0;
        if (atomic_compare_exchange_strong_explicit(
                &slot->in_use,
                &expected,
                1,
                memory_order_acquire,
                memory_order_relaxed)) {
            if (pthread_setspecific(llgo_coro_alloc_tls_key_v1, slot) == 0) {
                llgo_coro_alloc_tls_fast_v1 = slot;
                return slot;
            }
            atomic_store_explicit(&slot->in_use, 0, memory_order_release);
            return NULL;
        }
    }
    return NULL;
}

static __attribute__((noinline)) struct llgo_coro_alloc_tls_cache_v1 *
llgo_coro_alloc_tls_slow_v1(bool create) {
    if (!atomic_load_explicit(
            &llgo_coro_alloc_tls_key_ready_v1, memory_order_acquire)) {
        if (!create) {
            return NULL;
        }
        pthread_once(
            &llgo_coro_alloc_tls_key_once_v1,
            llgo_coro_alloc_tls_init_key_v1);
        if (!atomic_load_explicit(
                &llgo_coro_alloc_tls_key_ready_v1, memory_order_acquire)) {
            return NULL;
        }
    }
    struct llgo_coro_alloc_tls_cache_v1 *cache =
        (struct llgo_coro_alloc_tls_cache_v1 *)pthread_getspecific(
            llgo_coro_alloc_tls_key_v1);
    if (cache != NULL) {
        llgo_coro_alloc_tls_fast_v1 = cache;
        return cache;
    }
    if (!create) {
        return NULL;
    }
    return llgo_coro_alloc_tls_acquire_v1();
}

static inline struct llgo_coro_alloc_tls_cache_v1 *llgo_coro_alloc_tls_get_v1(
    bool create) {
    struct llgo_coro_alloc_tls_cache_v1 *cache = llgo_coro_alloc_tls_fast_v1;
    if (__builtin_expect(cache != NULL, 1)) {
        return cache;
    }
    if (!create && !atomic_load_explicit(
            &llgo_coro_alloc_tls_key_ready_v1, memory_order_acquire)) {
        return NULL;
    }
    return llgo_coro_alloc_tls_slow_v1(create);
}

static inline uint32_t llgo_coro_alloc_tls_bin_max_v1(int index) {
    if (index < llgo_coro_alloc_cache_small_count) {
        return llgo_coro_alloc_tls_max;
    }
    if (index < llgo_coro_alloc_cache_small_count +
                    llgo_coro_alloc_cache_large_count) {
        return llgo_coro_alloc_tls_large_max;
    }
    return llgo_coro_alloc_tls_8k_max;
}

static inline uint32_t llgo_coro_alloc_tls_bin_batch_v1(int index) {
    if (index < llgo_coro_alloc_cache_small_count) {
        return llgo_coro_alloc_tls_batch;
    }
    if (index < llgo_coro_alloc_cache_small_count +
                    llgo_coro_alloc_cache_large_count) {
        return llgo_coro_alloc_tls_large_batch;
    }
    return llgo_coro_alloc_tls_8k_batch;
}

void *__llgo_coro_alloc_cache_take_v1(uintptr_t size) {
    int index = llgo_coro_alloc_cache_index_v1(size);
    if (index < 0) {
        return NULL;
    }
    if (index < llgo_coro_alloc_tls_bin_count) {
        struct llgo_coro_alloc_tls_cache_v1 *cache =
            llgo_coro_alloc_tls_get_v1(false);
        if (cache != NULL) {
            struct llgo_coro_alloc_tls_bin_v1 *tbin = &cache->bins[index];
            void *local = tbin->head;
            if (local != NULL) {
                tbin->head = *(void **)local;
                tbin->count--;
                *(void **)local = NULL;
                return local;
            }
        }
        struct llgo_coro_alloc_cache_bin_v1 *bin =
            &llgo_coro_alloc_cache_bins_v1[index];
        if (atomic_load_explicit(&bin->count, memory_order_relaxed) == 0) {
            return NULL;
        }
        if (cache == NULL) {
            cache = llgo_coro_alloc_tls_get_v1(true);
        }
        uint32_t batch_size = llgo_coro_alloc_tls_bin_batch_v1(index);
        llgo_coro_alloc_cache_lock_v1(bin);
        if (index < llgo_coro_alloc_cache_small_count) {
            void *batch = bin->batch_head;
            if (batch != NULL && cache != NULL) {
                void **words = (void **)batch;
                bin->batch_head = words[1];
                uint32_t c =
                    atomic_load_explicit(&bin->count, memory_order_relaxed);
                atomic_store_explicit(
                    &bin->count,
                    c - llgo_coro_alloc_tls_batch,
                    memory_order_relaxed);
                llgo_coro_alloc_cache_unlock_v1(bin);
                words[1] = NULL;
                struct llgo_coro_alloc_tls_bin_v1 *tbin = &cache->bins[index];
                tbin->head = words[0];
                tbin->count = llgo_coro_alloc_tls_batch - 1;
                words[0] = NULL;
                return batch;
            }
            if (bin->head == NULL && batch != NULL) {
                /* cache == NULL fallback: detach one node from batch_head and
                   move the remaining llgo_coro_alloc_tls_batch-1 nodes to
                   bin->head. */
                void **words = (void **)batch;
                bin->batch_head = words[1];
                words[1] = NULL;
                void *result = batch;
                bin->head = words[0];
                uint32_t c =
                    atomic_load_explicit(&bin->count, memory_order_relaxed);
                atomic_store_explicit(&bin->count, c - 1, memory_order_relaxed);
                llgo_coro_alloc_cache_unlock_v1(bin);
                words[0] = NULL;
                return result;
            }
        }
        void *result = bin->head;
        if (result != NULL) {
            void *next = *(void **)result;
            uint32_t taken = 1;
            if (cache != NULL && next != NULL && batch_size > 1) {
                struct llgo_coro_alloc_tls_bin_v1 *tbin = &cache->bins[index];
                void *batch_head = next;
                void *batch_tail = next;
                uint32_t stash = 1;
                while (stash < batch_size - 1 && *(void **)batch_tail != NULL) {
                    batch_tail = *(void **)batch_tail;
                    ++stash;
                }
                next = *(void **)batch_tail;
                *(void **)batch_tail = NULL;
                tbin->head = batch_head;
                tbin->count = stash;
                taken += stash;
            }
            bin->head = next;
            uint32_t c = atomic_load_explicit(&bin->count, memory_order_relaxed);
            atomic_store_explicit(&bin->count, c - taken, memory_order_relaxed);
        }
        llgo_coro_alloc_cache_unlock_v1(bin);
        if (result != NULL) {
            *(void **)result = NULL;
        }
        return result;
    }

    struct llgo_coro_alloc_cache_bin_v1 *bin =
        &llgo_coro_alloc_cache_bins_v1[index];
    if (atomic_load_explicit(&bin->count, memory_order_relaxed) == 0) {
        return NULL;
    }
    llgo_coro_alloc_cache_lock_v1(bin);
    void *result = bin->head;
    if (result != NULL) {
        bin->head = *(void **)result;
        uint32_t c = atomic_load_explicit(&bin->count, memory_order_relaxed);
        atomic_store_explicit(&bin->count, c - 1, memory_order_relaxed);
    }
    llgo_coro_alloc_cache_unlock_v1(bin);
    if (result != NULL) {
        *(void **)result = NULL;
    }
    return result;
}

bool __llgo_coro_alloc_cache_put_used_v1(
    void *pointer, uintptr_t size, uintptr_t used) {
    int index = llgo_coro_alloc_cache_index_v1(size);
    if (index < 0 || pointer == NULL) {
        return false;
    }
    /* Fresh backend allocations are zero-filled and callers never write beyond
       used bytes, so clearing [0, clear_size) preserves the all-zero invariant
       for the complete bucket without dirtying unused tail cache lines. */
    uintptr_t clear_size = (used > 0 && used < size) ? used : size;
    if (clear_size < sizeof(void *)) {
        clear_size = sizeof(void *);
    }
    struct llgo_coro_alloc_cache_bin_v1 *bin =
        &llgo_coro_alloc_cache_bins_v1[index];
    if (index < llgo_coro_alloc_tls_bin_count) {
        struct llgo_coro_alloc_tls_cache_v1 *cache =
            llgo_coro_alloc_tls_get_v1(true);
        if (cache != NULL) {
            uint32_t tls_max = llgo_coro_alloc_tls_bin_max_v1(index);
            uint32_t tls_batch = llgo_coro_alloc_tls_bin_batch_v1(index);
            struct llgo_coro_alloc_tls_bin_v1 *tbin = &cache->bins[index];
            if (tbin->count < tls_max) {
                memset(pointer, 0, clear_size);
                *(void **)pointer = tbin->head;
                tbin->head = pointer;
                tbin->count++;
                return true;
            }
            uint32_t cap = llgo_coro_alloc_bin_capacity_v1(bin, size);
            if (cap == UINT32_MAX ||
                atomic_load_explicit(&bin->count, memory_order_relaxed) >= cap) {
                return false;
            }
            /* Split a full batch from the thread-local magazine BEFORE
               acquiring the global bin lock so the critical section is O(1). */
            uint32_t want_flush =
                tbin->count >= tls_batch ? tls_batch : tbin->count;
            void *batch_head = tbin->head;
            void *batch_tail = batch_head;
            for (uint32_t i = 1; i < want_flush; ++i) {
                batch_tail = *(void **)batch_tail;
            }
            void *rest_head = *(void **)batch_tail;

            llgo_coro_alloc_cache_lock_v1(bin);
            uint32_t count =
                atomic_load_explicit(&bin->count, memory_order_relaxed);
            uint32_t room = count < cap ? cap - count : 0;
            if (index < llgo_coro_alloc_cache_small_count &&
                room >= want_flush && want_flush == llgo_coro_alloc_tls_batch) {
                *(void **)batch_tail = NULL;
                ((void **)batch_head)[1] = bin->batch_head;
                bin->batch_head = batch_head;
                atomic_store_explicit(
                    &bin->count, count + want_flush, memory_order_relaxed);
                llgo_coro_alloc_cache_unlock_v1(bin);
                tbin->head = rest_head;
                tbin->count -= want_flush;
                memset(pointer, 0, clear_size);
                *(void **)pointer = tbin->head;
                tbin->head = pointer;
                tbin->count++;
                return true;
            }
            uint32_t flush = want_flush < room ? want_flush : room;
            if (flush > 0) {
                if (flush < want_flush) {
                    batch_tail = batch_head;
                    for (uint32_t i = 1; i < flush; ++i) {
                        batch_tail = *(void **)batch_tail;
                    }
                    rest_head = *(void **)batch_tail;
                }
                *(void **)batch_tail = bin->head;
                bin->head = batch_head;
                atomic_store_explicit(
                    &bin->count, count + flush, memory_order_relaxed);
                llgo_coro_alloc_cache_unlock_v1(bin);
                tbin->head = rest_head;
                tbin->count -= flush;
                memset(pointer, 0, clear_size);
                *(void **)pointer = tbin->head;
                tbin->head = pointer;
                tbin->count++;
                return true;
            }
            llgo_coro_alloc_cache_unlock_v1(bin);
            return false;
        }
    }

    uint32_t cap = llgo_coro_alloc_bin_capacity_v1(bin, size);
    if (cap == UINT32_MAX) {
        return false;
    }
    if (atomic_load_explicit(&bin->count, memory_order_relaxed) >= cap) {
        return false;
    }

    /* Break every conservative GC edge before the process cache roots this
       allocation. The first word is then reused only as the private free link. */
    memset(pointer, 0, clear_size);
    llgo_coro_alloc_cache_lock_v1(bin);
    uint32_t count = atomic_load_explicit(&bin->count, memory_order_relaxed);
    if (count >= cap) {
        llgo_coro_alloc_cache_unlock_v1(bin);
        return false;
    }
    *(void **)pointer = bin->head;
    bin->head = pointer;
    atomic_store_explicit(&bin->count, count + 1, memory_order_relaxed);
    llgo_coro_alloc_cache_unlock_v1(bin);
    return true;
}

bool __llgo_coro_alloc_cache_put_v1(void *pointer, uintptr_t size) {
    return __llgo_coro_alloc_cache_put_used_v1(pointer, size, size);
}
