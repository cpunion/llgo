//go:build !windows

package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

func main() {
	verifySyscallAPI()
	msg := []byte("Hello from Syscall!\n")
	r1, _, err := syscall.Syscall(
		syscall.SYS_WRITE,
		1,
		uintptr(unsafe.Pointer(&msg[0])),
		uintptr(len(msg)),
	)
	// write reports its byte count in r1; the second result is platform-specific.
	runtime.KeepAlive(msg)
	if r1 != uintptr(len(msg)) || err != 0 {
		panic("syscall error")
	}
}
