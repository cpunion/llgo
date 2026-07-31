#include <unistd.h>
#include <stdint.h>

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

/* Implemented by fault.c. The recover liveness probe must not dereference a
 * stale frame link after siglongjmp. */
int llgo_mem_readable(void *p);

/* Return a scalar mark for the Go frame which called this leaf. The frame
 * pointer itself never crosses as a Go pointer. */
__attribute__((noinline)) uintptr_t llgo_caller_frame_mark(void)
{
#if defined(__GNUC__) || defined(__clang__)
    uintptr_t *frame = (uintptr_t *)__builtin_frame_address(0);
    return frame ? frame[0] : 0;
#else
    return 0;
#endif
}

/* Report whether mark still lies inside the current live frame chain. The
 * whole walk remains in this synchronous leaf so stackless coroutine code only
 * observes a boolean, never a retained native-stack address. */
__attribute__((noinline)) int llgo_frame_chain_contains(uintptr_t mark)
{
#if defined(__GNUC__) || defined(__clang__)
    uintptr_t *fp = (uintptr_t *)__builtin_frame_address(0);
    const uintptr_t max_stride = (uintptr_t)1 << 20;
    const intptr_t max_frames = 4096;

    if (mark == 0) {
        return 0;
    }
    for (intptr_t i = 0; fp != 0 && i < max_frames; i++) {
        uintptr_t current = (uintptr_t)fp;
        if ((current & (sizeof(uintptr_t) - 1)) != 0 ||
            !llgo_mem_readable((void *)fp)) {
            break;
        }
        uintptr_t previous = fp[0];
        if (current <= mark && (previous > mark || previous == 0)) {
            return 1;
        }
        if (previous <= current || previous - current > max_stride ||
            (previous & (sizeof(uintptr_t) - 1)) != 0) {
            break;
        }
        fp = (uintptr_t *)previous;
    }
#endif
    return 0;
}
