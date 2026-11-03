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

#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#if defined(__APPLE__) || defined(__linux__)
#include <sys/mman.h>

/* The Go caller supplies the exact, compile-time certified POD-array size.
   Bound invalid requests before entering the OS. No page is cleared or read:
   anonymous mappings provide zero bytes on demand. Published storage lives
   for the program lifetime; only an unpublished CAS loser can be unmapped. */
void *__llgo_coro_keyed_slots_map_v2(uintptr_t bytes) {
    if (bytes == 0 || bytes > 128 * 1024 || (size_t)bytes != bytes) {
        return NULL;
    }
    void *slots = mmap(NULL, (size_t)bytes, PROT_READ | PROT_WRITE,
        MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    return slots == MAP_FAILED ? NULL : slots;
}

bool __llgo_coro_keyed_slots_unmap_v2(void *slots, uintptr_t bytes) {
    return slots != NULL && bytes != 0 && bytes <= 128 * 1024 &&
        (size_t)bytes == bytes && munmap(slots, (size_t)bytes) == 0;
}
#endif
