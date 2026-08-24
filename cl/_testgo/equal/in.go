package main

// Each source section below exercises a different equality lowering. Keep the
// checks function-scoped and follow the comparison result through to assert so
// a coincidental helper call elsewhere cannot satisfy the test.

// Function values: the generated closure code pointer is non-nil and that
// predicate is what reaches assert.


// DARWIN-ARM64-LABEL: define void @"main.init#1$2"(ptr swiftself %0){{.*}} {
// LINUX-AMD64-LABEL: define void @"main.init#1$2"(ptr nest %0){{.*}} {

// Arrays: all three elements participate in equality, and inequality negates
// the aggregate result rather than changing element semantics.

// Structs delegate string and interface fields to their semantic equality
// helpers and combine both results before asserting the aggregate result.

// Slices compare with nil through their data pointer. Check the non-empty and
// zero-length/non-zero-capacity make paths separately.

// Interface equality must feed the assertion directly, including a negated
// result for unequal dynamic types.



func test() {}

func assert(cond bool) {
	if !cond {
		panic("failed")
	}
}

// func
func init() {
	fn1 := test
	fn2 := func(i, j int) int { return i + j }
	var n int
	fn3 := func() { println(n) }
	var fn4 func() int
	assert(test != nil)
	assert(nil != test)
	assert(fn1 != nil)
	assert(nil != fn1)
	assert(fn2 != nil)
	assert(nil != fn2)
	assert(fn3 != nil)
	assert(nil != fn3)
	assert(fn4 == nil)
	assert(nil == fn4)
}

// array
func init() {
	assert([0]float64{} == [0]float64{})
	ar1 := [...]int{1, 2, 3}
	ar2 := [...]int{1, 2, 3}
	assert(ar1 == ar2)
	ar2[1] = 1
	assert(ar1 != ar2)
}

type T struct {
	X int
	Y int
	Z string
	V any
}

type N struct{}

// struct
func init() {
	var n1, n2 N
	var t1, t2 T
	x := T{10, 20, "hello", 1}
	y := T{10, 20, "hello", 1}
	z := T{10, 20, "hello", "ok"}
	assert(n1 == n2)
	assert(t1 == t2)
	assert(x == y)
	assert(x != z)
	assert(y != z)
}

// slice
func init() {
	var a []int
	var b = []int{1, 2, 3}
	c := make([]int, 2)
	d := make([]int, 0, 2)
	assert(a == nil)
	assert(b != nil)
	assert(c != nil)
	assert(d != nil)
	b = nil
	assert(b == nil)
}

// iface
func init() {
	var a any = 100
	var b any = struct{}{}
	var c any = T{10, 20, "hello", 1}
	x := T{10, 20, "hello", 1}
	y := T{10, 20, "hello", "ok"}
	assert(a == 100)
	assert(b == struct{}{})
	assert(b != N{})
	assert(c == x)
	assert(c != y)
}

// chan
func init() {
	a := make(chan int)
	b := make(chan int)
	assert(a == a)
	assert(a != b)
	assert(a != nil)
}

// map
func init() {
	m1 := make(map[int]string)
	var m2 map[int]string
	assert(m1 != nil)
	assert(m2 == nil)
}

func main() {
}
