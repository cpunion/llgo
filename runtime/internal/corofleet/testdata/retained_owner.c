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
#include <pthread.h>
#include <sched.h>
#include <stdint.h>
#include <stdatomic.h>
#include <stdio.h>

static _Thread_local uint32_t thread_visits;
static _Atomic uint32_t seen[65];
static _Atomic uint32_t tls_visits[65];
static _Atomic uint32_t failed;
static _Atomic uint32_t gate;
static pthread_key_t retirement_key;
static _Atomic uint32_t retired_threads;

static void retired_thread(void *value) {
    if (value != 0) atomic_fetch_add(&retired_threads, 1);
}

uint32_t __llgo_coro_native_fleet_owner_v2(uint32_t slot) {
    if (slot == 0 || slot > 64 ||
        __llgo_coro_fleet_owner_ready_v1(slot) != 0) {
        atomic_store(&failed, 1);
        return 0;
    }
    ++thread_visits;
    if (slot == 3 && pthread_setspecific(retirement_key, (void *)1) != 0)
        return 0;
    atomic_store(&tls_visits[slot], thread_visits);
    atomic_fetch_add(&seen[slot], 1);
    while (slot == 1 && atomic_load(&gate) != 0) {
        sched_yield();
    }
    return 2;
}

static int retained_pair(void) {
    pthread_t a = 0, b = 0, no_owner = 0;
    uint32_t ta = 0, tb = 0, empty_token = 0, da = 0, db = 0;
    atomic_store(&gate, 1);
    if (__llgo_coro_fleet_owner_create_v3(&a, &ta, 1) != 0 ||
        __llgo_coro_fleet_owner_create_v3(&b, &tb, 2) != 0) return 10;
    /* A is still running. A wrong slot/token must reject before any wait. */
    if (__llgo_coro_fleet_owner_retain_returned_v1(a, ta, 2, &da) == 0 ||
        __llgo_coro_fleet_owner_retain_returned_v1(a, tb, 1, &da) == 0 ||
        __llgo_coro_fleet_owner_redispatch_returned_v1(a, ta, 1, 1) == 0)
        return 11;
    atomic_store(&gate, 0);
    if (__llgo_coro_fleet_owner_retain_returned_v1(a, ta, 1, &da) != 0 ||
        __llgo_coro_fleet_owner_retain_returned_v1(b, tb, 2, &db) != 0)
        return 12;
    /* Neither retained thread may be lent to the clean standby pool. */
    if (__llgo_coro_fleet_owner_try_reuse_v1(
            &no_owner, &empty_token, 3) != 1 || no_owner != 0 || empty_token != 0)
        return 13;
    uint32_t stale = da;
    for (uint32_t i = 0; i < 128; ++i) {
        if (__llgo_coro_fleet_owner_redispatch_returned_v1(a, ta, 1, da) != 0 ||
            __llgo_coro_fleet_owner_retain_returned_v1(a, ta, 1, &da) != 0 ||
            da <= stale ||
            __llgo_coro_fleet_owner_redispatch_returned_v1(a, ta, 1, stale) == 0)
            return 14;
        if (atomic_load(&seen[1]) != i + 2 ||
            atomic_load(&tls_visits[1]) != i + 2) return 15;
        /* The older thread runs twice while B remains retained. */
        if ((i & 1) != 0) {
            if (__llgo_coro_fleet_owner_redispatch_returned_v1(b, tb, 2, db) != 0 ||
                __llgo_coro_fleet_owner_retain_returned_v1(b, tb, 2, &db) != 0)
                return 16;
        }
    }
    if (atomic_load(&seen[2]) != 65 || atomic_load(&tls_visits[2]) != 65)
        return 17;
    if (__llgo_coro_fleet_owner_release_v1(a, ta, 1) != 0 ||
        __llgo_coro_fleet_owner_release_v1(b, tb, 2) != 0) return 18;
    /* Redispatch receipts also expire across a clean standby reuse of the
       same logical slot and physical token. The C entry count never resets. */
    pthread_t reused = 0;
    uint32_t token = 0, dispatch = 0;
    if (__llgo_coro_fleet_owner_try_reuse_v1(&reused, &token, 2) != 0 ||
        !pthread_equal(reused, b) || token != tb ||
        __llgo_coro_fleet_owner_retain_returned_v1(reused, token, 2, &dispatch) != 0 ||
        dispatch <= db ||
        __llgo_coro_fleet_owner_redispatch_returned_v1(reused, token, 2, db) == 0 ||
        __llgo_coro_fleet_owner_release_v1(reused, token, 2) != 0) return 19;
    return 0;
}

static void *coordinator(void *argument) {
    uint32_t slot = (uint32_t)(uintptr_t)argument;
    pthread_t owner = 0;
    uint32_t token = 0, receipt = 0;
    if (__llgo_coro_fleet_owner_create_v3(&owner, &token, slot) != 0 ||
        __llgo_coro_fleet_owner_retain_returned_v1(owner, token, slot, &receipt) != 0)
        return (void *)1;
    for (uint32_t i = 0; i < 64; ++i) {
        uint32_t old = receipt;
        if (__llgo_coro_fleet_owner_redispatch_returned_v1(owner, token, slot, receipt) != 0 ||
            __llgo_coro_fleet_owner_retain_returned_v1(owner, token, slot, &receipt) != 0 ||
            receipt != old + 1 ||
            atomic_load(&seen[slot]) != i + 2 ||
            atomic_load(&tls_visits[slot]) != i + 2)
            return (void *)2;
    }
    int released = __llgo_coro_fleet_owner_release_v1(owner, token, slot);
    return released == 0 || released == 1 ? 0 : (void *)3;
}

static int discard_tainted(void) {
    pthread_t owner = 0;
    uint32_t token = 0, dispatch = 0;
    if (__llgo_coro_fleet_owner_create_v3(&owner, &token, 3) != 0 ||
        __llgo_coro_fleet_owner_retain_returned_v1(owner, token, 3, &dispatch) != 0)
        return 20;
    if (__llgo_coro_fleet_owner_discard_returned_v1(owner, token, 4) != -1 ||
        __llgo_coro_fleet_owner_discard_returned_v1(owner, token + 1, 3) != -1)
        return 21;
    if (__llgo_coro_fleet_owner_discard_returned_v1(owner, token, 3) != 1 ||
        atomic_load(&retired_threads) != 1 ||
        __llgo_coro_fleet_owner_redispatch_returned_v1(owner, token, 3, dispatch) != -1)
        return 22;
    return 0;
}

int main(void) {
    if (pthread_key_create(&retirement_key, retired_thread) != 0 ||
        __llgo_coro_fleet_factory_start_v1() != 0) return 1;
    int pair = retained_pair();
    if (pair != 0) { fprintf(stderr, "pair failure %d\n", pair); return pair; }
    int discarded = discard_tainted();
    if (discarded != 0) return discarded;
    pthread_t coordinators[16];
    for (uint32_t i = 0; i < 16; ++i)
        if (pthread_create(&coordinators[i], 0, coordinator,
                (void *)(uintptr_t)(i + 4)) != 0) return 2;
    for (uint32_t i = 0; i < 16; ++i) {
        void *result = (void *)1;
        if (pthread_join(coordinators[i], &result) != 0 || result != 0) return 3;
    }
    uint32_t joined = 0;
    if (atomic_load(&failed) != 0 ||
        __llgo_coro_fleet_owner_stop_standby_v1(&joined) != 0 || joined != 8 ||
        __llgo_coro_fleet_factory_stop_v2(0) != 0) return 4;
    puts("PASS retained same-M TLS, non-LIFO dispatch, stale receipts, clean reuse, tainted pthread destruction, concurrent retirement");
    return 0;
}
