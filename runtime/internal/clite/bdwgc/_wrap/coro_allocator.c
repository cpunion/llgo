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
