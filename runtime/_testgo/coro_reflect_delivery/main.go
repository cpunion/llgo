//go:build darwin || linux

package main

import (
	"reflect"
	"runtime"
	"unsafe"
)

const LLGoFiles = "../coro_resource_delivery_slot/wrap/delivery.c; wrap/post_native.c"

type result = struct{ R0 int32 }

//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*result), func(*result, unsafe.Pointer, uint32, uint32)) *result

//go:linkname install C.llgo_delivery_install
func install()

//go:linkname openInto C.llgo_delivery_open
func openInto(*result, uint32)

//go:linkname closeFD C.llgo_delivery_close
func closeFD(int32)

//go:linkname checkAlive C.llgo_delivery_check_alive
func checkAlive(int32)

//go:linkname blocked C.llgo_delivery_blocked
func blocked() int32

//go:linkname fixtureMode C.llgo_delivery_fixture_mode
func fixtureMode() int32

//go:linkname postNativeEnable C.llgo_reflect_post_native_enable
func postNativeEnable()

//go:linkname postNativeEntered C.llgo_reflect_post_native_entered
func postNativeEntered() int32

//go:linkname waitBorrowed C.llgo_reflect_post_native_wait
func waitBorrowed()

//go:linkname expectBorrowed C.llgo_delivery_expect_borrowed
func expectBorrowed()

//go:linkname releaseWaits C.llgo_reflect_release_waits
func releaseWaits()

//go:linkname escaped C.llgo_delivery_escaped
func escaped()

func initialize(out *result) { out.R0 = -1 }
func drop(out *result, _ unsafe.Pointer, token, mode uint32) {
	if token != 17 {
		escaped()
	}
	fd := out.R0
	out.R0 = -1
	if fd >= 0 {
		closeFD(fd)
	}
}

func acquire(token, mode uint32) int32 {
	out := borrow(initialize, drop)
	if token != 17 {
		escaped()
	}
	openInto(out, mode)
	return out.R0
}

type receiver struct{ Token uint32 }
type promotedValue struct{ reflect.Value }
type otherReceiver struct{ Token uint32 }
type ordinaryReceiver struct{}
type borrowedReceiver struct{}

var borrowedFD int32

func (r otherReceiver) Acquire(mode uint32) int32 { return acquire(r.Token, mode) }
func (ordinaryReceiver) Acquire(uint32) int32     { return -1 }
func (borrowedReceiver) Acquire(uint32) int32     { waitBorrowed(); return borrowedFD }

func (r receiver) Acquire(mode uint32) int32 { return acquire(r.Token, mode) }

type payload struct {
	Bytes [131073]byte
	Keep  *uint32
	Check func() uint32
}

type largeResult = struct {
	R0 int32
	R1 payload
}

//go:linkname openLargeInto C.llgo_delivery_open_large
func openLargeInto(*largeResult, uint32)

//go:linkname borrowLarge llgo.coroBorrowResult
func borrowLarge(func(*largeResult), func(*largeResult, unsafe.Pointer, uint32, uint32)) *largeResult

func initializeLarge(out *largeResult) { out.R0 = -1 }
func dropLarge(out *largeResult, _ unsafe.Pointer, token, _ uint32) {
	fd := out.R0
	out.R0 = -1
	if fd >= 0 {
		if token != 17 || out.R1.Keep == nil || *out.R1.Keep != 37 || out.R1.Check() != 41 || out.R1.Bytes[131072] != 43 {
			escaped()
		}
		closeFD(fd)
	}
}

func acquireLarge(token, mode uint32) (int32, payload) {
	out := borrowLarge(initializeLarge, dropLarge)
	keep := new(uint32)
	*keep = 37
	out.R1.Keep = keep
	out.R1.Check = func() uint32 { return *keep + 4 }
	out.R1.Bytes[131072] = 43
	openLargeInto(out, mode)
	return out.R0, out.R1
}

func (r receiver) AcquireLarge(mode uint32) (int32, payload) { return acquireLarge(r.Token, mode) }

//go:linkname borrowVariadic llgo.coroBorrowResult
func borrowVariadic(func(*result), func(*result, unsafe.Pointer, uint32, []uint32)) *result

func dropVariadic(out *result, env unsafe.Pointer, token uint32, modes []uint32) {
	drop(out, env, token, modes[0])
}

func acquireVariadic(token uint32, modes ...uint32) int32 {
	out := borrowVariadic(initialize, dropVariadic)
	if token != 17 || len(modes) != 1 {
		escaped()
	}
	openInto(out, modes[0])
	return out.R0
}

var token uint32 = 17

//go:linkname borrowMethodVariadic llgo.coroBorrowResult
func borrowMethodVariadic(func(*result), func(*result, unsafe.Pointer, receiver, []uint32)) *result

func dropMethodVariadic(out *result, env unsafe.Pointer, r receiver, modes []uint32) {
	drop(out, env, r.Token, modes[0])
}

func (r receiver) AcquireVariadic(modes ...uint32) int32 {
	out := borrowMethodVariadic(initialize, dropMethodVariadic)
	if r.Token != 17 || len(modes) != 1 {
		escaped()
	}
	openInto(out, modes[0])
	return out.R0
}

var pointer = &receiver{Token: 17}
var reflected = reflect.ValueOf(acquire)

func invoke(mode uint32) int32 {
	var values []reflect.Value
	switch fixtureMode() {
	case 13:
		values = reflect.ValueOf(acquireVariadic).CallSlice([]reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf([]uint32{mode})})
	case 14:
		values = reflect.ValueOf(acquireVariadic).Call([]reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf(mode)})
	case 15:
		method, _ := reflect.TypeOf(receiver{}).MethodByName("Acquire")
		values = method.Func.Call([]reflect.Value{reflect.ValueOf(receiver{Token: 17}), reflect.ValueOf(mode)})
	case 16:
		values = reflect.ValueOf(receiver{Token: 17}).MethodByName("Acquire").Call([]reflect.Value{reflect.ValueOf(mode)})
	case 17:
		bound := reflect.ValueOf(receiver{Token: 17}).MethodByName("Acquire").Interface().(func(uint32) int32)
		return bound(mode)
	case 18:
		values = reflect.ValueOf(pointer).MethodByName("Acquire").Call([]reflect.Value{reflect.ValueOf(mode)})
	case 19, 25:
		bound := reflect.ValueOf(pointer).MethodByName("Acquire").Interface()
		values = reflect.ValueOf(bound).Call([]reflect.Value{reflect.ValueOf(mode)})
	case 20:
		bound := reflected.Call
		values = bound([]reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf(mode)})
	case 21:
		values = (*reflect.Value).Call(&reflected, []reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf(mode)})
	case 22:
		method, _ := reflect.TypeOf(reflected).MethodByName("Call")
		outer := method.Func.Call([]reflect.Value{reflect.ValueOf(reflected), reflect.ValueOf([]reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf(mode)})})
		values = outer[0].Interface().([]reflect.Value)
	case 23:
		value := reflect.ValueOf(acquireVariadic)
		bound := value.CallSlice
		values = bound([]reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf([]uint32{mode})})
	case 26:
		values = reflect.ValueOf(acquireLarge).Call([]reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf(mode)})
	case 27:
		method, _ := reflect.TypeOf(receiver{}).MethodByName("AcquireLarge")
		values = method.Func.Call([]reflect.Value{reflect.ValueOf(receiver{Token: 17}), reflect.ValueOf(mode)})
	case 28:
		values = reflect.ValueOf(pointer).MethodByName("AcquireLarge").Call([]reflect.Value{reflect.ValueOf(mode)})
	case 29, 30:
		bound := reflect.ValueOf(pointer).MethodByName("AcquireLarge").Interface()
		values = reflect.ValueOf(bound).Call([]reflect.Value{reflect.ValueOf(mode)})
	case 31:
		// The inner producer has delivered before this ordinary callback
		// blocks. The exit checker must find its borrowed fd still alive.
		callback := reflect.MakeFunc(reflect.TypeOf(acquire), func(args []reflect.Value) []reflect.Value {
			fd := acquire(uint32(args[0].Uint()), uint32(args[1].Uint()))
			waitBorrowed()
			return []reflect.Value{reflect.ValueOf(fd)}
		})
		values = callback.Call([]reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf(mode)})
	case 32:
		// Embedding Value also creates private promoted method roots. Their
		// original standard-library ABI must still resolve to the patch.
		value := promotedValue{reflect.ValueOf(acquire)}
		values = value.Call([]reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf(mode)})
	case 33:
		value := &promotedValue{reflect.ValueOf(acquireVariadic)}
		values = value.CallSlice([]reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf([]uint32{mode})})
	case 34:
		r := receiver{Token: 99}
		bound := reflect.ValueOf(&r).Elem().MethodByName("Acquire").Interface().(func(uint32) int32)
		r.Token = 17
		return bound(mode)
	case 35, 39, 40:
		pointer = &receiver{Token: 99}
		bound := reflect.ValueOf(&pointer).Elem().MethodByName("Acquire").Interface().(func(uint32) int32)
		pointer = &receiver{Token: 17}
		return bound(mode)
	case 36, 37:
		var r interface{ Acquire(uint32) int32 } = receiver{Token: 99}
		if fixtureMode() == 37 {
			r = ordinaryReceiver{}
		}
		bound := reflect.ValueOf(&r).Elem().MethodByName("Acquire").Interface().(func(uint32) int32)
		r = otherReceiver{Token: 17}
		return bound(mode)
	case 38:
		pointer = &receiver{Token: 99}
		bound := reflect.ValueOf(&pointer).Elem().MethodByName("AcquireLarge").Interface()
		pointer = &receiver{Token: 17}
		values = reflect.ValueOf(bound).Call([]reflect.Value{reflect.ValueOf(mode)})
	case 41:
		borrowedFD = acquire(17, 0)
		var r interface{ Acquire(uint32) int32 } = receiver{Token: 17}
		bound := reflect.ValueOf(&r).Elem().MethodByName("Acquire").Interface().(func(uint32) int32)
		r = borrowedReceiver{}
		return bound(mode)
	case 42:
		pointer = &receiver{Token: 99}
		bound := reflect.ValueOf(&pointer).Elem().MethodByName("AcquireVariadic").Interface().(func(...uint32) int32)
		pointer = &receiver{Token: 17}
		values = reflect.ValueOf(bound).CallSlice([]reflect.Value{reflect.ValueOf([]uint32{mode})})
	default:
		values = reflect.ValueOf(acquire).Call([]reflect.Value{reflect.ValueOf(&token).Elem(), reflect.ValueOf(mode)})
	}
	if len(values) == 2 {
		data := values[1].Interface().(payload)
		runtime.GC()
		if data.Keep == nil || *data.Keep != 37 || data.Check() != 41 || data.Bytes[131072] != 43 {
			escaped()
		}
	}
	return int32(values[0].Int())
}

func postNativeMode() bool {
	mode := fixtureMode()
	return mode == 24 || mode == 25 || mode == 30 || mode == 31 || mode == 41
}

func reflectedOwner() {
	mode := uint32(1)
	if postNativeMode() {
		mode = 0
	}
	if fixtureMode() == 39 || fixtureMode() == 40 {
		pointer = &receiver{Token: 99}
		bound := reflect.ValueOf(&pointer).Elem().MethodByName("Acquire").Interface().(func(uint32) int32)
		pointer = &receiver{Token: 17}
		if fixtureMode() == 39 {
			go bound(mode)
			var never chan struct{}
			<-never
		} else {
			defer bound(mode)
			return
		}
	}
	invoke(mode)
	escaped()
}

func sentinel(ready chan struct{}) {
	// Main return may stop a user defer at its next resume gate. Release both
	// test waits in one foreign activation so neither depends on that gate.
	defer releaseWaits()
	close(ready)
	var never chan struct{}
	<-never
}

func main() {
	install()
	if fixtureMode() == 31 || fixtureMode() == 41 {
		expectBorrowed()
	}
	ordinary := reflect.ValueOf(func(n int) int { return n + 1 })
	if ordinary.Call([]reflect.Value{reflect.ValueOf(16)})[0].Int() != 17 {
		panic("ordinary reflection result")
	}
	for i := 0; i < 2; i++ {
		fd := invoke(0)
		runtime.GC()
		checkAlive(fd)
		closeFD(fd)
	}
	ready := make(chan struct{})
	go sentinel(ready)
	<-ready
	if postNativeMode() {
		postNativeEnable()
	}
	go reflectedOwner()
	for !postNativeMode() && blocked() == 0 || postNativeMode() && postNativeEntered() == 0 {
		runtime.Gosched()
	}
	token, pointer.Token = 99, 99
	reflected = reflect.Value{}
	runtime.GC()
	runtime.GC()
}
