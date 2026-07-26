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

#include "owner.h"

#include <sched.h>
#include <stdint.h>
#include <stdlib.h>
#include <unistd.h>

#if defined(LLGO_CORO_FLEET_BDWGC)
#include <gc/gc.h>
#endif

/*
 * This is intentionally the only C-to-Go scheduler-owner edge. The routine
 * carries only the small stable M-directory slot supplied as pthread's opaque
 * argument.
 * A zero pthread result means clean coordinated stop; a nonzero sentinel lets
 * the joining program owner reject a failed raw scheduler loop.
 */
extern uint32_t __llgo_coro_native_fleet_owner_v2(uint32_t slot);

static void *llgo_coro_fleet_owner_main_v2(void *slot_word) {
    uintptr_t slot = (uintptr_t)slot_word;
    if (slot == 0 || slot > UINT32_MAX) {
        return (void *)(uintptr_t)1;
    }
    return __llgo_coro_native_fleet_owner_v2((uint32_t)slot) == 1
        ? (void *)0
        : (void *)(uintptr_t)1;
}

enum llgo_coro_fleet_factory_state_v1 {
    LLGO_CORO_FLEET_FACTORY_UNUSED_V1 = 0,
    LLGO_CORO_FLEET_FACTORY_IDLE_V1,
    LLGO_CORO_FLEET_FACTORY_REQUESTED_V1,
    LLGO_CORO_FLEET_FACTORY_STARTING_V1,
    LLGO_CORO_FLEET_FACTORY_READY_V1,
    LLGO_CORO_FLEET_FACTORY_STOPPING_V1,
    LLGO_CORO_FLEET_FACTORY_FAILED_V1,
};

/*
 * Only this process-lifetime template thread creates scheduler-owner
 * pthreads after managed code starts. A LockOSThread owner may have changed
 * its signal mask, namespace, cwd/fs view, credentials, or other inherited
 * per-thread state. Creating a replacement directly from that M would copy
 * the tainted state into the scheduler fleet.
 *
 * Requests are deliberately serialized. Thread creation is exceptional and
 * the caller already cannot resume managed work until its exact replacement
 * exists. Keeping one scalar request avoids a second scheduler, an allocator,
 * a function pointer queue, and any retained Go address.
 */
struct llgo_coro_fleet_factory_v1 {
    pthread_mutex_t mutex;
    pthread_cond_t changed;
    pthread_t factory;
    pthread_t result_thread;
    uint32_t slot;
    int result;
    enum llgo_coro_fleet_factory_state_v1 state;
};

static struct llgo_coro_fleet_factory_v1 llgo_coro_fleet_factory_v1 = {
    .mutex = PTHREAD_MUTEX_INITIALIZER,
    .changed = PTHREAD_COND_INITIALIZER,
};

static int llgo_coro_fleet_owner_create_direct_v1(
        pthread_t *thread, uint32_t slot) {
    if (thread == 0 || slot == 0) {
        return -1;
    }
#if defined(LLGO_CORO_FLEET_BDWGC)
    return GC_pthread_create(
        thread, 0, llgo_coro_fleet_owner_main_v2, (void *)(uintptr_t)slot);
#else
    return pthread_create(
        thread, 0, llgo_coro_fleet_owner_main_v2, (void *)(uintptr_t)slot);
#endif
}

static int llgo_coro_fleet_thread_join_v1(
        pthread_t thread, void **result) {
#if defined(LLGO_CORO_FLEET_BDWGC)
    return GC_pthread_join(thread, result);
#else
    return pthread_join(thread, result);
#endif
}

static void *llgo_coro_fleet_factory_main_v1(void *unused) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    (void)unused;
    if (pthread_mutex_lock(&factory->mutex) != 0) {
        return (void *)(uintptr_t)1;
    }
    for (;;) {
        while (factory->state == LLGO_CORO_FLEET_FACTORY_IDLE_V1) {
            if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
                factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
                (void)pthread_cond_broadcast(&factory->changed);
                (void)pthread_mutex_unlock(&factory->mutex);
                return (void *)(uintptr_t)1;
            }
        }
        if (factory->state == LLGO_CORO_FLEET_FACTORY_STOPPING_V1) {
            (void)pthread_mutex_unlock(&factory->mutex);
            return 0;
        }
        if (factory->state != LLGO_CORO_FLEET_FACTORY_REQUESTED_V1 ||
            factory->slot == 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            (void)pthread_mutex_unlock(&factory->mutex);
            return (void *)(uintptr_t)1;
        }

        uint32_t slot = factory->slot;
        if (pthread_mutex_unlock(&factory->mutex) != 0) {
            return (void *)(uintptr_t)1;
        }
        pthread_t thread = (pthread_t)0;
        int result = llgo_coro_fleet_owner_create_direct_v1(&thread, slot);
        if (pthread_mutex_lock(&factory->mutex) != 0) {
            return (void *)(uintptr_t)1;
        }
        if (factory->state != LLGO_CORO_FLEET_FACTORY_REQUESTED_V1 ||
            factory->slot != slot) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            (void)pthread_mutex_unlock(&factory->mutex);
            return (void *)(uintptr_t)1;
        }
        factory->result = result;
        factory->result_thread = result == 0 ? thread : (pthread_t)0;
        factory->state = result == 0
            ? LLGO_CORO_FLEET_FACTORY_STARTING_V1
            : LLGO_CORO_FLEET_FACTORY_READY_V1;
        if (pthread_cond_broadcast(&factory->changed) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return (void *)(uintptr_t)1;
        }
        while (factory->state == LLGO_CORO_FLEET_FACTORY_STARTING_V1) {
            if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
                factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
                (void)pthread_cond_broadcast(&factory->changed);
                (void)pthread_mutex_unlock(&factory->mutex);
                return (void *)(uintptr_t)1;
            }
        }
        while (factory->state == LLGO_CORO_FLEET_FACTORY_READY_V1) {
            if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
                factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
                (void)pthread_cond_broadcast(&factory->changed);
                (void)pthread_mutex_unlock(&factory->mutex);
                return (void *)(uintptr_t)1;
            }
        }
        if (factory->state == LLGO_CORO_FLEET_FACTORY_STOPPING_V1) {
            (void)pthread_mutex_unlock(&factory->mutex);
            return 0;
        }
        if (factory->state != LLGO_CORO_FLEET_FACTORY_IDLE_V1 &&
            factory->state != LLGO_CORO_FLEET_FACTORY_REQUESTED_V1) {
            (void)pthread_mutex_unlock(&factory->mutex);
            return (void *)(uintptr_t)1;
        }
    }
}

static uint32_t llgo_coro_fleet_positive_decimal_v1(const char *text) {
    uint64_t value = 0;
    int overflow = 0;
    if (text == 0 || *text == 0) {
        return 0;
    }
    for (; *text != 0; text++) {
        if (*text < '0' || *text > '9') {
            return 0;
        }
        if (!overflow) {
            value = value * 10 + (uint32_t)(*text - '0');
            overflow = value > UINT32_MAX;
        }
    }
    return overflow ? UINT32_MAX : (uint32_t)value;
}

uint32_t __llgo_coro_fleet_owner_count_v1(uint32_t maximum) {
    if (maximum == 0) {
        return 0;
    }
    uint32_t selected = llgo_coro_fleet_positive_decimal_v1(getenv("GOMAXPROCS"));
    if (selected == 0) {
#ifdef _SC_NPROCESSORS_ONLN
        long online = sysconf(_SC_NPROCESSORS_ONLN);
        selected = online > 0 && (uint64_t)online <= UINT32_MAX
            ? (uint32_t)online
            : 1;
#else
        selected = 1;
#endif
    }
    return selected > maximum ? maximum : selected;
}

int __llgo_coro_fleet_factory_start_v1(void) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    if (factory->state != LLGO_CORO_FLEET_FACTORY_UNUSED_V1 ||
        factory->slot != 0 || factory->result != 0 ||
        factory->factory != (pthread_t)0 ||
        factory->result_thread != (pthread_t)0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    factory->state = LLGO_CORO_FLEET_FACTORY_IDLE_V1;
#if defined(LLGO_CORO_FLEET_BDWGC)
    int result = GC_pthread_create(
        &factory->factory, 0, llgo_coro_fleet_factory_main_v1, 0);
#else
    int result = pthread_create(
        &factory->factory, 0, llgo_coro_fleet_factory_main_v1, 0);
#endif
    if (result != 0) {
        factory->factory = (pthread_t)0;
        factory->state = LLGO_CORO_FLEET_FACTORY_UNUSED_V1;
    }
    return pthread_mutex_unlock(&factory->mutex) == 0 ? result : -1;
}

int __llgo_coro_fleet_owner_create_v2(pthread_t *thread, uint32_t slot) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (thread == 0 || slot == 0 || pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    *thread = (pthread_t)0;
    while (factory->state == LLGO_CORO_FLEET_FACTORY_REQUESTED_V1 ||
           factory->state == LLGO_CORO_FLEET_FACTORY_STARTING_V1 ||
           factory->state == LLGO_CORO_FLEET_FACTORY_READY_V1) {
        if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
    }
    if (factory->state != LLGO_CORO_FLEET_FACTORY_IDLE_V1) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    factory->slot = slot;
    factory->state = LLGO_CORO_FLEET_FACTORY_REQUESTED_V1;
    if (pthread_cond_broadcast(&factory->changed) != 0) {
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    while (factory->state == LLGO_CORO_FLEET_FACTORY_REQUESTED_V1 ||
           factory->state == LLGO_CORO_FLEET_FACTORY_STARTING_V1) {
        if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
    }
    if (factory->state != LLGO_CORO_FLEET_FACTORY_READY_V1 ||
        factory->slot != slot) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    int result = factory->result;
    *thread = result == 0 ? factory->result_thread : (pthread_t)0;
    factory->slot = 0;
    factory->result = 0;
    factory->result_thread = (pthread_t)0;
    factory->state = LLGO_CORO_FLEET_FACTORY_IDLE_V1;
    if (pthread_cond_broadcast(&factory->changed) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    return result;
}

/*
 * CreateOwner does not return merely because pthread_create published an
 * identity. The new raw owner calls this after it has claimed its scalar M
 * slot and route. This acknowledgement closes the old-owner exit versus
 * successor-start gap without exposing a Go pointer or callback address to C.
 */
int __llgo_coro_fleet_owner_ready_v1(uint32_t slot) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (slot == 0 || pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    /*
     * The new pthread may reach this acknowledgement before its factory has
     * reacquired the mutex after pthread_create and published result_thread.
     * Wait for that exact REQUESTED -> STARTING transition; rejecting the
     * early arrival would strand the factory in STARTING forever.
     */
    while (factory->state == LLGO_CORO_FLEET_FACTORY_REQUESTED_V1 &&
           factory->slot == slot) {
        if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
    }
    if (factory->state != LLGO_CORO_FLEET_FACTORY_STARTING_V1 ||
        factory->slot != slot ||
        factory->result != 0 ||
        factory->result_thread == (pthread_t)0 ||
        pthread_equal(factory->result_thread, pthread_self()) == 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    factory->state = LLGO_CORO_FLEET_FACTORY_READY_V1;
    if (pthread_cond_broadcast(&factory->changed) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    return 0;
}

int __llgo_coro_fleet_owner_detach_self_v1(void) {
#if defined(LLGO_CORO_FLEET_BDWGC)
    return GC_pthread_detach(pthread_self());
#else
    return pthread_detach(pthread_self());
#endif
}

int __llgo_coro_fleet_owner_yield_v1(void) {
    return sched_yield();
}

int __llgo_coro_fleet_factory_stop_v1(void) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    if (factory->state != LLGO_CORO_FLEET_FACTORY_IDLE_V1 ||
        factory->slot != 0 || factory->result != 0 ||
        factory->result_thread != (pthread_t)0 ||
        factory->factory == (pthread_t)0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    pthread_t thread = factory->factory;
    factory->state = LLGO_CORO_FLEET_FACTORY_STOPPING_V1;
    if (pthread_cond_broadcast(&factory->changed) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    void *result = (void *)(uintptr_t)1;
    int joined = llgo_coro_fleet_thread_join_v1(thread, &result);
    if (pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    if (joined != 0 || result != 0) {
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    factory->factory = (pthread_t)0;
    factory->state = LLGO_CORO_FLEET_FACTORY_UNUSED_V1;
    return pthread_mutex_unlock(&factory->mutex) == 0 ? 0 : -1;
}
