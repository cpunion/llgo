#include <stdint.h>

/* Copy return PCs while this native frame and the complete chain are still
 * alive. Go must never retain a frame pointer returned from a C frame across a
 * stackless-coroutine safepoint. A zero text range selects the panic-time raw
 * snapshot; symbol-aware callers provide a non-zero range. */
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
    int bounded = text_low != 0 && text_high > text_low;

    if (pcs == 0 || capacity <= 0 ||
        ((text_low == 0) != (text_high == 0)) ||
        (text_low != 0 && text_high <= text_low)) {
        return 0;
    }
    /* The first saved return address is in the Go PhysicalCallers wrapper.
     * Skip it so skip=0 starts at that wrapper's caller. */
    skip++;
    for (intptr_t i = 0; fp != 0 && count < capacity && i < max_frames; i++) {
        uintptr_t previous = fp[0];
        uintptr_t return_pc = fp[1];
        uintptr_t current = (uintptr_t)fp;
        if (return_pc < 4096 ||
            (bounded && (return_pc < text_low || return_pc >= text_high))) {
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
