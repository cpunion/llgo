#include "libffi/ffi.h"

void *llgo_ffi_closure_alloc(void **code) {
  return ffi_closure_alloc(sizeof(ffi_closure), code);
}

void llgo_ffi_call_with_env(ffi_cif *cif, void (*fn)(void), void *rvalue,
                            void **avalue, void *env) {
  (void)env;
  ffi_call(cif, fn, rvalue, avalue);
}
