//go:build darwin || linux

package main

import (
	"runtime"
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

// Reuse the existing exact FD-count/strong-join checker, not a mock scheduler.
const LLGoFiles = "../coro_resource_delivery_slot/wrap/delivery.c"

type result = struct{ R0 int32 }
type largeResult = struct {
	R0 int32
	R1 [131073]byte
	R2 *[128]byte
}

//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*result), func(*result, unsafe.Pointer, uint32, uint32)) *result

//go:linkname borrowLarge llgo.coroBorrowResult
func borrowLarge(func(*largeResult), func(*largeResult, unsafe.Pointer, uint32, uint32)) *largeResult

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

//go:linkname blocked C.llgo_delivery_blocked
func blocked() int32

//go:linkname opened C.llgo_delivery_opened
func opened() int32

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

// The exact runtime type is intentional: the private route is uint16, not a
// guessed machine word. This fixture observes placement, never changes it.
//
//go:linkname currentRoute github.com/xgo-dev/llgo/runtime/internal/runtime.coroCurrentTaskRouteV1
func currentRoute() coro.RouteID

func initialize(out *result) { out.R0 = -1 }
func drop(out *result, env unsafe.Pointer, token, mode uint32) {
	if env != nil {
		println("context-free base drop received a forwarding environment")
		escaped()
	}
	if token != 17 {
		println("forwarded small drop token mismatch", token, mode)
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

func acquire(token, mode uint32) int32 {
	out := borrow(initialize, drop)
	if token != 17 {
		println("forwarded producer token mismatch", token, mode)
		escaped()
	}
	openInto(out, mode)
	if mode == 2 {
		panic("failed after acquiring forwarded resource")
	}
	return out.R0
}

// Equal Go signatures are deliberately not equal argument mappings.
func permuted(mode, token uint32) int32 { return acquire(token, mode) }
func literal(mode uint32) int32         { return permuted(mode, 17) }
func chained(mode uint32) int32         { return literal(mode) }

//go:noinline
func noinline(mode uint32) int32 { return chained(mode) }

var published = chained
var publishedPermutation = permuted

type fieldInputs struct {
	unused func()
	inner  struct{ token uint32 }
}
type fieldAlias = fieldInputs

func projected(value fieldAlias, mode uint32) int32      { return acquire(value.inner.token, mode) }
func projectedChain(value fieldAlias, mode uint32) int32 { return projected(value, mode) }

var publishedProjected = projectedChain

func projectedOwner(value fieldAlias)         { projectedChain(value, 1); escaped() }
func projectedDeferredOwner(value fieldAlias) { defer projectedChain(value, 1) }
func projectedDynamicOwner(f func(fieldAlias, uint32) int32, value fieldAlias) {
	f(value, 1)
	escaped()
}
func projectedDynamicDeferredOwner(f func(fieldAlias, uint32) int32, value fieldAlias) {
	defer f(value, 1)
}

type boxedFactory fieldInputs

func (value boxedFactory) fieldAcquire(mode uint32) int32 { return acquire(value.inner.token, mode) }

type boxedResourceFactory interface{ fieldAcquire(uint32) int32 }

func boxedInterfaceOwner(owner boxedResourceFactory)         { owner.fieldAcquire(1); escaped() }
func boxedInterfaceDeferredOwner(owner boxedResourceFactory) { defer owner.fieldAcquire(1) }

type factory uint32

func (token factory) acquire(mode uint32) int32 { return acquire(uint32(token), mode) }

func (factory) fixedAcquire(mode uint32) int32 { return acquire(17, mode) }

type resourceFactory interface{ fixedAcquire(uint32) int32 }

func interfaceOwner(owner resourceFactory)         { owner.fixedAcquire(1); escaped() }
func interfaceDeferredOwner(owner resourceFactory) { defer owner.fixedAcquire(1) }

type pointerFactory struct{ token uint32 }

//go:linkname borrowPointer llgo.coroBorrowResult
func borrowPointer(func(*result), func(*result, unsafe.Pointer, *pointerFactory, uint32)) *result

func dropPointer(out *result, env unsafe.Pointer, receiver *pointerFactory, mode uint32) {
	if receiver == nil || receiver.token != 17 {
		println("interface drop lost its original receiver pointer")
		escaped()
	}
	drop(out, env, receiver.token, mode)
}

func (receiver *pointerFactory) pointerAcquire(mode uint32) int32 {
	out := borrowPointer(initialize, dropPointer)
	openInto(out, mode)
	return out.R0
}

type pointerResourceFactory interface{ pointerAcquire(uint32) int32 }

func pointerInterfaceOwner(owner pointerResourceFactory)         { owner.pointerAcquire(1); escaped() }
func pointerInterfaceDeferredOwner(owner pointerResourceFactory) { defer owner.pointerAcquire(1) }

func boundOwner() {
	token := factory(opened() + 15) // Runtime value 17, not a folded capture.
	bound := token.acquire
	token = 99
	if token != 99 {
		escaped()
	}
	bound(1)
	escaped()
}

func initializeLarge(out *largeResult) { out.R0 = -1 }
func checkLarge(payload *[131073]byte, owner *[128]byte) {
	if payload[0] != 17 || payload[len(payload)-1] != 91 {
		println("forwarded large payload mismatch", payload[0], payload[len(payload)-1])
		escaped()
	}
	if owner == nil {
		println("forwarded large owner is nil")
		escaped()
	}
	if owner[0] != 37 {
		println("forwarded large owner value mismatch", owner[0])
		escaped()
	}
}
func dropLarge(out *largeResult, _ unsafe.Pointer, token, mode uint32) {
	if token != 17 {
		println("forwarded large drop token mismatch", token, mode)
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
func acquireLarge(token, mode uint32) (int32, [131073]byte, *[128]byte) {
	out := borrowLarge(initializeLarge, dropLarge)
	out.R1[0], out.R1[len(out.R1)-1] = 17, 91
	out.R2 = new([128]byte)
	out.R2[0] = 37
	openLargeInto(&out.R0, mode)
	return out.R0, out.R1, out.R2
}
func largeLiteral(mode uint32) (int32, [131073]byte, *[128]byte) { return acquireLarge(17, mode) }
func largeChain(mode uint32) (int32, [131073]byte, *[128]byte)   { return largeLiteral(mode) }

var publishedLarge = largeChain

func sentinel(ready chan struct{}) {
	defer release()
	close(ready)
	var never chan struct{}
	<-never
}

func resizeShutdownQuota() {
	if old := runtime.GOMAXPROCS(8); old != 1 || runtime.GOMAXPROCS(0) != 8 {
		panic("shutdown cleanup could not resize logical execution quota")
	}
}

func peerSentinel(ready chan struct{}, resizeOnShutdown bool) {
	for currentRoute() != 2 {
		runtime.Gosched()
	}
	// Keep the peer route alive, but disable further runnable transfers before
	// publishing readiness. Thus its shutdown defer stays on P2 and the next
	// child, created by the static main G, stays on P1. No OS-thread lock or
	// timing sleep is involved in establishing this dependency.
	runtime.GOMAXPROCS(1)
	if currentRoute() != 2 {
		panic("shutdown sentinel moved from P2")
	}
	if resizeOnShutdown {
		// Cancellation cleanup still has the full Go API. Growing the logical
		// quota here must not try to start another M behind the sealed barrier.
		defer resizeShutdownQuota()
	}
	sentinel(ready)
}

func crossPDeferredOwner() {
	if currentRoute() != 1 {
		panic("shutdown worker did not start on P1")
	}
	defer chained(1)
}
func owner()                                                                     { noinline(1); escaped() }
func failingOwner()                                                              { defer func() { recover() }(); chained(2); escaped() }
func deferredOwner()                                                             { defer chained(1) }
func dynamicOwner(f func(uint32) int32)                                          { f(1); escaped() }
func dynamicDeferredOwner(f func(uint32) int32)                                  { defer f(1) }
func permutationOwner(f func(uint32, uint32) int32)                              { f(1, 17); escaped() }
func largeOwner()                                                                { largeChain(1); println("forwarded large owner continuation ran"); escaped() }
func largeDeferredOwner()                                                        { defer largeChain(1) }
func largeDynamicDeferredOwner(f func(uint32) (int32, [131073]byte, *[128]byte)) { defer f(1) }

func main() {
	// GOMAXPROCS is selected by the runner: do not collapse the P=2 gate to P=1.
	install()
	mode := fixtureMode()
	var fd int32
	if mode == 30 || mode == 31 {
		var value boxedFactory
		value.inner.token = 17
		var owner boxedResourceFactory = value
		value.inner.token = 99
		fd = owner.fieldAcquire(0)
		owner, value = nil, boxedFactory{}
	} else if mode >= 25 {
		var value fieldAlias
		value.inner.token = 17
		fd = projectedChain(value, 0)
	} else {
		fd = chained(0)
	}
	runtime.Gosched()
	runtime.GC()
	checkAlive(fd)
	closeFD(fd)
	if mode == 9 {
		fd = publishedPermutation(0, 17)
		checkAlive(fd)
		closeFD(fd)
	}
	if mode >= 12 {
		fd, payload, owner := largeChain(0)
		runtime.Gosched()
		runtime.GC()
		checkLarge(&payload, owner)
		checkAlive(fd)
		closeFD(fd)
	}
	ready := make(chan struct{})
	if mode == 16 || mode == 17 {
		if runtime.GOMAXPROCS(0) != 2 {
			panic("cross-P shutdown fixture requires GOMAXPROCS=2")
		}
		go peerSentinel(ready, mode == 17)
		// Keep main runnable until the sentinel reaches P2: a lone already-run
		// G deliberately cannot bounce between idle routes.
	readyLoop:
		for {
			select {
			case <-ready:
				break readyLoop
			default:
				runtime.Gosched()
			}
		}
	} else {
		go sentinel(ready)
		<-ready
	}
	if mode == 1 {
		go failingOwner()
		for closing() == 0 {
			runtime.Gosched()
		}
		runtime.GC()
		return
	}
	switch mode {
	case 2:
		go chained(1)
	case 3:
		go deferredOwner()
	case 4:
		go owner()
	case 6:
		go dynamicOwner(published)
	case 7:
		go dynamicDeferredOwner(published)
	case 9:
		go permutationOwner(publishedPermutation)
	case 12:
		go largeOwner()
	case 13:
		go largeChain(1)
	case 14:
		go largeDeferredOwner()
	case 15:
		go largeDynamicDeferredOwner(publishedLarge)
	case 16, 17:
		go crossPDeferredOwner()
	case 18:
		go boundOwner()
	case 19, 20:
		token := factory(opened() + 15)
		bound := token.acquire
		token = 99
		if token != 99 {
			escaped()
		}
		if mode == 19 {
			go dynamicOwner(bound)
		} else {
			go dynamicDeferredOwner(bound)
		}
		bound = nil
	case 21, 22:
		token := factory(opened() + 15)
		var owner resourceFactory = token
		token = 99
		if token != 99 {
			escaped()
		}
		if mode == 21 {
			go interfaceOwner(owner)
		} else {
			go interfaceDeferredOwner(owner)
		}
		owner = nil
	case 23, 24:
		receiver := &pointerFactory{token: uint32(opened() + 15)}
		var owner pointerResourceFactory = receiver
		receiver = &pointerFactory{token: 99}
		if receiver.token != 99 {
			escaped()
		}
		if mode == 23 {
			go pointerInterfaceOwner(owner)
		} else {
			go pointerInterfaceDeferredOwner(owner)
		}
		owner, receiver = nil, nil
	case 25, 26, 27, 28, 29:
		var value fieldAlias
		value.inner.token = uint32(opened() + 15)
		switch mode {
		case 25:
			go projectedOwner(value)
		case 26:
			go projectedDeferredOwner(value)
		case 27:
			go projectedDynamicOwner(publishedProjected, value)
		case 28:
			go projectedDynamicDeferredOwner(publishedProjected, value)
		case 29:
			go projectedChain(value, 1)
		}
		value.inner.token = 99
		if value.inner.token != 99 {
			escaped()
		}
		value = fieldAlias{}
	case 30, 31:
		var value boxedFactory
		value.inner.token = uint32(opened() + 15)
		var owner boxedResourceFactory = value
		value.inner.token = 99
		if value.inner.token != 99 {
			escaped()
		}
		if mode == 30 {
			go boxedInterfaceOwner(owner)
		} else {
			go boxedInterfaceDeferredOwner(owner)
		}
		owner, value = nil, boxedFactory{}
	default:
		escaped()
	}
	for blocked() == 0 {
		runtime.Gosched()
	}
	runtime.GC()
	runtime.GC()
	if mode == 16 || mode == 17 {
		if currentRoute() != 1 {
			panic("shutdown coordinator moved from P1")
		}
		println("cross-P shutdown topology ready")
	}
	// Shutdown's claimed sentinel releases the foreign operation. Its result
	// must be strong-joined and closed exactly once, without user continuation.
}
