/* Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Use of this source code is governed by an Apache 2.0 license that can be
 * found in the LICENSE file.
 */

#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif
/* Only the native pthread targets change the collector's startup policy. */
#if defined(__APPLE__) || defined(__linux__)
#ifndef GC_THREADS
#define GC_THREADS 1
#endif
#endif
#include <gc/gc.h>
#include <limits.h>
#include <stdlib.h>
#if defined(__APPLE__) || defined(__linux__)
#include <unistd.h>
#endif

/* Called in the single-threaded allocator bootstrap. BDWGC fixes its marker
 * count during GC_init, before LLGo creates executor and worker threads. Honor
 * an explicit collector setting and an embedder which initialized GC first.
 * Otherwise a valid initial Go execution quota also bounds parallel marking:
 * GOMAXPROCS=1 should not create one marker thread per host CPU. Later changes
 * to GOMAXPROCS still resize managed execution; this collector API is startup
 * only, so they do not resize an already initialized marker team.
 */
void __llgo_coro_gc_init_v1(void) {
#if defined(GC_THREADS) && (defined(__APPLE__) || defined(__linux__))
    const char *text = getenv("GOMAXPROCS");
    if (!GC_is_init_called() && getenv("GC_MARKERS") == NULL &&
        getenv("GC_NPROCS") == NULL && text != NULL && *text != '\0') {
        unsigned quota = 0;
        int valid = 1;
        for (; *text != '\0'; ++text) {
            if (*text < '0' || *text > '9') {
                valid = 0;
                break;
            }
            unsigned digit = (unsigned)(*text - '0');
            quota = quota > (UINT_MAX - digit) / 10
                ? UINT_MAX : quota * 10 + digit;
        }
        if (valid && quota != 0) {
            long online = sysconf(_SC_NPROCESSORS_ONLN);
            unsigned available = online > 0 && (unsigned long)online <= UINT_MAX
                ? (unsigned)online : 1;
            GC_set_markers_count(quota < available ? quota : available);
        }
    }
#endif
    GC_init();
}
