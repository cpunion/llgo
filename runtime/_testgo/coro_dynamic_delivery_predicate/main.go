package main

import (
	"runtime"
	"unsafe"
)

// Keep an exact static delivery contract in the universe without publishing
// a resource producer descriptor. The channel carries an ordinary Go callable;
// its unknown managed result must not break production predicate discovery.
type result = struct {
	R0 int
	R1 error
}

func initialize(out *result)                       { out.R0 = -1 }
func drop(out *result, _ unsafe.Pointer, _ string) { out.R0 = -1 }

//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*result), func(*result, unsafe.Pointer, string)) *result

func staticResource(path string) (int, error) {
	out := borrow(initialize, drop)
	return out.R0, out.R1
}

var calls int

func ordinary(path string) (int, error) {
	runtime.Gosched()
	calls++
	return len(path), nil
}

func exercise() {
	ch := make(chan any, 1)
	ch <- ordinary
	fn := (<-ch).(func(string) (int, error))
	if n, err := fn("first"); n != 5 || err != nil {
		panic("ordinary dynamic result")
	}
	for i := 0; i < 2; i++ {
		defer fn("discarded")
	}
}

func main() {
	if n, err := staticResource("seed"); n != -1 || err != nil {
		panic("static delivery result")
	}
	exercise()
	if calls != 3 {
		panic("ordinary dynamic defer effects")
	}
	println("opaque callable predicate closure ok")
}
