package main

type My[T any] struct {
	fn   func(n T)
	next *My[T]
}

func main() {
	// DARWIN-ARM64-NEXT: call void %__llgo_funcval_code(ptr swiftself [[FN_ENV]], i64 100)
	// LINUX-AMD64-NEXT: call void %__llgo_funcval_code(ptr nest [[FN_ENV]], i64 100)
	m := &My[int]{next: &My[int]{fn: func(n int) { println(n) }}}
	m.next.fn(100)
}
