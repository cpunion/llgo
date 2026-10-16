#include <fcntl.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t ready = PTHREAD_COND_INITIALIZER;
static _Atomic int owned = -1;
static _Atomic int opens, closes, waiting, released;

static void check_finished(void) {
    int fd = atomic_load(&owned);
    if (atomic_load(&opens) != 3 || atomic_load(&closes) != 3 || fd != -1 ||
        !atomic_load(&waiting) || !atomic_load(&released)) {
        /* Negative controls reclaim their own resource before reporting. */
        if (fd >= 0) close(fd);
        fprintf(stderr, "delivery close mismatch: open=%d close=%d waiting=%d released=%d\n",
                atomic_load(&opens), atomic_load(&closes),
                atomic_load(&waiting), atomic_load(&released));
        _Exit(86);
    }
    static const char marker[] = "typed resource delivery normal, discarded and canceled passed\n";
    if (write(1, marker, sizeof(marker) - 1) != sizeof(marker) - 1) _Exit(87);
}

void llgo_delivery_install(void) {
    if (atexit(check_finished) != 0) _Exit(88);
}

void llgo_delivery_open(int32_t *out, uint32_t mode) {
    int fd = open("/dev/null", O_RDONLY);
    if (fd < 0 || atomic_exchange(&owned, fd) != -1) _Exit(89);
    *out = fd;
    atomic_fetch_add(&opens, 1);
    if (mode != 0) {
        pthread_mutex_lock(&lock);
        atomic_store(&waiting, 1);
        while (!atomic_load(&released)) pthread_cond_wait(&ready, &lock);
        pthread_mutex_unlock(&lock);
    }
}

void llgo_delivery_close(int32_t fd) {
    if (fd < 0 || atomic_load(&owned) != fd || close(fd) != 0 ||
        atomic_exchange(&owned, -1) != fd) _Exit(90);
    atomic_fetch_add(&closes, 1);
}

void llgo_delivery_check_alive(int32_t fd) {
    if (fd < 0 || atomic_load(&owned) != fd || fcntl(fd, F_GETFD) < 0) _Exit(91);
}

int32_t llgo_delivery_closed(void) { return atomic_load(&closes); }
int32_t llgo_delivery_blocked(void) { return atomic_load(&waiting); }

void llgo_delivery_release(void) {
    pthread_mutex_lock(&lock);
    atomic_store(&released, 1);
    pthread_cond_broadcast(&ready);
    pthread_mutex_unlock(&lock);
}

void llgo_delivery_escaped(void) { _Exit(92); }
