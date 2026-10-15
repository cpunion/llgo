package main

import (
	"runtime"
	"syscall/js"
	_ "unsafe"
)

// This tests the private platform primitive, not public syscall.Open delivery.
//
//go:linkname openInto syscall/js.llgoFSOpenInto
func openInto(output *int32, path string, flags int, perm uint32) js.Value

//go:linkname fsCall syscall/js.llgoFSCall
func fsCall(name string, args ...any) (js.Value, js.Value)

type fdGuard struct{ fd int32 }

func (guard *fdGuard) close() {
	if guard.fd >= 0 {
		fd := guard.fd
		guard.fd = -1 // A failed or ambiguous close is never retried.
		_, failure := fsCall("close", fd)
		if !failure.IsNull() {
			panic("borrowed JS descriptor close failed")
		}
	}
}

func mark(event int) { js.Global().Call("llgoBorrowedMark", event) }
func state() int     { return js.Global().Call("llgoBorrowedState").Int() }

func acquire(path string, mode int) {
	guard := fdGuard{fd: -1}
	defer guard.close() // Installed before the host can produce a descriptor.
	failure := openInto(&guard.fd, path, 0, 0)
	if mode == 3 {
		if failure.IsNull() || guard.fd != -1 || failure.Get("code").String() != "ENOENT" {
			panic("borrowed JS failed open acquired a resource")
		}
		return
	}
	if !failure.IsNull() || guard.fd < 0 {
		panic("borrowed JS descriptor acquisition failed")
	}
	if mode == 1 {
		panic("canceled borrowed JS acquire resumed normally")
	}
	// A second real host await retains the same pre-registered ordinary guard.
	if _, failure := fsCall("fstat", guard.fd); !failure.IsNull() {
		panic("borrowed JS fstat failed")
	}
	if mode == 0 {
		return
	}
	mark(2)
	var never chan struct{}
	<-never
	panic("canceled borrowed JS owner resumed normally")
}

func cleanupSentinel(ready chan<- struct{}) {
	defer func() {
		mark(1) // Only shutdown cleanup permits the early open to complete.
		for state()&12 != 12 {
			runtime.Gosched()
		}
		for i := 0; i < 8; i++ {
			mark(3) // Both physical closes remain blocked while Go progresses.
			runtime.Gosched()
		}
		mark(4)
	}()
	ready <- struct{}{}
	for {
		runtime.Gosched()
	}
}

func main() {
	base := js.Global().Get("llgoBorrowedPath").String()
	acquire(base+"/missing", 3)
	for i := 0; i < 4; i++ {
		acquire(base+"/normal", 0)
	}
	ready := make(chan struct{})
	go cleanupSentinel(ready)
	<-ready
	go acquire(base+"/early", 1)
	go acquire(base+"/late", 2)
	for state()&3 != 3 {
		runtime.Gosched()
	}
	runtime.GC()
	mark(5)
	// The runner checks physical joins and all FDs before emitting success.
}
