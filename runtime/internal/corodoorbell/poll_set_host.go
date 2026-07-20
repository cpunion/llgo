//go:build !llgo && (darwin || linux) && !baremetal

package corodoorbell

import (
	"syscall"
	"unsafe"
)

func nativePollSet(first *PollFD, count uint32, timeoutMS int32) (int, int32) {
	result, _, errno := syscall.Syscall(
		syscall.SYS_POLL,
		uintptr(unsafe.Pointer(first)),
		uintptr(count),
		uintptr(uint32(timeoutMS)),
	)
	if errno != 0 {
		return -1, int32(errno)
	}
	return int(result), 0
}
