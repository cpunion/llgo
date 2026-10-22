#define GC_THREADS
#define GC_NO_THREAD_REDIRECTS
#include <gc/gc.h>
#include <gc/gc_pthread_redirects.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>

int __llgo_coro_gc_unregister_initial_thread_v1(void);
static pthread_t original;

static void *successor(void *unused) {
    (void)unused;
    void *result = (void *)1;
    int joined = pthread_join(original, &result);
    if (joined != 0 || result != 0) {
        fprintf(stderr, "original join failed %d %p\n", joined, result);
        exit(10);
    }
    for (unsigned i = 0; i != 4; ++i) {
        void *allocation = GC_malloc(1024);
        if (allocation == NULL) exit(11);
        GC_gcollect();
        GC_reachable_here(allocation);
    }
    puts("PASS collection after original GC thread exit");
    fflush(stdout);
    exit(0);
}

int main(void) {
    GC_INIT();
    original = pthread_self();
    pthread_t thread;
    if (GC_pthread_create(&thread, NULL, successor, NULL) != 0) return 12;
    if (__llgo_coro_gc_unregister_initial_thread_v1() != GC_SUCCESS) return 13;
    pthread_exit(NULL);
}
