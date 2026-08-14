//go:build llgo && (darwin || linux) && !baremetal

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

package corodoorbell

import (
	_ "unsafe"

	catomic "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
	csyscall "github.com/goplus/llgo/runtime/internal/clite/syscall"
)

const (
	// Keep the coroutine doorbell's contextual libc capability in this package.
	// The C leaf validates bounded shapes before it touches a descriptor;
	// generic clite/os Pipe, Read, Write, Poll, and Close remain uncertified.
	LLGoFiles   = "_wrap/doorbell.c"
	LLGoPackage = "link"
)

type nativePollFD struct {
	fd      int32
	events  int16
	revents int16
}

// nativeCDoorbellOpen is a fixed startup leaf. A successful return proves that
// both descriptors are O_NONBLOCK and FD_CLOEXEC before Go can publish them.
// Its C implementation has bounded EINTR retries and closes partial state.
// Every live call is owned by the compiler-verified scheduler raw-host closure;
// the declaration itself publishes no managed executor-safe capability.
//
//go:linkname nativeCDoorbellOpen C.__llgo_coro_doorbell_open_v1
func nativeCDoorbellOpen(fds *[2]int32) int32

// nativeCDoorbellRead performs at most one <=64-byte read from the private
// nonblocking read end. The C leaf rejects every wider request.
// Its exact raw-host use domain is frozen before physical lowering.
//
//go:linkname nativeCDoorbellRead C.__llgo_coro_doorbell_read_v1
func nativeCDoorbellRead(fd int32, buffer *byte, size uintptr) uint64

// nativeCDoorbellWrite performs exactly one one-byte write to the private
// nonblocking write end. EAGAIN is returned to the retained-wake protocol.
// The descriptor capability is established by nativeCDoorbellOpen before the
// Pipe is published, while this declaration supplies the one fact unavailable
// from its C type: the exact call cannot block an executor.
//
//llgo:coro contract foreign.v1 scope=declaration progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return
//go:linkname nativeCDoorbellWrite C.__llgo_coro_doorbell_write_v1
func nativeCDoorbellWrite(fd int32, buffer *byte, size uintptr) uint64

// nativeCDoorbellClose closes only a descriptor whose ownership has already
// been sealed and removed from the published Pipe. It never retries close.
// Its exact raw-host use domain is frozen before physical lowering.
//
//go:linkname nativeCDoorbellClose C.__llgo_coro_doorbell_close_v1
func nativeCDoorbellClose(fd int32) int32

// A packed result keeps errno capture inside the same exact C leaf. The low
// word is a signed syscall result and the high word is zero or positive errno.
func unpackNativeDoorbellResult(packed uint64) (int, int32) {
	return int(int32(uint32(packed))), int32(uint32(packed >> 32))
}

func nativePipeOpen() ([2]int32, bool) {
	result := [2]int32{invalidFD, invalidFD}
	var fds [2]int32
	if nativeCDoorbellOpen(&fds) == 0 {
		return result, false
	}
	return fds, true
}

func nativePipeRead(fd int32, buffer *byte, size uintptr) (int, int32) {
	return unpackNativeDoorbellResult(nativeCDoorbellRead(fd, buffer, size))
}

func nativePipeWrite(fd int32, buffer *byte, size uintptr) (int, int32) {
	return unpackNativeDoorbellResult(nativeCDoorbellWrite(fd, buffer, size))
}

func nativePipePollForWait(fd int32, timeoutMS int32) (int, int16, int32) {
	return nativePipePoll(fd, timeoutMS)
}

func nativePipeClose(fd int32) bool {
	return nativeCDoorbellClose(fd) != 0
}

func nativeErrInterrupted(errno int32) bool {
	return int(errno) == int(csyscall.EINTR)
}

func nativeErrWouldBlock(errno int32) bool {
	return int(errno) == int(csyscall.EAGAIN) || int(errno) == int(csyscall.EWOULDBLOCK)
}

func nativeAtomicLoad(value *uint32) uint32 {
	return catomic.Load(value)
}

func nativeAtomicStore(value *uint32, next uint32) {
	catomic.Store(value, next)
}

func nativeAtomicExchange(value *uint32, next uint32) uint32 {
	return catomic.Exchange(value, next)
}

func nativeAtomicCompareAndSwap(value *uint32, old, next uint32) bool {
	_, swapped := catomic.CompareAndExchange(value, old, next)
	return swapped
}
