// LITTEST
package main

type Point struct {
	x float64
	y float64
}

type MyPoint = Point

// Pointer receiver dereferences use explicit-status fault lowering, so the
// method is a coroutine. The alias must still resolve to the one Point layout.
// CHECK-LABEL: define ptr @"main.(*Point).Move$coro"(ptr %0, ptr %1, ptr %2, double %3, double %4){{.*}} {
// CHECK: call void @__llgo_coro_fault_prepare_v1
// CHECK: getelementptr inbounds %main.Point, ptr %2, i32 0, i32 0
// CHECK: fadd double
// CHECK: store double
// CHECK: getelementptr inbounds %main.Point, ptr %2, i32 0, i32 1
// CHECK: fadd double
// CHECK: store double
func (p *MyPoint) Move(dx, dy float64) {
	p.x += dx
	p.y += dy
}

// CHECK-LABEL: define ptr @"main.(*Point).Scale$coro"(ptr %0, ptr %1, ptr %2, double %3){{.*}} {
// CHECK: call void @__llgo_coro_fault_prepare_v1
// CHECK: getelementptr inbounds %main.Point, ptr %2, i32 0, i32 0
// CHECK: fmul double
// CHECK: store double
// CHECK: getelementptr inbounds %main.Point, ptr %2, i32 0, i32 1
// CHECK: fmul double
// CHECK: store double
func (p *Point) Scale(factor float64) {
	p.x *= factor
	p.y *= factor
}

// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
func main() {
	// CHECK: call ptr @"{{.*}}AllocZ"(i64 16)
	// CHECK: call ptr @"main.(*Point).Scale$coro"
	// CHECK: call void @__llgo_coro_await_prepare_v3
	// CHECK: call ptr @"main.(*Point).Move$coro"
	// CHECK: call void @__llgo_coro_await_prepare_v3
	// CHECK: call ptr @"{{.*}}PrintFloat$coro"
	// CHECK: call void @__llgo_coro_await_prepare_v3
	// CHECK: call ptr @"{{.*}}PrintByte$coro"({{.*}}i8 32)
	// CHECK: call void @__llgo_coro_await_prepare_v3
	// CHECK: call ptr @"{{.*}}PrintFloat$coro"
	// CHECK: call void @__llgo_coro_await_prepare_v3
	// CHECK: call ptr @"{{.*}}PrintByte$coro"({{.*}}i8 10)
	// CHECK: call void @__llgo_coro_await_prepare_v3
	pt := &MyPoint{1, 2}
	pt.Scale(2)
	pt.Move(3, 4)
	println(pt.x, pt.y)
}
