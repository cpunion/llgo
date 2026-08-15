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

package coroalloc

// nativeCacheAllocationSize is target-independent so the host test suite can
// gate the exact classes used by the llgo-only native cache.
func nativeCacheAllocationSize(size uintptr) uintptr {
	if size <= 1024 {
		if size <= 256 {
			return 256
		}
		return (size + 31) &^ 31
	}
	if size <= 4096 {
		return (size + 127) &^ 127
	}
	switch {
	case size <= 8192:
		return 8192
	case size <= 16384:
		return 16384
	case size <= 32768:
		return 32768
	case size <= 65536:
		return 65536
	default:
		return size
	}
}
