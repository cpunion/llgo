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
#include <sched.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>

static _Thread_local uint32_t owner_slot;
static _Thread_local int held_dispatch;
static _Thread_local int release_observer;
static _Atomic uint32_t release_event;
static _Atomic int release_result = -2;
static pthread_t queued_owner;
static uint32_t queued_owner_token;
static _Atomic uint32_t hold_dispatch;
static _Atomic uint32_t dispatch_held;
static pthread_cond_t dispatch_gate = PTHREAD_COND_INITIALIZER;
static int observed_cond_wait(pthread_cond_t *, pthread_mutex_t *);
static int observed_mutex_lock(pthread_mutex_t *);
#define pthread_cond_wait observed_cond_wait
#define pthread_mutex_lock observed_mutex_lock
#include "owner.c"
#undef pthread_cond_wait
#undef pthread_mutex_lock

static int hold_dispatch_locked(pthread_mutex_t *mutex) {
    if (owner_slot == 0 || held_dispatch || !atomic_load(&hold_dispatch)) return 0;
    struct llgo_coro_fleet_owner_record_v1 *record = llgo_coro_fleet_factory_v1.records;
    while (record != 0 && pthread_equal(record->thread, pthread_self()) == 0)
        record = record->all_next;
    if (record != 0 && record->state == LLGO_CORO_FLEET_OWNER_STARTING_V1) {
        held_dispatch = 1;
        atomic_store(&dispatch_held, 1);
        while (atomic_load(&hold_dispatch))
            if (pthread_cond_wait(&dispatch_gate, mutex) != 0) return -1;
    }
    return 0;
}
static int observed_cond_wait(pthread_cond_t *condition, pthread_mutex_t *mutex) {
    if (release_observer && atomic_load(&hold_dispatch))
        atomic_store(&release_event, 1);
    int result = pthread_cond_wait(condition, mutex);
    return result == 0 ? hold_dispatch_locked(mutex) : result;
}
static int observed_mutex_lock(pthread_mutex_t *mutex) {
    int result = pthread_mutex_lock(mutex);
    return result == 0 ? hold_dispatch_locked(mutex) : result;
}
uint32_t __llgo_coro_native_fleet_owner_v2(uint32_t slot) {
    owner_slot = slot;
    return __llgo_coro_fleet_owner_ready_v1(slot) == 0 ? 2 : 0;
}
static void *release_queued_owner(void *unused) {
    (void)unused;
    release_observer = 1;
    int result = __llgo_coro_fleet_owner_release_v1(queued_owner, queued_owner_token, 9);
    atomic_store(&release_result, result);
    atomic_store(&release_event, 2);
    return 0;
}
int main(void) {
    pthread_t owners[8]; uint32_t tokens[8];
    if (__llgo_coro_fleet_factory_start_v1() != 0) return 1;
    for (uint32_t i = 0; i < 8; ++i) {
        if (__llgo_coro_fleet_owner_create_v3(&owners[i], &tokens[i], i + 1) != 0 ||
            __llgo_coro_fleet_owner_release_v1(owners[i], tokens[i], i + 1) != 0) return 2;
    }
    pthread_t queued = 0; uint32_t queued_token = 0;
    atomic_store(&hold_dispatch, 1);
    if (__llgo_coro_fleet_owner_request_reuse_v1(&queued, &queued_token, 9) != 0) return 3;
    while (!atomic_load(&dispatch_held)) sched_yield();
    /* Fill the cache credit released by RequestReuse while its exact M is
       still queued, before any Go entry can win cancellation. */
    pthread_t fresh = 0; uint32_t fresh_token = 0;
    if (__llgo_coro_fleet_owner_create_v3(&fresh, &fresh_token, 10) != 0 ||
        __llgo_coro_fleet_owner_release_v1(fresh, fresh_token, 10) != 0) return 4;
    int canceled = __llgo_coro_fleet_owner_cancel_reuse_v1(queued, queued_token, 9);
    printf("full-cache cancel=%d cached=%u\n", canceled, llgo_coro_fleet_factory_v1.standby_count);
    if (canceled != 1) return 11;
    /* The added wait admits only the exact published token/slot. */
    if (__llgo_coro_fleet_owner_release_v1(queued, queued_token + 1, 9) != -1 ||
        __llgo_coro_fleet_owner_release_v1(queued, queued_token, 10) != -1) return 14;
    queued_owner = queued;
    queued_owner_token = queued_token;
    pthread_t releaser;
    if (pthread_create(&releaser, 0, release_queued_owner, 0) != 0) return 12;
    while (atomic_load(&release_event) == 0) sched_yield();
    if (atomic_load(&release_event) != 1 || atomic_load(&release_result) != -2) {
        fprintf(stderr, "FAIL queued release returned before dispatch: %d\n", atomic_load(&release_result));
        return 13;
    }
    struct llgo_coro_fleet_factory_v1 *factory = &llgo_coro_fleet_factory_v1;
    if (pthread_mutex_lock(&factory->mutex) != 0) return 5;
    atomic_store(&hold_dispatch, 0);
    if (pthread_cond_broadcast(&dispatch_gate) != 0 || pthread_mutex_unlock(&factory->mutex) != 0) return 6;
    if (pthread_join(releaser, 0) != 0 || atomic_load(&release_result) != 1) return 7;
    uint32_t joined = 0;
    if (__llgo_coro_fleet_owner_stop_standby_v1(&joined) != 0 || joined != 8 ||
        __llgo_coro_fleet_factory_stop_v2(0) != 0) return 8;
    puts("PASS queued full-cache release waits for dispatch and strong join");
    return 0;
}
