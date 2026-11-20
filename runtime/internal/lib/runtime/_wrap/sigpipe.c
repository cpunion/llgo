/* Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Use of this source code is governed by an Apache 2.0 license that can be
 * found in the LICENSE file.
 */

#if defined(__APPLE__) || defined(__linux__)
#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif

#include <errno.h>
#include <pthread.h>
#include <signal.h>
#include <stdint.h>
#include <stdlib.h>

/* The default Go disposition lets ordinary writes return EPIPE. A no-op
 * handler distinguishes that default from os/signal.Ignore. Notify installs
 * its own handler and Reset restores this one through the signal adapter. */
static void llgo_sigpipe_default_v1(int signum) {
    (void)signum;
}

int32_t __llgo_runtime_sigpipe_install_v1(void) {
    int saved_errno = errno;
    struct sigaction current = {0};
    int status = sigaction(SIGPIPE, NULL, &current);
    if (status == 0 && (current.sa_flags & SA_SIGINFO) == 0 &&
        (current.sa_handler == SIG_DFL || current.sa_handler == SIG_IGN)) {
        struct sigaction action = {0};
        action.sa_handler = llgo_sigpipe_default_v1;
        action.sa_flags = SA_RESTART;
        sigemptyset(&action.sa_mask);
        status = sigaction(SIGPIPE, &action, NULL);
    }
    errno = saved_errno;
    return status;
}

void __llgo_runtime_sigpipe_stdout_v1(void) {
    int saved_errno = errno;
    struct sigaction current = {0};
    if (sigaction(SIGPIPE, NULL, &current) != 0 ||
        (current.sa_flags & SA_SIGINFO) != 0 ||
        current.sa_handler != llgo_sigpipe_default_v1) {
        /* Explicit ignore or an installed handler already owns the kernel
         * signal. A second raise could duplicate a Notify delivery. */
        errno = saved_errno;
        return;
    }
    struct sigaction action = {0};
    action.sa_handler = SIG_DFL;
    sigemptyset(&action.sa_mask);
    if (sigaction(SIGPIPE, &action, NULL) == 0) {
        sigset_t unblock;
        sigemptyset(&unblock);
        sigaddset(&unblock, SIGPIPE);
        (void)pthread_sigmask(SIG_UNBLOCK, &unblock, NULL);
        (void)raise(SIGPIPE);
    }
    _Exit(128 + SIGPIPE);
}
#endif
