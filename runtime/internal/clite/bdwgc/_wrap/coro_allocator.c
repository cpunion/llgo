/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

#include <gc/gc.h>
#include <stddef.h>
#include <stdint.h>

/*
 * This symbol is the narrow physical boundary used only by LLGo's managed
 * object allocator.  Its scalar-only input means that it cannot retain a
 * pointer into the calling LLVM coroutine frame.
 *
 * This wrapper deliberately does not claim that GC_malloc is lock-free,
 * wait-free, or constant-time.  GC_malloc may acquire collector-internal
 * locks and may perform a collection, pausing the native executor thread.
 * What remains synchronous is the LLGo execution protocol: this call neither
 * parks through the LLGo scheduler nor transfers ownership of a coroutine
 * frame to an asynchronous completion source.
 */
void *__llgo_coro_runtime_gc_malloc_v1(size_t size) {
  return GC_malloc(size);
}

/* A disappearing link is cleared before finalization. Keeping the address
 * complemented in Go is essential: uintptr alone is still a conservative GC
 * root. The link cell contains 1, not the referent, so it cannot retain it. */
int __llgo_coro_gc_register_weak_v1(void *object, uintptr_t *live) {
  void *base = GC_base(object);
  int result = GC_SUCCESS;
  if (base != NULL)
    result = GC_general_register_disappearing_link((void **)live, base);
  GC_reachable_here(object);
  if (result != GC_SUCCESS && result != GC_DUPLICATE) {
    *live = 0;
    return 0;
  }
  return 1;
}

struct llgo_weak_lookup {
  uintptr_t key;
  uintptr_t *live;
};

static void *llgo_weak_reveal_locked(void *raw) {
  struct llgo_weak_lookup *lookup = (struct llgo_weak_lookup *)raw;
  if (*lookup->live == 0)
    return NULL;
  return (void *)~lookup->key;
}

void *__llgo_coro_gc_weak_strong_v1(uintptr_t key, uintptr_t *live) {
  struct llgo_weak_lookup lookup = {key, live};
  /* Testing the cell and revealing the pointer form one collector-locked
   * transaction. A separate atomic Live read would permit resurrection after
   * mark had decided the object was dead but before the link was cleared. */
  return GC_call_with_alloc_lock(llgo_weak_reveal_locked, &lookup);
}

void *__llgo_coro_gc_weak_base_v1(void *object) {
  return GC_base(object);
}
