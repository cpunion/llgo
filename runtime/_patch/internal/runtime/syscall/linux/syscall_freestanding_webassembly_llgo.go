//go:build tinygo.wasm

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

package linux

//llgo:skip Syscall6

const enosys = 38

// Named freestanding WebAssembly targets reuse Linux/ARM source declarations
// for 32-bit Go layout only. They have no Linux kernel trap ABI, so preserve
// the exact internal syscall signature and fail closed synchronously.
func Syscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr) {
	return ^uintptr(0), 0, enosys
}
