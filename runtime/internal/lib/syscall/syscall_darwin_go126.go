//go:build darwin && go1.26

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

// Go 1.26 routes every fixed Darwin wrapper through variadic syscalln and
// rawsyscalln runtime declarations. LLGo replaces the fixed wrappers instead:
// their scalar call sites can be proven and lowered directly to the common
// coroutine worker operation without retaining a variadic slice across a
// suspension.

//go:linkname llgoSyscall3 llgo.syscall
func llgoSyscall3(fn, a1, a2, a3 uintptr) (r1, r2, err uintptr)

//go:linkname llgoSyscall6 llgo.syscall
func llgoSyscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr)

//go:linkname llgoSyscall9 llgo.syscall
func llgoSyscall9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2, err uintptr)

func syscall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall3(fn, a1, a2, a3)
	return r1, r2, llgoErrno32(r1, errno)
}

func syscallX(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall3(fn, a1, a2, a3)
	return r1, r2, llgoErrnoWord(r1, errno)
}

func syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall6(fn, a1, a2, a3, a4, a5, a6)
	return r1, r2, llgoErrno32(r1, errno)
}

func syscall6X(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall6(fn, a1, a2, a3, a4, a5, a6)
	return r1, r2, llgoErrnoWord(r1, errno)
}

func syscall9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9)
	return r1, r2, llgoErrno32(r1, errno)
}

func rawSyscall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall3(fn, a1, a2, a3)
	return r1, r2, llgoErrno32(r1, errno)
}

func rawSyscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall6(fn, a1, a2, a3, a4, a5, a6)
	return r1, r2, llgoErrno32(r1, errno)
}

func rawSyscall9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9)
	return r1, r2, llgoErrno32(r1, errno)
}

func syscallPtr(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall3(fn, a1, a2, a3)
	if r1 == 0 {
		return r1, r2, stdsyscall.Errno(errno)
	}
	return r1, r2, 0
}

func llgoErrno32(result, errno uintptr) stdsyscall.Errno {
	if int32(result) == -1 {
		return stdsyscall.Errno(errno)
	}
	return 0
}

func llgoErrnoWord(result, errno uintptr) stdsyscall.Errno {
	if result == ^uintptr(0) {
		return stdsyscall.Errno(errno)
	}
	return 0
}
