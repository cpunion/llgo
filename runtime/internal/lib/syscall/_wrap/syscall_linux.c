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

#if defined(__linux__)

#define _GNU_SOURCE
#include <stdint.h>
#include <unistd.h>

/*
 * Fixed scalar adapters for Linux's syscall-number ABI. libc syscall(2)
 * converts a kernel error to -1 and records the positive errno in the current
 * invocation thread's TLS. Returning UINTPTR_MAX gives the common
 * llgo.syscall lowering one exact failure predicate. These adapters carry no
 * worker authority; a dynamic syscall number must remain on a proven plain
 * current-thread path unless the compiler supplies a separate exact trap
 * capability.
 */
uintptr_t __llgo_linux_syscall3_v1(
    uintptr_t number,
    uintptr_t a1,
    uintptr_t a2,
    uintptr_t a3) {
    long result = syscall((long)number, (long)a1, (long)a2, (long)a3);
    return result == -1L ? UINTPTR_MAX : (uintptr_t)(unsigned long)result;
}

uintptr_t __llgo_linux_syscall6_v1(
    uintptr_t number,
    uintptr_t a1,
    uintptr_t a2,
    uintptr_t a3,
    uintptr_t a4,
    uintptr_t a5,
    uintptr_t a6) {
    long result = syscall(
        (long)number,
        (long)a1,
        (long)a2,
        (long)a3,
        (long)a4,
        (long)a5,
        (long)a6);
    return result == -1L ? UINTPTR_MAX : (uintptr_t)(unsigned long)result;
}

#endif
