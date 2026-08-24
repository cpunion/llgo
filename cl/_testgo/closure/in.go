package main

type T func(n int)

func main() {
	// A closure without captures is represented by a direct code pointer, while
	// the captured closure is called with its environment.
	// DARWIN-ARM64: call void [[V2_CODE]](ptr swiftself [[V2_CALL_ENV]], i64 200)
	// LINUX-AMD64: call void [[V2_CODE]](ptr nest [[V2_CALL_ENV]], i64 200)
	var env string = "env"
	var v1 T = func(i int) {
		println("func", i)
	}
	var v2 T = func(i int) {
		// DARWIN-ARM64-LABEL: define void @"main.main$2"(ptr swiftself %0, i64 %1){{.*}} {
		// LINUX-AMD64-LABEL: define void @"main.main$2"(ptr nest %0, i64 %1){{.*}} {
		println("closure", i, env)
	}
	v1(100)
	v2(200)
}
