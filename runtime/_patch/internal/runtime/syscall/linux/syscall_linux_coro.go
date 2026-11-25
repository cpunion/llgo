//go:build linux && !tinygo.wasm && !baremetal

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

import _ "unsafe"

//llgo:skip Syscall6

// Native Linux internal runtime syscalls reuse the fixed C adapter linked by
// runtime's native LLGoFiles manifest instead of translating raw SYSCALL
// Plan 9 assembly, so ProgramIR can certify exact constant traps (such as
// cgroup Open/Close/Read/Pread) without leaking OpaqueSuspend into callers.
//
//go:linkname libc___llgo_linux_syscall6_v1_trampoline C.__llgo_linux_syscall6_v1
func libc___llgo_linux_syscall6_v1_trampoline()

//go:linkname llgoLinuxFuncPCABI0 llgo.funcPCABI0
func llgoLinuxFuncPCABI0(fn any) uintptr

//go:linkname llgoLinuxSyscall7 llgo.syscall
func llgoLinuxSyscall7(fn, trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr)

func Syscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr) {
	r1, r2, errno = llgoLinuxSyscall7(
		llgoLinuxFuncPCABI0(libc___llgo_linux_syscall6_v1_trampoline),
		num, a1, a2, a3, a4, a5, a6,
	)
	if r1 == ^uintptr(0) {
		return ^uintptr(0), 0, errno
	}
	return r1, r2, 0
}
