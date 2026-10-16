#include <stdint.h>

static int32_t *volatile fault_pointer;
void coro_foreign_funcval_probe(void) { *fault_pointer = 42; }
int32_t coro_foreign_funcval_reenter(int32_t (*callback)(int32_t), int32_t mode) {
    int32_t value = callback(mode);
    if (mode == 1) coro_foreign_funcval_probe();
    return value;
}
