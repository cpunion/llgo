//go:build linux || darwin

package main

import (
	"runtime"
	"runtime/debug"
	"syscall"
	"unsafe"
)

var cleanupSink byte

func faultCopy(dst, src []byte) (n int, err error) {
	defer func() {
		if recovered, ok := recover().(error); ok {
			err = recovered
		}
	}()
	for i := 0; i < len(dst) && i < len(src); i++ {
		dst[i] = src[i]
		n++
	}
	return
}

func faultDuringPanicCleanup(src []byte) (recovered any) {
	defer func() {
		recovered = recover()
	}()
	defer func() {
		cleanupSink = src[0]
	}()
	panic("superseded by cleanup fault")
}

func rejectUnexpectedFault(src []byte) {
	defer func() {
		if recover() != nil {
			println("unexpected non-nil fault recovered")
			syscall.Exit(86)
		}
	}()
	cleanupSink = src[0]
	panic("unexpected non-nil fault returned")
}

func main() {
	old := debug.SetPanicOnFault(true)
	defer debug.SetPanicOnFault(old)

	size := syscall.Getpagesize()
	data, err := syscall.Mmap(-1, 0, 16*size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		panic("mmap failed")
	}
	defer syscall.Munmap(data)

	hole := data[len(data)/2 : 3*(len(data)/4)]
	if syscall.Mprotect(hole, syscall.PROT_NONE) != nil {
		panic("mprotect failed")
	}
	if mode, ok := syscall.Getenv("LLGO_CORO_REJECT_FAULT"); ok && mode == "1" {
		debug.SetPanicOnFault(false)
		rejectUnexpectedFault(hole)
		panic("unexpected non-nil fault did not terminate")
	}

	const offset = 5
	n, err := faultCopy(data[offset:], make([]byte, len(data)))
	if err == nil || n != len(data)/2-offset {
		panic("fault copy did not preserve its named result")
	}
	addressable, ok := err.(interface{ Addr() uintptr })
	if !ok || addressable.Addr() != uintptr(unsafe.Pointer(&data[len(data)/2])) {
		panic("fault copy did not preserve its fault address")
	}
	cleanupSink = 1
	recovered := faultDuringPanicCleanup(hole)
	if recovered == nil {
		panic("cleanup fault was not recovered")
	}
	if _, ok := recovered.(error); !ok {
		panic("cleanup fault did not replace the active panic")
	}
	if cleanupSink != 1 {
		panic("cleanup fault load unexpectedly completed")
	}

	// Force the next physical root resume to begin with panic-on-fault off,
	// then enable it and fault without crossing another suspension boundary.
	// This distinguishes the scheduler's outer landing from the inline-child
	// landing exercised above.
	debug.SetPanicOnFault(false)
	runtime.Gosched()
	debug.SetPanicOnFault(true)
	defer func() {
		if recovered := recover(); recovered == nil {
			panic("direct root fault was not recovered")
		} else if _, ok := recovered.(error); !ok {
			panic("direct root fault did not produce a runtime error")
		}
	}()
	cleanupSink = hole[0]
}
