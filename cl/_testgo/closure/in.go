// LITTEST
package main

type T func(n int)

// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
func main() {
	// CHECK: call ptr @"{{.*}}AllocZ"(i64 16)
	// CHECK: store %"{{.*}}String" { ptr @{{[0-9]+}}, i64 3 }, ptr %{{.*}}, align 8
	// CHECK: call ptr @"{{.*}}AllocU"(i64 8)
	// CHECK: { ptr @"main.main$2$coro", ptr undef }
	// CHECK: call ptr @"main.main$1$coro"(ptr %0, ptr {{.*}}, i64 100)
	// CHECK: call ptr %{{.*}}(ptr %0, ptr {{.*}}, ptr %{{.*}}, i64 200)
	var env string = "env"
	var v1 T = func(i int) {
		// CHECK-LABEL: define ptr @"main.main$1$coro"(ptr %0, ptr %1, i64 %2){{.*}} {
		// CHECK-DAG: call ptr @"{{.*}}PrintString$coro"(ptr %0, ptr %{{.*}}, %"{{.*}}String" { ptr @{{[0-9]+}}, i64 4 })
		// CHECK-DAG: call ptr @"{{.*}}PrintInt$coro"(ptr %0, ptr %{{.*}}, i64 %2)
		// CHECK-DAG: call void @__llgo_coro_complete_prepare_v2
		println("func", i)
	}
	var v2 T = func(i int) {
		// CHECK-LABEL: define ptr @"main.main$2$coro"(ptr %0, ptr %1, ptr swiftself %2, i64 %3){{.*}} {
		// CHECK-DAG: load { ptr }, ptr %2, align 8
		// CHECK-DAG: load %"{{.*}}String", ptr %{{.*}}, align 8
		// CHECK-DAG: call ptr @"{{.*}}PrintString$coro"(ptr %0, ptr %{{.*}}, %"{{.*}}String" { ptr @{{[0-9]+}}, i64 7 })
		// CHECK-DAG: call ptr @"{{.*}}PrintInt$coro"(ptr %0, ptr %{{.*}}, i64 %3)
		// CHECK-DAG: call ptr @"{{.*}}PrintString$coro"(ptr %0, ptr %{{.*}}, %"{{.*}}String" %{{.*}})
		// CHECK-DAG: call void @__llgo_coro_complete_prepare_v2
		println("closure", i, env)
	}
	v1(100)
	v2(200)
}

// CHECK-LABEL: define linkonce ptr @__llgo_coro_func_coro_v1.{{.*}}(ptr %0, ptr %1, ptr %2, i64 %3) {
// CHECK: call ptr @"main.main$2$coro"(ptr %0, ptr %1, ptr swiftself %2, i64 %3)
// CHECK-NOT: __llgo_stub.
