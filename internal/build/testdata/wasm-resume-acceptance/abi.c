#include <stdint.h>

extern int32_t wasm_acceptance_export(int32_t value);

int32_t llgo_call_wasm_acceptance_export(int32_t value) {
  return wasm_acceptance_export(value);
}
