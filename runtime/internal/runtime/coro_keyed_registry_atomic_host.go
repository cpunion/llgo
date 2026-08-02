//go:build !llgo || !llgo_coro

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

func coroKeyedAtomicLoadUint32(address *uint32) uint32 {
	return atomic.LoadUint32(address)
}

func coroKeyedAtomicStoreUint32(address *uint32, value uint32) {
	atomic.StoreUint32(address, value)
}

func coroKeyedAtomicAddUint32(address *uint32, delta uint32) uint32 {
	return atomic.AddUint32(address, delta)
}

func coroKeyedAtomicCompareAndSwapUint32(address *uint32, old, new uint32) bool {
	return atomic.CompareAndSwapUint32(address, old, new)
}

func coroKeyedAtomicLoadUintptr(address *uintptr) uintptr {
	return atomic.LoadUintptr(address)
}

func coroKeyedAtomicStoreUintptr(address *uintptr, value uintptr) {
	atomic.StoreUintptr(address, value)
}
