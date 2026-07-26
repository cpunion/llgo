//go:build (darwin || linux) && !baremetal && !llgo

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

package runtime

import "sync/atomic"

func coroNativeAtomicLoadV1(word *uint32) uint32 {
	return atomic.LoadUint32(word)
}

func coroNativeAtomicStoreV1(word *uint32, value uint32) {
	atomic.StoreUint32(word, value)
}

func coroNativeAtomicCASV1(word *uint32, old, next uint32) bool {
	return atomic.CompareAndSwapUint32(word, old, next)
}
