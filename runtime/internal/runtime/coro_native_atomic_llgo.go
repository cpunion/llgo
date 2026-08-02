//go:build llgo && llgo_coro && llgo_coro_native_pipe && (darwin || linux) && !baremetal

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

import (
	"unsafe"

	catomic "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
)

func coroNativeAtomicLoadV1(word *uint32) uint32 {
	return catomic.Load(word)
}

func coroNativeAtomicStoreV1(word *uint32, value uint32) {
	catomic.Store(word, value)
}

func coroNativeAtomicCASV1(word *uint32, old, next uint32) bool {
	_, swapped := catomic.CompareAndExchange(word, old, next)
	return swapped
}

func coroNativeAtomicLoadPointerV1(word *unsafe.Pointer) unsafe.Pointer {
	return catomic.Load(word)
}

func coroNativeAtomicCASPointerV1(word *unsafe.Pointer, old, next unsafe.Pointer) bool {
	_, swapped := catomic.CompareAndExchange(word, old, next)
	return swapped
}
