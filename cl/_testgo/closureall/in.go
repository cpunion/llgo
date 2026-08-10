package main

import _ "unsafe" // for go:linkname

import "github.com/goplus/lib/c"

// These checks intentionally describe stable stackless-coroutine contracts
// instead of numbered SSA values or the exact instruction layout.
//
// A statically synchronous function keeps its ordinary ABI.
//
// A colored method has the coroutine ABI and publishes a resumable frame.
//
// A statically synchronous free function also keeps its ordinary ABI.
//
// The entry coroutine uses the common await protocol for colored Go calls and
// the worker protocol for a potentially blocking foreign call.
//
// The closure constructor itself is colored because the callable may cross a
// dynamic boundary; its body follows the same frame protocol.

//go:linkname cSqrt C.sqrt
func cSqrt(x c.Double) c.Double

//llgo:coro noblock
//llgo:link cAbs C.abs
func cAbs(x c.Int) c.Int { return 0 }

// llgo:type C
type CCallback func(c.Int) c.Int

type Fn func(int) int

type S struct {
	v int
}

func (s S) Inc(x int) int {
	return s.v + x
}

func (s *S) Add(x int) int {
	return s.v + x
}

func callCallback(cb CCallback, v c.Int) c.Int {
	return cb(v)
}

func globalAdd(x, y int) int {
	return x + y
}

func main() {
	nf := makeNoFree()
	wf := makeWithFree(3)
	_ = nf(1)
	_ = wf(2)

	g := globalAdd
	_ = g(1, 2)

	s := &S{v: 5}
	mv := s.Add
	_ = mv(7)
	me := (*S).Add
	_ = me(s, 8)

	var i interface{ Add(int) int } = s
	im := i.Add
	_ = im(9)

	cs := cSqrt
	_ = cs(4)
	ca := cAbs
	_ = ca(-3)

	cb := CCallback(func(x c.Int) c.Int { return x + 1 })
	_ = callCallback(cb, 7)
}

func makeNoFree() Fn {
	return func(x int) int { return x + 1 }
}

func makeWithFree(base int) Fn {
	return func(x int) int { return x + base }
}
