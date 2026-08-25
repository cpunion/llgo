//go:build darwin && arm64 && go1.26

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

// Go 1.26 Darwin arm64 publishes the stat family without the legacy 64
// suffix. Keep these exact producer names out of the common catalog: amd64's
// FuncPCABI0 operands and physical C symbols are different identities.

//go:linkname libc_fstat_trampoline C.fstat
func libc_fstat_trampoline()

//go:linkname libc_lstat_trampoline C.lstat
func libc_lstat_trampoline()

//go:linkname libc_stat_trampoline C.stat
func libc_stat_trampoline()

//go:linkname libc_fstatat_trampoline C.fstatat
func libc_fstatat_trampoline()
