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

#include <stdatomic.h>
#include <stdint.h>
void llgo_deferred_demand_hold(uint32_t *words) {
 _Atomic(uint32_t) *p=(_Atomic(uint32_t) *)words;
 atomic_store_explicit(p,1,memory_order_release);
 for(uint64_t i=0;i<1000000000ULL;i++) {
  if(atomic_load_explicit(p+1,memory_order_acquire)) return;
 }
 atomic_store_explicit(p,2,memory_order_release);
}
