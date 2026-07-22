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
