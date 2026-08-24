//go:build linux && cgo

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
#if defined(__GLIBC__)
#include <malloc.h>
#endif

static void llgo_release_native_heap(void) {
#if defined(__GLIBC__)
	malloc_trim(0);
#endif
}
*/
import "C"

// releaseNativeHeap asks glibc to return free LLVM/backend pages. Other Linux
// allocators retain their ordinary policy rather than acquiring a hard ABI
// dependency on malloc_trim.
func releaseNativeHeap() {
	C.llgo_release_native_heap()
}
