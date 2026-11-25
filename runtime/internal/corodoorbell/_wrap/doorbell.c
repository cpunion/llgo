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

#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif
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
#include <time.h>
#include <unistd.h>
#if defined(__APPLE__)
#include <stdatomic.h>
#include <sys/event.h>
#include <sys/time.h>
#endif

#define LLGO_DOORBELL_OPEN_RETRIES_V1 16
#define LLGO_DOORBELL_READ_MAX_V1 64u
#define LLGO_DOORBELL_WRITE_SIZE_V1 1u
#define LLGO_DOORBELL_POLL_CAPACITY_V1 1025u
#define LLGO_DOORBELL_POLL_MAX_MS_V1 1000
#define LLGO_DOORBELL_POLL_MAX_NS_V1 ((int64_t)LLGO_DOORBELL_POLL_MAX_MS_V1 * 1000000LL)

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

#if defined(__APPLE__)
#define LLGO_DOORBELL_KQ_SLOTS_V2 8u
#define LLGO_DOORBELL_KQ_WATCH_CAP_V2 64u

struct llgo_doorbell_kq_watch_v2 {
    int32_t fd;
    int16_t filter;
};

struct llgo_doorbell_kq_slot_v2 {
    _Alignas(64) _Atomic int32_t doorbell_fd;
    _Atomic int32_t kq_fd;
    uint32_t close_epoch;
    uint32_t watch_count;
    struct llgo_doorbell_kq_watch_v2 watches[LLGO_DOORBELL_KQ_WATCH_CAP_V2];
};

static _Atomic uint32_t llgo_doorbell_close_epoch_v2 = 1u;
static _Atomic(uint32_t *) llgo_doorbell_ext_close_epoch_v2 = 0;
static _Atomic(uint32_t *) llgo_doorbell_ext_owned_fds_v2 = 0;
static struct llgo_doorbell_kq_slot_v2 llgo_doorbell_kq_slots_v2[LLGO_DOORBELL_KQ_SLOTS_V2] = {
    {-1, -1, 0, 0, {{0, 0}}}, {-1, -1, 0, 0, {{0, 0}}},
    {-1, -1, 0, 0, {{0, 0}}}, {-1, -1, 0, 0, {{0, 0}}},
    {-1, -1, 0, 0, {{0, 0}}}, {-1, -1, 0, 0, {{0, 0}}},
    {-1, -1, 0, 0, {{0, 0}}}, {-1, -1, 0, 0, {{0, 0}}},
};

static void llgo_doorbell_kq_publish_owned_v2(
    struct llgo_doorbell_kq_slot_v2 *slot,
    int32_t kq)
{
    uint32_t *ext_owned = atomic_load_explicit(&llgo_doorbell_ext_owned_fds_v2, memory_order_acquire);
    if (ext_owned != 0) {
        uint32_t idx = (uint32_t)(slot - llgo_doorbell_kq_slots_v2);
        if (idx < LLGO_DOORBELL_KQ_SLOTS_V2) {
            uint32_t encoded = kq >= 0 ? (uint32_t)kq + 1u : 0u;
            atomic_store_explicit((_Atomic uint32_t *)&ext_owned[idx], encoded, memory_order_release);
        }
    }
}

void __llgo_coro_doorbell_poll_bind_state_v1(uint32_t *close_epoch, uint32_t *owned_fds)
{
    uint32_t i;
    if (close_epoch != 0) {
        atomic_store_explicit(&llgo_doorbell_ext_close_epoch_v2, close_epoch, memory_order_release);
    }
    if (owned_fds != 0) {
        atomic_store_explicit(&llgo_doorbell_ext_owned_fds_v2, owned_fds, memory_order_release);
        for (i = 0; i < LLGO_DOORBELL_KQ_SLOTS_V2; i++) {
            int32_t kq = atomic_load_explicit(&llgo_doorbell_kq_slots_v2[i].kq_fd, memory_order_acquire);
            llgo_doorbell_kq_publish_owned_v2(&llgo_doorbell_kq_slots_v2[i], kq);
        }
    }
}

void __llgo_coro_doorbell_poll_close_epoch_bump_v1(void)
{
    atomic_fetch_add_explicit(&llgo_doorbell_close_epoch_v2, 1u, memory_order_release);
}

int32_t __llgo_coro_doorbell_owns_poll_fd_v1(int32_t fd)
{
    uint32_t i;
    if (fd < 0) {
        return 0;
    }
    for (i = 0; i < LLGO_DOORBELL_KQ_SLOTS_V2; i++) {
        if (atomic_load_explicit(&llgo_doorbell_kq_slots_v2[i].kq_fd, memory_order_acquire) == fd) {
            return 1;
        }
    }
    return 0;
}

static void llgo_doorbell_kq_reset_slot_v2(struct llgo_doorbell_kq_slot_v2 *slot)
{
    int32_t kq = atomic_exchange_explicit(&slot->kq_fd, -1, memory_order_acq_rel);
    llgo_doorbell_kq_publish_owned_v2(slot, -1);
    slot->watch_count = 0;
    slot->close_epoch = 0;
    if (kq >= 0) {
        (void)close((int)kq);
    }
}

static void llgo_doorbell_kq_release_doorbell_v2(int32_t fd)
{
    uint32_t i;
    atomic_fetch_add_explicit(&llgo_doorbell_close_epoch_v2, 1u, memory_order_release);
    for (i = 0; i < LLGO_DOORBELL_KQ_SLOTS_V2; i++) {
        int32_t expected = fd;
        if (atomic_compare_exchange_strong_explicit(
                &llgo_doorbell_kq_slots_v2[i].doorbell_fd,
                &expected,
                -2,
                memory_order_acq_rel,
                memory_order_acquire)) {
            llgo_doorbell_kq_reset_slot_v2(&llgo_doorbell_kq_slots_v2[i]);
            atomic_store_explicit(&llgo_doorbell_kq_slots_v2[i].doorbell_fd, -1, memory_order_release);
            return;
        }
    }
}

static int llgo_doorbell_kq_contains_v2(
    const struct llgo_doorbell_kq_watch_v2 *list,
    uint32_t count,
    int32_t fd,
    int16_t filter)
{
    uint32_t i;
    for (i = 0; i < count; i++) {
        if (list[i].fd == fd && list[i].filter == filter) {
            return 1;
        }
    }
    return 0;
}

static struct llgo_doorbell_kq_slot_v2 *llgo_doorbell_kq_find_or_claim_v2(int32_t doorbell_fd)
{
    uint32_t i;
    if (atomic_load_explicit(&llgo_doorbell_kq_slots_v2[0].doorbell_fd, memory_order_acquire) == doorbell_fd) {
        return &llgo_doorbell_kq_slots_v2[0];
    }
    for (i = 1; i < LLGO_DOORBELL_KQ_SLOTS_V2; i++) {
        if (atomic_load_explicit(&llgo_doorbell_kq_slots_v2[i].doorbell_fd, memory_order_acquire) == doorbell_fd) {
            return &llgo_doorbell_kq_slots_v2[i];
        }
    }
    for (i = 0; i < LLGO_DOORBELL_KQ_SLOTS_V2; i++) {
        int32_t expected = -1;
        if (atomic_compare_exchange_strong_explicit(
                &llgo_doorbell_kq_slots_v2[i].doorbell_fd,
                &expected,
                doorbell_fd,
                memory_order_acq_rel,
                memory_order_acquire)) {
            return &llgo_doorbell_kq_slots_v2[i];
        }
    }
    return 0;
}

#define LLGO_DOORBELL_KQ_FAST_CAP_V2 16u

/*
 * Persistent level-triggered kqueue readiness wait on Darwin (general path).
 * Kept out-of-line so the steady-state fast path avoids an 8 KiB stack frame.
 */
static __attribute__((noinline)) int llgo_doorbell_kevent_poll_set_slow_v2(
    struct pollfd *fds,
    uint32_t count,
    int64_t timeout_ns,
    uint64_t *result_out)
{
    struct llgo_doorbell_kq_watch_v2 desired[LLGO_DOORBELL_KQ_WATCH_CAP_V2];
    struct kevent changes[LLGO_DOORBELL_KQ_WATCH_CAP_V2 * 2u];
    struct kevent events[LLGO_DOORBELL_KQ_WATCH_CAP_V2 * 2u];
    struct llgo_doorbell_kq_slot_v2 *slot;
    struct timespec ts;
    uint32_t desired_count = 0;
    uint32_t i;
    uint32_t epoch;
    int32_t kq;
    int nchanges = 0;
    int pass;

    if (count == 0 || count > LLGO_DOORBELL_KQ_WATCH_CAP_V2 ||
        fds[0].fd < 0 || fds[0].events != POLLIN) {
        return 0;
    }
    for (i = 0; i < count; i++) {
        int16_t filter;
        if (fds[i].fd < 0) {
            return 0;
        }
        if (fds[i].events == POLLIN) {
            filter = EVFILT_READ;
        } else if (fds[i].events == POLLOUT) {
            filter = EVFILT_WRITE;
        } else {
            return 0;
        }
        fds[i].revents = 0;
        if (!llgo_doorbell_kq_contains_v2(desired, desired_count, fds[i].fd, filter)) {
            desired[desired_count].fd = fds[i].fd;
            desired[desired_count].filter = filter;
            desired_count++;
        }
    }

    slot = llgo_doorbell_kq_find_or_claim_v2(fds[0].fd);
    if (slot == 0) {
        return 0;
    }

    epoch = atomic_load_explicit(&llgo_doorbell_close_epoch_v2, memory_order_acquire);
    {
        uint32_t *ext_epoch = atomic_load_explicit(&llgo_doorbell_ext_close_epoch_v2, memory_order_acquire);
        if (ext_epoch != 0) {
            epoch += atomic_load_explicit((_Atomic uint32_t *)ext_epoch, memory_order_acquire);
        }
    }
    kq = atomic_load_explicit(&slot->kq_fd, memory_order_relaxed);
    if (kq >= 0 && slot->close_epoch != epoch) {
        llgo_doorbell_kq_reset_slot_v2(slot);
        kq = -1;
    }
    if (kq < 0) {
        int opened = kqueue();
        if (opened < 0 || llgo_doorbell_set_flag_v1(opened, F_GETFD, F_SETFD, FD_CLOEXEC) != 0) {
            if (opened >= 0) {
                (void)close(opened);
            }
            return 0;
        }
        kq = (int32_t)opened;
        atomic_store_explicit(&slot->kq_fd, kq, memory_order_release);
        llgo_doorbell_kq_publish_owned_v2(slot, kq);
        slot->watch_count = 0;
        slot->close_epoch = epoch;
    }

    for (i = 0; i < slot->watch_count; i++) {
        if (!llgo_doorbell_kq_contains_v2(desired, desired_count, slot->watches[i].fd, slot->watches[i].filter)) {
            EV_SET(
                &changes[nchanges++],
                (uintptr_t)slot->watches[i].fd,
                slot->watches[i].filter,
                EV_DELETE,
                0,
                0,
                0);
        }
    }
    for (i = 0; i < desired_count; i++) {
        if (!llgo_doorbell_kq_contains_v2(slot->watches, slot->watch_count, desired[i].fd, desired[i].filter)) {
            EV_SET(
                &changes[nchanges++],
                (uintptr_t)desired[i].fd,
                desired[i].filter,
                EV_ADD | EV_ENABLE,
                0,
                0,
                0);
        }
    }

    ts.tv_sec = (time_t)(timeout_ns / 1000000000LL);
    ts.tv_nsec = (long)(timeout_ns % 1000000000LL);

    for (pass = 0; pass < 2; pass++) {
        int nev;
        int k;
        int saw_delete_noise = 0;
        int32_t ready_entries = 0;
        uint32_t new_watch_count = 0;

        nev = kevent(
            (int)kq,
            nchanges > 0 ? changes : 0,
            nchanges,
            events,
            (int)(LLGO_DOORBELL_KQ_WATCH_CAP_V2 * 2u),
            &ts);
        if (nev < 0) {
            int err = errno;
            if (err == EBADF) {
                llgo_doorbell_kq_reset_slot_v2(slot);
                return 0;
            }
            if (nchanges > 0) {
                /* Do not retain an uncertain filter delta after an interrupted change pass. */
                llgo_doorbell_kq_reset_slot_v2(slot);
            }
            *result_out = llgo_doorbell_result_v1(-1, err);
            return 1;
        }

        if (nchanges > 0) {
            for (i = 0; i < desired_count; i++) {
                slot->watches[i] = desired[i];
            }
            slot->watch_count = desired_count;
            nchanges = 0;
        }

        for (k = 0; k < nev; k++) {
            struct kevent *ev = &events[k];
            int32_t ev_fd = (int32_t)ev->ident;
            short revent = 0;
            int matched_desired = 0;

            if ((ev->flags & EV_ERROR) != 0) {
                int err = (int)ev->data;
                if (err == 0) {
                    continue;
                }
                matched_desired = llgo_doorbell_kq_contains_v2(desired, desired_count, ev_fd, ev->filter);
                if (!matched_desired && (err == ENOENT || err == EBADF)) {
                    /* Stale EV_DELETE for a descriptor already closed by the kernel. */
                    saw_delete_noise = 1;
                    continue;
                }
                if (matched_desired && err == EBADF) {
                    revent = POLLNVAL;
                    /* Drop the bad descriptor from our persistent watch cache. */
                    new_watch_count = 0;
                    for (i = 0; i < slot->watch_count; i++) {
                        if (!(slot->watches[i].fd == ev_fd && slot->watches[i].filter == ev->filter)) {
                            slot->watches[new_watch_count++] = slot->watches[i];
                        }
                    }
                    slot->watch_count = new_watch_count;
                } else {
                    /* Non-kqueueable descriptor (e.g., char device): reset and fall back to poll(2). */
                    llgo_doorbell_kq_reset_slot_v2(slot);
                    for (i = 0; i < count; i++) {
                        fds[i].revents = 0;
                    }
                    return 0;
                }
            } else if (ev->filter == EVFILT_READ) {
                revent = POLLIN;
                if ((ev->flags & EV_EOF) != 0) {
                    revent |= POLLHUP;
                }
                if (ev->fflags != 0) {
                    revent |= POLLERR;
                }
            } else if (ev->filter == EVFILT_WRITE) {
                revent = POLLOUT;
                if ((ev->flags & EV_EOF) != 0) {
                    revent |= POLLHUP;
                }
                if (ev->fflags != 0) {
                    revent |= POLLERR;
                }
            } else {
                continue;
            }

            for (i = 0; i < count; i++) {
                if (fds[i].fd != ev_fd) {
                    continue;
                }
                if ((ev->flags & EV_ERROR) != 0 ||
                    (ev->filter == EVFILT_READ && (fds[i].events & POLLIN) != 0) ||
                    (ev->filter == EVFILT_WRITE && (fds[i].events & POLLOUT) != 0)) {
                    fds[i].revents |= revent;
                }
            }
        }

        for (i = 0; i < count; i++) {
            if (fds[i].revents != 0) {
                ready_entries++;
            }
        }
        if (ready_entries > 0 || !saw_delete_noise || pass == 1) {
            *result_out = llgo_doorbell_result_v1(ready_entries, 0);
            return 1;
        }
    }
    return 0;
}

/*
 * Fast steady-state kqueue wait: when the watch set is small (<= 16) and
 * matches the slot's cached watch list in order with an unchanged close epoch,
 * skip changelist diffing and the 8 KiB slow-path stack arrays.
 */
static int llgo_doorbell_kevent_poll_set_v2(
    struct pollfd *fds,
    uint32_t count,
    int64_t timeout_ns,
    uint64_t *result_out)
{
    if (count > 0 && count <= LLGO_DOORBELL_KQ_FAST_CAP_V2 &&
        fds[0].fd >= 0 && fds[0].events == POLLIN) {
        struct llgo_doorbell_kq_slot_v2 *slot = llgo_doorbell_kq_find_or_claim_v2(fds[0].fd);
        if (slot != 0) {
            uint32_t epoch = atomic_load_explicit(&llgo_doorbell_close_epoch_v2, memory_order_acquire);
            uint32_t *ext_epoch = atomic_load_explicit(&llgo_doorbell_ext_close_epoch_v2, memory_order_acquire);
            if (ext_epoch != 0) {
                epoch += atomic_load_explicit((_Atomic uint32_t *)ext_epoch, memory_order_acquire);
            }
            int32_t kq = atomic_load_explicit(&slot->kq_fd, memory_order_relaxed);
            if (kq >= 0 && slot->close_epoch == epoch &&
                ((slot->watch_count == count) ||
                 (timeout_ns == 0 && slot->watch_count >= count &&
                  slot->watch_count <= LLGO_DOORBELL_KQ_FAST_CAP_V2))) {
                uint32_t i;
                int matched = 1;
                for (i = 0; i < count; i++) {
                    int16_t filter;
                    if (fds[i].events == POLLIN) {
                        filter = EVFILT_READ;
                    } else if (fds[i].events == POLLOUT) {
                        filter = EVFILT_WRITE;
                    } else {
                        matched = 0;
                        break;
                    }
                    if (!(i < slot->watch_count &&
                          slot->watches[i].fd == fds[i].fd &&
                          slot->watches[i].filter == filter) &&
                        !llgo_doorbell_kq_contains_v2(
                            slot->watches, slot->watch_count, fds[i].fd, filter)) {
                        matched = 0;
                        break;
                    }
                    fds[i].revents = 0;
                }
                if (matched && slot->watch_count == count) {
                    if (count == 2) {
                        if (fds[0].fd == fds[1].fd && fds[0].events == fds[1].events) {
                            matched = 0;
                        }
                    } else if (count > 2) {
                        /* Verify no duplicate fds[i] masked an extra cached watch. */
                        for (i = 0; i < count; i++) {
                            uint32_t j;
                            for (j = i + 1; j < count; j++) {
                                if (fds[i].fd == fds[j].fd && fds[i].events == fds[j].events) {
                                    matched = 0;
                                    break;
                                }
                            }
                            if (!matched) {
                                break;
                            }
                        }
                    }
                }
                if (matched) {
                    struct kevent events[LLGO_DOORBELL_KQ_FAST_CAP_V2];
                    struct timespec ts;
                    int nev;
                    int k;
                    int32_t ready_entries = 0;
                    if (timeout_ns < 1000000000LL) {
                        ts.tv_sec = 0;
                        ts.tv_nsec = (long)timeout_ns;
                    } else {
                        ts.tv_sec = (time_t)(timeout_ns / 1000000000LL);
                        ts.tv_nsec = (long)(timeout_ns % 1000000000LL);
                    }
                    nev = kevent((int)kq, 0, 0, events, (int)LLGO_DOORBELL_KQ_FAST_CAP_V2, &ts);
                    if (nev == 0) {
                        *result_out = llgo_doorbell_result_v1(0, 0);
                        return 1;
                    }
                    if (nev > 0) {
                        int has_error = 0;
                        for (k = 0; k < nev; k++) {
                            struct kevent *ev = &events[k];
                            if ((ev->flags & EV_ERROR) != 0) {
                                has_error = 1;
                                break;
                            }
                            int32_t ev_fd = (int32_t)ev->ident;
                            short revent = 0;
                            if (ev->filter == EVFILT_READ) {
                                revent = POLLIN;
                            } else if (ev->filter == EVFILT_WRITE) {
                                revent = POLLOUT;
                            } else {
                                continue;
                            }
                            if ((ev->flags & EV_EOF) != 0) {
                                revent |= POLLHUP;
                            }
                            if (ev->fflags != 0) {
                                revent |= POLLERR;
                            }
                            for (i = 0; i < count; i++) {
                                if (fds[i].fd == ev_fd &&
                                    ((ev->filter == EVFILT_READ && fds[i].events == POLLIN) ||
                                     (ev->filter == EVFILT_WRITE && fds[i].events == POLLOUT))) {
                                    if (fds[i].revents == 0) {
                                        ready_entries++;
                                    }
                                    fds[i].revents |= revent;
                                }
                            }
                        }
                        if (!has_error) {
                            *result_out = llgo_doorbell_result_v1(ready_entries, 0);
                            return 1;
                        }
                        for (i = 0; i < count; i++) {
                            fds[i].revents = 0;
                        }
                    } else {
                        int err = errno;
                        if (err != EBADF) {
                            *result_out = llgo_doorbell_result_v1(-1, err);
                            return 1;
                        }
                    }
                }
            }
        }
    }
    return llgo_doorbell_kevent_poll_set_slow_v2(fds, count, timeout_ns, result_out);
}
#endif

int32_t __llgo_coro_doorbell_close_v1(int32_t fd)
{
    if (fd < 0) {
        return 0;
    }
#if defined(__APPLE__)
    llgo_doorbell_kq_release_doorbell_v2(fd);
#endif
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

uint64_t __llgo_coro_doorbell_poll_set_ns_v1(
    struct pollfd *fds,
    llgo_doorbell_nfds_v1 count,
    int64_t timeout_ns)
{
    int result;
    if (fds == 0 || count == 0 || count > LLGO_DOORBELL_POLL_CAPACITY_V1 ||
        timeout_ns < 0 || timeout_ns > LLGO_DOORBELL_POLL_MAX_NS_V1) {
        return llgo_doorbell_result_v1(-1, EINVAL);
    }
#if defined(__APPLE__)
    {
        uint64_t kq_result = 0;
        if (llgo_doorbell_kevent_poll_set_v2(fds, (uint32_t)count, timeout_ns, &kq_result)) {
            return kq_result;
        }
    }
    {
        int32_t timeout_ms = (int32_t)(timeout_ns / 1000000LL);
        if (timeout_ns % 1000000LL != 0) {
            timeout_ms++;
        }
        if (timeout_ms > LLGO_DOORBELL_POLL_MAX_MS_V1) {
            timeout_ms = LLGO_DOORBELL_POLL_MAX_MS_V1;
        }
        result = poll(fds, (nfds_t)count, (int)timeout_ms);
    }
#else
    {
        struct timespec ts;
        ts.tv_sec = (time_t)(timeout_ns / 1000000000LL);
        ts.tv_nsec = (long)(timeout_ns % 1000000000LL);
        result = ppoll(fds, (nfds_t)count, &ts, 0);
    }
#endif
    return result < 0
        ? llgo_doorbell_result_v1(-1, errno)
        : llgo_doorbell_result_v1((int32_t)result, 0);
}

uint64_t __llgo_coro_doorbell_poll_set_v1(
    struct pollfd *fds,
    llgo_doorbell_nfds_v1 count,
    int32_t timeout_ms)
{
    if (timeout_ms < 0 || timeout_ms > LLGO_DOORBELL_POLL_MAX_MS_V1) {
        return llgo_doorbell_result_v1(-1, EINVAL);
    }
    return __llgo_coro_doorbell_poll_set_ns_v1(fds, count, (int64_t)timeout_ms * 1000000LL);
}

#endif
