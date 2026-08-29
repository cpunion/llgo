package main

import (
	"github.com/goplus/lib/c"
)

func main() {
	func(n1, n2 int) {
		c.Printf(c.Str("%d %d\n"), n1, n2)
	}(100, 200)

	fn1 := func(n1, n2 int) {
		c.Printf(c.Str("%d %d\n"), n1, n2)
	}

	fn2 := func() {
		fn1(100, 200)
	}
	fn2()
}

// DARWIN-ARM64-NEXT:   call void %__llgo_funcval_code(ptr swiftself %[[TMP4]])
// LINUX-AMD64-NEXT:   call void %__llgo_funcval_code(ptr nest %[[TMP4]])

// DARWIN-ARM64-SAME: ptr swiftself %[[TMP0:[0-9]+]]){{.*}} {
// LINUX-AMD64-SAME: ptr nest %[[TMP0:[0-9]+]]){{.*}} {
// DARWIN-ARM64-NEXT:   call void %__llgo_funcval_code(ptr swiftself %[[TMP4]], i64 100, i64 200)
// LINUX-AMD64-NEXT:   call void %__llgo_funcval_code(ptr nest %[[TMP4]], i64 100, i64 200)
