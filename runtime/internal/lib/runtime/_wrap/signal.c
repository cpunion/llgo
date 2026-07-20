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
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif

#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <stdatomic.h>
#include <stdint.h>
#include <unistd.h>

/*
 * The handler is deliberately a C leaf. It performs only lock-free atomic
 * operations and one async-signal-safe, non-blocking write of a POD signum.
 * It never calls Go, allocates, locks, or depends on libuv/pthread.
 */
#if ATOMIC_INT_LOCK_FREE != 2
#error "llgo coroutine signals require lock-free 32-bit atomics"
#endif

#define LLGO_SIGNAL_CAPACITY_V1 128u
#define LLGO_SIGNAL_NONE_V1 UINT32_MAX

enum llgo_signal_mode_v1 {
    LLGO_SIGNAL_DISABLED_V1 = 0,
    LLGO_SIGNAL_ENABLED_V1 = 1,
    LLGO_SIGNAL_IGNORED_V1 = 2
};

struct llgo_signal_slot_v1 {
    _Atomic uint32_t pending;
    _Atomic uint32_t mode;
    struct sigaction previous;
    uint32_t previous_valid;
};

static struct llgo_signal_slot_v1 llgo_signal_slots_v1[LLGO_SIGNAL_CAPACITY_V1];
static _Atomic uint32_t llgo_signal_published_v1;
static _Atomic uint32_t llgo_signal_acknowledged_v1;
static _Atomic uint32_t llgo_signal_idle_generation_v1;
static _Atomic uint32_t llgo_signal_delivering_v1;
static int llgo_signal_read_fd_v1 = -1;
static int llgo_signal_write_fd_v1 = -1;

static int llgo_signal_valid_v1(uint32_t sig)
{
    return sig > 0 && sig < LLGO_SIGNAL_CAPACITY_V1;
}

static void llgo_signal_handler_v1(int signum)
{
    uint32_t sig = (uint32_t)signum;
    uint32_t expected = 0;
    int saved_errno = errno;

    atomic_fetch_add_explicit(&llgo_signal_delivering_v1, 1, memory_order_relaxed);
    if (llgo_signal_valid_v1(sig) &&
        atomic_load_explicit(&llgo_signal_slots_v1[sig].mode, memory_order_acquire) ==
            LLGO_SIGNAL_ENABLED_V1 &&
        atomic_compare_exchange_strong_explicit(
            &llgo_signal_slots_v1[sig].pending,
            &expected,
            1,
            memory_order_acq_rel,
            memory_order_acquire)) {
        atomic_fetch_add_explicit(&llgo_signal_published_v1, 1, memory_order_release);

        /*
         * One pending bit per signum matches Go's coalescing semantics. If the
         * pipe is full (or a nested handler interrupts write), pending remains
         * published. A full pipe is already readable, and receive_v1 scans all
         * pending slots after draining tokens, so overflow cannot lose it.
         */
        while (write(llgo_signal_write_fd_v1, &sig, sizeof(sig)) < 0 && errno == EINTR) {
        }
    }
    atomic_fetch_sub_explicit(&llgo_signal_delivering_v1, 1, memory_order_release);
    errno = saved_errno;
}

static int llgo_signal_set_fd_flags_v1(int fd, int command, int flag)
{
    int value = fcntl(fd, command, 0);
    if (value < 0 || fcntl(fd, command == F_GETFL ? F_SETFL : F_SETFD, value | flag) < 0) {
        return -1;
    }
    return 0;
}

int32_t __llgo_runtime_signal_init_v1(void)
{
    int fds[2];
    int saved_errno = errno;

    if (llgo_signal_read_fd_v1 >= 0) {
        return (int32_t)llgo_signal_read_fd_v1;
    }
    if (pipe(fds) != 0) {
        return -(int32_t)errno;
    }
    if (llgo_signal_set_fd_flags_v1(fds[0], F_GETFL, O_NONBLOCK) != 0 ||
        llgo_signal_set_fd_flags_v1(fds[1], F_GETFL, O_NONBLOCK) != 0 ||
        llgo_signal_set_fd_flags_v1(fds[0], F_GETFD, FD_CLOEXEC) != 0 ||
        llgo_signal_set_fd_flags_v1(fds[1], F_GETFD, FD_CLOEXEC) != 0) {
        int failure = errno;
        close(fds[0]);
        close(fds[1]);
        return -(int32_t)failure;
    }
    llgo_signal_read_fd_v1 = fds[0];
    llgo_signal_write_fd_v1 = fds[1];
    errno = saved_errno;
    return (int32_t)llgo_signal_read_fd_v1;
}

static int llgo_signal_capture_previous_v1(uint32_t sig)
{
    struct llgo_signal_slot_v1 *slot = &llgo_signal_slots_v1[sig];
    if (slot->previous_valid) {
        return 0;
    }
    if (sigaction((int)sig, 0, &slot->previous) != 0) {
        return -1;
    }
    slot->previous_valid = 1;
    return 0;
}

void __llgo_runtime_signal_enable_v1(uint32_t sig)
{
    struct sigaction action = {0};
    struct llgo_signal_slot_v1 *slot;
    int saved_errno = errno;

    if (!llgo_signal_valid_v1(sig)) {
        return;
    }
    slot = &llgo_signal_slots_v1[sig];
    if (llgo_signal_capture_previous_v1(sig) != 0) {
        errno = saved_errno;
        return;
    }
    action.sa_handler = llgo_signal_handler_v1;
    sigemptyset(&action.sa_mask);
    action.sa_flags = SA_RESTART;
    if (sigaction((int)sig, &action, 0) == 0) {
        atomic_store_explicit(&slot->mode, LLGO_SIGNAL_ENABLED_V1, memory_order_release);
    }
    errno = saved_errno;
}

void __llgo_runtime_signal_disable_v1(uint32_t sig)
{
    struct llgo_signal_slot_v1 *slot;
    int saved_errno = errno;

    if (!llgo_signal_valid_v1(sig)) {
        return;
    }
    slot = &llgo_signal_slots_v1[sig];
    atomic_store_explicit(&slot->mode, LLGO_SIGNAL_DISABLED_V1, memory_order_release);
    if (slot->previous_valid) {
        if (sigaction((int)sig, &slot->previous, 0) == 0) {
            slot->previous_valid = 0;
        }
    }
    errno = saved_errno;
}

void __llgo_runtime_signal_ignore_v1(uint32_t sig)
{
    struct sigaction action = {0};
    struct llgo_signal_slot_v1 *slot;
    int saved_errno = errno;

    if (!llgo_signal_valid_v1(sig)) {
        return;
    }
    slot = &llgo_signal_slots_v1[sig];
    if (llgo_signal_capture_previous_v1(sig) != 0) {
        errno = saved_errno;
        return;
    }
    action.sa_handler = SIG_IGN;
    sigemptyset(&action.sa_mask);
    action.sa_flags = 0;
    if (sigaction((int)sig, &action, 0) == 0) {
        atomic_store_explicit(&slot->mode, LLGO_SIGNAL_IGNORED_V1, memory_order_release);
    }
    errno = saved_errno;
}

uint32_t __llgo_runtime_signal_ignored_v1(uint32_t sig)
{
    struct sigaction current = {0};
    uint32_t mode;
    int saved_errno = errno;

    if (!llgo_signal_valid_v1(sig)) {
        return 0;
    }
    mode = atomic_load_explicit(&llgo_signal_slots_v1[sig].mode, memory_order_acquire);
    if (mode == LLGO_SIGNAL_IGNORED_V1) {
        return 1;
    }
    if (mode == LLGO_SIGNAL_ENABLED_V1) {
        return 0;
    }
    if (sigaction((int)sig, 0, &current) != 0) {
        errno = saved_errno;
        return 0;
    }
    errno = saved_errno;
    return current.sa_handler == SIG_IGN;
}

static uint32_t llgo_signal_claim_v1(uint32_t sig)
{
    uint32_t expected = 1;
    if (!llgo_signal_valid_v1(sig) ||
        !atomic_compare_exchange_strong_explicit(
            &llgo_signal_slots_v1[sig].pending,
            &expected,
            0,
            memory_order_acq_rel,
            memory_order_acquire)) {
        return LLGO_SIGNAL_NONE_V1;
    }
    atomic_fetch_add_explicit(&llgo_signal_acknowledged_v1, 1, memory_order_release);
    return sig;
}

static int llgo_signal_has_pending_v1(void)
{
    uint32_t sig;
    for (sig = 1; sig < LLGO_SIGNAL_CAPACITY_V1; ++sig) {
        if (atomic_load_explicit(&llgo_signal_slots_v1[sig].pending, memory_order_acquire) != 0) {
            return 1;
        }
    }
    return 0;
}

uint32_t __llgo_runtime_signal_receive_v1(void)
{
    uint32_t sig;
    uint32_t claimed;
    ssize_t count;
    int saved_errno = errno;

    for (;;) {
        count = read(llgo_signal_read_fd_v1, &sig, sizeof(sig));
        if (count == (ssize_t)sizeof(sig)) {
            claimed = llgo_signal_claim_v1(sig);
            if (claimed != LLGO_SIGNAL_NONE_V1) {
                errno = saved_errno;
                return claimed;
            }
            continue; /* stale token for a signal claimed by overflow scan */
        }
        if (count < 0 && errno == EINTR) {
            continue;
        }
        break;
    }

    /* Recover publications whose non-blocking handler write overflowed. */
    for (sig = 1; sig < LLGO_SIGNAL_CAPACITY_V1; ++sig) {
        claimed = llgo_signal_claim_v1(sig);
        if (claimed != LLGO_SIGNAL_NONE_V1) {
            errno = saved_errno;
            return claimed;
        }
    }

    /*
     * Only publish an idle generation after a stable empty observation. The
     * second observation closes the race with a handler that starts between
     * the pending scan and the counter loads; its pipe write will wake poll.
     */
    if (atomic_load_explicit(&llgo_signal_delivering_v1, memory_order_acquire) == 0) {
        uint32_t published = atomic_load_explicit(&llgo_signal_published_v1, memory_order_acquire);
        uint32_t acknowledged = atomic_load_explicit(&llgo_signal_acknowledged_v1, memory_order_acquire);
        if (published == acknowledged && !llgo_signal_has_pending_v1()) {
            atomic_store_explicit(
                &llgo_signal_idle_generation_v1,
                acknowledged,
                memory_order_release);
        }
    }
    errno = saved_errno;
    return LLGO_SIGNAL_NONE_V1;
}

uint32_t __llgo_runtime_signal_generation_v1(void)
{
    return atomic_load_explicit(&llgo_signal_published_v1, memory_order_acquire);
}

static int llgo_signal_generation_reached_v1(uint32_t current, uint32_t target)
{
    return (int32_t)(current - target) >= 0;
}

uint32_t __llgo_runtime_signal_idle_v1(uint32_t target)
{
    uint32_t published;
    uint32_t acknowledged;
    uint32_t idle;

    if (atomic_load_explicit(&llgo_signal_delivering_v1, memory_order_acquire) != 0) {
        return 0;
    }
    published = atomic_load_explicit(&llgo_signal_published_v1, memory_order_acquire);
    acknowledged = atomic_load_explicit(&llgo_signal_acknowledged_v1, memory_order_acquire);
    idle = atomic_load_explicit(&llgo_signal_idle_generation_v1, memory_order_acquire);
    if (atomic_load_explicit(&llgo_signal_delivering_v1, memory_order_acquire) != 0) {
        return 0;
    }
    return llgo_signal_generation_reached_v1(acknowledged, target) &&
        llgo_signal_generation_reached_v1(idle, target) &&
        llgo_signal_generation_reached_v1(acknowledged, published) &&
        llgo_signal_generation_reached_v1(idle, published);
}

#endif
