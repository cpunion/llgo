//go:build !llgo && (darwin || linux) && !baremetal

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
	"reflect"
	"sync/atomic"
	"syscall"
	"unsafe"
)

func nativePipeOpen() ([2]int32, bool) {
	result := [2]int32{invalidFD, invalidFD}
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		return result, false
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	if err := syscall.SetNonblock(fds[0], true); err != nil {
		_ = syscall.Close(fds[0])
		_ = syscall.Close(fds[1])
		return result, false
	}
	if err := syscall.SetNonblock(fds[1], true); err != nil {
		_ = syscall.Close(fds[0])
		_ = syscall.Close(fds[1])
		return result, false
	}
	return [2]int32{int32(fds[0]), int32(fds[1])}, true
}

func nativePipeRead(fd int32, buffer *byte, size uintptr) (int, int32) {
	read, err := syscall.Read(int(fd), unsafe.Slice(buffer, size))
	if err != nil {
		return read, int32(err.(syscall.Errno))
	}
	return read, 0
}

func nativePipeWrite(fd int32, buffer *byte, size uintptr) (int, int32) {
	written, err := syscall.Write(int(fd), unsafe.Slice(buffer, size))
	if err != nil {
		return written, int32(err.(syscall.Errno))
	}
	return written, 0
}

// nativePipePollForWait is a host-test seam. Coroutine runtime targets take
// the direct implementation in pipe_llgo.go and carry no mutable hook.
var nativePipePollForWaitTestHook func(fd int32, timeoutMS int32) (int, int16, int32)

func nativePipePollForWait(fd int32, timeoutMS int32) (int, int16, int32) {
	if hook := nativePipePollForWaitTestHook; hook != nil {
		return hook(fd, timeoutMS)
	}
	return nativePipePoll(fd, timeoutMS)
}

func nativePipeReadSet(fd int32) (syscall.FdSet, bool) {
	var readSet syscall.FdSet
	bits := reflect.ValueOf(&readSet).Elem().FieldByName("Bits")
	if !bits.IsValid() || bits.Kind() != reflect.Array || fd < 0 {
		return syscall.FdSet{}, false
	}
	wordBits := int(bits.Type().Elem().Bits())
	word, bit := int(fd)/wordBits, uint(fd)%uint(wordBits)
	if word < 0 || word >= bits.Len() {
		return syscall.FdSet{}, false
	}
	entry := bits.Index(word)
	entry.SetInt(int64(uint64(entry.Int()) | uint64(1)<<bit))
	return readSet, true
}

func nativePipeReadSetContains(readSet *syscall.FdSet, fd int32) bool {
	if readSet == nil || fd < 0 {
		return false
	}
	bits := reflect.ValueOf(readSet).Elem().FieldByName("Bits")
	if !bits.IsValid() || bits.Kind() != reflect.Array {
		return false
	}
	wordBits := int(bits.Type().Elem().Bits())
	word, bit := int(fd)/wordBits, uint(fd)%uint(wordBits)
	return word >= 0 && word < bits.Len() && uint64(bits.Index(word).Int())&(uint64(1)<<bit) != 0
}

func nativePipeClose(fd int32) bool {
	return syscall.Close(int(fd)) == nil
}

func nativeErrInterrupted(errno int32) bool {
	return syscall.Errno(errno) == syscall.EINTR
}

func nativeErrWouldBlock(errno int32) bool {
	return syscall.Errno(errno) == syscall.EAGAIN || syscall.Errno(errno) == syscall.EWOULDBLOCK
}

func nativeAtomicLoad(value *uint32) uint32 {
	return atomic.LoadUint32(value)
}

func nativeAtomicStore(value *uint32, next uint32) {
	atomic.StoreUint32(value, next)
}

func nativeAtomicExchange(value *uint32, next uint32) uint32 {
	return atomic.SwapUint32(value, next)
}

func nativeAtomicCompareAndSwap(value *uint32, old, next uint32) bool {
	return atomic.CompareAndSwapUint32(value, old, next)
}
