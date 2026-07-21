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

#if defined(LLGO_CORO_FLEET_BDWGC)
#include <gc/gc.h>
#endif

/*
 * This is intentionally the only C-to-Go scheduler-owner edge. The routine
 * has no argument and the Go body selects its statically assigned route. A
 * zero pthread result means clean coordinated stop; a nonzero sentinel lets
 * the joining program owner reject a failed raw scheduler loop.
 */
extern uint32_t __llgo_coro_native_fleet_owner_v1(void);

static void *llgo_coro_fleet_owner_main_v1(void *unused) {
    (void)unused;
    return __llgo_coro_native_fleet_owner_v1() == 1
        ? (void *)0
        : (void *)(uintptr_t)1;
}

int __llgo_coro_fleet_owner_create_v1(pthread_t *thread) {
    if (thread == 0) {
        return -1;
    }
#if defined(LLGO_CORO_FLEET_BDWGC)
    return GC_pthread_create(thread, 0, llgo_coro_fleet_owner_main_v1, 0);
#else
    return pthread_create(thread, 0, llgo_coro_fleet_owner_main_v1, 0);
#endif
}
