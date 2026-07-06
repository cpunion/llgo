#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE
#endif

#include <stdint.h>
#include <pthread.h>
#include <unistd.h>

#if defined(__APPLE__)
// The patched runtime provides os.runtime_args even when package os is not
// linked. Darwin's external linker diagnoses its reference to
// os.executablePath before dead stripping the unused function. Provide a weak,
// zero-valued Go string so runtime-only shared/static libraries remain
// linkable; package os supplies the strong definition when it is present.
struct llgo_go_string {
    const char *p;
    intptr_t n;
};
struct llgo_go_string llgo_os_executable_path __asm("_os.executablePath")
    __attribute__((weak));
#endif

int llgo_maxprocs()
{
#ifdef _SC_NPROCESSORS_ONLN
    return (int)sysconf(_SC_NPROCESSORS_ONLN);
#else
    return 1;
#endif
}

// Walk the frame-pointer chain while this native frame is still alive. Returning
// __builtin_frame_address(0) to Go made the pointer refer to an already-returned
// C frame and allowed a stackless coroutine safepoint to retain it. This helper
// transports only copied return PCs across the synchronous ABI.
__attribute__((noinline)) intptr_t llgo_fp_callers(
    intptr_t skip,
    uintptr_t *pcs,
    intptr_t capacity,
    uintptr_t text_low,
    uintptr_t text_high)
{
#if defined(__GNUC__) || defined(__clang__)
    uintptr_t *fp = (uintptr_t *)__builtin_frame_address(0);
    intptr_t count = 0;
    const uintptr_t max_stride = (uintptr_t)1 << 20;
    const intptr_t max_frames = 4096;

    if (pcs == 0 || capacity <= 0 || text_low == 0 || text_high <= text_low) {
        return 0;
    }
    // The first saved return address is in the Go fpCallers wrapper. Skip it so
    // skip=0 starts at that wrapper's caller, matching runtime.Callers.
    skip++;
    for (intptr_t i = 0; fp != 0 && count < capacity && i < max_frames; i++) {
        uintptr_t previous = fp[0];
        uintptr_t return_pc = fp[1];
        uintptr_t current = (uintptr_t)fp;
        if (return_pc < 4096 || return_pc < text_low || return_pc >= text_high) {
            break;
        }
        if (skip > 0) {
            skip--;
        } else {
            pcs[count++] = return_pc;
        }
        if (previous <= current || previous - current > max_stride ||
            (previous & (sizeof(uintptr_t) - 1)) != 0) {
            break;
        }
        fp = (uintptr_t *)previous;
    }
    return count;
#else
    return 0;
#endif
}

void llgo_clobber_pointer_regs(uintptr_t a0, uintptr_t a1, uintptr_t a2, uintptr_t a3,
    uintptr_t a4, uintptr_t a5, uintptr_t a6, uintptr_t a7)
{
    volatile uintptr_t sink = a0 | a1 | a2 | a3 | a4 | a5 | a6 | a7;
    (void)sink;
}

void llgo_clear_stack_ptr(uintptr_t target)
{
    if (target == 0) {
        return;
    }

    volatile uintptr_t marker = 0;
    uintptr_t *cur = 0;
    uintptr_t *end = 0;

#if defined(__APPLE__)
    void *stackaddr = pthread_get_stackaddr_np(pthread_self());
    size_t stacksize = pthread_get_stacksize_np(pthread_self());
    if (stackaddr != 0 && stacksize != 0) {
        uintptr_t *mark = (uintptr_t *)&marker;
        uintptr_t *lo = (uintptr_t *)((char *)stackaddr - stacksize);
        uintptr_t *hi = (uintptr_t *)stackaddr;
        if (mark >= lo && mark < hi) {
            cur = lo;
            end = hi;
        } else {
            lo = (uintptr_t *)stackaddr;
            hi = (uintptr_t *)((char *)stackaddr + stacksize);
            if (mark >= lo && mark < hi) {
                cur = lo;
                end = hi;
            }
        }
    }
#elif defined(__linux__)
    pthread_attr_t attr;
    void *stackaddr = 0;
    size_t stacksize = 0;
    if (pthread_getattr_np(pthread_self(), &attr) == 0) {
        if (pthread_attr_getstack(&attr, &stackaddr, &stacksize) == 0) {
            cur = (uintptr_t *)stackaddr;
            end = (uintptr_t *)((char *)stackaddr + stacksize);
        }
        pthread_attr_destroy(&attr);
    }
#endif

    if (cur == 0 || end == 0 || end <= cur) {
        return;
    }
    if ((uintptr_t *)target >= cur && (uintptr_t *)target < end) {
        return;
    }
    for (; cur < end; cur++) {
        if (*cur == target) {
            *cur = 0;
        }
    }
}
