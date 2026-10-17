#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <errno.h>
#include <fcntl.h>
#include <stdarg.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <unistd.h>

static char fixture_path[128];
static struct stat fifo_stat, stdin_stat;
static int stdin_valid;
static uint32_t fixture_mode;

static int at_file_stage(void) {
  return (fixture_mode >= 8 && fixture_mode <= 10) || fixture_mode == 14 || fixture_mode == 15;
}
static _Atomic uint32_t reader_entered, writer_done, file_entered,
    file_released;

#if defined(__APPLE__)
#include <dlfcn.h>
#include <sched.h>
typedef int (*next_fstat_fn)(int, struct stat *);
static _Atomic(next_fstat_fn) next_fstat;
static int unobserved_fstat(int fd, struct stat *st) {
  next_fstat_fn fn = atomic_load_explicit(&next_fstat, memory_order_acquire);
  if (fn == NULL) {
    fn = (next_fstat_fn)dlsym(RTLD_NEXT, "fstat");
    if (fn == NULL) {
      _Exit(98);
    }
    atomic_store_explicit(&next_fstat, fn, memory_order_release);
  }
  return fn(fd, st);
}
static void observe_file_stage(uint64_t device, uint64_t inode) {
  if (at_file_stage() && device == (uint64_t)fifo_stat.st_dev &&
      inode == (uint64_t)fifo_stat.st_ino) {
    atomic_store_explicit(&file_entered, 1, memory_order_release);
    while (!atomic_load_explicit(&file_released, memory_order_acquire)) {
      sched_yield();
    }
  }
}
int fstat(int fd, struct stat *st) {
  int result = unobserved_fstat(fd, st);
  if (result == 0) {
    observe_file_stage((uint64_t)st->st_dev, (uint64_t)st->st_ino);
  }
  return result;
}
#if defined(__x86_64__)
typedef int (*next_fstat64_fn)(int, struct stat64 *);
static _Atomic(next_fstat64_fn) next_fstat64;
int fstat64(int fd, struct stat64 *st) {
  next_fstat64_fn fn =
      atomic_load_explicit(&next_fstat64, memory_order_acquire);
  if (fn == NULL) {
    fn = (next_fstat64_fn)dlsym(RTLD_NEXT, "fstat64");
    if (fn == NULL) {
      _Exit(98);
    }
    atomic_store_explicit(&next_fstat64, fn, memory_order_release);
  }
  int result = fn(fd, st);
  if (result == 0) {
    observe_file_stage((uint64_t)st->st_dev, (uint64_t)st->st_ino);
  }
  return result;
}
#endif
#else
#define unobserved_fstat fstat
#endif

static int fixture_open(const char *path, int flags, mode_t mode) {
  if (fixture_path[0] != 0 && strcmp(path, fixture_path) == 0 &&
      (flags & O_ACCMODE) == O_RDONLY) {
    atomic_store_explicit(&reader_entered, 1, memory_order_release);
  }
#if defined(__APPLE__)
  return (int)syscall(SYS_open, path, flags, mode);
#else
  return (int)syscall(SYS_openat, AT_FDCWD, path, flags, mode);
#endif
}

// Both versions execute the real kernel open. Only physical admission is
// observed; no Go state or cancellation protocol enters this shim.
int open(const char *path, int flags, ...) {
  mode_t mode = 0;
  if (flags & O_CREAT) {
    va_list ap;
    va_start(ap, flags);
    mode = (mode_t)va_arg(ap, unsigned int);
    va_end(ap);
  }
  return fixture_open(path, flags, mode);
}
#if defined(__linux__)
int open64(const char *path, int flags, ...) {
  mode_t mode = 0;
  if (flags & O_CREAT) {
    va_list ap;
    va_start(ap, flags);
    mode = (mode_t)va_arg(ap, unsigned int);
    va_end(ap);
  }
  return fixture_open(path, flags, mode);
}
#endif

static void check_exit(void) {
  int leaks = 0;
  long limit = sysconf(_SC_OPEN_MAX);
  for (int fd = 0; fd < limit; fd++) {
    struct stat st;
    if (unobserved_fstat(fd, &st) == 0 && st.st_dev == fifo_stat.st_dev &&
        st.st_ino == fifo_stat.st_ino) {
      leaks++;
    }
  }
  struct stat current;
  int stdin_changed = stdin_valid && (unobserved_fstat(0, &current) != 0 ||
                                      current.st_dev != stdin_stat.st_dev ||
                                      current.st_ino != stdin_stat.st_ino);
  unlink(fixture_path);
  if (!atomic_load_explicit(&writer_done, memory_order_acquire) || leaks != 0 ||
      stdin_changed || (at_file_stage() && !atomic_load(&file_released))) {
    fprintf(stderr,
            "public Open custody failed: writer=%u fifo_fds=%d "
            "stdin_changed=%d file_stage=%u file_released=%u\n",
            atomic_load(&writer_done), leaks, stdin_changed,
            atomic_load(&file_entered), atomic_load(&file_released));
    _Exit(99);
  }
  puts("public Open cancellation and unique fd cleanup passed");
}

int32_t llgo_public_open_install(uint8_t *output, uint32_t capacity) {
  int length = snprintf(fixture_path, sizeof(fixture_path),
                        "/tmp/llgo-coro-public-open-%ld", (long)getpid());
  if (length < 0 || (uint32_t)length + 1 > capacity) {
    return -1;
  }
  if (mkfifo(fixture_path, 0600) != 0 || stat(fixture_path, &fifo_stat) != 0) {
    return -2;
  }
  stdin_valid = fstat(0, &stdin_stat) == 0;
  const char *mode = getenv("LLGO_PUBLIC_OPEN_MODE");
  fixture_mode = mode == NULL ? 0 : (uint32_t)strtoul(mode, NULL, 10);
  memcpy(output, fixture_path, (size_t)length + 1);
  return atexit(check_exit);
}
uint32_t llgo_public_open_entered(void) {
  return atomic_load_explicit(&reader_entered, memory_order_acquire) |
         (atomic_load_explicit(&file_entered, memory_order_acquire) << 1) |
         (atomic_load_explicit(&writer_done, memory_order_acquire) << 2);
}
uint32_t llgo_public_open_mode(void) { return fixture_mode; }
void llgo_public_open_writer_done(void) {
  atomic_store_explicit(&writer_done, 1, memory_order_release);
}

void llgo_public_open_release_file_stage(void) {
  atomic_store_explicit(&file_released, 1, memory_order_release);
}
