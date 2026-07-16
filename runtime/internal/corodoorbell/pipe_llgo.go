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
	"unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
	cliteos "github.com/goplus/llgo/runtime/internal/clite/os"
	catomic "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
	csyscall "github.com/goplus/llgo/runtime/internal/clite/syscall"
)

type nativePollFD struct {
	fd      c.Int
	events  int16
	revents int16
}

func nativeFcntl(fd c.Int, get, set, flag c.Int) bool {
	for {
		current := cliteos.Fcntl(fd, get)
		if current < 0 {
			if int(nativeErrno()) == int(csyscall.EINTR) {
				continue
			}
			return false
		}
		for {
			if cliteos.Fcntl(fd, set, current|flag) >= 0 {
				return true
			}
			if int(nativeErrno()) != int(csyscall.EINTR) {
				return false
			}
		}
	}
}

func nativePipeOpen() ([2]int32, bool) {
	result := [2]int32{invalidFD, invalidFD}
	var fds [2]c.Int
	for {
		if cliteos.Pipe(&fds) == 0 {
			break
		}
		if int(nativeErrno()) != int(csyscall.EINTR) {
			return result, false
		}
	}
	ok := nativeFcntl(fds[0], c.Int(csyscall.F_GETFD), c.Int(csyscall.F_SETFD), c.Int(csyscall.FD_CLOEXEC)) &&
		nativeFcntl(fds[1], c.Int(csyscall.F_GETFD), c.Int(csyscall.F_SETFD), c.Int(csyscall.FD_CLOEXEC)) &&
		nativeFcntl(fds[0], c.Int(csyscall.F_GETFL), c.Int(csyscall.F_SETFL), c.Int(csyscall.O_NONBLOCK)) &&
		nativeFcntl(fds[1], c.Int(csyscall.F_GETFL), c.Int(csyscall.F_SETFL), c.Int(csyscall.O_NONBLOCK))
	if !ok {
		_ = cliteos.Close(fds[0])
		_ = cliteos.Close(fds[1])
		return result, false
	}
	return [2]int32{int32(fds[0]), int32(fds[1])}, true
}

func nativePipeRead(fd int32, buffer *byte, size uintptr) (int, int32) {
	result := cliteos.Read(c.Int(fd), unsafe.Pointer(buffer), size)
	if result < 0 {
		return result, nativeErrno()
	}
	return result, 0
}

func nativePipeWrite(fd int32, buffer *byte, size uintptr) (int, int32) {
	result := cliteos.Write(c.Int(fd), unsafe.Pointer(buffer), size)
	if result < 0 {
		return result, nativeErrno()
	}
	return result, 0
}

func nativePipeClose(fd int32) bool {
	return cliteos.Close(c.Int(fd)) == 0
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
