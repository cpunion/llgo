#include <stdbool.h>
#include <stdint.h>

typedef struct {
    uint32_t kind, slot, generation, epoch, lo, hi, reserved0, reserved1;
} host_action;

extern int __real_main(int, char **);
extern uint32_t __llgo_coro_host_profile_v1(void);
extern uint32_t __llgo_coro_host_next_action_v1(host_action *);
extern bool __llgo_coro_host_publish_time_v1(uint32_t, uint32_t);
extern bool __llgo_coro_host_ack_cancel_v1(uint32_t, uint32_t, uint32_t, uint32_t);
extern uint32_t __llgo_coro_host_continue_slice_v1(
    uint32_t, uint32_t, uint32_t, uint32_t, uint32_t, uint32_t,
    uint32_t, uint32_t *);

static volatile uint32_t phase, returned, early_complete;

void llgo_baremetal_timer_phase(uint32_t next) {
    phase = next;
    if (next == 2 && !returned)
        early_complete = 1;
}

static __attribute__((noreturn)) void finish(uint32_t code) {
#if defined(__arm__)
    const uint32_t status[2] = {0x20026, code};
    register uint32_t operation __asm__("r0") = 0x20;
    register const uint32_t *reason __asm__("r1") = status;
    __asm__ volatile("bkpt 0xab" : : "r"(operation), "r"(reason) : "memory");
#elif defined(__riscv)
    *(volatile uint32_t *)0x100000 = code ? (code << 16) | 0x3333 : 0x5555;
#endif
    for (;;) {}
}

/* Fail immediately rather than letting a target fault look like a parked G. */
void handleHardFault(void) { finish(11); }

/* Deterministic test embedding: no IRQ or scheduler activation spans a host
 * wait. A board replaces the artificial deadline advance with its monotonic
 * clock, compare interrupt and WFI, keeping this same pull/continue protocol. */
int __wrap_main(int argc, char **argv) {
    (void)argc;
    (void)argv;
    /* Exercise Go time.Now's monotonic-zero sentinel without an RTC. */
    if (!__llgo_coro_host_publish_time_v1(0, 0))
        finish(10);
    if (__real_main(0, 0) != 0 || early_complete)
        finish(1);
    returned = 1;
    uint32_t profile = __llgo_coro_host_profile_v1();
    if ((profile & 0xff) != 3 || (profile & 0x300) != 0x300)
        finish(2);
    uint64_t now = 0;
    uint32_t alarms = 0;
    for (uint32_t turns = 0; turns < 4096; ++turns) {
        host_action action = {0};
        uint32_t kind = __llgo_coro_host_next_action_v1(&action);
        if (kind != action.kind || action.reserved0 || action.reserved1)
            finish(3);
        if (kind == 3 || kind == 4) {
            if (!__llgo_coro_host_ack_cancel_v1(action.slot, action.generation,
                                               action.epoch, kind))
                finish(4);
            continue;
        }
        if (kind != 1 && kind != 2)
            finish(5);
        if (kind == 2) {
            uint64_t deadline = ((uint64_t)action.hi << 32) | action.lo;
            if (deadline < now || phase == 0)
                finish(6);
            now = deadline;
            ++alarms;
        }
        uint32_t result[8] = {0};
        uint32_t status = __llgo_coro_host_continue_slice_v1(
            action.slot, action.generation, action.epoch, kind,
            (uint32_t)now, (uint32_t)(now >> 32), 256, result);
        if (status == 1)
            finish(phase == 2 && alarms >= 4 && !early_complete ? 0 : 7);
        if (status == 0)
            finish(8);
    }
    finish(9);
}
