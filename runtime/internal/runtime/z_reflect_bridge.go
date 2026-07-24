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

	"github.com/goplus/llgo/runtime/abi"
)

// The reflect compatibility package intentionally sees runtime objects through
// ABI-prefix and unsafe.Pointer views. Keep those conversions in exact,
// bodyful Go bridges. This lets coroutine planning pair each go:linkname
// declaration structurally instead of treating a physically compatible but
// source-incompatible declaration as an opaque external call.

func ReflectChanCap(ch unsafe.Pointer) int {
	return ChanCap((*Chan)(ch))
}

func ReflectChanLen(ch unsafe.Pointer) int {
	return ChanLen((*Chan)(ch))
}

func ReflectMakeChan(eltSize, capacity int) unsafe.Pointer {
	return unsafe.Pointer(NewChan(eltSize, capacity))
}

func ReflectMakeMap(t *abi.Type, capacity int) unsafe.Pointer {
	return unsafe.Pointer(MakeMap((*maptype)(unsafe.Pointer(t)), capacity))
}

func ReflectMapLen(h unsafe.Pointer) int {
	return MapLen((*hmap)(h))
}

func ReflectMapAccess(t *abi.Type, h, key unsafe.Pointer) (unsafe.Pointer, bool) {
	return MapAccess2((*maptype)(unsafe.Pointer(t)), (*hmap)(h), key)
}

func ReflectMapAssign(t *abi.Type, h, key unsafe.Pointer) unsafe.Pointer {
	return MapAssign((*maptype)(unsafe.Pointer(t)), (*hmap)(h), key)
}

func ReflectMapDelete(t *abi.Type, h, key unsafe.Pointer) {
	MapDelete((*maptype)(unsafe.Pointer(t)), (*hmap)(h), key)
}

func ReflectMapIterInit(t *abi.Type, h, iterator unsafe.Pointer) {
	mapiterinit((*maptype)(unsafe.Pointer(t)), (*hmap)(h), (*hiter)(iterator))
}

func ReflectMapIterNext(iterator unsafe.Pointer) {
	mapiternext((*hiter)(iterator))
}

func ReflectMapClear(t *abi.Type, h unsafe.Pointer) {
	MapClear((*maptype)(unsafe.Pointer(t)), (*hmap)(h))
}

func ReflectSliceClear(t *abi.Type, s Slice) {
	SliceClear((*abi.SliceType)(unsafe.Pointer(t)), s)
}

func ReflectIfaceE2I(t *abi.Type, src any, dst unsafe.Pointer) {
	IfaceE2I(
		(*interfacetype)(unsafe.Pointer(t)),
		*(*eface)(unsafe.Pointer(&src)),
		(*iface)(dst),
	)
}
