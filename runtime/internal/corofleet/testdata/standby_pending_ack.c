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
static _Thread_local int held;
static _Atomic uint32_t held_acks;
static _Atomic uint32_t finished_releases;
static _Atomic uint32_t release_acks;
static pthread_cond_t acknowledgement_gate = PTHREAD_COND_INITIALIZER;
static int observed_cond_wait(pthread_cond_t *, pthread_mutex_t *);
#define pthread_cond_wait observed_cond_wait
#include "owner.c"
#undef pthread_cond_wait

static int observed_cond_wait(pthread_cond_t *condition, pthread_mutex_t *mutex) {
    int result = pthread_cond_wait(condition, mutex);
    if (result != 0 || owner_slot == 0 || held) return result;
    struct llgo_coro_fleet_owner_record_v1 *record =
        llgo_coro_fleet_factory_v1.records;
    while (record != 0 && pthread_equal(record->thread, pthread_self()) == 0)
        record = record->all_next;
    if (record != 0 && record->state == LLGO_CORO_FLEET_OWNER_PARKING_V1) {
        held = 1;
        atomic_fetch_add(&held_acks, 1);
        while (!atomic_load(&release_acks)) {
            result = pthread_cond_wait(&acknowledgement_gate, mutex);
            if (result != 0) return result;
        }
    }
    return result;
}

uint32_t __llgo_coro_native_fleet_owner_v2(uint32_t slot) {
    owner_slot = slot;
    if (__llgo_coro_fleet_owner_ready_v1(slot) != 0) return 0;
    return 2;
}

struct release_request { pthread_t owner; uint32_t token; uint32_t slot; };
static void *release_owner(void *argument) {
    struct release_request *request = argument;
    int result = __llgo_coro_fleet_owner_release_v1(
        request->owner, request->token, request->slot);
    atomic_fetch_add(&finished_releases, 1);
    return result == 0 || result == 1 ? 0 : (void *)1;
}

int main(void) {
    enum { count = 16 };
    struct release_request requests[count];
    pthread_t releasers[count];
    if (__llgo_coro_fleet_factory_start_v1() != 0) return 1;
    for (uint32_t i = 0; i < count; ++i) {
        requests[i].slot = i + 1;
        if (__llgo_coro_fleet_owner_create_v3(
                &requests[i].owner, &requests[i].token, i + 1) != 0) return 2;
    }
    /* Every C-to-Go entry has returned before release begins. The test only
       holds the later PARKING acknowledgement, not a running Go task. */
    struct llgo_coro_fleet_factory_v1 *factory = &llgo_coro_fleet_factory_v1;
    if (pthread_mutex_lock(&factory->mutex) != 0) return 3;
    for (;;) {
        int all_returned = 1;
        for (uint32_t i = 0; i < count; ++i) {
            struct llgo_coro_fleet_owner_record_v1 *record = 0;
            if (llgo_coro_fleet_find_record_for_token_locked_v1(
                    requests[i].token, &record) != 0 || record == 0) return 4;
            if (record->state != LLGO_CORO_FLEET_OWNER_RETURNED_V1) all_returned = 0;
        }
        if (all_returned) break;
        if (pthread_cond_wait(&factory->changed, &factory->mutex) != 0) return 5;
    }
    if (pthread_mutex_unlock(&factory->mutex) != 0) return 6;
    for (uint32_t i = 0; i < count; ++i)
        if (pthread_create(&releasers[i], 0, release_owner, &requests[i]) != 0) return 7;
    /* No acknowledgement can finish until all16 releases have either parked
       or strongly joined. This forces the capacity decision to overlap. */
    while (atomic_load(&held_acks) + atomic_load(&finished_releases) < count)
        sched_yield();
    if (pthread_mutex_lock(&factory->mutex) != 0) return 8;
    atomic_store(&release_acks, 1);
    if (pthread_cond_broadcast(&acknowledgement_gate) != 0 ||
        pthread_mutex_unlock(&factory->mutex) != 0) return 9;
    for (uint32_t i = 0; i < count; ++i) {
        void *result = (void *)1;
        if (pthread_join(releasers[i], &result) != 0 || result != 0) return 10;
    }
    uint32_t cached = factory->standby_count;
    printf("admitted=%u cached=%u capacity=%u\n", atomic_load(&held_acks),
        cached, LLGO_CORO_FLEET_OWNER_STANDBY_CAPACITY_V1);
    if (cached > LLGO_CORO_FLEET_OWNER_STANDBY_CAPACITY_V1) return 11;
    uint32_t joined = 0;
    if (__llgo_coro_fleet_owner_stop_standby_v1(&joined) != 0 || joined != 8 ||
        __llgo_coro_fleet_factory_stop_v2(0) != 0) return 12;
    puts("PASS pending standby acknowledgements respect capacity");
    return 0;
}
