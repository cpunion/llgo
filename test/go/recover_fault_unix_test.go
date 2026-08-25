//go:build linux || darwin

package gotest

import (
	"runtime/debug"
	"syscall"
	"testing"
	"unsafe"
)

var faultCleanupSink byte

func faultCopy(dst, src []byte) (n int, err error) {
	defer func() {
		if r, ok := recover().(error); ok {
			err = r
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
		faultCleanupSink = src[0]
	}()
	panic("superseded by cleanup fault")
}

func TestRecoverAfterFaultPreservesNamedResult(t *testing.T) {
	old := debug.SetPanicOnFault(true)
	defer debug.SetPanicOnFault(old)

	size := syscall.Getpagesize()
	data, err := syscall.Mmap(-1, 0, 16*size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Fatalf("mmap: %v", err)
	}
	defer syscall.Munmap(data)

	hole := data[len(data)/2 : 3*(len(data)/4)]
	if err := syscall.Mprotect(hole, syscall.PROT_NONE); err != nil {
		t.Fatalf("mprotect: %v", err)
	}

	const offset = 5
	n, err := faultCopy(data[offset:], make([]byte, len(data)))
	if err == nil {
		t.Fatal("no error from copy across memory hole")
	}
	if want := len(data)/2 - offset; n != want {
		t.Fatalf("copy returned %d, want %d", n, want)
	}
	addressable, ok := err.(interface{ Addr() uintptr })
	wantAddress := uintptr(unsafe.Pointer(&data[len(data)/2]))
	if !ok || addressable.Addr() != wantAddress {
		t.Fatalf("fault address = (%#x, %t), want %#x", func() uintptr {
			if !ok {
				return 0
			}
			return addressable.Addr()
		}(), ok, wantAddress)
	}
	faultCleanupSink = 1
	if recovered := faultDuringPanicCleanup(hole); recovered == nil {
		t.Fatal("fault during panic cleanup was not recovered")
	} else if _, ok := recovered.(error); !ok {
		t.Fatalf("cleanup fault recovered %T, want runtime error", recovered)
	}
	if faultCleanupSink != 1 {
		t.Fatalf("cleanup fault load unexpectedly completed: sink=%d", faultCleanupSink)
	}
}
