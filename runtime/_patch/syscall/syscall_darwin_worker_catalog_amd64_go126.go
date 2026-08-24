//go:build darwin && amd64 && go1.26

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

package syscall

import _ "unsafe"

// Go 1.26 Darwin amd64 retains distinct stat64 producer and C symbol names.
// These declarations must match those upstream names exactly; treating the
// arm64 spelling as common would create an unused patch-private capability and
// leave the real amd64 FuncPCABI0 producer uncertified.

//go:linkname libc_fstat64_trampoline C.fstat64
func libc_fstat64_trampoline()

//go:linkname libc_lstat64_trampoline C.lstat64
func libc_lstat64_trampoline()

//go:linkname libc_stat64_trampoline C.stat64
func libc_stat64_trampoline()

//go:linkname libc_fstatat64_trampoline C.fstatat64
func libc_fstatat64_trampoline()
