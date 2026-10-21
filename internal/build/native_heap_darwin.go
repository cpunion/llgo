//go:build darwin && cgo

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

package build

/*
#include <malloc/malloc.h>

static void llgo_release_native_heap(void) {
	malloc_zone_pressure_relief(NULL, 0);
}
*/
import "C"

import "time"

// releaseNativeHeap asks the host allocator to return free LLVM/backend pages
// at an explicit compiler phase boundary.
func releaseNativeHeap() {
	C.llgo_release_native_heap()
}

// beginNativeHeapRelief returns free allocator pages at intervals while a large
// detached module runs its requested optimization pipeline. It never changes
// the LLVM pipeline or frees live allocations. The owner joins this loop
// before disposing its context or returning from the optimization phase.
func beginNativeHeapRelief() func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				releaseNativeHeap()
			}
		}
	}()
	return func() { close(stop); <-done; releaseNativeHeap() }
}
