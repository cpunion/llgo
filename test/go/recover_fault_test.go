//go:build linux || darwin || windows

// This test uses platform-specific protected-memory helpers.
package gotest

import (
	"runtime/debug"
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

func sameFaultAddress(got uintptr, address unsafe.Pointer) bool {
	return got == uintptr(address)
}

func TestRecoverAfterFaultPreservesNamedResult(t *testing.T) {
	old := debug.SetPanicOnFault(true)
	defer debug.SetPanicOnFault(old)

	const protectedPage, protectedPages = 8, 4
	data, pageSize := protectedMemory(t, 16, protectedPage, protectedPages)
	hole := data[protectedPage*pageSize : (protectedPage+protectedPages)*pageSize]

	const offset = 5
	n, err := faultCopy(data[offset:], make([]byte, len(data)))
	if err == nil {
		t.Fatal("no error from copy across memory hole")
	}
	checkRecoveredFaultAddress(t, err, &data[len(data)/2])
	if want := len(data)/2 - offset; n != want {
		t.Fatalf("copy returned %d, want %d", n, want)
	}
	addressable, ok := err.(interface{ Addr() uintptr })
	if !ok {
		t.Fatalf("fault has type %T without Addr method", err)
	}
	got := addressable.Addr()
	if address := unsafe.Pointer(&data[len(data)/2]); !sameFaultAddress(got, address) {
		t.Fatalf("fault address = %#x, want %p", got, address)
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
