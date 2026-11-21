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

#if defined(__APPLE__)
#include <sys/event.h>
#endif

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

#if defined(__APPLE__)

enum {
    llgo_kevent_max_sockets_v1 = 2,
    llgo_kevent_bell_v1 = 1,
    llgo_kevent_watch_v1 = 2,
    llgo_kevent_delete_v1 = llgo_kevent_watch_v1 + llgo_kevent_max_sockets_v1,
};

struct llgo_doorbell_kevent_watch_v1 {
    int32_t fd;
    int32_t filter;
};

struct llgo_doorbell_kevent_set_v1 {
    int32_t descriptor;
    struct llgo_doorbell_kevent_watch_v1 watches[llgo_kevent_max_sockets_v1];
};

static int llgo_doorbell_kevent_open_v1(int bell)
{
    int fd = kqueue();
    if (fd < 0) {
        return -1;
    }
    struct kevent change;
    EV_SET(&change, bell, EVFILT_READ, EV_ADD, 0, 0,
           (void *)(uintptr_t)llgo_kevent_bell_v1);
    if (fd == INT32_MAX ||
        llgo_doorbell_set_flag_v1(fd, F_GETFD, F_SETFD, FD_CLOEXEC) != 0 ||
        kevent(fd, &change, 1, NULL, 0, NULL) != 0) {
        (void)close(fd);
        return -1;
    }
    return fd;
}

static uint64_t llgo_doorbell_kevent_reset_v1(
    struct llgo_doorbell_kevent_set_v1 *queue,
    struct pollfd *fds, uint32_t count, int error)
{
    int fd = queue->descriptor - 1;
    *queue = (struct llgo_doorbell_kevent_set_v1){
        .descriptor = error == EINTR ? 0 : -1,
    };
    if (close(fd) != 0) {
        return llgo_doorbell_result_v1(-1, errno);
    }
    if (error == EINTR) {
        return llgo_doorbell_result_v1(-1, EINTR);
    }
    /* The original wait may already have used its budget. Only sample here. */
    return __llgo_coro_doorbell_poll_set_v1(fds, count, 0);
}

static int llgo_doorbell_kevent_same_watch_v1(
    const struct llgo_doorbell_kevent_watch_v1 *first,
    const struct llgo_doorbell_kevent_watch_v1 *second)
{
    return first->fd == second->fd && first->filter == second->filter;
}

uint64_t __llgo_coro_doorbell_kevent_set_v1(
    struct llgo_doorbell_kevent_set_v1 *queue, struct pollfd *fds,
    uint32_t count, int32_t timeout_ms)
{
    if (queue == NULL || fds == NULL || count == 0 ||
        count > LLGO_DOORBELL_POLL_CAPACITY_V1 || timeout_ms < 0 ||
        timeout_ms > LLGO_DOORBELL_POLL_MAX_MS_V1) {
        return llgo_doorbell_result_v1(-1, EINVAL);
    }
    if (count < 2 || count > llgo_kevent_max_sockets_v1 + 1 ||
        queue->descriptor < 0 || fds[0].fd < 0 || fds[0].events != POLLIN) {
        return __llgo_coro_doorbell_poll_set_v1(fds, count, timeout_ms);
    }
    uint32_t sockets = count - 1;
    struct llgo_doorbell_kevent_watch_v1 current[llgo_kevent_max_sockets_v1] = {0};
    for (uint32_t i = 0; i < sockets; i++) {
        const struct pollfd *watch = &fds[i + 1];
        if (watch->fd < 0 || watch->fd == fds[0].fd ||
            (watch->events != POLLIN && watch->events != POLLOUT)) {
            return __llgo_coro_doorbell_poll_set_v1(fds, count, timeout_ms);
        }
        current[i].fd = watch->fd;
        current[i].filter = watch->events == POLLIN ? EVFILT_READ : EVFILT_WRITE;
    }
    if (sockets == 2 && llgo_doorbell_kevent_same_watch_v1(&current[0], &current[1])) {
        return __llgo_coro_doorbell_poll_set_v1(fds, count, timeout_ms);
    }
    if (queue->descriptor == 0) {
        int fd = llgo_doorbell_kevent_open_v1(fds[0].fd);
        if (fd < 0) {
            queue->descriptor = -1;
            return __llgo_coro_doorbell_poll_set_v1(fds, count, timeout_ms);
        }
        queue->descriptor = fd + 1;
    }

    struct kevent changes[2 * llgo_kevent_max_sockets_v1];
    struct kevent events[2 * llgo_kevent_max_sockets_v1 + 1];
    unsigned deletes = 0;
    int nchange = 0;
    for (unsigned i = 0; i < llgo_kevent_max_sockets_v1; i++) {
        const struct llgo_doorbell_kevent_watch_v1 *old = &queue->watches[i];
        if (old->filter != 0 &&
            !llgo_doorbell_kevent_same_watch_v1(old, &current[0]) &&
            !llgo_doorbell_kevent_same_watch_v1(old, &current[1])) {
            EV_SET(&changes[nchange], old->fd, old->filter, EV_DELETE, 0, 0,
                   (void *)(uintptr_t)(llgo_kevent_delete_v1 + i));
            deletes |= 1u << i;
            nchange++;
        }
    }
    for (uint32_t i = 0; i < sockets; i++) {
        EV_SET(&changes[nchange], current[i].fd, current[i].filter, EV_ADD | EV_ONESHOT, 0, 0,
               (void *)(uintptr_t)(llgo_kevent_watch_v1 + i));
        nchange++;
    }
    struct timespec timeout = {
        .tv_sec = timeout_ms / 1000,
        .tv_nsec = (timeout_ms % 1000) * 1000000L,
    };
    int ready = kevent(queue->descriptor - 1, changes, nchange,
                       events, (int)(sizeof(events) / sizeof(events[0])), &timeout);
    if (ready < 0) {
        return llgo_doorbell_kevent_reset_v1(queue, fds, count, errno);
    }

    for (uint32_t i = 0; i < count; i++) {
        fds[i].revents = 0;
    }
    int completed = 0;
    for (int i = 0; i < ready; i++) {
        const struct kevent *event = &events[i];
        uintptr_t identity = (uintptr_t)event->udata;
        if (event->flags & EV_ERROR) {
            /* An old fd may already have closed. Darwin can return only
             * that delete receipt while retaining the new ready watch. */
            if (identity >= llgo_kevent_delete_v1 &&
                identity < llgo_kevent_delete_v1 + llgo_kevent_max_sockets_v1 &&
                (event->data == ENOENT || event->data == EBADF)) {
                unsigned index = (unsigned)(identity - llgo_kevent_delete_v1);
                const struct llgo_doorbell_kevent_watch_v1 *old = &queue->watches[index];
                if ((deletes & (1u << index)) && event->ident == (uintptr_t)old->fd &&
                    event->filter == old->filter) {
                    continue;
                }
            }
            return llgo_doorbell_kevent_reset_v1(queue, fds, count, (int)event->data);
        }
        if (identity == llgo_kevent_bell_v1 && event->ident == (uintptr_t)fds[0].fd &&
            event->filter == EVFILT_READ && fds[0].revents == 0) {
            fds[0].revents = POLLIN;
            completed++;
            continue;
        }
        if (identity >= llgo_kevent_watch_v1 && identity < llgo_kevent_watch_v1 + sockets) {
            unsigned index = (unsigned)(identity - llgo_kevent_watch_v1);
            if (event->ident == (uintptr_t)current[index].fd && event->filter == current[index].filter) {
                fds[index + 1].revents = fds[index + 1].events;
                current[index].filter = 0;
                completed++;
                continue;
            }
        }
        return llgo_doorbell_kevent_reset_v1(queue, fds, count, EINVAL);
    }
    for (unsigned i = 0; i < llgo_kevent_max_sockets_v1; i++) {
        queue->watches[i] = current[i];
    }
    return llgo_doorbell_result_v1(completed, 0);
}
#endif

#endif
