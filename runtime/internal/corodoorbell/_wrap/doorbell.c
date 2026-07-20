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

#if defined(__APPLE__) || defined(__linux__)

#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif

#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <stdint.h>
#include <unistd.h>

#define LLGO_DOORBELL_OPEN_RETRIES_V1 16
#define LLGO_DOORBELL_READ_MAX_V1 64u
#define LLGO_DOORBELL_WRITE_SIZE_V1 1u
#define LLGO_DOORBELL_POLL_CAPACITY_V1 1025u
#define LLGO_DOORBELL_POLL_MAX_MS_V1 1000

#if defined(__APPLE__)
typedef uint32_t llgo_doorbell_nfds_v1;
#else
typedef uintptr_t llgo_doorbell_nfds_v1;
#endif

/*
 * Every syscall result is packed with the errno captured in the same C leaf.
 * The low word is a signed 32-bit result and the high word is errno. Doorbell
 * reads/writes and poll counts are deliberately bounded to fit this shape.
 */
static uint64_t llgo_doorbell_result_v1(int32_t result, int error)
{
    return ((uint64_t)(uint32_t)error << 32) | (uint64_t)(uint32_t)result;
}

static int llgo_doorbell_set_flag_v1(int fd, int get, int set, int flag)
{
    int attempt;
    for (attempt = 0; attempt < LLGO_DOORBELL_OPEN_RETRIES_V1; attempt++) {
        int current = fcntl(fd, get, 0);
        if (current < 0) {
            if (errno == EINTR) {
                continue;
            }
            return -1;
        }
        if (fcntl(fd, set, current | flag) == 0) {
            return 0;
        }
        if (errno != EINTR) {
            return -1;
        }
    }
    errno = EINTR;
    return -1;
}

/*
 * This is the only creator for descriptors admitted to the Go Pipe object.
 * Neither fd becomes visible to Go until both O_NONBLOCK and FD_CLOEXEC have
 * been verified. All EINTR loops have a fixed upper bound.
 */
int32_t __llgo_coro_doorbell_open_v1(int32_t output[2])
{
    int fds[2] = {-1, -1};
    int attempt;

    if (output == 0) {
        return 0;
    }
    output[0] = -1;
    output[1] = -1;
    for (attempt = 0; attempt < LLGO_DOORBELL_OPEN_RETRIES_V1; attempt++) {
        if (pipe(fds) == 0) {
            break;
        }
        if (errno != EINTR) {
            return 0;
        }
    }
    if (fds[0] < 0 || fds[1] < 0 ||
        llgo_doorbell_set_flag_v1(fds[0], F_GETFL, F_SETFL, O_NONBLOCK) != 0 ||
        llgo_doorbell_set_flag_v1(fds[1], F_GETFL, F_SETFL, O_NONBLOCK) != 0 ||
        llgo_doorbell_set_flag_v1(fds[0], F_GETFD, F_SETFD, FD_CLOEXEC) != 0 ||
        llgo_doorbell_set_flag_v1(fds[1], F_GETFD, F_SETFD, FD_CLOEXEC) != 0) {
        if (fds[0] >= 0) {
            (void)close(fds[0]);
        }
        if (fds[1] >= 0) {
            (void)close(fds[1]);
        }
        return 0;
    }
    output[0] = (int32_t)fds[0];
    output[1] = (int32_t)fds[1];
    return 1;
}

uint64_t __llgo_coro_doorbell_read_v1(int32_t fd, void *buffer, uintptr_t size)
{
    ssize_t result;
    if (fd < 0 || buffer == 0 || size == 0 || size > LLGO_DOORBELL_READ_MAX_V1) {
        return llgo_doorbell_result_v1(-1, EINVAL);
    }
    result = read((int)fd, buffer, (size_t)size);
    if (result < 0) {
        return llgo_doorbell_result_v1(-1, errno);
    }
    return llgo_doorbell_result_v1((int32_t)result, 0);
}

uint64_t __llgo_coro_doorbell_write_v1(int32_t fd, const void *buffer, uintptr_t size)
{
    ssize_t result;
    if (fd < 0 || buffer == 0 || size != LLGO_DOORBELL_WRITE_SIZE_V1) {
        return llgo_doorbell_result_v1(-1, EINVAL);
    }
    result = write((int)fd, buffer, (size_t)size);
    if (result < 0) {
        return llgo_doorbell_result_v1(-1, errno);
    }
    return llgo_doorbell_result_v1((int32_t)result, 0);
}

int32_t __llgo_coro_doorbell_close_v1(int32_t fd)
{
    if (fd < 0) {
        return 0;
    }
    /* Never retry close: the descriptor may already have been reused. */
    return close((int)fd) == 0 ? 1 : 0;
}

uint64_t __llgo_coro_doorbell_poll_one_v1(
    struct pollfd *fds,
    llgo_doorbell_nfds_v1 count,
    int32_t timeout_ms)
{
    int result;
    if (fds == 0 || count != 1 || timeout_ms < 0 ||
        timeout_ms > LLGO_DOORBELL_POLL_MAX_MS_V1) {
        return llgo_doorbell_result_v1(-1, EINVAL);
    }
    result = poll(fds, (nfds_t)count, (int)timeout_ms);
    return result < 0
        ? llgo_doorbell_result_v1(-1, errno)
        : llgo_doorbell_result_v1((int32_t)result, 0);
}

uint64_t __llgo_coro_doorbell_poll_set_v1(
    struct pollfd *fds,
    llgo_doorbell_nfds_v1 count,
    int32_t timeout_ms)
{
    int result;
    if (fds == 0 || count == 0 || count > LLGO_DOORBELL_POLL_CAPACITY_V1 ||
        timeout_ms < 0 || timeout_ms > LLGO_DOORBELL_POLL_MAX_MS_V1) {
        return llgo_doorbell_result_v1(-1, EINVAL);
    }
    result = poll(fds, (nfds_t)count, (int)timeout_ms);
    return result < 0
        ? llgo_doorbell_result_v1(-1, errno)
        : llgo_doorbell_result_v1((int32_t)result, 0);
}

#endif
