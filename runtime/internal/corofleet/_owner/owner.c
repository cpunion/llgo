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
 * This remains the only C-to-Go scheduler-owner edge. The raw pthread wrapper
 * may execute it more than once, but every invocation carries only one stable
 * scalar M-directory slot. C never retains a Go pointer, function pointer, G,
 * P, or LLVM coroutine handle.
 *
 * Return 1 terminates a permanent owner after coordinated stop. Return 2
 * publishes a temporary replacement to its parent, which must acknowledge the
 * exact physical token before the thread can enter the bounded standby cache.
 * Every other value is an invariant failure reported to the joining owner.
 */
extern uint32_t __llgo_coro_native_fleet_owner_v2(uint32_t slot);

enum {
    LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1 = 10000,
    LLGO_CORO_FLEET_OWNER_STANDBY_CAPACITY_V1 = 8,
};

enum llgo_coro_fleet_owner_state_v1 {
    LLGO_CORO_FLEET_OWNER_UNUSED_V1 = 0,
    LLGO_CORO_FLEET_OWNER_STARTING_V1,
    LLGO_CORO_FLEET_OWNER_DISPATCHING_V1,
    LLGO_CORO_FLEET_OWNER_RUNNING_V1,
    LLGO_CORO_FLEET_OWNER_RETURNED_V1,
    LLGO_CORO_FLEET_OWNER_PARKING_V1,
    LLGO_CORO_FLEET_OWNER_STANDBY_V1,
    LLGO_CORO_FLEET_OWNER_STOPPING_V1,
    LLGO_CORO_FLEET_OWNER_EXITED_V1,
    LLGO_CORO_FLEET_OWNER_RETIRING_V1,
    LLGO_CORO_FLEET_OWNER_FAILED_V1,
};

struct llgo_coro_fleet_owner_record_v1 {
    pthread_t thread;
    uint32_t slot;
    uint32_t token;
    uint32_t acknowledged;
    uint32_t published;
    uint32_t joining;
    enum llgo_coro_fleet_owner_state_v1 state;
    struct llgo_coro_fleet_owner_record_v1 *all_next;
    struct llgo_coro_fleet_owner_record_v1 *standby_next;
};

enum llgo_coro_fleet_factory_state_v1 {
    LLGO_CORO_FLEET_FACTORY_UNUSED_V1 = 0,
    LLGO_CORO_FLEET_FACTORY_IDLE_V1,
    LLGO_CORO_FLEET_FACTORY_REQUESTED_V1,
    LLGO_CORO_FLEET_FACTORY_CREATING_V1,
    LLGO_CORO_FLEET_FACTORY_READY_V1,
    LLGO_CORO_FLEET_FACTORY_STOPPING_V1,
    LLGO_CORO_FLEET_FACTORY_FAILED_V1,
};

/*
 * One mutex owns both the clean-thread creation rendezvous and the raw standby
 * cache. Only actual physical threads have records; lifecycle-only list scans
 * avoid reserving storage for all 10,000 logical M slots. Tokens are monotonic
 * and never reused, so a stale pthread/token pair cannot name a later record.
 * Request serialization is intentional: replacement creation is exceptional,
 * while a single fixed rendezvous avoids a second scheduler, allocator,
 * callback queue, and inherited tainted-thread state.
 */
struct llgo_coro_fleet_factory_v1 {
    pthread_mutex_t mutex;
    pthread_cond_t changed;
    pthread_t factory;
    pthread_t result_thread;
    uint32_t slot;
    uint32_t result_token;
    uint32_t next_token;
    uint32_t active_records;
    uint32_t standby_count;
    int result;
    enum llgo_coro_fleet_factory_state_v1 state;
    struct llgo_coro_fleet_owner_record_v1 *records;
    struct llgo_coro_fleet_owner_record_v1 *standby_head;
};

static struct llgo_coro_fleet_factory_v1 llgo_coro_fleet_factory_v1 = {
    .mutex = PTHREAD_MUTEX_INITIALIZER,
    .changed = PTHREAD_COND_INITIALIZER,
};

static int llgo_coro_fleet_thread_create_v1(
        pthread_t *thread, void *(*entry)(void *), void *argument) {
#if defined(LLGO_CORO_FLEET_BDWGC)
    return GC_pthread_create(thread, 0, entry, argument);
#else
    return pthread_create(thread, 0, entry, argument);
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

static int llgo_coro_fleet_thread_detach_self_v1(void) {
#if defined(LLGO_CORO_FLEET_BDWGC)
    return GC_pthread_detach(pthread_self());
#else
    return pthread_detach(pthread_self());
#endif
}

static int llgo_coro_fleet_find_record_for_token_locked_v1(
        uint32_t token,
        struct llgo_coro_fleet_owner_record_v1 **match) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (token == 0 || match == 0 ||
        factory->active_records > LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1) {
        return -1;
    }
    *match = 0;
    struct llgo_coro_fleet_owner_record_v1 *record = factory->records;
    uint32_t count = 0;
    while (record != 0 && count < LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1) {
        if (record->token == token) {
            if (*match != 0) {
                return -1;
            }
            *match = record;
        }
        record = record->all_next;
        count++;
    }
    return record == 0 && count == factory->active_records ? 0 : -1;
}

static int llgo_coro_fleet_find_record_for_slot_locked_v1(
        uint32_t slot,
        struct llgo_coro_fleet_owner_record_v1 **match) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (slot == 0 || slot > LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1 ||
        match == 0 ||
        factory->active_records > LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1) {
        return -1;
    }
    *match = 0;
    struct llgo_coro_fleet_owner_record_v1 *record = factory->records;
    uint32_t count = 0;
    while (record != 0 && count < LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1) {
        if (record->slot == slot) {
            if (*match != 0) {
                return -1;
            }
            *match = record;
        }
        record = record->all_next;
        count++;
    }
    return record == 0 && count == factory->active_records ? 0 : -1;
}

static void llgo_coro_fleet_clear_slot_locked_v1(
        struct llgo_coro_fleet_owner_record_v1 *record) {
    struct llgo_coro_fleet_owner_record_v1 *match = 0;
    if (record != 0 && record->slot != 0 &&
        (llgo_coro_fleet_find_record_for_slot_locked_v1(
             record->slot, &match) != 0 ||
         match != record)) {
        record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
        llgo_coro_fleet_factory_v1.state =
            LLGO_CORO_FLEET_FACTORY_FAILED_V1;
    }
}

static void llgo_coro_fleet_clear_record_locked_v1(
        struct llgo_coro_fleet_owner_record_v1 *record) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (record == 0 || record->state == LLGO_CORO_FLEET_OWNER_UNUSED_V1 ||
        record->standby_next != 0 || factory->active_records == 0) {
        if (record != 0) {
            record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
        }
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        return;
    }
    llgo_coro_fleet_clear_slot_locked_v1(record);
    struct llgo_coro_fleet_owner_record_v1 **link = &factory->records;
    uint32_t count = 0;
    while (*link != 0 && *link != record &&
           count < LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1) {
        link = &(*link)->all_next;
        count++;
    }
    if (*link != record) {
        record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        return;
    }
    *link = record->all_next;
    factory->active_records--;
    free(record);
    (void)pthread_cond_broadcast(&factory->changed);
}

static struct llgo_coro_fleet_owner_record_v1 *
llgo_coro_fleet_allocate_record_locked_v1(uint32_t slot) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    struct llgo_coro_fleet_owner_record_v1 *existing = 0;
    if (slot == 0 || slot > LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1 ||
        llgo_coro_fleet_find_record_for_slot_locked_v1(
            slot, &existing) != 0 || existing != 0 ||
        factory->active_records >= LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1 ||
        factory->next_token == UINT32_MAX) {
        return 0;
    }
    struct llgo_coro_fleet_owner_record_v1 *record =
        (struct llgo_coro_fleet_owner_record_v1 *)calloc(1, sizeof(*record));
    if (record == 0) {
        return 0;
    }
    factory->next_token++;
    record->slot = slot;
    record->token = factory->next_token;
    record->state = LLGO_CORO_FLEET_OWNER_STARTING_V1;
    record->all_next = factory->records;
    factory->records = record;
    factory->active_records++;
    return record;
}

static int llgo_coro_fleet_wait_owner_ack_locked_v1(
        struct llgo_coro_fleet_owner_record_v1 *record) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    while (record != 0 && record->acknowledged == 0 &&
           record->state != LLGO_CORO_FLEET_OWNER_FAILED_V1 &&
           factory->state != LLGO_CORO_FLEET_FACTORY_FAILED_V1 &&
           factory->state != LLGO_CORO_FLEET_FACTORY_STOPPING_V1) {
        if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            return -1;
        }
    }
    return record != 0 && record->acknowledged == 1 &&
            record->state == LLGO_CORO_FLEET_OWNER_RUNNING_V1
        ? 0
        : -1;
}

static void llgo_coro_fleet_owner_exit_cleanup_v1(void *opaque) {
    struct llgo_coro_fleet_owner_record_v1 *record =
        (struct llgo_coro_fleet_owner_record_v1 *)opaque;
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (record == 0 || pthread_mutex_lock(&factory->mutex) != 0) {
        return;
    }
    if (record->state == LLGO_CORO_FLEET_OWNER_RETIRING_V1 &&
        record->thread != (pthread_t)0 &&
        pthread_equal(record->thread, pthread_self()) != 0) {
        llgo_coro_fleet_clear_record_locked_v1(record);
    } else {
        record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        (void)pthread_cond_broadcast(&factory->changed);
    }
    (void)pthread_mutex_unlock(&factory->mutex);
}

static void *llgo_coro_fleet_owner_run_v1(
        struct llgo_coro_fleet_owner_record_v1 *record) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    for (;;) {
        if (pthread_mutex_lock(&factory->mutex) != 0) {
            return (void *)(uintptr_t)1;
        }
        while (record->state == LLGO_CORO_FLEET_OWNER_STANDBY_V1) {
            if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
                record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
                factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
                (void)pthread_mutex_unlock(&factory->mutex);
                return (void *)(uintptr_t)1;
            }
        }
        if (record->state == LLGO_CORO_FLEET_OWNER_STOPPING_V1) {
            record->state = LLGO_CORO_FLEET_OWNER_EXITED_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            (void)pthread_mutex_unlock(&factory->mutex);
            return 0;
        }
        while (record->state == LLGO_CORO_FLEET_OWNER_STARTING_V1 &&
               record->thread == (pthread_t)0) {
            if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
                record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
                factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
                (void)pthread_mutex_unlock(&factory->mutex);
                return (void *)(uintptr_t)1;
            }
        }
        if (record->state != LLGO_CORO_FLEET_OWNER_STARTING_V1 ||
            record->thread == (pthread_t)0 ||
            pthread_equal(record->thread, pthread_self()) == 0 ||
            record->slot == 0 || record->token == 0 ||
            record->acknowledged != 0 || record->published > 1) {
            record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            (void)pthread_mutex_unlock(&factory->mutex);
            return (void *)(uintptr_t)1;
        }
        uint32_t slot = record->slot;
        record->state = LLGO_CORO_FLEET_OWNER_DISPATCHING_V1;
        if (pthread_cond_broadcast(&factory->changed) != 0) {
            record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return (void *)(uintptr_t)1;
        }
        if (pthread_mutex_unlock(&factory->mutex) != 0) {
            return (void *)(uintptr_t)1;
        }

        uint32_t result = __llgo_coro_native_fleet_owner_v2(slot);

        if (pthread_mutex_lock(&factory->mutex) != 0) {
            return (void *)(uintptr_t)1;
        }
        struct llgo_coro_fleet_owner_record_v1 *slot_record = 0;
        if (record->state != LLGO_CORO_FLEET_OWNER_RUNNING_V1 ||
            record->slot != slot || record->acknowledged != 1 ||
            record->published != 1 ||
            llgo_coro_fleet_find_record_for_slot_locked_v1(
                slot, &slot_record) != 0 ||
            slot_record != record) {
            record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            (void)pthread_mutex_unlock(&factory->mutex);
            return (void *)(uintptr_t)1;
        }
        if (result == 2) {
            record->state = LLGO_CORO_FLEET_OWNER_RETURNED_V1;
            if (pthread_cond_broadcast(&factory->changed) != 0) {
                record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
                factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
                (void)pthread_mutex_unlock(&factory->mutex);
                return (void *)(uintptr_t)1;
            }
            while (record->state == LLGO_CORO_FLEET_OWNER_RETURNED_V1) {
                if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
                    record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
                    factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
                    (void)pthread_mutex_unlock(&factory->mutex);
                    return (void *)(uintptr_t)1;
                }
            }
            if (record->state == LLGO_CORO_FLEET_OWNER_PARKING_V1) {
                record->state = LLGO_CORO_FLEET_OWNER_STANDBY_V1;
                if (pthread_cond_broadcast(&factory->changed) != 0) {
                    record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
                    factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
                    (void)pthread_mutex_unlock(&factory->mutex);
                    return (void *)(uintptr_t)1;
                }
            } else if (record->state != LLGO_CORO_FLEET_OWNER_STOPPING_V1) {
                record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
                factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
                (void)pthread_cond_broadcast(&factory->changed);
                (void)pthread_mutex_unlock(&factory->mutex);
                return (void *)(uintptr_t)1;
            }
            if (pthread_mutex_unlock(&factory->mutex) != 0) {
                return (void *)(uintptr_t)1;
            }
            continue;
        }

        llgo_coro_fleet_clear_slot_locked_v1(record);
        record->slot = 0;
        record->state = result == 1
            ? LLGO_CORO_FLEET_OWNER_EXITED_V1
            : LLGO_CORO_FLEET_OWNER_FAILED_V1;
        if (result != 1) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        }
        (void)pthread_cond_broadcast(&factory->changed);
        (void)pthread_mutex_unlock(&factory->mutex);
        return result == 1 ? 0 : (void *)(uintptr_t)1;
    }
}

static void *llgo_coro_fleet_owner_main_v3(void *opaque) {
    struct llgo_coro_fleet_owner_record_v1 *record =
        (struct llgo_coro_fleet_owner_record_v1 *)opaque;
    void *result = (void *)(uintptr_t)1;
    pthread_cleanup_push(llgo_coro_fleet_owner_exit_cleanup_v1, opaque);
    result = llgo_coro_fleet_owner_run_v1(record);
    pthread_cleanup_pop(0);
    return result;
}

static void *llgo_coro_fleet_factory_main_v1(void *unused) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    (void)unused;
    if (pthread_mutex_lock(&factory->mutex) != 0) {
        return (void *)(uintptr_t)1;
    }
    for (;;) {
        while (factory->state == LLGO_CORO_FLEET_FACTORY_IDLE_V1 ||
               factory->state == LLGO_CORO_FLEET_FACTORY_READY_V1) {
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
            factory->slot == 0 || factory->result != 0 ||
            factory->result_thread != (pthread_t)0 ||
            factory->result_token != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            (void)pthread_mutex_unlock(&factory->mutex);
            return (void *)(uintptr_t)1;
        }

        uint32_t slot = factory->slot;
        struct llgo_coro_fleet_owner_record_v1 *record =
            llgo_coro_fleet_allocate_record_locked_v1(slot);
        if (record == 0) {
            factory->result = -1;
            factory->state = LLGO_CORO_FLEET_FACTORY_READY_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            continue;
        }
        factory->state = LLGO_CORO_FLEET_FACTORY_CREATING_V1;
        if (pthread_mutex_unlock(&factory->mutex) != 0) {
            return (void *)(uintptr_t)1;
        }
        pthread_t thread = (pthread_t)0;
        int result = llgo_coro_fleet_thread_create_v1(
            &thread,
            llgo_coro_fleet_owner_main_v3,
            record);
        if (pthread_mutex_lock(&factory->mutex) != 0) {
            return (void *)(uintptr_t)1;
        }
        if (factory->state != LLGO_CORO_FLEET_FACTORY_CREATING_V1 ||
            factory->slot != slot || record->slot != slot ||
            record->state != LLGO_CORO_FLEET_OWNER_STARTING_V1) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            (void)pthread_mutex_unlock(&factory->mutex);
            return (void *)(uintptr_t)1;
        }
        if (result != 0) {
            llgo_coro_fleet_clear_record_locked_v1(record);
            factory->result = result;
            factory->state = LLGO_CORO_FLEET_FACTORY_READY_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            continue;
        }
        record->thread = thread;
        factory->result_thread = thread;
        factory->result_token = record->token;
        (void)pthread_cond_broadcast(&factory->changed);
        if (llgo_coro_fleet_wait_owner_ack_locked_v1(record) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_cond_broadcast(&factory->changed);
            (void)pthread_mutex_unlock(&factory->mutex);
            return (void *)(uintptr_t)1;
        }
        factory->result = 0;
        factory->state = LLGO_CORO_FLEET_FACTORY_READY_V1;
        if (pthread_cond_broadcast(&factory->changed) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
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
    uint32_t selected =
        llgo_coro_fleet_positive_decimal_v1(getenv("GOMAXPROCS"));
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
        factory->result_thread != (pthread_t)0 ||
        factory->result_token != 0 || factory->active_records != 0 ||
        factory->records != 0 ||
        factory->standby_head != 0 || factory->standby_count != 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    factory->state = LLGO_CORO_FLEET_FACTORY_IDLE_V1;
    int result = llgo_coro_fleet_thread_create_v1(
        &factory->factory,
        llgo_coro_fleet_factory_main_v1,
        0);
    if (result != 0) {
        factory->factory = (pthread_t)0;
        factory->state = LLGO_CORO_FLEET_FACTORY_UNUSED_V1;
    }
    return pthread_mutex_unlock(&factory->mutex) == 0 ? result : -1;
}

int __llgo_coro_fleet_owner_create_v3(
        pthread_t *thread, uint32_t *token, uint32_t slot) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (thread == 0 || token == 0 || slot == 0 ||
        slot > LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1 ||
        pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    *thread = (pthread_t)0;
    *token = 0;
    while (factory->state == LLGO_CORO_FLEET_FACTORY_REQUESTED_V1 ||
           factory->state == LLGO_CORO_FLEET_FACTORY_CREATING_V1 ||
           factory->state == LLGO_CORO_FLEET_FACTORY_READY_V1) {
        if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
    }
    struct llgo_coro_fleet_owner_record_v1 *existing = 0;
    if (factory->state != LLGO_CORO_FLEET_FACTORY_IDLE_V1 ||
        llgo_coro_fleet_find_record_for_slot_locked_v1(
            slot, &existing) != 0 || existing != 0) {
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
           factory->state == LLGO_CORO_FLEET_FACTORY_CREATING_V1) {
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
    if (result == 0) {
        struct llgo_coro_fleet_owner_record_v1 *record = 0;
        if (llgo_coro_fleet_find_record_for_token_locked_v1(
                factory->result_token, &record) != 0 ||
            record == 0 || record->thread == (pthread_t)0 ||
            pthread_equal(record->thread, factory->result_thread) == 0 ||
            record->slot != slot ||
            record->state != LLGO_CORO_FLEET_OWNER_RUNNING_V1 ||
            record->acknowledged != 1 || record->published != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
        *thread = record->thread;
        *token = record->token;
        record->published = 1;
    }
    factory->slot = 0;
    factory->result = 0;
    factory->result_thread = (pthread_t)0;
    factory->result_token = 0;
    factory->state = LLGO_CORO_FLEET_FACTORY_IDLE_V1;
    if (pthread_cond_broadcast(&factory->changed) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    return result;
}

int __llgo_coro_fleet_owner_try_reuse_v1(
        pthread_t *thread, uint32_t *token, uint32_t slot) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (thread == 0 || token == 0 || slot == 0 ||
        slot > LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1 ||
        pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    *thread = (pthread_t)0;
    *token = 0;
    struct llgo_coro_fleet_owner_record_v1 *existing = 0;
    if (factory->state == LLGO_CORO_FLEET_FACTORY_UNUSED_V1 ||
        factory->state == LLGO_CORO_FLEET_FACTORY_STOPPING_V1 ||
        factory->state == LLGO_CORO_FLEET_FACTORY_FAILED_V1 ||
        llgo_coro_fleet_find_record_for_slot_locked_v1(
            slot, &existing) != 0 || existing != 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    if (factory->standby_head == 0) {
        if (factory->standby_count != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
        (void)pthread_mutex_unlock(&factory->mutex);
        return 1;
    }
    struct llgo_coro_fleet_owner_record_v1 *record =
        factory->standby_head;
    if (factory->standby_count == 0) {
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    if (record->state != LLGO_CORO_FLEET_OWNER_STANDBY_V1 ||
        record->thread == (pthread_t)0 || record->slot != 0 ||
        record->token == 0 || record->acknowledged != 0 ||
        record->published != 0 || record->joining != 0) {
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    factory->standby_head = record->standby_next;
    factory->standby_count--;
    record->standby_next = 0;
    record->slot = slot;
    record->state = LLGO_CORO_FLEET_OWNER_STARTING_V1;
    if (pthread_cond_broadcast(&factory->changed) != 0 ||
        llgo_coro_fleet_wait_owner_ack_locked_v1(record) != 0) {
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    *thread = record->thread;
    *token = record->token;
    record->published = 1;
    if (pthread_cond_broadcast(&factory->changed) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    return 0;
}

int __llgo_coro_fleet_owner_request_reuse_v1(
        pthread_t *thread, uint32_t *token, uint32_t slot) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (thread == 0 || token == 0 || slot == 0 ||
        slot > LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1 ||
        pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    *thread = (pthread_t)0;
    *token = 0;
    struct llgo_coro_fleet_owner_record_v1 *existing = 0;
    if (factory->state == LLGO_CORO_FLEET_FACTORY_UNUSED_V1 ||
        factory->state == LLGO_CORO_FLEET_FACTORY_STOPPING_V1 ||
        factory->state == LLGO_CORO_FLEET_FACTORY_FAILED_V1 ||
        llgo_coro_fleet_find_record_for_slot_locked_v1(
            slot, &existing) != 0 || existing != 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    if (factory->standby_head == 0) {
        if (factory->standby_count != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
        (void)pthread_mutex_unlock(&factory->mutex);
        return 1;
    }
    struct llgo_coro_fleet_owner_record_v1 *record =
        factory->standby_head;
    if (factory->standby_count == 0 ||
        record->state != LLGO_CORO_FLEET_OWNER_STANDBY_V1 ||
        record->thread == (pthread_t)0 || record->slot != 0 ||
        record->token == 0 || record->acknowledged != 0 ||
        record->published != 0 || record->joining != 0) {
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    factory->standby_head = record->standby_next;
    factory->standby_count--;
    record->standby_next = 0;
    record->slot = slot;
    record->published = 1;
    record->state = LLGO_CORO_FLEET_OWNER_STARTING_V1;
    *thread = record->thread;
    *token = record->token;
    if (pthread_cond_broadcast(&factory->changed) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    return 0;
}

int __llgo_coro_fleet_owner_cancel_reuse_v1(
        pthread_t thread, uint32_t token, uint32_t slot) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (thread == (pthread_t)0 || token == 0 || slot == 0 ||
        pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    struct llgo_coro_fleet_owner_record_v1 *record = 0;
    struct llgo_coro_fleet_owner_record_v1 *slot_record = 0;
    if (llgo_coro_fleet_find_record_for_token_locked_v1(
            token, &record) != 0 ||
        llgo_coro_fleet_find_record_for_slot_locked_v1(
            slot, &slot_record) != 0 ||
        record == 0 || slot_record != record ||
        record->thread == (pthread_t)0 ||
        pthread_equal(record->thread, thread) == 0 ||
        record->slot != slot || record->token != token ||
        record->published != 1 || record->joining != 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    if (record->state != LLGO_CORO_FLEET_OWNER_STARTING_V1) {
        int dispatched =
            (record->state == LLGO_CORO_FLEET_OWNER_DISPATCHING_V1 &&
             record->acknowledged == 0) ||
            ((record->state == LLGO_CORO_FLEET_OWNER_RUNNING_V1 ||
              record->state == LLGO_CORO_FLEET_OWNER_RETURNED_V1) &&
             record->acknowledged == 1);
        (void)pthread_mutex_unlock(&factory->mutex);
        return dispatched ? 1 : -1;
    }
    if (record->acknowledged != 0 || factory->standby_count >=
        LLGO_CORO_FLEET_OWNER_STANDBY_CAPACITY_V1) {
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    llgo_coro_fleet_clear_slot_locked_v1(record);
    record->slot = 0;
    record->published = 0;
    record->state = LLGO_CORO_FLEET_OWNER_STANDBY_V1;
    record->standby_next = factory->standby_head;
    factory->standby_head = record;
    factory->standby_count++;
    if (pthread_cond_broadcast(&factory->changed) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    return 0;
}

int __llgo_coro_fleet_owner_ready_v1(uint32_t slot) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (slot == 0 || slot > LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1 ||
        pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    struct llgo_coro_fleet_owner_record_v1 *record = 0;
    if (llgo_coro_fleet_find_record_for_slot_locked_v1(
            slot, &record) != 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    while (record != 0 && record->thread == (pthread_t)0 &&
           record->state == LLGO_CORO_FLEET_OWNER_DISPATCHING_V1 &&
           factory->state != LLGO_CORO_FLEET_FACTORY_FAILED_V1) {
        if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
    }
    if (record == 0 || record->thread == (pthread_t)0 ||
        pthread_equal(record->thread, pthread_self()) == 0 ||
        record->state != LLGO_CORO_FLEET_OWNER_DISPATCHING_V1 ||
        record->slot != slot || record->acknowledged != 0 ||
        record->published > 1) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    record->state = LLGO_CORO_FLEET_OWNER_RUNNING_V1;
    record->acknowledged = 1;
    if (pthread_cond_broadcast(&factory->changed) != 0) {
        record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    while (record->published == 0 &&
           record->state == LLGO_CORO_FLEET_OWNER_RUNNING_V1 &&
           factory->state != LLGO_CORO_FLEET_FACTORY_FAILED_V1) {
        if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
            record->state = LLGO_CORO_FLEET_OWNER_FAILED_V1;
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
    }
    int result = record->published == 1 &&
            record->state == LLGO_CORO_FLEET_OWNER_RUNNING_V1
        ? 0
        : -1;
    return pthread_mutex_unlock(&factory->mutex) == 0 ? result : -1;
}

int __llgo_coro_fleet_owner_join_v1(
        pthread_t thread, uint32_t token) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (thread == (pthread_t)0 || token == 0 ||
        pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    struct llgo_coro_fleet_owner_record_v1 *record = 0;
    if (llgo_coro_fleet_find_record_for_token_locked_v1(
            token, &record) != 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    if (record == 0 || record->thread == (pthread_t)0 ||
        pthread_equal(record->thread, thread) == 0 ||
        record->joining != 0 ||
        record->state == LLGO_CORO_FLEET_OWNER_UNUSED_V1 ||
        record->state == LLGO_CORO_FLEET_OWNER_STANDBY_V1 ||
        record->state == LLGO_CORO_FLEET_OWNER_RETURNED_V1 ||
        record->state == LLGO_CORO_FLEET_OWNER_PARKING_V1 ||
        record->state == LLGO_CORO_FLEET_OWNER_RETIRING_V1) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    record->joining = 1;
    if (pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    void *thread_result = (void *)(uintptr_t)1;
    int joined = llgo_coro_fleet_thread_join_v1(thread, &thread_result);
    if (pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    record = 0;
    int found = llgo_coro_fleet_find_record_for_token_locked_v1(
        token, &record);
    int valid = joined == 0 && thread_result == 0 &&
        found == 0 && record != 0 && record->thread != (pthread_t)0 &&
        pthread_equal(record->thread, thread) != 0 &&
        record->joining == 1 &&
        record->state == LLGO_CORO_FLEET_OWNER_EXITED_V1;
    if (record != 0 &&
        (record->state == LLGO_CORO_FLEET_OWNER_EXITED_V1 ||
         record->state == LLGO_CORO_FLEET_OWNER_FAILED_V1)) {
        llgo_coro_fleet_clear_record_locked_v1(record);
    } else if (!valid) {
        factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
    }
    int unlocked = pthread_mutex_unlock(&factory->mutex);
    return valid && unlocked == 0 ? 0 : -1;
}

int __llgo_coro_fleet_owner_release_v1(
        pthread_t thread, uint32_t token, uint32_t slot) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (thread == (pthread_t)0 || token == 0 || slot == 0 ||
        pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    struct llgo_coro_fleet_owner_record_v1 *record = 0;
    if (llgo_coro_fleet_find_record_for_token_locked_v1(
            token, &record) != 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    while (record != 0 &&
           (record->state == LLGO_CORO_FLEET_OWNER_DISPATCHING_V1 ||
            record->state == LLGO_CORO_FLEET_OWNER_RUNNING_V1)) {
        if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
    }
    struct llgo_coro_fleet_owner_record_v1 *slot_record = 0;
    if (record == 0 || record->thread == (pthread_t)0 ||
        pthread_equal(record->thread, thread) == 0 ||
        record->slot != slot ||
        record->state != LLGO_CORO_FLEET_OWNER_RETURNED_V1 ||
        record->acknowledged != 1 || record->published != 1 ||
        record->joining != 0 ||
        llgo_coro_fleet_find_record_for_slot_locked_v1(
            slot, &slot_record) != 0 ||
        slot_record != record) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    llgo_coro_fleet_clear_slot_locked_v1(record);
    record->slot = 0;
    record->acknowledged = 0;
    record->published = 0;
    if (factory->standby_count <
            LLGO_CORO_FLEET_OWNER_STANDBY_CAPACITY_V1 &&
        factory->state != LLGO_CORO_FLEET_FACTORY_STOPPING_V1 &&
        factory->state != LLGO_CORO_FLEET_FACTORY_FAILED_V1) {
        record->state = LLGO_CORO_FLEET_OWNER_PARKING_V1;
        if (pthread_cond_broadcast(&factory->changed) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
        while (record->state == LLGO_CORO_FLEET_OWNER_PARKING_V1) {
            if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
                factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
                (void)pthread_mutex_unlock(&factory->mutex);
                return -1;
            }
        }
        if (record->state != LLGO_CORO_FLEET_OWNER_STANDBY_V1) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
        record->standby_next = factory->standby_head;
        factory->standby_head = record;
        factory->standby_count++;
        if (pthread_mutex_unlock(&factory->mutex) != 0) {
            return -1;
        }
        return 0;
    }
    record->state = LLGO_CORO_FLEET_OWNER_STOPPING_V1;
    if (pthread_cond_broadcast(&factory->changed) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    return __llgo_coro_fleet_owner_join_v1(thread, token) == 0 ? 1 : -1;
}

int __llgo_coro_fleet_owner_retire_self_v1(uint32_t slot) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (slot == 0 || pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    struct llgo_coro_fleet_owner_record_v1 *record = 0;
    if (llgo_coro_fleet_find_record_for_slot_locked_v1(
            slot, &record) != 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    if (record == 0 || record->thread == (pthread_t)0 ||
        pthread_equal(record->thread, pthread_self()) == 0 ||
        record->state != LLGO_CORO_FLEET_OWNER_RUNNING_V1 ||
        record->acknowledged != 1 || record->published != 1 ||
        record->joining != 0 ||
        llgo_coro_fleet_thread_detach_self_v1() != 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    llgo_coro_fleet_clear_slot_locked_v1(record);
    record->slot = 0;
    record->acknowledged = 0;
    record->published = 0;
    record->state = LLGO_CORO_FLEET_OWNER_RETIRING_V1;
    if (pthread_cond_broadcast(&factory->changed) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    return 0;
}

int __llgo_coro_fleet_owner_stop_standby_v1(uint32_t *joined) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    pthread_t threads[LLGO_CORO_FLEET_OWNER_STANDBY_CAPACITY_V1];
    uint32_t tokens[LLGO_CORO_FLEET_OWNER_STANDBY_CAPACITY_V1];
    if (joined == 0 || pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    *joined = 0;
    if (factory->state != LLGO_CORO_FLEET_FACTORY_IDLE_V1 ||
        factory->standby_count >
            LLGO_CORO_FLEET_OWNER_STANDBY_CAPACITY_V1) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    uint32_t count = factory->standby_count;
    struct llgo_coro_fleet_owner_record_v1 *record =
        factory->standby_head;
    factory->standby_head = 0;
    factory->standby_count = 0;
    for (uint32_t offset = 0; offset < count; offset++) {
        if (record == 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
        if (record->state != LLGO_CORO_FLEET_OWNER_STANDBY_V1 ||
            record->thread == (pthread_t)0 || record->token == 0 ||
            record->slot != 0 || record->joining != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
        threads[offset] = record->thread;
        tokens[offset] = record->token;
        struct llgo_coro_fleet_owner_record_v1 *next =
            record->standby_next;
        record->standby_next = 0;
        record->state = LLGO_CORO_FLEET_OWNER_STOPPING_V1;
        record = next;
    }
    if (record != 0 || pthread_cond_broadcast(&factory->changed) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) {
        return -1;
    }
    for (uint32_t offset = 0; offset < count; offset++) {
        if (__llgo_coro_fleet_owner_join_v1(
                threads[offset], tokens[offset]) != 0) {
            return -1;
        }
        (*joined)++;
    }
    return 0;
}

int __llgo_coro_fleet_owner_yield_v1(void) {
    return sched_yield();
}

int __llgo_coro_fleet_factory_stop_v2(uint32_t terminal_owner_token) {
    struct llgo_coro_fleet_factory_v1 *factory =
        &llgo_coro_fleet_factory_v1;
    if (pthread_mutex_lock(&factory->mutex) != 0) {
        return -1;
    }
    if (factory->state != LLGO_CORO_FLEET_FACTORY_IDLE_V1 ||
        factory->slot != 0 || factory->result != 0 ||
        factory->result_thread != (pthread_t)0 ||
        factory->result_token != 0 || factory->factory == (pthread_t)0 ||
        factory->standby_head != 0 || factory->standby_count != 0) {
        (void)pthread_mutex_unlock(&factory->mutex);
        return -1;
    }
    struct llgo_coro_fleet_owner_record_v1 *terminal = 0;
    uint32_t retained_records = 0;
    if (terminal_owner_token != 0) {
        if (llgo_coro_fleet_find_record_for_token_locked_v1(
                terminal_owner_token, &terminal) != 0 ||
            terminal == 0 || terminal->thread == (pthread_t)0 ||
            pthread_equal(terminal->thread, pthread_self()) == 0 ||
            terminal->state != LLGO_CORO_FLEET_OWNER_RUNNING_V1 ||
            terminal->acknowledged != 1 || terminal->published != 1 ||
            terminal->joining != 0) {
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
        retained_records = 1;
    }
    while (factory->active_records > retained_records) {
        if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) {
            factory->state = LLGO_CORO_FLEET_FACTORY_FAILED_V1;
            (void)pthread_mutex_unlock(&factory->mutex);
            return -1;
        }
    }
    struct llgo_coro_fleet_owner_record_v1 *retained = 0;
    if (factory->active_records != retained_records ||
        (retained_records == 0 && factory->records != 0) ||
        (terminal != 0 &&
         (llgo_coro_fleet_find_record_for_token_locked_v1(
              terminal_owner_token, &retained) != 0 ||
          retained != terminal ||
          terminal->state != LLGO_CORO_FLEET_OWNER_RUNNING_V1))) {
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
