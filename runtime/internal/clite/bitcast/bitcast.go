/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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

package bitcast

import "unsafe"

// These operations intentionally remain tiny defined Go bodies. Besides
// removing a C leaf dependency for wasm and bare-metal targets, the bodies let
// coroutine preflight prove the complete scalar-only, no-suspend transform
// instead of trusting an opaque foreign declaration.

func ToFloat64(v int64) float64 {
	return *(*float64)(unsafe.Pointer(&v))
}

func ToFloat32(v int32) float32 {
	return *(*float32)(unsafe.Pointer(&v))
}

func FromFloat64(v float64) int64 {
	return *(*int64)(unsafe.Pointer(&v))
}

func FromFloat32(v float32) int32 {
	return *(*int32)(unsafe.Pointer(&v))
}

// FromPointer extracts the machine word carried by an unsafe.Pointer. LLGo's
// reflect Value representation also uses its pointer field for direct scalar
// payloads; keeping that representation cast in this tiny leaf gives the
// coroutine compiler one exact terminal pointer-to-uintptr operation instead
// of making every scalar decoder carry pointer provenance through arithmetic.
func FromPointer(v unsafe.Pointer) uintptr {
	return uintptr(v)
}

// ToPointer installs one machine word in an unsafe.Pointer-shaped carrier
// without performing address arithmetic. Reflect uses that carrier for direct
// scalar interface payloads as well as real pointers.
func ToPointer(v uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&v))
}
