// LITTEST
package foo

// Bar and F are dynamically publishable and may panic while boxing, so each
// has one coroutine primary and one allocation-preserving outcome entry. There
// is no third plain source body and no scheduler-root factory in ModeGen.
// CHECK-LABEL: define ptr @"{{.*}}/cl/_testdata/foo.Bar$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call token @llvm.coro.id
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 8)

func Bar() any {
	return struct{ V int }{1}
}

// CHECK-LABEL: define void @"{{.*}}/cl/_testdata/foo.Bar$outcome"(ptr %0, ptr %1, ptr %2){{.*}} {
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 8)
// CHECK: store %"{{.*}}/runtime/internal/runtime.eface" %{{[0-9]+}}, ptr %{{[0-9]+}}, align 8
// CHECK: store i32 1, ptr %{{[0-9]+}}, align 4
// CHECK-NEXT: ret void

// CHECK-LABEL: define ptr @"{{.*}}/cl/_testdata/foo.F$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call token @llvm.coro.id
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 8)

func F() any {
	return struct{ v int }{1}
}

// CHECK-LABEL: define void @"{{.*}}/cl/_testdata/foo.F$outcome"(ptr %0, ptr %1, ptr %2){{.*}} {
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 8)
// CHECK: store %"{{.*}}/runtime/internal/runtime.eface" %{{[0-9]+}}, ptr %{{[0-9]+}}, align 8
// CHECK: store i32 1, ptr %{{[0-9]+}}, align 4
// CHECK-NEXT: ret void

type Foo struct {
	pb *byte
	F  float32
}

// CHECK-LABEL: define ptr @"{{.*}}/cl/_testdata/foo.Foo.Pb"(
// CHECK-SAME: %"{{.*}}/cl/_testdata/foo.Foo" %0){{.*}} {
// CHECK-NOT: @llvm.coro.
// CHECK: [[PB:%[0-9]+]] = load ptr, ptr %{{[0-9]+}}, align 8
// CHECK-NEXT: ret ptr [[PB]]

func (v Foo) Pb() *byte {
	return v.pb
}

type Gamer interface {
	initGame()
	Load()
}

type Game struct {
}

func (g *Game) initGame() {
}

// CHECK-LABEL: define ptr @"{{.*}}/cl/_testdata/foo.(*Game).Load$coro"(ptr %0, ptr %1, ptr %2){{.*}} {
// CHECK: call token @llvm.coro.id
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintBatchV1$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
func (g *Game) Load() {
	println("load")
}
