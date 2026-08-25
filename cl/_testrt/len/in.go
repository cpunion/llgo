package main

type data struct {
	s string
	c chan int
	m map[int]string
	a []int
}

// First read every len/cap operation from the same zero-valued struct.
// Then construct and query the populated value, preserving channel/map/slice identities.
func main() {
	d := &data{}
	println(len(d.s), len(d.c), len(d.m), len(d.a), cap(d.c), cap(d.a))
	v := &data{s: "hello", c: make(chan int, 2), m: map[int]string{1: "hello"}, a: []int{1, 2, 3}}
	println(len(v.s), len(v.c), len(v.m), len(v.a), cap(v.c), cap(v.a))
}
