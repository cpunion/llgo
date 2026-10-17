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
static _Atomic int opens, closes, waiting, close_waiting, released, fixture_mode;

static void check_finished(void) {
    int fd = atomic_load(&owned);
    int mode = atomic_load(&fixture_mode);
    int wanted = mode == 0 || mode == 5 || mode == 9 || mode >= 12 ? 3 : 2;
    int waited = atomic_load(&fixture_mode) == 1 ? atomic_load(&close_waiting) : atomic_load(&waiting);
    if (atomic_load(&opens) != wanted || atomic_load(&closes) != wanted || fd != -1 ||
        !waited || !atomic_load(&released)) {
        /* Negative controls reclaim their own resource before reporting. */
        if (fd >= 0) close(fd);
        fprintf(stderr, "delivery close mismatch: open=%d close=%d waiting=%d close_waiting=%d released=%d\n",
                atomic_load(&opens), atomic_load(&closes),
                atomic_load(&waiting), atomic_load(&close_waiting), atomic_load(&released));
        _Exit(86);
    }
    static const char normal[] = "typed resource delivery normal, discarded and canceled passed\n";
    static const char shielded[] = "typed resource drop survives late cancellation\n";
    const char *marker = atomic_load(&fixture_mode) == 1 ? shielded : normal;
    size_t length = atomic_load(&fixture_mode) == 1 ? sizeof(shielded) - 1 : sizeof(normal) - 1;
    if (write(1, marker, length) != length) _Exit(87);
}

void llgo_delivery_install(void) {
    const char *mode = getenv("LLGO_DELIVERY_FIXTURE_MODE");
    char *end = NULL;
    long value = mode ? strtol(mode, &end, 10) : 0;
    atomic_store(&fixture_mode, mode && end != mode && *end == '\0' && value >= 1 && value <= 31 ? (int)value : 0);
    if (atexit(check_finished) != 0) _Exit(88);
}

void llgo_delivery_open(int32_t *out, uint32_t mode) {
    int fd = open("/dev/null", O_RDONLY);
    if (fd < 0 || atomic_exchange(&owned, fd) != -1) _Exit(89);
    *out = fd;
    atomic_fetch_add(&opens, 1);
    if (mode == 1) {
        pthread_mutex_lock(&lock);
        atomic_store(&waiting, 1);
        while (!atomic_load(&released)) pthread_cond_wait(&ready, &lock);
        pthread_mutex_unlock(&lock);
    }
}

void llgo_delivery_open_large(int32_t *out, uint32_t mode) {
    llgo_delivery_open(out, mode);
}

void llgo_delivery_close(int32_t fd) {
    if (fd < 0 || atomic_load(&owned) != fd || close(fd) != 0 ||
        atomic_exchange(&owned, -1) != fd) _Exit(90);
    atomic_fetch_add(&closes, 1);
}

void llgo_delivery_check_alive(int32_t fd) {
    if (fd < 0 || atomic_load(&owned) != fd || fcntl(fd, F_GETFD) < 0) _Exit(91);
}

int32_t llgo_delivery_opened(void) { return atomic_load(&opens); }
int32_t llgo_delivery_owned(void) { return atomic_load(&owned); }
int32_t llgo_delivery_blocked(void) { return atomic_load(&waiting); }
int32_t llgo_delivery_closing(void) { return atomic_load(&close_waiting); }
int32_t llgo_delivery_fixture_mode(void) { return atomic_load(&fixture_mode); }

void llgo_delivery_wait_close(void) {
    pthread_mutex_lock(&lock);
    atomic_store(&close_waiting, 1);
    while (!atomic_load(&released)) pthread_cond_wait(&ready, &lock);
    pthread_mutex_unlock(&lock);
}

void llgo_delivery_release(void) {
    pthread_mutex_lock(&lock);
    atomic_store(&released, 1);
    pthread_cond_broadcast(&ready);
    pthread_mutex_unlock(&lock);
}

void llgo_delivery_escaped(void) { _Exit(92); }
