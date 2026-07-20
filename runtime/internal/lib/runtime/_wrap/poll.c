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

#include <errno.h>
#include <poll.h>
#include <sys/socket.h>
#include <unistd.h>

uint32_t __llgo_runtime_poll_fd_stream_v1(int32_t fd)
{
    int saved_errno = errno;
    int socket_type = 0;
    socklen_t socket_type_size = (socklen_t)sizeof(socket_type);
    int status = getsockopt(
        (int)fd,
        SOL_SOCKET,
        SO_TYPE,
        &socket_type,
        &socket_type_size);
    uint32_t result = status == 0 &&
        socket_type_size == (socklen_t)sizeof(socket_type) &&
        socket_type == SOCK_STREAM;
    errno = saved_errno;
    return result;
}

/*
 * One bounded non-blocking I/O attempt with explicit errno transport.
 *
 * The Go owner admits these leaves only for a kernel-confirmed SOCK_STREAM.
 * Each leaf clamps its own copy length to 64 KiB, and MSG_DONTWAIT applies to
 * this one call, so the target-owned executor-safe contract remains true for
 * every size and even when shared open-file-description flags are changed
 * through dup or a raw descriptor callback. result occupies the low 32 bits
 * and errno the high 32 bits. The wrapper restores incoming C errno: no
 * executor-thread TLS state is observed after returning to Go.
 */
static uint64_t llgo_runtime_poll_pack_io_attempt_v1(ssize_t result, int error)
{
    uint32_t result_word = (uint32_t)(int32_t)result;
    uint32_t error_word = result < 0 ? (uint32_t)error : 0;
    return ((uint64_t)error_word << 32) | (uint64_t)result_word;
}

#define LLGO_RUNTIME_POLL_MAX_INLINE_ATTEMPT_V1 ((uintptr_t)64u << 10)

static size_t llgo_runtime_poll_bounded_size_v1(uintptr_t size)
{
    if (size > LLGO_RUNTIME_POLL_MAX_INLINE_ATTEMPT_V1) {
        size = LLGO_RUNTIME_POLL_MAX_INLINE_ATTEMPT_V1;
    }
    return (size_t)size;
}

uint64_t __llgo_runtime_poll_read_attempt_v1(
    int32_t fd,
    void *address,
    uintptr_t size)
{
    int saved_errno = errno;
    ssize_t result = recv(
        (int)fd,
        address,
        llgo_runtime_poll_bounded_size_v1(size),
        MSG_DONTWAIT);
    int error = result < 0 ? errno : 0;
    uint64_t packed = llgo_runtime_poll_pack_io_attempt_v1(result, error);
    errno = saved_errno;
    return packed;
}

uint64_t __llgo_runtime_poll_write_attempt_v1(
    int32_t fd,
    void *address,
    uintptr_t size)
{
    int saved_errno = errno;
    ssize_t result = send(
        (int)fd,
        address,
        llgo_runtime_poll_bounded_size_v1(size),
        MSG_DONTWAIT);
    int error = result < 0 ? errno : 0;
    uint64_t packed = llgo_runtime_poll_pack_io_attempt_v1(result, error);
    errno = saved_errno;
    return packed;
}

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
