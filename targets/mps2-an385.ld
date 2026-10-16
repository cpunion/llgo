/* QEMU's MPS2 AN385 Cortex-M3 model. This is a separate large-memory
 * integration target, not an enlargement of the LM3S6965 board's budget. */
MEMORY
{
    FLASH_TEXT (rw) : ORIGIN = 0x00000000, LENGTH = 4M
    RAM (xrw)       : ORIGIN = 0x20000000, LENGTH = 4M
}

/* Explicit integration budget for shared bootstrap/synchronous frames. This
 * is the one physical host stack, not a per-G stack or a measured minimum. */
_stack_size = 16K;

INCLUDE "targets/arm.ld"
