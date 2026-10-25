#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif
#define _XOPEN_SOURCE 700
#define _DARWIN_C_SOURCE 1
#include <dlfcn.h>
#include <errno.h>
#include <signal.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <ucontext.h>

static int disabled, unresolved, fail_mapping;
static unsigned mapping_calls;
static size_t mapping_bytes;
static char *test_getenv(const char *);
#if defined(__linux__)
static void *test_dlopen(const char *, int);
#endif
static void *test_dlsym(void *, const char *);
static void *test_mmap(void *, size_t, int, int, int, off_t);
static int test_init(void *, void *);
static int test_init2(void *, void *, int);
static int test_step(void *);
static int test_get_reg(void *, int, uintptr_t *);
static int test_get_name(void *, char *, size_t, uintptr_t *);

#define getenv test_getenv
#define dlopen test_dlopen
#define dlsym test_dlsym
#define mmap test_mmap
#include "dynunwind-under-test.c"
#undef getenv
#undef dlopen
#undef dlsym
#undef mmap

_Static_assert(DYNUNW_CURSOR_WORDS == 8192, "cursor capacity changed");
_Static_assert(DYNUNW_CONTEXT_WORDS == 1024, "context capacity changed");

static char *test_getenv(const char *name) {
    return disabled && strcmp(name, "LLGO_DYNUNWIND") == 0 ? "0" : NULL;
}
#if defined(__linux__)
static void *test_dlopen(const char *name, int mode) {
    (void)name; (void)mode;
    return unresolved ? NULL : (void *)(uintptr_t)1;
}
#endif
static void *test_dlsym(void *handle, const char *name) {
    (void)handle;
    if (unresolved) return NULL;
    if (strstr(name, "init_local2")) return (void *)test_init2;
    if (strstr(name, "init_local")) return (void *)test_init;
    if (strstr(name, "get_proc_name")) return (void *)test_get_name;
    if (strstr(name, "get_reg")) return (void *)test_get_reg;
    if (strstr(name, "step")) return (void *)test_step;
    return NULL;
}
static void *test_mmap(void *hint, size_t bytes, int protection, int flags,
                       int fd, off_t offset) {
    ++mapping_calls;
    mapping_bytes = bytes;
    if (fail_mapping) {
        errno = ENOMEM;
        return MAP_FAILED;
    }
    if (hint != NULL || bytes != (8192 + 1024) * sizeof(uint64_t) ||
        protection != (PROT_READ | PROT_WRITE) ||
        flags != (MAP_PRIVATE | MAP_ANONYMOUS) || fd != -1 || offset != 0) {
        abort();
    }
    return mmap(hint, bytes, protection, flags, fd, offset);
}
static int test_init(void *cursor, void *context) {
    (void)context;
    return cursor == dynunw_cursor ? 0 : -1;
}
static int test_init2(void *cursor, void *context, int flags) {
    return flags == UNW_INIT_SIGNAL_FRAME ? test_init(cursor, context) : -1;
}
static int test_step(void *cursor) { (void)cursor; return 0; }
static int test_get_reg(void *cursor, int reg, uintptr_t *value) {
    (void)cursor; (void)reg;
    *value = 0;
    return 0;
}
static int test_get_name(void *cursor, char *buffer, size_t size, uintptr_t *offset) {
    (void)cursor;
    if (size) buffer[0] = 0;
    *offset = 0;
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 2) return 1;
    disabled = strcmp(argv[1], "disabled") == 0;
    unresolved = strcmp(argv[1], "unresolved") == 0;
    fail_mapping = strcmp(argv[1], "mapping-failure") == 0;
    llgo_dynunwind_init();
    if (disabled || unresolved) {
        if (dynunw_enabled || mapping_calls || dynunw_cursor || dynunw_ctx) return 2;
        llgo_dynunwind_capture(NULL);
        return llgo_dynunwind_pccount() == 0 ? 0 : 3;
    }
    if (fail_mapping) {
        if (dynunw_enabled || mapping_calls != 1 || dynunw_cursor || dynunw_ctx) return 4;
        llgo_dynunwind_capture(NULL);
        fail_mapping = 0;
        llgo_dynunwind_init();
    }
    if (!dynunw_enabled || dynunw_cursor == NULL ||
        dynunw_ctx != dynunw_cursor + 8192 ||
        (uintptr_t)dynunw_cursor % _Alignof(uint64_t) != 0 ||
        mapping_bytes != (8192 + 1024) * sizeof(uint64_t)) return 5;
    unsigned calls = mapping_calls;
    for (size_t word = 0; word < 8192 + 1024; ++word) {
        if (dynunw_cursor[word] != 0) return 6;
    }
    uint64_t *cursor = dynunw_cursor, *context = dynunw_ctx;
    cursor[8191] = UINT64_C(0x0123456789abcdef);
    context[1023] = UINT64_C(0xfedcba9876543210);
    for (unsigned repeat = 0; repeat < 16; ++repeat) llgo_dynunwind_init();
    if (mapping_calls != calls || cursor != dynunw_cursor || context != dynunw_ctx ||
        cursor[8191] != UINT64_C(0x0123456789abcdef) ||
        context[1023] != UINT64_C(0xfedcba9876543210)) return 7;
    puts("dynunwind initialization contract ok");
    return 0;
}
