#include <errno.h>
#include <fcntl.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <unistd.h>

static _Atomic int opened, closed, early_open, late_returned;
static _Atomic int cancel_started, close_started, release_close, go_progress, gc_done;
static _Atomic int semaphore_cleanup;
static _Atomic int owned[3] = {-1, -1, -1};

static void fail_at(int line) {
    fprintf(stderr, "borrowed fd lifecycle failed line=%d open=%d close=%d owned=%d,%d,%d early=%d late=%d cancel=%d close_started=%d release=%d progress=%d gc=%d sema=%d\n",
            line, atomic_load(&opened), atomic_load(&closed),
            atomic_load(&owned[0]), atomic_load(&owned[1]), atomic_load(&owned[2]),
            atomic_load(&early_open), atomic_load(&late_returned), atomic_load(&cancel_started),
            atomic_load(&close_started), atomic_load(&release_close), atomic_load(&go_progress), atomic_load(&gc_done), atomic_load(&semaphore_cleanup));
    _Exit(93);
}

#define fail() fail_at(__LINE__)

static void wait_for(_Atomic int *flag) {
    const struct timespec delay = {0, 1000000};
    while (!atomic_load_explicit(flag, memory_order_acquire)) {
        nanosleep(&delay, NULL);
    }
}

int32_t llgo_resource_open_into(int32_t *out, int32_t mode) {
    if (out == NULL || *out != -1 || mode < 0 || mode > 2 ||
        atomic_load(&owned[mode]) != -1) fail();
    int fd = open("/dev/null", O_RDONLY);
    if (fd < 0) return errno;
    atomic_store(&owned[mode], fd);
    atomic_fetch_add(&opened, 1);
    *out = fd;
    if (mode == 1) {
        atomic_store_explicit(&early_open, 1, memory_order_release);
        wait_for(&cancel_started);
    }
    return 0;
}

int32_t llgo_resource_close(int32_t fd, int32_t mode) {
    if (mode < 0 || mode > 2 || fd < 0 || atomic_load(&owned[mode]) != fd) fail();
    if (mode != 0) {
        wait_for(&cancel_started);
        atomic_fetch_or_explicit(&close_started, mode == 1 ? 4 : 8, memory_order_release);
        wait_for(&release_close);
    }
    if (close(fd) != 0) fail();
    atomic_store(&owned[mode], -1);
    atomic_fetch_add(&closed, 1);
    return 0;
}

int32_t llgo_resource_state(void) {
    return (atomic_load_explicit(&early_open, memory_order_acquire) ? 1 : 0) |
           (atomic_load_explicit(&late_returned, memory_order_acquire) ? 2 : 0) |
           atomic_load_explicit(&close_started, memory_order_acquire);
}

void llgo_resource_mark(int32_t event) {
    switch (event) {
    case 1:
        atomic_store_explicit(&cancel_started, 1, memory_order_release);
        break;
    case 2:
        atomic_store_explicit(&late_returned, 1, memory_order_release);
        break;
    case 3:
        if (atomic_load(&close_started) != 12 || atomic_load(&release_close)) fail();
        atomic_fetch_add(&go_progress, 1);
        break;
    case 4:
        if (atomic_load(&go_progress) != 8) fail();
        atomic_store_explicit(&release_close, 1, memory_order_release);
        break;
    case 5:
        atomic_store(&gc_done, 1);
        break;
    case 6:
        if (!atomic_load(&gc_done) || atomic_fetch_add(&semaphore_cleanup, 1) != 0) fail();
        break;
    default:
        fail();
    }
}

static void check_finished(void) {
    if (atomic_load(&opened) != 18 || atomic_load(&closed) != 18 ||
        atomic_load(&owned[0]) != -1 || atomic_load(&owned[1]) != -1 ||
        atomic_load(&owned[2]) != -1 || atomic_load(&go_progress) != 8 ||
        !atomic_load(&gc_done) || !atomic_load(&cancel_started) ||
        !atomic_load(&release_close) || atomic_load(&close_started) != 12 ||
        atomic_load(&semaphore_cleanup) != 1) fail();
    static const char text[] = "borrowed fd cancellation and asynchronous cleanup ok\n";
    if (write(1, text, sizeof(text) - 1) != sizeof(text) - 1) fail();
}

void llgo_resource_install_check(void) {
    if (atexit(check_finished) != 0) fail();
}
