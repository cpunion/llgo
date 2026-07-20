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

import _ "unsafe"

// This is the current P0 producer-side catalog for the ordinary generated
// Darwin wrappers needed by the synchronous file and TCP probes; it is not a
// complete Darwin syscall catalog. Every declaration
// aliases the same-named bodyless upstream FuncPCABI0 operand to one physical C
// target and publishes its word-call ABI before an address exists. The shared
// syscall/rawSyscall carriers consume only that forward shadow; they never
// recover a target or policy from uintptr.
//
// These fixed libc operations may block but have no caller-thread affinity or
// managed callback. Pointer arguments remain owned by the suspended Go frame
// through worker completion. Using the conservative borrow-until-complete
// memory class uniformly also covers the scalar-only members without granting
// them a stronger capability.
//
// There are intentionally no entries for fork, execve, exit, pthread/host
// thread control, no-return/process-image transitions, or unreviewed generic
// ioctl, kevent, and ptrace operations. Those calls must remain in their
// separately proven raw/plain island or fail closed. Adding a target here
// requires an exact physical symbol and one explicit arity; the sink is not
// allowed to infer either fact.

// Three-word int32-result carrier targets.

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_accept_trampoline C.accept
func libc_accept_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_bind_trampoline C.bind
func libc_bind_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_chmod_trampoline C.chmod
func libc_chmod_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_chown_trampoline C.chown
func libc_chown_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_closedir_trampoline C.closedir
func libc_closedir_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_connect_trampoline C.connect
func libc_connect_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_dup_trampoline C.dup
func libc_dup_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_fchmod_trampoline C.fchmod
func libc_fchmod_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_fchown_trampoline C.fchown
func libc_fchown_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_ftruncate_trampoline C.ftruncate
func libc_ftruncate_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_getcwd_trampoline C.getcwd
func libc_getcwd_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_getpeername_trampoline C.getpeername
func libc_getpeername_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_getsockname_trampoline C.getsockname
func libc_getsockname_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_lchown_trampoline C.lchown
func libc_lchown_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_link_trampoline C.link
func libc_link_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_listen_trampoline C.listen
func libc_listen_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_mkdir_trampoline C.mkdir
func libc_mkdir_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_readlink_trampoline C.readlink
func libc_readlink_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_recvmsg_trampoline C.recvmsg
func libc_recvmsg_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_rename_trampoline C.rename
func libc_rename_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_rmdir_trampoline C.rmdir
func libc_rmdir_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_sendmsg_trampoline C.sendmsg
func libc_sendmsg_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_shutdown_trampoline C.shutdown
func libc_shutdown_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_socket_trampoline C.socket
func libc_socket_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_symlink_trampoline C.symlink
func libc_symlink_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_truncate_trampoline C.truncate
func libc_truncate_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_unlink_trampoline C.unlink
func libc_unlink_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_unlinkat_trampoline C.unlinkat
func libc_unlinkat_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
//go:linkname libc_write_trampoline C.write
func libc_write_trampoline()

// Six-word int32-result carrier targets. The generated Darwin wrappers keep
// pointer arguments rooted until worker completion and pad unused words.

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/6
//go:linkname libc_sendfile_trampoline C.sendfile
func libc_sendfile_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/6
//go:linkname libc_socketpair_trampoline C.socketpair
func libc_socketpair_trampoline()
