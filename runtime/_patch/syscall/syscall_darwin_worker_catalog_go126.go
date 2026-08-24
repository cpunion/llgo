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
// complete Darwin syscall catalog. Every declaration aliases the same-named
// bodyless upstream FuncPCABI0 operand to one physical C target. The compiler
// carries that identity forward and derives its word-call ABI from the exact
// syscall/rawSyscall sink; it never recovers a target or policy from uintptr.
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
// requires an exact physical symbol. Every active sink must derive the same
// arity or the producer fails closed.

// Three-word int32-result carrier targets.

//go:linkname libc_accept_trampoline C.accept
func libc_accept_trampoline()

//go:linkname libc_bind_trampoline C.bind
func libc_bind_trampoline()

// chdir changes process-wide state but has no caller-thread affinity. Once the
// host operation starts cancellation is logical-only; the pathname remains
// rooted by the suspended wrapper until worker completion.
//
//go:linkname libc_chdir_trampoline C.chdir
func libc_chdir_trampoline()

//go:linkname libc_chmod_trampoline C.chmod
func libc_chmod_trampoline()

//go:linkname libc_chown_trampoline C.chown
func libc_chown_trampoline()

//go:linkname libc_closedir_trampoline C.closedir
func libc_closedir_trampoline()

//go:linkname libc_connect_trampoline C.connect
func libc_connect_trampoline()

//go:linkname libc_dup_trampoline C.dup
func libc_dup_trampoline()

//go:linkname libc_fchmod_trampoline C.fchmod
func libc_fchmod_trampoline()

//go:linkname libc_fchown_trampoline C.fchown
func libc_fchown_trampoline()

//go:linkname libc_ftruncate_trampoline C.ftruncate
func libc_ftruncate_trampoline()

//go:linkname libc_getcwd_trampoline C.getcwd
func libc_getcwd_trampoline()

// getpid is executor-safe on Darwin, but the current FuncPCABI0 word-call
// carrier has no target-specific inline operation recipe. Route this exact
// producer through the bounded worker contract for now so the generic
// rawSyscall carrier cannot block or acquire unchecked address authority on
// the executor. A future executor-safe word-call recipe may specialize this
// occurrence without changing the producer-forward identity.
//
//go:linkname libc_getpid_trampoline C.getpid
func libc_getpid_trampoline()

//go:linkname libc_getrusage_trampoline C.getrusage
func libc_getrusage_trampoline()

//go:linkname libc_getpeername_trampoline C.getpeername
func libc_getpeername_trampoline()

//go:linkname libc_getsockname_trampoline C.getsockname
func libc_getsockname_trampoline()

//go:linkname libc_lchown_trampoline C.lchown
func libc_lchown_trampoline()

// kill is a process-wide, irreversible host operation but is not bound to the
// executor thread and does not reenter managed Go. The fixed three-word
// transport therefore uses the common worker completion path.
//
//go:linkname libc_kill_trampoline C.kill
func libc_kill_trampoline()

//go:linkname libc_link_trampoline C.link
func libc_link_trampoline()

//go:linkname libc_listen_trampoline C.listen
func libc_listen_trampoline()

//go:linkname libc_mkdir_trampoline C.mkdir
func libc_mkdir_trampoline()

// mprotect changes process VM metadata for a caller-owned mapped slice. It has
// no executor-thread affinity or managed callback, while the slice data remains
// rooted by the suspended generated wrapper through worker completion.
//
//go:linkname libc_mprotect_trampoline C.mprotect
func libc_mprotect_trampoline()

//go:linkname libc_pipe_trampoline C.pipe
func libc_pipe_trampoline()

//go:linkname libc_readlink_trampoline C.readlink
func libc_readlink_trampoline()

//go:linkname libc_recvmsg_trampoline C.recvmsg
func libc_recvmsg_trampoline()

//go:linkname libc_rename_trampoline C.rename
func libc_rename_trampoline()

//go:linkname libc_rmdir_trampoline C.rmdir
func libc_rmdir_trampoline()

//go:linkname libc_sendmsg_trampoline C.sendmsg
func libc_sendmsg_trampoline()

//go:linkname libc_shutdown_trampoline C.shutdown
func libc_shutdown_trampoline()

//go:linkname libc_socket_trampoline C.socket
func libc_socket_trampoline()

//go:linkname libc_symlink_trampoline C.symlink
func libc_symlink_trampoline()

//go:linkname libc_truncate_trampoline C.truncate
func libc_truncate_trampoline()

//go:linkname libc_unlink_trampoline C.unlink
func libc_unlink_trampoline()

//go:linkname libc_unlinkat_trampoline C.unlinkat
func libc_unlinkat_trampoline()

//go:linkname libc_write_trampoline C.write
func libc_write_trampoline()

// Six-word int32-result carrier targets. The generated Darwin wrappers keep
// pointer arguments rooted until worker completion and pad unused words.

//go:linkname libc_sendfile_trampoline C.sendfile
func libc_sendfile_trampoline()

//go:linkname libc_socketpair_trampoline C.socketpair
func libc_socketpair_trampoline()
