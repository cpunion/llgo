//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && darwin && go1.26 && !baremetal && !coro_runtime_adapter_test

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

import "unsafe"

// fork, execve, and exit have process/thread semantics which cannot be
// represented by the bounded arbitrary-thread worker contract. Keep their
// exact source wrappers, but call exact typed C declarations instead of the
// shared function-word rawSyscall carrier used by ordinary file and socket
// targets. Otherwise one uncertified process target would invalidate the
// producer-forward worker certificate for every safe target that shares
// rawSyscall. Compiler-owned typed control operations preserve exact
// current-thread semantics without declaration-wide foreign contracts.
//
//llgo:skip fork execve exit

//go:linkname llgoCoroFork llgo.controlFork
func llgoCoroFork() int32

//go:linkname llgoCoroExecve llgo.controlExecve
func llgoCoroExecve(path *int8, argv **int8, envp **int8) int32

//go:linkname llgoCoroExit llgo.controlExit
func llgoCoroExit(res int32)

//go:linkname llgoCoroProcessErrno C.cliteErrno
func llgoCoroProcessErrno() int32

func fork() (pid int, err error) {
	result := llgoCoroFork()
	pid = int(result)
	if result == -1 {
		err = errnoErr(Errno(llgoCoroProcessErrno()))
	}
	return
}

func execve(path *byte, argv **byte, envp **byte) (err error) {
	if llgoCoroExecve(
		(*int8)(unsafe.Pointer(path)),
		(**int8)(unsafe.Pointer(argv)),
		(**int8)(unsafe.Pointer(envp)),
	) == -1 {
		err = errnoErr(Errno(llgoCoroProcessErrno()))
	}
	return
}

func exit(res int) (err error) {
	llgoCoroExit(int32(res))
	return
}
