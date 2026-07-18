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

#include <stdint.h>

#if defined(__APPLE__) || defined(__linux__)

#include <poll.h>

/*
 * Fixed scalar ABI for the llgo.syscall worker intrinsic.
 *
 * Darwin's nfds_t is unsigned int while Linux uses unsigned long, and poll
 * returns int. Keeping those platform types behind this wrapper lets the
 * worker call one exact uintptr-only signature on both systems. timeout_word
 * carries the low 32 bits of Go's int32 C.int; the explicit unsigned then
 * signed conversion restores -1 without depending on uintptr width.
 */
uintptr_t __llgo_runtime_poll_wait_v1(
    uintptr_t fds_address,
    uintptr_t nfds_word,
    uintptr_t timeout_word) {
    struct pollfd *fds = (struct pollfd *)(uintptr_t)fds_address;
    nfds_t nfds = (nfds_t)nfds_word;
    int timeout = (int)(int32_t)(uint32_t)timeout_word;
    int result = poll(fds, nfds, timeout);
    if (result < 0) {
        return UINTPTR_MAX;
    }
    return (uintptr_t)(unsigned int)result;
}

#endif
