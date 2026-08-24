package main

import (
	"github.com/goplus/lib/c"
)

func add() func(int, int) int {
	return func(x, y int) int {
		return x + y
	}
}

func add2() (func(int, int) int, int) {
	return func(x, y int) int {
		return x + y
	}, 1
}

func main() {
	fn := func() func(int, int) int {
		return func(x, y int) int {
			return x + y
		}
	}()
	c.Printf(c.Str("%d\n"), fn(100, 200))
	c.Printf(c.Str("%d\n"), add()(100, 200))
	fn, n := add2()
	c.Printf(c.Str("%d %d\n"), add()(100, 200), n)
}

// DARWIN-ARM64-NEXT:   %[[TMP3:[0-9]+]] = call i64 %__llgo_funcval_code(ptr swiftself %[[TMP1]], i64 100, i64 200)
// LINUX-AMD64-NEXT:   %[[TMP3:[0-9]+]] = call i64 %__llgo_funcval_code(ptr nest %[[TMP1]], i64 100, i64 200)
// DARWIN-ARM64-NEXT:   %[[TMP8:[0-9]+]] = call i64 %__llgo_funcval_code1(ptr swiftself %[[TMP6]], i64 100, i64 200)
// LINUX-AMD64-NEXT:   %[[TMP8:[0-9]+]] = call i64 %__llgo_funcval_code1(ptr nest %[[TMP6]], i64 100, i64 200)
// DARWIN-ARM64-NEXT:   %[[TMP16:[0-9]+]] = call i64 %__llgo_funcval_code2(ptr swiftself %[[TMP14]], i64 100, i64 200)
// LINUX-AMD64-NEXT:   %[[TMP16:[0-9]+]] = call i64 %__llgo_funcval_code2(ptr nest %[[TMP14]], i64 100, i64 200)
