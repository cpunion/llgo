// LITTEST
package main

import "github.com/goplus/lib/c"

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	// The C vector and each C string outlive an await, so neither may be a
	// dynamic native-stack allocation in the physical coroutine.
	// CHECK-NOT: alloca ptr, i64
	// CHECK-NOT: alloca i8, i64
	// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64
	// CHECK-NOT: alloca ptr, i64
	// CHECK-NOT: alloca i8, i64
	// CHECK: [[CSTRMEM:%[0-9]+]] = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64
	// CHECK-NOT: alloca ptr, i64
	// CHECK-NOT: alloca i8, i64
	// CHECK: call void @"{{.*}}/runtime/internal/runtime.CStrCopy$outcome"({{.*}}ptr [[CSTRMEM]]
	// CHECK-NOT: alloca ptr, i64
	// CHECK-NOT: alloca i8, i64
	// CHECK: call void @__llgo_coro_worker_park_v1
	cstrs := c.AllocaCStrs([]string{"a", "b", "c"}, true)
	n := 0
	for {
		cstr := *c.Advance(cstrs, n)
		if cstr == nil {
			break
		}
		c.Printf(c.Str("%s\n"), cstr)
		n++
	}
}

// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_foreign_thunk_v1_
// CHECK: call i32 (ptr, ...) @printf(ptr {{%.*}}, ptr {{%.*}})
