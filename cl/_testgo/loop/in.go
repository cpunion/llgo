// LITTEST
// Scope: common
package main

// This package owns two distinct loop lowerings without another package
// compile: dynamic defer nodes and allocation-free constant strings.
// CHECK: [[LOOP_TEXT:@[0-9]+]] = private unnamed_addr constant [4 x i8] c"loop"
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: [[HEAD_CANDIDATE:%[^, ]+]] = load ptr, ptr [[HEAD:%[^, ]+]]
// CHECK-NEXT: [[HAS_NODE:%.*]] = icmp ne ptr [[HEAD_CANDIDATE]], null
// CHECK: [[I:%.*]] = phi i64 [ 0, %{{.*}} ], [ [[NEXT_I:%.*]], %{{.*}} ]
// CHECK: [[IN_RANGE:%.*]] = icmp slt i64 [[I]], 3
// CHECK: br i1 [[IN_RANGE]], label %{{.*}}, label %{{.*}}
// CHECK: [[NODE:%.*]] = call ptr @"{{.*}}AllocU"(i64 40)
// CHECK: [[OLD_HEAD:%.*]] = load ptr, ptr [[HEAD]]
// CHECK: store ptr [[OLD_HEAD]], ptr %{{.*}}
// CHECK: store %"{{.*}}String" { ptr [[LOOP_TEXT]], i64 4 }, ptr %{{.*}}
// CHECK: store i64 [[I]], ptr %{{.*}}
// CHECK: store ptr [[NODE]], ptr [[HEAD]]
// CHECK: [[NEXT_I]] = add i64 [[I]], 1
// CHECK: call i64 @main.zStringLoop()
// CHECK: [[NEXT_NODE_PTR:%.*]] = getelementptr inbounds {{.*}}, ptr [[HEAD_CANDIDATE]], i32 0, i32 0
// CHECK-NEXT: [[NEXT_NODE:%[^, ]+]] = load ptr, ptr [[NEXT_NODE_PTR]]
// CHECK: store ptr [[NEXT_NODE]], ptr [[HEAD]]
// CHECK: call void @"{{.*}}FreeDeferNode"(ptr [[HEAD_CANDIDATE]])
// CHECK: call ptr @"{{.*}}PrintBatchV1$coro"({{.*}})

// CHECK-LABEL: define i64 @main.zStringLoop(){{.*}} {
// CHECK-NOT: Alloc
// CHECK: call i64 @main.zStringLen(%"{{.*}}String" { ptr @{{[0-9]+}}, i64 5 })
// CHECK-NOT: Alloc
// CHECK: ret i64

func main() {
	for i := 0; i < 3; i++ {
		defer println("loop", i)
	}
	if zStringLoop() != 20 {
		panic("constant string loop")
	}
}
