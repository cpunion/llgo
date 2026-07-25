//go:build linux && !tinygo.wasm

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

import (
	stdsyscall "syscall"
	_ "unsafe"
)

const (
	LLGoPackage = true
	LLGoFiles   = "_wrap/syscall_linux.c"
)

// The Linux kernel ABI starts with a syscall number rather than a callable
// function address. These fixed C leaves supply the typed word-call half of a
// worker capability. The compiler grants the other half only when the trap is
// an exact constant accepted by the target-owned Linux trap policy on every
// active managed incoming edge. A dynamic/process-control path therefore
// remains synchronous only in a proven raw/plain body and fails closed if it
// reaches a managed coroutine.
//
//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/4
//go:linkname libc___llgo_linux_syscall3_v1_trampoline C.__llgo_linux_syscall3_v1
func libc___llgo_linux_syscall3_v1_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/7
//go:linkname libc___llgo_linux_syscall6_v1_trampoline C.__llgo_linux_syscall6_v1
func libc___llgo_linux_syscall6_v1_trampoline()

//go:linkname llgoLinuxFuncPCABI0 llgo.funcPCABI0
func llgoLinuxFuncPCABI0(fn any) uintptr

//go:linkname llgoLinuxSyscall4 llgo.syscall
func llgoLinuxSyscall4(fn, trap, a1, a2, a3 uintptr) (r1, r2, errno uintptr)

//go:linkname llgoLinuxSyscall7 llgo.syscall
func llgoLinuxSyscall7(fn, trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr)

func linuxSyscallErrno(r1, errno uintptr) stdsyscall.Errno {
	if r1 == ^uintptr(0) {
		return stdsyscall.Errno(errno)
	}
	return 0
}

// Syscall and Syscall6 preserve the ordinary synchronous Go API. In a proven
// plain body llgo.syscall calls the fixed C adapter directly, so no scheduler
// or process-global current-G lookup is introduced. In a managed coroutine,
// ProgramIR requires both this adapter contract and an exact target-specific
// constant-trap capability before replacing the call with a worker park.
func Syscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoLinuxSyscall4(
		llgoLinuxFuncPCABI0(libc___llgo_linux_syscall3_v1_trampoline),
		trap, a1, a2, a3,
	)
	return r1, r2, linuxSyscallErrno(r1, errno)
}

func Syscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoLinuxSyscall7(
		llgoLinuxFuncPCABI0(libc___llgo_linux_syscall6_v1_trampoline),
		trap, a1, a2, a3, a4, a5, a6,
	)
	return r1, r2, linuxSyscallErrno(r1, errno)
}

// RawSyscall deliberately has no entersyscall/exitsyscall hooks. It remains a
// direct current-thread call when reached from a proven plain context (for
// example a post-fork raw path). Dynamic, fork, exec, exit, and other
// process-control trap numbers have no worker certificate; an ordinary
// constant file/network/resource trap can still park transparently when all
// active managed incoming edges carry the same target-owned proof.
func RawSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoLinuxSyscall4(
		llgoLinuxFuncPCABI0(libc___llgo_linux_syscall3_v1_trampoline),
		trap, a1, a2, a3,
	)
	return r1, r2, linuxSyscallErrno(r1, errno)
}

func RawSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoLinuxSyscall7(
		llgoLinuxFuncPCABI0(libc___llgo_linux_syscall6_v1_trampoline),
		trap, a1, a2, a3, a4, a5, a6,
	)
	return r1, r2, linuxSyscallErrno(r1, errno)
}
