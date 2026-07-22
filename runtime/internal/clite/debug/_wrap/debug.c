#if defined(__linux__)
#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif
#include <features.h>
#endif

#include <dlfcn.h>
#include <errno.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>

void *llgo_address() {
    return __builtin_return_address(0);
}

int llgo_addrinfo(uintptr_t addr, Dl_info *info) {
    int saved_errno = errno;
    int ret = dladdr((void *)addr, info);
    errno = saved_errno;
    return ret;
}

void *llgo_symbol(char *name) {
    int saved_errno = errno;
    void *ret = dlsym(RTLD_DEFAULT, name);
    errno = saved_errno;
    return ret;
}

typedef struct {
    void *pc;
    uintptr_t offset;
    void *sp;
    char *name;
} llgo_stacktrace_frame;

typedef int (*llgo_stacktrace_visitor)(void *ctx, void *pc, uintptr_t offset, void *sp, char *name);

static void llgo_walk_stack(int skip, void *ctx, llgo_stacktrace_visitor fn) {
    /* Frame-pointer chain walk. LLGo compiles every Go function with
     * "frame-pointer"="non-leaf", so [fp] is the previous frame pointer and
     * [fp+1] the return address on both arm64 and x86-64. This replaces the
     * libunwind cursor: no unwind tables, no -lunwind, and it keeps working
     * through any frame that maintains the chain (C code compiled with
     * frame pointers included). The walk stops at the first frame that
     * breaks chain discipline.
     *
     * The Go-side walker (runtime/internal/lib/runtime/unwind_llgo.go
     * fpCallers) implements the same discipline plus a text-range bound the
     * frame tables provide; keep the chain guards below in sync with it. */
    int saved_errno = errno;
    uintptr_t fp = (uintptr_t)__builtin_frame_address(0);
    int depth = 0;
    while (fp) {
        uintptr_t prev = *(uintptr_t *)fp;
        uintptr_t pc = *((uintptr_t *)fp + 1);
        if (pc < 4096)
            break;
        if (depth >= skip) {
            Dl_info info;
            const char *name = "";
            uintptr_t offset = 0;
            if (dladdr((void *)pc, &info) && info.dli_sname) {
                name = info.dli_sname;
                offset = pc - (uintptr_t)info.dli_saddr;
            }
            if (fn(ctx, (void *)pc, offset, (void *)fp, (char *)name) == 0)
                break;
        }
        depth++;
        if (prev <= fp || prev - fp > (uintptr_t)1 << 20 || (prev & (sizeof(uintptr_t) - 1)))
            break;
        fp = prev;
    }
    errno = saved_errno;
}

typedef struct {
    llgo_stacktrace_frame *frames;
    int capacity;
    int count;
} llgo_stacktrace_capture;

static int llgo_capture_stack_frame(void *ctx, void *pc, uintptr_t offset, void *sp, char *name) {
    llgo_stacktrace_capture *capture = (llgo_stacktrace_capture *)ctx;
    if (capture->count >= capture->capacity)
        return 0;
    llgo_stacktrace_frame *frame = &capture->frames[capture->count++];
    frame->pc = pc;
    frame->offset = offset;
    frame->sp = sp;
    frame->name = name;
    return capture->count < capture->capacity;
}

int llgo_stacktrace(int skip, llgo_stacktrace_frame *frames, int capacity) {
    if (!frames || capacity <= 0)
        return 0;
    llgo_stacktrace_capture capture = {frames, capacity, 0};
    llgo_walk_stack(skip, &capture, llgo_capture_stack_frame);
    return capture.count;
}

static int llgo_print_stack_frame(void *ctx, void *pc, uintptr_t offset, void *sp, char *name) {
    (void)ctx;
    fprintf(stderr, "[0x%08" PRIXPTR " %s+0x%" PRIxPTR ", SP = 0x%" PRIxPTR "]\n",
            (uintptr_t)pc, name, offset, (uintptr_t)sp);
    return 1;
}

void llgo_print_stack(int skip) {
    /* Account for this C adapter in addition to the caller-selected frames. */
    llgo_walk_stack(skip + 1, NULL, llgo_print_stack_frame);
}
