//go:build darwin || linux

package main

import (
	"runtime"
	"unsafe"
)

const LLGoFiles = "wrap/delivery.c"

type result = struct{ R0 int32 }

type largeResult = struct {
	R0 int32
	R1 [131073]byte
	R2 *[128]byte
}

var capturedFixture bool

//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*result), func(*result, unsafe.Pointer, uint32)) *result

//go:linkname borrowLarge llgo.coroBorrowResult
func borrowLarge(func(*largeResult), func(*largeResult, unsafe.Pointer, uint32)) *largeResult

//go:linkname install C.llgo_delivery_install
func install()

//go:linkname openInto C.llgo_delivery_open
func openInto(*result, uint32)

//go:linkname openLargeInto C.llgo_delivery_open_large
func openLargeInto(*int32, uint32)

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

func initializeLarge(out *largeResult) { out.R0 = -1 }

func checkLarge(payload *[131073]byte, owner *[128]byte) {
	if payload[0] != 17 || payload[len(payload)-1] != 91 || owner == nil || owner[0] != 37 {
		escaped()
	}
}

func dropLarge(out *largeResult, env unsafe.Pointer, mode uint32) {
	if capturedFixture && env == nil {
		escaped()
	}
	runtime.Gosched()
	runtime.GC()
	checkLarge(&out.R1, out.R2)
	fd := out.R0
	out.R0, out.R2 = -1, nil
	if fd >= 0 {
		closeFD(fd)
	}
}

func acquireLarge(mode uint32) (int32, [131073]byte, *[128]byte) {
	out := borrowLarge(initializeLarge, dropLarge)
	out.R1[0], out.R1[len(out.R1)-1] = 17, 91
	out.R2 = new([128]byte)
	out.R2[0] = 37
	openLargeInto(&out.R0, mode)
	return out.R0, out.R1, out.R2
}

func capturedLargeAcquire() func(uint32) (int32, [131073]byte, *[128]byte) {
	state := new([128]byte)
	state[0] = 37
	return func(mode uint32) (int32, [131073]byte, *[128]byte) {
		out := borrowLarge(initializeLarge, dropLarge)
		out.R1[0], out.R1[len(out.R1)-1] = 17, 91
		out.R2 = state
		openLargeInto(&out.R0, mode)
		return out.R0, out.R1, out.R2
	}
}

func largeOwner() {
	acquireLarge(1)
	escaped()
}

func largeDeferredOwner() { defer acquireLarge(1) }

func largeDynamicDeferredOwner(acquire func(uint32) (int32, [131073]byte, *[128]byte)) {
	for i := 0; i < 1; i++ {
		defer acquire(1)
	}
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
	install()
	mode := fixtureMode()
	capturedFixture = mode >= 6 && mode <= 9 || mode == 11 || mode == 15
	fd := acquire(0)
	runtime.Gosched()
	runtime.GC()
	checkAlive(fd) // Normal Return transfers ownership; it must not auto-drop.
	closeFD(fd)
	if mode >= 12 {
		fd, payload, owner := acquireLarge(0)
		runtime.Gosched()
		runtime.GC()
		checkLarge(&payload, owner)
		checkAlive(fd)
		closeFD(fd)
	}
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
	case 12:
		go largeOwner()
	case 13:
		go acquireLarge(1) // Oversized independent fallback, not a caller-owned slot.
	case 14:
		go largeDeferredOwner()
	case 15:
		go largeDynamicDeferredOwner(capturedLargeAcquire())
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
