//go:build !llgo && linux && !baremetal

package corodoorbell

import (
	"syscall"
	"unsafe"
)

func nativePollSet(first *PollFD, count uint32, timeoutMS int32) (int, int32) {
	return nativePollSetNano(first, count, int64(timeoutMS)*deadlineNanosPerMilli)
}

func nativePollSetNano(first *PollFD, count uint32, timeoutNS int64) (int, int32) {
	timeout := syscall.NsecToTimespec(timeoutNS)
	result, _, errno := syscall.Syscall6(
		syscall.SYS_PPOLL,
		uintptr(unsafe.Pointer(first)),
		uintptr(count),
		uintptr(unsafe.Pointer(&timeout)),
		0,
		0,
		0,
	)
	if errno != 0 {
		return -1, int32(errno)
	}
	return int(result), 0
}
