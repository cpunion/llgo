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

#include <errno.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

enum { LLGO_CORO_WORKER_MAX_ARGS_V1 = 6 };

struct llgo_coro_worker_result_v1 {
    uintptr_t r1;
    uintptr_t r2;
    uintptr_t error;
};

typedef uintptr_t (*llgo_coro_worker_fn0_v1)(void);
typedef uintptr_t (*llgo_coro_worker_fn1_v1)(uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn2_v1)(uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn3_v1)(uintptr_t, uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn4_v1)(uintptr_t, uintptr_t, uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn5_v1)(uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t);
typedef uintptr_t (*llgo_coro_worker_fn6_v1)(uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t, uintptr_t);

bool __llgo_coro_worker_call_v1(
    uintptr_t function,
    uint32_t argc,
    const uintptr_t args[LLGO_CORO_WORKER_MAX_ARGS_V1],
    struct llgo_coro_worker_result_v1 *result) {
    if (function == 0 || argc > LLGO_CORO_WORKER_MAX_ARGS_V1 || args == NULL || result == NULL) {
        return false;
    }

    errno = 0;
    uintptr_t r1;
    switch (argc) {
    case 0:
        r1 = ((llgo_coro_worker_fn0_v1)function)();
        break;
    case 1:
        r1 = ((llgo_coro_worker_fn1_v1)function)(args[0]);
        break;
    case 2:
        r1 = ((llgo_coro_worker_fn2_v1)function)(args[0], args[1]);
        break;
    case 3:
        r1 = ((llgo_coro_worker_fn3_v1)function)(args[0], args[1], args[2]);
        break;
    case 4:
        r1 = ((llgo_coro_worker_fn4_v1)function)(args[0], args[1], args[2], args[3]);
        break;
    case 5:
        r1 = ((llgo_coro_worker_fn5_v1)function)(args[0], args[1], args[2], args[3], args[4]);
        break;
    case 6:
        r1 = ((llgo_coro_worker_fn6_v1)function)(args[0], args[1], args[2], args[3], args[4], args[5]);
        break;
    default:
        return false;
    }

    result->r1 = r1;
    result->r2 = 0;
    result->error = r1 == UINTPTR_MAX ? (uintptr_t)errno : 0;
    return true;
}
