#include <fcntl.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

static _Atomic int owned = -1;
static _Atomic int close_count, started, released;

static void check_finished(void) {
    int fd = atomic_load(&owned);
    if (atomic_load(&close_count) != 1 || fd != -1 ||
        !atomic_load(&started) || !atomic_load(&released)) {
        /* The negative probe explicitly reclaims its own fd before exiting. */
        if (fd >= 0) close(fd);
        fprintf(stderr, "normal cleanup skipped final close: closed=%d started=%d released=%d\n",
                atomic_load(&close_count), atomic_load(&started), atomic_load(&released));
        _Exit(86);
    }
    static const char marker[] = "normal asynchronous cleanup survives late cancellation\n";
    if (write(1, marker, sizeof(marker) - 1) != sizeof(marker) - 1) _Exit(87);
}

void llgo_normal_cleanup_install(void) {
    if (atexit(check_finished) != 0) _Exit(88);
}

int32_t llgo_normal_cleanup_open(void) {
    int fd = open("/dev/null", O_RDONLY);
    if (fd >= 0) atomic_store(&owned, fd);
    return fd;
}

void llgo_normal_cleanup_close(int32_t fd) {
    if (fd < 0 || atomic_load(&owned) != fd || close(fd) != 0 ||
        atomic_fetch_add(&close_count, 1) != 0) _Exit(89);
    atomic_store(&owned, -1);
}

int32_t llgo_normal_cleanup_started(void) { return atomic_load(&started); }
int32_t llgo_normal_cleanup_released(void) { return atomic_load(&released); }
void llgo_normal_cleanup_mark_started(void) { atomic_store(&started, 1); }
void llgo_normal_cleanup_release(void) { atomic_store(&released, 1); }
