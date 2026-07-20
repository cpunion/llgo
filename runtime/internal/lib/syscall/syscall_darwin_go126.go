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

const LLGoPackage = true

//go:linkname llgoDarwinFuncPCABI0 llgo.funcPCABI0
func llgoDarwinFuncPCABI0(fn any) uintptr

// Go 1.26 routes every fixed Darwin wrapper through variadic syscalln and
// rawsyscalln runtime declarations. LLGo replaces the fixed wrappers instead:
// their scalar call sites can be proven and lowered directly to the common
// coroutine worker operation without retaining a variadic slice across a
// suspension.

// This alternate declaration is exact capability metadata for the upstream
// zsyscall FuncPCABI0 operand of the same name. Patch selection aliases that
// original declaration to this worker-safe C target. Targets with process- or
// thread-affine semantics (fork, exec, pthread operations, and unknown code
// words) intentionally have no corresponding declaration and remain closed.
//
//llgo:coro workeraddr 3
//go:linkname libc_getrlimit_trampoline C.getrlimit
func libc_getrlimit_trampoline()

// syscall.init may raise RLIMIT_NOFILE immediately after Getrlimit. This is
// the matching fixed-target capability; fork/exec and other thread-affine
// trampolines remain deliberately absent.
//
//llgo:coro workeraddr 3
//go:linkname libc_setrlimit_trampoline C.setrlimit
func libc_setrlimit_trampoline()

// syscall.adjustFileLimit queries kern.maxfilesperproc through the fixed
// six-word sysctl ABI during package initialization.
//
//llgo:coro workeraddr 6
//go:linkname libc_sysctl_trampoline C.sysctl
func libc_sysctl_trampoline()

// Opening a regular file may block on the filesystem. The Go 1.26 time zone
// loader reaches this exact three-word libc target through syscall.Open; keep
// the target explicit so the private syscall(fn, ...) carrier can be colored
// only when every active incoming function word has a worker capability.
//
//llgo:coro workeraddr 3
//go:linkname libc_open_trampoline C.llgo_open
func libc_open_trampoline()

// Closing the time-zone file shares the fixed three-word carrier used by the
// generated Darwin syscall wrapper. It has no thread-affine semantics, and
// moving it to a worker also prevents a slow device close from blocking the
// executor owner.
//
//llgo:coro workeraddr 3
//go:linkname libc_close_trampoline C.close
func libc_close_trampoline()

// Reading time-zone data may block on the filesystem. The buffer address is
// retained by the suspended managed caller's exact uintptr keepalive proof
// until this fixed worker operation completes.
//
//llgo:coro workeraddr 3
//go:linkname libc_read_trampoline C.read
func libc_read_trampoline()

// The time zone zip reader seeks within a regular file before issuing reads.
// lseek uses the word-width failure convention but the same three-word worker
// transport; capability remains attached to this exact physical target.
//
//llgo:coro workeraddr 3
//go:linkname libc_lseek_trampoline C.lseek
func libc_lseek_trampoline()

// syscall.mmap is the sole Darwin caller of the word-width six-argument
// wrapper. mmap may block in the host VM, but its fixed target and by-value
// arguments/results satisfy the worker transport contract.
//
//llgo:coro workeraddr 6
//go:linkname libc_mmap_trampoline C.mmap
func libc_mmap_trampoline()

// Releasing an mmap region is the matching fixed three-word VM operation. It
// is process-wide rather than thread-affine and may be serialized by the host,
// so it uses the same worker executor boundary as mmap.
//
//llgo:coro workeraddr 3
//go:linkname libc_munmap_trampoline C.munmap
func libc_munmap_trampoline()

// fdopendir returns an opaque libc DIR pointer encoded by syscallPtr as a
// uintptr word. The pointee is C-owned, so worker completion does not create
// an unrooted Go pointer interval; the standard wrapper reconstructs the
// pointer only after the suspended caller resumes.
//
//llgo:coro workeraddr 3
//go:linkname libc_fdopendir_trampoline C.fdopendir
func libc_fdopendir_trampoline()

// writev shares syscallX's word-width result convention. The iovec slice and
// every pointer-bearing element remain reachable through the suspended
// caller's exact keepalive roots under the current conservative/nonmoving (or
// nogc) coroutine frame profile.
//
//llgo:coro workeraddr 3
//go:linkname libc_writev_trampoline C.writev
func libc_writev_trampoline()

// fcntl is variadic in libc; the LLGo C shim fixes its third word and is also
// used by runtime's poll descriptor setup through this single physical target.
//
//llgo:coro workeraddr 3
//go:linkname libc_fcntl_trampoline C.llgo_fcntl
func libc_fcntl_trampoline()

// fsync may wait for durable storage and therefore belongs on the worker
// boundary even though only its descriptor word is meaningful.
//
//llgo:coro workeraddr 3
//go:linkname libc_fsync_trampoline C.fsync
func libc_fsync_trampoline()

// fchdir changes process-wide state but is not bound to the executor thread.
// The host operation is irreversible once started, so coroutine cancellation
// only discards its eventual completion; the fixed descriptor call itself can
// run on the common worker without blocking the scheduler owner.
//
//llgo:coro workeraddr 3
//go:linkname libc_fchdir_trampoline C.fchdir
func libc_fchdir_trampoline()

// Positional file I/O uses the generated six-word Darwin carrier even though
// libc consumes only four logical arguments on the supported 64-bit targets.
// Both operations are process-wide, may block on storage, and retain their
// buffer through the suspended caller's uintptr keepalive proof.
//
//llgo:coro workeraddr 6
//go:linkname libc_pread_trampoline C.pread
func libc_pread_trampoline()

//llgo:coro workeraddr 6
//go:linkname libc_pwrite_trampoline C.pwrite
func libc_pwrite_trampoline()

// openat is variadic in libc. LLGo's fixed C shim supplies the mode argument
// with the exact word widths expected by the generated six-word wrapper.
//
//llgo:coro workeraddr 6
//go:linkname libc_openat_trampoline C.llgo_openat
func libc_openat_trampoline()

// Socket option accessors are fixed six-word operations. Their option value
// and length pointers remain owned by the suspended caller, so both file-side
// descriptor setup and the TCP poll path share the common worker completion.
//
//llgo:coro workeraddr 6
//go:linkname libc_getsockopt_trampoline C.getsockopt
func libc_getsockopt_trampoline()

//llgo:coro workeraddr 6
//go:linkname libc_setsockopt_trampoline C.setsockopt
func libc_setsockopt_trampoline()

// Updating path-relative timestamps is another generated six-word filesystem
// operation; the pathname and Timespec array remain live in the coroutine
// frame while the worker owns the call.
//
//llgo:coro workeraddr 6
//go:linkname libc_utimensat_trampoline C.utimensat
func libc_utimensat_trampoline()

// Datagram send/receive use the full six-word socket ABI. Buffer and sockaddr
// storage remain rooted by the suspended caller for the entire worker lease.
//
//llgo:coro workeraddr 6
//go:linkname libc_sendto_trampoline C.sendto
func libc_sendto_trampoline()

//llgo:coro workeraddr 6
//go:linkname libc_recvfrom_trampoline C.recvfrom
func libc_recvfrom_trampoline()

// wait4 may block until a child changes state but has no executor-thread
// affinity. Cancellation is logical-only: the worker operation is retired
// after its irreversible host completion, matching the scheduler contract.
//
//llgo:coro workeraddr 6
//go:linkname libc_wait4_trampoline C.wait4
func libc_wait4_trampoline()

//go:linkname llgoSyscall3Int32 llgo.syscall32
func llgoSyscall3Int32(fn, a1, a2, a3 uintptr) (r1, r2, err uintptr)

//go:linkname llgoSyscall6Int32 llgo.syscall32
func llgoSyscall6Int32(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr)

//go:linkname llgoSyscall9Int32 llgo.syscall32
func llgoSyscall9Int32(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2, err uintptr)

//go:linkname llgoSyscall3Word llgo.syscall
func llgoSyscall3Word(fn, a1, a2, a3 uintptr) (r1, r2, err uintptr)

//go:linkname llgoSyscall6Word llgo.syscall
func llgoSyscall6Word(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr)

//go:linkname llgoSyscall3Pointer llgo.syscallPtr
func llgoSyscall3Pointer(fn, a1, a2, a3 uintptr) (r1, r2, err uintptr)

//llgo:coro workerresult v1 fn=0 map=r1:r1
func syscall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall3Int32(fn, a1, a2, a3)
	return r1, r2, llgoErrno32(r1, errno)
}

func syscallX(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall3Word(fn, a1, a2, a3)
	return r1, r2, llgoErrnoWord(r1, errno)
}

func syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall6Int32(fn, a1, a2, a3, a4, a5, a6)
	return r1, r2, llgoErrno32(r1, errno)
}

func syscall6X(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall6Word(fn, a1, a2, a3, a4, a5, a6)
	return r1, r2, llgoErrnoWord(r1, errno)
}

func syscall9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall9Int32(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9)
	return r1, r2, llgoErrno32(r1, errno)
}

func rawSyscall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall3Int32(fn, a1, a2, a3)
	return r1, r2, llgoErrno32(r1, errno)
}

func rawSyscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall6Int32(fn, a1, a2, a3, a4, a5, a6)
	return r1, r2, llgoErrno32(r1, errno)
}

func rawSyscall9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall9Int32(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9)
	return r1, r2, llgoErrno32(r1, errno)
}

func syscallPtr(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	r1, r2, errno := llgoSyscall3Pointer(fn, a1, a2, a3)
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

// llgoRuntimeFcntl is the typed, fixed-target bridge used by runtime's poll
// descriptor setup. Keeping FuncPCABI0 and llgo.syscall32 in this package
// avoids publishing another same-symbol intrinsic declaration into runtime's
// alternate package, where linkname alias selection would erase the fixed
// target provenance before coroutine analysis.
func llgoRuntimeFcntl(fd, cmd, arg int32) (result, errno int32) {
	r1, _, errnoWord := llgoSyscall3Int32(
		llgoDarwinFuncPCABI0(libc_fcntl_trampoline),
		uintptr(int64(fd)),
		uintptr(int64(cmd)),
		uintptr(int64(arg)),
	)
	if uint32(r1) == ^uint32(0) {
		return -1, int32(errnoWord)
	}
	return int32(r1), 0
}
