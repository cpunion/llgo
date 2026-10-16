//go:build darwin || linux

package main

import (
	"runtime"
	"unsafe"
)

const LLGoFiles = "wrap/delivery.c"

type result = struct{ R0 int32 }

var capturedFixture bool

//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*result), func(*result, unsafe.Pointer, uint32)) *result

//go:linkname install C.llgo_delivery_install
func install()

//go:linkname openInto C.llgo_delivery_open
func openInto(*result, uint32)

//go:linkname closeFD C.llgo_delivery_close
func closeFD(int32)

//go:linkname checkAlive C.llgo_delivery_check_alive
func checkAlive(int32)

//go:linkname openCount C.llgo_delivery_opened
func openCount() int32

//go:linkname ownedFD C.llgo_delivery_owned
func ownedFD() int32

//go:linkname blocked C.llgo_delivery_blocked
func blocked() int32

//go:linkname closing C.llgo_delivery_closing
func closing() int32

//go:linkname fixtureMode C.llgo_delivery_fixture_mode
func fixtureMode() int32

//go:linkname waitClose C.llgo_delivery_wait_close
func waitClose()

//go:linkname release C.llgo_delivery_release
func release()

//go:linkname escaped C.llgo_delivery_escaped
func escaped()

func initialize(out *result) { out.R0 = -1 }

func drop(out *result, env unsafe.Pointer, mode uint32) {
	if capturedFixture && env == nil {
		escaped()
	}
	fd := out.R0
	out.R0 = -1
	if fd >= 0 {
		if mode == 2 {
			waitClose()
		}
		closeFD(fd)
	}
}

func acquire(mode uint32) int32 {
	out := borrow(initialize, drop)
	openInto(out, mode)
	if mode == 2 {
		panic("acquisition failed after producing its resource")
	}
	return out.R0
}

func capturedAcquire() func(uint32) int32 {
	state := new([128]byte)
	state[0] = 37
	return func(mode uint32) int32 {
		out := borrow(initialize, drop)
		openInto(out, mode)
		if state[0] != 37 {
			escaped()
		}
		return out.R0
	}
}

func capturedOwner(acquire func(uint32) int32) {
	acquire(1)
	escaped()
}

func capturedDeferredOwner(acquire func(uint32) int32) {
	for i := 0; i < 1; i++ {
		defer acquire(1)
	}
}

func releaseWait() { release() }

func sentinel(ready chan struct{}) {
	defer releaseWait()
	close(ready)
	var never chan struct{}
	<-never
}

func canceledOwner() {
	acquire(1)
	escaped()
}

func failingOwner() {
	defer func() { recover() }()
	acquire(2)
	escaped()
}

func deferredOwner() { defer acquire(1) }

func sequence(yield func(int) bool) { yield(0) }

func rangeOwner() {
	for range sequence {
		defer func() {}() // Registered on rangeOwner, not on its yield frame.
		acquire(1)
		escaped()
	}
}

func rangeDeferredOwner() {
	for range sequence {
		defer acquire(1)
	}
}

func rangeDynamicDeferredOwner(acquire func(uint32) int32) {
	for range sequence {
		defer acquire(1)
	}
}

func secondSiteOwner() {
	fd := acquire(0)
	checkAlive(fd)
	closeFD(fd)
	acquire(1)
	escaped()
}

func main() {
	runtime.GOMAXPROCS(1)
	install()
	mode := fixtureMode()
	capturedFixture = mode >= 6 && mode <= 9 || mode == 11
	fd := acquire(0)
	runtime.Gosched()
	runtime.GC()
	checkAlive(fd) // Normal Return transfers ownership; it must not auto-drop.
	closeFD(fd)
	ready := make(chan struct{})
	go sentinel(ready)
	<-ready
	if fixtureMode() == 1 {
		go failingOwner()
		for closing() == 0 {
			runtime.Gosched()
		}
		runtime.GC()
		runtime.GC()
		// Panic cleanup has started before any task stop and is awaiting
		// waitClose, not the physical close itself. Shutdown must defer until
		// the second await closes the fd; the older defer recovers the panic
		// without losing the retained shutdown base.
		return
	}
	switch fixtureMode() {
	case 2:
		go acquire(1) // Canceled independent entry: nil-output fallback owns it.
	case 3:
		go deferredOwner()
	case 4:
		go rangeOwner()
	case 5:
		go secondSiteOwner()
	case 6:
		go capturedOwner(capturedAcquire())
	case 7:
		go capturedDeferredOwner(capturedAcquire())
	case 8:
		go capturedAcquire()(1) // A captured discarded-result entry owns fallback custody.
	case 9:
		acquire := capturedAcquire()
		fd := acquire(0)
		checkAlive(fd)
		closeFD(fd)
		go capturedOwner(acquire)
	case 10:
		go rangeDeferredOwner()
	case 11:
		go rangeDynamicDeferredOwner(capturedAcquire())
	default:
		baseline := runtime.NumGoroutine()
		go acquire(0)
		for openCount() != 2 || runtime.NumGoroutine() != baseline {
			runtime.Gosched()
		}
		fd = ownedFD()
		checkAlive(fd) // Go does not auto-close a normally discarded raw fd.
		closeFD(fd)
		go canceledOwner()
	}

	for blocked() == 0 {
		runtime.Gosched()
	}
	runtime.GC()
	runtime.GC()
	// Only the sentinel's claimed shutdown cleanup releases the C worker.
	// The acquisition result then belongs to the parent's canceled lane, not
	// to user continuation. The C checker requires exactly one physical close.
}
