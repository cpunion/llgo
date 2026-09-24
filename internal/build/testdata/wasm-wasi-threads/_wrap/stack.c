#define _GNU_SOURCE
#include <pthread.h>
#include <stdint.h>

int32_t llgo_wasi_worker_stack_bounds(void) {
  pthread_attr_t attr;
  void *base = 0;
  size_t size = 0;
  if (pthread_getattr_np(pthread_self(), &attr) != 0) {
    return 0;
  }
  int status = pthread_attr_getstack(&attr, &base, &size);
  pthread_attr_destroy(&attr);
  uintptr_t sp = (uintptr_t)&attr;
  uintptr_t start = (uintptr_t)base;
  return status == 0 && size > 0 && sp >= start && sp - start < size;
}
