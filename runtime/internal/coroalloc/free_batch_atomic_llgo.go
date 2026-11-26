//go:build llgo && !nogc && !baremetal && !wasm && !tinygo.wasm

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

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/sync/atomic"
)

func freeBatchLoad(ptr *uint32) uint32 {
	return atomic.Load(ptr)
}

func freeBatchCAS(ptr *uint32, old, new uint32) bool {
	_, swapped := atomic.CompareAndExchange(ptr, old, new)
	return swapped
}

func freeBatchStore(ptr *uint32, value uint32) {
	atomic.Store(ptr, value)
}

func freeBatchLoadPtr(ptr *unsafe.Pointer) unsafe.Pointer {
	return atomic.Load(ptr)
}

func freeBatchCASPtr(ptr *unsafe.Pointer, old, new unsafe.Pointer) bool {
	_, swapped := atomic.CompareAndExchange(ptr, old, new)
	return swapped
}

func freeBatchSwapPtr(ptr *unsafe.Pointer, value unsafe.Pointer) unsafe.Pointer {
	return atomic.Exchange(ptr, value)
}
