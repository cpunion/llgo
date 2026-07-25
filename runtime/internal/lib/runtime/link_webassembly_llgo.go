//go:build !baremetal && (wasm || tinygo.wasm)

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
	_ "unsafe"
)

// The freestanding WebAssembly core has no implicit process capability.
// Arguments and environment are deliberately empty until an embedding-owned
// ABI publishes them; this prevents the Linux frontend from importing argc,
// argv, environ, setenv, or unsetenv through a nonexistent libc process.

//go:linkname os_runtime_args os.runtime_args
func os_runtime_args() []string {
	return nil
}

//go:linkname syscall_runtime_envs syscall.runtime_envs
func syscall_runtime_envs() []string {
	return nil
}

//llgo:managedlink
//go:linkname syscall_runtimeSetenv syscall.runtimeSetenv
func syscall_runtimeSetenv(key, value string) {
	if key == "GODEBUG" {
		godebugEnvChanged(value)
	}
}

//llgo:managedlink
//go:linkname syscall_runtimeUnsetenv syscall.runtimeUnsetenv
func syscall_runtimeUnsetenv(key string) {
	if key == "GODEBUG" {
		godebugEnvChanged("")
	}
}

//go:linkname os_beforeExit os.runtime_beforeExit
func os_beforeExit(exitCode int) {
	_ = exitCode
}

//go:linkname os_sigpipe os.sigpipe
func os_sigpipe() {}

//go:linkname os_ignoreSIGSYS os.ignoreSIGSYS
func os_ignoreSIGSYS() {}

//go:linkname os_restoreSIGSYS os.restoreSIGSYS
func os_restoreSIGSYS() {}

// WebAssembly 1.0 linear memory has a fixed 64 KiB page. Keep this exact and
// independent from a host libc so allocator and syscall page accounting agree.
//
//go:linkname syscall_Getpagesize syscall.Getpagesize
func syscall_Getpagesize() int {
	return 64 << 10
}

//go:linkname syscall_Exit syscall.Exit
//go:nosplit
func syscall_Exit(code int) {
	_ = code
	c_debugtrap()
	for {
	}
}

//go:linkname syscall_runtime_BeforeFork syscall.runtime_BeforeFork
func syscall_runtime_BeforeFork() {}

//go:linkname syscall_runtime_AfterFork syscall.runtime_AfterFork
func syscall_runtime_AfterFork() {}

//go:linkname syscall_runtime_AfterForkInChild syscall.runtime_AfterForkInChild
func syscall_runtime_AfterForkInChild() {}

//go:linkname syscall_runtime_BeforeExec syscall.runtime_BeforeExec
func syscall_runtime_BeforeExec() {}

//go:linkname syscall_runtime_AfterExec syscall.runtime_AfterExec
func syscall_runtime_AfterExec() {}

// The named freestanding WebAssembly targets use a Linux/arm frontend only
// for Go type/layout selection. HostOp descriptors are already logical
// nonblocking handles and are never inherited by a POSIX process, so the one
// runtime.fcntl query used by os.NewFile reports no descriptor flags. Other
// Linux fcntl commands have no target-neutral meaning at this boundary.
func fcntl(fd int32, cmd int32, arg int32) (int32, int32) {
	const (
		linuxFGetFL = int32(3)
		linuxENOSYS = int32(38)
	)
	_, _ = fd, arg
	if cmd == linuxFGetFL {
		return 0, 0
	}
	return -1, linuxENOSYS
}
