package main


func main() {
	var m int = 200
	fn := func(n int) int {
		return m + n
	}
	var i any = fn
	f, ok := i.(func(int) int)
	if !ok {
		panic("error")
	}
	println(f(100))
}

// DARWIN-ARM64-NEXT:   %[[TMP12:[0-9]+]] = call i64 %__llgo_funcval_code(ptr swiftself %[[TMP10]], i64 100)
// LINUX-AMD64-NEXT:   %[[TMP12:[0-9]+]] = call i64 %__llgo_funcval_code(ptr nest %[[TMP10]], i64 100)

// DARWIN-ARM64-SAME: ptr swiftself %[[TMP0:[0-9]+]], i64 %[[TMP1:[0-9]+]]){{.*}} {
// LINUX-AMD64-SAME: ptr nest %[[TMP0:[0-9]+]], i64 %[[TMP1:[0-9]+]]){{.*}} {
