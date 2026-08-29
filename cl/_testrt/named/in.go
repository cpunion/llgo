package main

import "github.com/goplus/lib/c"

type mSpanList struct {
	first *mspan
	last  *mspan
}

type minfo struct {
	span *mspan
	info int
}

type mspan struct {
	next  *mspan
	prev  *mspan
	list  *mSpanList
	info  minfo
	value int
	check func(int) int
}

// The recursive named structure is rooted through a slot captured by check.
// The six printf values follow the source field paths, including two calls to
// the same stored closure reached directly and through info.span.
func main() {
	m := &mspan{}
	m.value = 100
	m.next = &mspan{}
	m.next.value = 200
	m.list = &mSpanList{}
	m.list.last = &mspan{}
	m.list.last.value = 300
	m.info.info = 10
	m.info.span = m
	m.check = func(n int) int {
		return m.value * n
	}
	c.Printf(c.Str("%d %d %d %d %d %d\n"), m.next.value, m.list.last.value, m.info.info,
		m.info.span.value, m.check(-2), m.info.span.check(-3))
}

// DARWIN-ARM64-NEXT:   %[[TMP64:[0-9]+]] = call i64 %__llgo_funcval_code(ptr swiftself %[[TMP62]], i64 -2)
// LINUX-AMD64-NEXT:   %[[TMP64:[0-9]+]] = call i64 %__llgo_funcval_code(ptr nest %[[TMP62]], i64 -2)
// DARWIN-ARM64-NEXT:   %[[TMP73:[0-9]+]] = call i64 %__llgo_funcval_code1(ptr swiftself %[[TMP71]], i64 -3)
// LINUX-AMD64-NEXT:   %[[TMP73:[0-9]+]] = call i64 %__llgo_funcval_code1(ptr nest %[[TMP71]], i64 -3)

// DARWIN-ARM64-SAME: ptr swiftself %[[TMP0:[0-9]+]], i64 %[[TMP1:[0-9]+]]){{.*}} {
// LINUX-AMD64-SAME: ptr nest %[[TMP0:[0-9]+]], i64 %[[TMP1:[0-9]+]]){{.*}} {
