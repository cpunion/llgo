//go:build !llgo

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

package coro

import "sync/atomic"

func preemptLoad(ptr *uint32) uint32 {
	return atomic.LoadUint32(ptr)
}

func preemptStore(ptr *uint32, value uint32) {
	atomic.StoreUint32(ptr, value)
}

func preemptCompareAndSwap(ptr *uint32, old, new uint32) bool {
	return atomic.CompareAndSwapUint32(ptr, old, new)
}
