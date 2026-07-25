// LITTEST
package main

// CHECK-LABEL: define ptr @"{{.*}}/deferpanic.main$coro"(
func main() {
	defer func() {
		e := recover()
		println(e.(string))
	}()
	// CHECK: call void @__llgo_coro_panic_prepare_v1
	defer panic("panic in defer")
	println("run main")
}
