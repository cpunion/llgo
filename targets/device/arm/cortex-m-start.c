#include <stdint.h>

extern uint32_t _sidata[], _sdata[], _edata[], _sbss[], _ebss[];
extern int main(int, char **);

/* Cortex-M installs the initial stack pointer from the vector table. Keep
 * startup independent of an OS or a second scheduler: main runs the initial
 * coroutine slice, and an embedding owns all later pull/continue turns. */
__attribute__((weak, noreturn)) void Reset_Handler(void) {
    uint32_t *source = _sidata;
    for (uint32_t *dest = _sdata; dest < _edata; ++dest)
        *dest = *source++;
    for (uint32_t *dest = _sbss; dest < _ebss; ++dest)
        *dest = 0;
    (void)main(0, (char **)0);
    for (;;)
        __asm__ volatile("wfi");
}

/* A board may override this terminal boundary with its own fault recorder. */
__attribute__((weak, noreturn)) void handleHardFault(void) {
    for (;;)
        __asm__ volatile("wfi");
}
