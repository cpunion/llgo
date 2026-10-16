//go:build darwin || linux

package main

import (
	"runtime"
	"sync"
	_ "unsafe"
)

const LLGoFiles = "wrap/resource.c"

// The producer borrows only this POD output, just as read borrows a buffer.
// It cannot see a Go frame, G, capsule, or scheduler metadata.
//
//go:linkname openInto C.llgo_resource_open_into
func openInto(fd *int32, mode int32) int32

//go:linkname closeFD C.llgo_resource_close
func closeFD(fd, mode int32) int32

//go:linkname state C.llgo_resource_state
func state() int32

//go:linkname mark C.llgo_resource_mark
func mark(event int32)

//go:linkname installCheck C.llgo_resource_install_check
func installCheck()

type fdGuard struct{ fd int32 }

func (guard *fdGuard) close(mode int32) {
	if guard.fd >= 0 {
		fd := guard.fd
		guard.fd = -1 // Never retry an ambiguous or failed close.
		if closeFD(fd, mode) != 0 {
			panic("resource close failed")
		}
	}
}

func acquire(mode int32) {
	guard := fdGuard{fd: -1}
	defer guard.close(mode) // Registered BEFORE the possibly suspending acquire.
	if openInto(&guard.fd, mode) != 0 || guard.fd < 0 {
		panic("resource open failed")
	}
	if mode == 0 {
		return
	}
	if mode == 1 {
		// main's shutdown and the cleanup sentinel must precede completion.
		panic("canceled acquire executed its normal continuation")
	}
	mark(2)
	var never chan struct{}
	<-never // Retain the same ordinary defer across a later suspension.
	panic("canceled late owner executed its normal continuation")
}

func cleanupSentinel(ready chan<- struct{}) {
	defer func() {
		mark(1) // Cancellation cleanup releases the still-running open worker.
		for state()&12 != 12 {
			runtime.Gosched()
		}
		// Both close workers are physically blocked. With GOMAXPROCS=1,
		// this Go cleanup must still be scheduled to release them.
		for i := 0; i < 8; i++ {
			mark(3)
			runtime.Gosched()
		}
		mark(4)
	}()
	ready <- struct{}{}
	for {
		runtime.Gosched() // Cancellation must also activate zero-ticket resumes.
	}
}

func recoverClosedSend(done chan<- struct{}) {
	if recover() == nil {
		panic("closed send cleanup lost its panic")
	}
	runtime.Gosched() // The fault cleanup is a real asynchronous deferred child.
	done <- struct{}{}
}

func checkFaultCleanup() {
	ch := make(chan int)
	ready, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer recoverClosedSend(done)
		ready <- struct{}{}
		ch <- 1
		panic("send on closed channel resumed normally")
	}()
	<-ready
	// With one P and no other runnable child, yield lets the unbuffered send
	// park before close produces its fault result.
	runtime.Gosched()
	close(ch)
	<-done
}

func waitSemaphore(ready chan<- struct{}) {
	var never sync.WaitGroup
	never.Add(1)
	defer mark(6)
	ready <- struct{}{}
	never.Wait()
	panic("canceled semaphore wait executed its normal continuation")
}

func main() {
	runtime.GOMAXPROCS(1)
	installCheck()
	for i := 0; i < 16; i++ {
		acquire(0)
	}
	checkFaultCleanup()
	ready := make(chan struct{})
	go waitSemaphore(ready)
	<-ready
	runtime.Gosched() // Park the frame-owned semaphore state before GC/shutdown.
	go cleanupSentinel(ready)
	<-ready
	go acquire(1)
	go acquire(2)
	for state()&3 != 3 {
		runtime.Gosched()
	}
	runtime.GC() // The output slot and its defer must survive suspended-frame GC.
	mark(5)
	// The native atexit checker validates all physical closes and prints the
	// completion sentinel only after command shutdown has joined every worker.
}
