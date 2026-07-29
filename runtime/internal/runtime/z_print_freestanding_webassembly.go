//go:build wasip1 || wasip2 || wasm_unknown

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

import "unsafe"

// Go's bootstrap print/println operations are implementation diagnostics.
// Freestanding core modules deliberately have no implicit stderr import, and
// WASI Preview 1 cannot perform a potentially blocking diagnostic write from
// the executor stack. A component or custom embedding can provide output at a
// higher boundary.
func PrintBool(bool)              {}
func PrintByte(byte)              {}
func PrintUint(uint64)            {}
func PrintInt(int64)              {}
func PrintFloat(float64)          {}
func PrintComplex(complex128)     {}
func PrintHex(uint64)             {}
func PrintPointer(unsafe.Pointer) {}
func PrintString(String)          {}
func PrintSlice(Slice)            {}
func PrintEface(Eface)            {}
func PrintIface(Iface)            {}
