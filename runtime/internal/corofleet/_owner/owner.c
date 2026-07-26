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

int __llgo_coro_fleet_owner_create_v2(pthread_t *thread, uint32_t slot) {
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
