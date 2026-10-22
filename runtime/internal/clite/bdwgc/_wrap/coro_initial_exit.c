/* Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Use of this source code is governed by an Apache 2.0 license that can be
 * found in the LICENSE file.
 */
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif
#if defined(__APPLE__) || defined(__linux__)
#ifndef GC_THREADS
#define GC_THREADS 1
#endif
#endif
#include <gc/gc.h>
#if defined(__APPLE__) || defined(__linux__)
#include <pthread.h>
#include <unistd.h>
#endif
#if defined(__linux__)
#include <sys/syscall.h>
#endif

/* GC_pthread_create owns automatic cleanup for factory-created pthreads.
 * The primordial OS thread has no such wrapper. It may leave its program P
 * after an unbalanced LockOSThread task exits, so remove that one registration
 * before pthread_exit. A registered primordial thread is either the collector
 * main thread or manually registered; both may explicitly unregister. Foreign
 * adopted pthreads retain their existing wrapper/context cleanup ownership. */
int __llgo_coro_gc_unregister_initial_thread_v1(void) {
#if defined(GC_THREADS) && defined(__APPLE__)
    if (pthread_main_np()) return GC_unregister_my_thread();
#elif defined(GC_THREADS) && defined(__linux__)
    if ((pid_t)syscall(SYS_gettid) == getpid()) return GC_unregister_my_thread();
#endif
    return GC_SUCCESS;
}
