// LITTEST
package main

// CHECK: {{^}}@0 = private unnamed_addr constant [6 x i8] c"count:", align 1{{$}}

type stateFn func(*counter) stateFn

type counter struct {
	value int
	max   int
	state stateFn
}

func countState(c *counter) stateFn {
	c.value++
	println("count:", c.value)

	if c.value >= c.max {
		return nil
	}
	return countState
}

func main() {
	c := &counter{max: 5, state: countState}

	for c.state != nil {
		c.state = c.state(c)
	}
}

// CHECK-LABEL: define %main.stateFn @main.countState(ptr %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %1 = getelementptr inbounds %main.counter, ptr %0, i32 0, i32 0
// CHECK-NEXT:   %2 = icmp eq ptr %0, null
// CHECK-NEXT:   br i1 %2, label %3, label %4
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:                                          ; preds = %21
// CHECK-NEXT:   ret %main.stateFn zeroinitializer
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_2:                                          ; preds = %21
// CHECK-NEXT:   ret %main.stateFn { ptr @main.countState, ptr null }
// CHECK-EMPTY:
// CHECK-NEXT: 3:                                                ; preds = %_llgo_0
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 4:                                                ; preds = %_llgo_0
// CHECK-NEXT:   %5 = load i64, ptr %1, align 8
// CHECK-NEXT:   %6 = add i64 %5, 1
// CHECK-NEXT:   %7 = getelementptr inbounds %main.counter, ptr %0, i32 0, i32 0
// CHECK-NEXT:   store i64 %6, ptr %7, align 8
// CHECK-NEXT:   %8 = getelementptr inbounds %main.counter, ptr %0, i32 0, i32 0
// CHECK-NEXT:   %9 = icmp eq ptr %0, null
// CHECK-NEXT:   br i1 %9, label %10, label %11
// CHECK-EMPTY:
// CHECK-NEXT: 10:                                               ; preds = %4
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 11:                                               ; preds = %4
// CHECK-NEXT:   %12 = load i64, ptr %8, align 8
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintString"(%"{{.*}}/runtime/internal/runtime.String" { ptr @0, i64 6 })
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintByte"(i8 32)
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintInt"(i64 %12)
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintByte"(i8 10)
// CHECK-NEXT:   %13 = getelementptr inbounds %main.counter, ptr %0, i32 0, i32 0
// CHECK-NEXT:   %14 = icmp eq ptr %0, null
// CHECK-NEXT:   br i1 %14, label %15, label %16
// CHECK-EMPTY:
// CHECK-NEXT: 15:                                               ; preds = %11
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 16:                                               ; preds = %11
// CHECK-NEXT:   %17 = load i64, ptr %13, align 8
// CHECK-NEXT:   %18 = getelementptr inbounds %main.counter, ptr %0, i32 0, i32 1
// CHECK-NEXT:   %19 = icmp eq ptr %0, null
// CHECK-NEXT:   br i1 %19, label %20, label %21
// CHECK-EMPTY:
// CHECK-NEXT: 20:                                               ; preds = %16
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 21:                                               ; preds = %16
// CHECK-NEXT:   %22 = load i64, ptr %18, align 8
// CHECK-NEXT:   %23 = icmp sge i64 %17, %22
// CHECK-NEXT:   br i1 %23, label %_llgo_1, label %_llgo_2
// CHECK-NEXT: }

// CHECK-LABEL: define void @main.init(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %0 = load i1, ptr @"main.init$guard", align 1
// CHECK-NEXT:   br i1 %0, label %_llgo_2, label %_llgo_1
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:                                          ; preds = %_llgo_0
// CHECK-NEXT:   store i1 true, ptr @"main.init$guard", align 1
// CHECK-NEXT:   br label %_llgo_2
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_2:                                          ; preds = %_llgo_1, %_llgo_0
// CHECK-NEXT:   ret void
// CHECK-NEXT: }

// CHECK-LABEL: define void @main.main(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %0 = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 32)
// CHECK-NEXT:   %1 = getelementptr inbounds %main.counter, ptr %0, i32 0, i32 1
// CHECK-NEXT:   %2 = getelementptr inbounds %main.counter, ptr %0, i32 0, i32 2
// CHECK-NEXT:   store i64 5, ptr %1, align 8
// CHECK-NEXT:   store %main.stateFn { ptr @main.countState, ptr null }, ptr %2, align 8
// CHECK-NEXT:   br label %_llgo_3
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:                                          ; preds = %_llgo_3
// CHECK-NEXT:   %3 = getelementptr inbounds %main.counter, ptr %0, i32 0, i32 2
// CHECK-NEXT:   %4 = load %main.stateFn, ptr %3, align 8
// CHECK-NEXT:   %5 = extractvalue %main.stateFn %4, 1
// CHECK-NEXT:   %6 = extractvalue %main.stateFn %4, 0
// CHECK-NEXT:   %__llgo_funcval_code = call ptr asm "", "=r,0"(ptr %6)
// CHECK-NEXT:   %7 = call %main.stateFn %__llgo_funcval_code(ptr {{(nest|swiftself)}} %5, ptr %0)
// CHECK-NEXT:   %8 = getelementptr inbounds %main.counter, ptr %0, i32 0, i32 2
// CHECK-NEXT:   store %main.stateFn %7, ptr %8, align 8
// CHECK-NEXT:   br label %_llgo_3
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_2:                                          ; preds = %_llgo_3
// CHECK-NEXT:   ret void
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_3:                                          ; preds = %_llgo_1, %_llgo_0
// CHECK-NEXT:   %9 = getelementptr inbounds %main.counter, ptr %0, i32 0, i32 2
// CHECK-NEXT:   %10 = load %main.stateFn, ptr %9, align 8
// CHECK-NEXT:   %11 = extractvalue %main.stateFn %10, 0
// CHECK-NEXT:   %12 = icmp ne ptr %11, null
// CHECK-NEXT:   br i1 %12, label %_llgo_1, label %_llgo_2
// CHECK-NEXT: }
