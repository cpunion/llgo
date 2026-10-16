package main

import (
	"runtime"
	"unsafe"
)

type payload struct {
	value int
	pad   [64]byte
}

var fixed = payload{value: 11}
var fail bool

func plain() (*payload, int, unsafe.Pointer) {
	return &fixed, fixed.value, unsafe.Pointer(&fixed)
}

func outcome() (*payload, int, unsafe.Pointer) {
	if fail {
		panic("unexpected outcome")
	}
	return &fixed, fixed.value, unsafe.Pointer(&fixed)
}

func first() (*payload, int, unsafe.Pointer) {
	p := &payload{value: 17}
	runtime.Gosched()
	return p, p.value, unsafe.Pointer(p)
}

func second() (*payload, int, unsafe.Pointer) {
	p := &payload{value: 23}
	runtime.Gosched()
	return p, p.value, unsafe.Pointer(p)
}

var coroutineCalls = []func() (*payload, int, unsafe.Pointer){first, second}
var mixedCalls = []func() (*payload, int, unsafe.Pointer){plain, outcome, first, second}

func check(p *payload, n int, raw unsafe.Pointer) {
	// The caller's consumed result storage must not be reloaded after its
	// pointer words were retired. The loaded SSA values must also remain live
	// across this new suspension and collection.
	runtime.Gosched()
	runtime.GC()
	if p == nil || raw == nil || raw != unsafe.Pointer(p) || p.value != n {
		panic("dynamic pointer result was cleared or lost")
	}
}

type source interface {
	get() (*payload, int, unsafe.Pointer)
}

type asyncSource int
type plainSource int

func (s asyncSource) get() (*payload, int, unsafe.Pointer) {
	p := &payload{value: int(s)}
	runtime.Gosched()
	return p, p.value, unsafe.Pointer(p)
}

func (s plainSource) get() (*payload, int, unsafe.Pointer) {
	p := &payload{value: int(s)}
	return p, p.value, unsafe.Pointer(p)
}

var sources = []source{asyncSource(31), plainSource(37)}

func pointerA() *payload {
	p := &payload{value: 41}
	runtime.Gosched()
	return p
}

func pointerB() *payload {
	p := &payload{value: 43}
	runtime.Gosched()
	return p
}

var pointerCalls = []func() *payload{pointerA, pointerB}

func main() {
	for _, procs := range []int{1, 2} {
		runtime.GOMAXPROCS(procs)
		for _, f := range coroutineCalls {
			check(f())
		}
		for _, f := range mixedCalls {
			check(f())
		}
		for _, s := range sources {
			check(s.get())
		}
		for _, f := range pointerCalls {
			p := f()
			if p == nil || (p.value != 41 && p.value != 43) {
				panic("single dynamic pointer result was cleared")
			}
			check(p, p.value, unsafe.Pointer(p))
		}
		captured := &payload{value: 47}
		closures := []func() (*payload, int, unsafe.Pointer){
			func() (*payload, int, unsafe.Pointer) {
				runtime.Gosched()
				return captured, captured.value, unsafe.Pointer(captured)
			},
			first,
		}
		for _, f := range closures {
			check(f())
		}
	}
	println("dynamic pointer results survive single consumption and GC")
}
