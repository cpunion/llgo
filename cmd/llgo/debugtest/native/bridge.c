#include "bridge.h"

extern int llgo_debug_go_callback(int value);

__attribute__((noinline))
int llgo_debug_host_bridge(int value) {
    int result = llgo_debug_go_callback(value); /* LLDB_STOP: host_callback */
    return result + 1;
}

__attribute__((noinline))
void llgo_debug_trap(void) {
    __builtin_trap(); /* LLDB_STOP: host_trap */
}
