// LITTEST darwin/arm64 linux/amd64 windows/arm64 windows/amd64
// Scope: arch (arm64, amd64)
package main

type CInt = int32

type Fn func(int) int

//go:noinline
func callCInt(fn func(CInt) CInt, x CInt) CInt {
	return fn(x)
}

type S struct {
	v int
}

func (s S) Inc(x int) int {
	return s.v + x
}

func (s *S) Add(x int) int {
	return s.v + x
}

func globalAdd(x, y int) int {
	return x + y
}

func goCInt(x CInt) CInt {
	return x + 1
}

func main() {
	nf := makeNoFree()
	wf := makeWithFree(3)
	_ = nf(1)
	_ = wf(2)

	g := globalAdd
	_ = g(1, 2)

	s := &S{v: 5}
	mv := s.Add
	_ = mv(7)
	me := (*S).Add
	_ = me(s, 8)

	var i interface{ Add(int) int } = s
	im := i.Add
	_ = im(9)

	_ = callCInt(goCInt, -3)
}

func makeNoFree() Fn {
	return func(x int) int { return x + 1 }
}

func makeWithFree(base int) Fn {
	return func(x int) int { return x + base }
}

// Dynamic Go function values carry a compiler-emitted descriptor and an
// environment. The dispatcher validates that descriptor once, then selects the
// typed structured or plain entry without recovering metadata from an address.
// CHECK-LABEL: define ptr @"main.callCInt$coro"(ptr %0, ptr %1, { ptr, ptr } %2, i32 %3){{.*}} {
// CHECK: extractvalue { ptr, ptr } %2, 0
// CHECK: [[C_DESC:%[0-9]+]] = extractvalue { ptr, ptr } %2, 0
// CHECK-NEXT: [[C_ENV:%[0-9]+]] = extractvalue { ptr, ptr } %2, 1
// CHECK-NEXT: load { i32, i32, i64, i64, ptr, ptr, i64, i64, ptr }, ptr [[C_DESC]]
// CHECK: %coro.dispatch.version.invalid = icmp ne i32
// CHECK: [[C_STRUCTURED:%[0-9]+]] = call ptr %{{[0-9]+}}(ptr %0, ptr %{{[0-9]+}}, ptr [[C_ENV]], i32 %3)
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[C_STRUCTURED]],
// CHECK: [[C_PLAIN:%[0-9]+]] = call i32 %{{[0-9]+}}(ptr [[C_ENV]], i32 %3)

// Direct non-suspending Go calls remain direct.
// CHECK-LABEL: define i64 @main.globalAdd(i64 %0, i64 %1){{.*}} {
// CHECK: [[GLOBAL_SUM:%[0-9]+]] = add i64 %0, %1
// CHECK-NEXT: ret i64 [[GLOBAL_SUM]]

// Closure factories are outcome-only, while bound/interface method values use
// {descriptor, environment} transport and a typed coroutine entry.
// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call void @"main.makeNoFree$outcome"(
// CHECK: call void @"main.makeWithFree$outcome"(
// CHECK: call i64 @main.globalAdd(i64 1, i64 2)
// CHECK: [[BOUND_PAIR:%[0-9]+]] = insertvalue { ptr, ptr } { ptr @"__llgo$generated$bound$Add$bound$llgo$promoted$v1${{[0-9a-f]+}}$coro", ptr undef }, ptr %{{[0-9]+}}, 1
// CHECK: [[BOUND_ENV:%[0-9]+]] = extractvalue { ptr, ptr } [[BOUND_PAIR]], 1
// CHECK: call ptr @"__llgo$generated$bound$Add$bound$llgo$promoted$v1${{[0-9a-f]+}}$coro"({{.*}}ptr {{swiftself|nest}} [[BOUND_ENV]], i64 7)
// CHECK: call ptr @"__llgo$generated$thunk$Add$thunk$llgo$promoted$v1${{[0-9a-f]+}}$coro"({{.*}}i64 8)
// CHECK: call ptr @"__llgo$generated$bound$Add$bound$llgo$promoted$v1${{[0-9a-f]+}}$coro"({{.*}}i64 9)
// CHECK: call ptr @"main.callCInt$coro"({{.*}}{ ptr, ptr } { ptr @__llgo_coro_func_descriptor_v2.{{[0-9a-f.]+}}, ptr null }, i32 -3)

// No-capture closures use a null environment; capturing closures allocate one
// typed environment and place it next to the descriptor.
// CHECK-LABEL: define void @"main.makeNoFree$outcome"(ptr %0, ptr %1, ptr %2){{.*}} {
// CHECK-NOT: llvm.coro.
// CHECK: store %main.Fn { ptr @__llgo_coro_func_descriptor_v2.{{[0-9a-f.]+}}, ptr null },

// CHECK-LABEL: define i64 @"main.makeNoFree$1"(i64 %0){{.*}} {
// CHECK: [[NO_FREE_RESULT:%[0-9]+]] = add i64 %0, 1
// CHECK-NEXT: ret i64 [[NO_FREE_RESULT]]

// CHECK-LABEL: define void @"main.makeWithFree$outcome"(ptr %0, ptr %1, ptr %2, i64 %3){{.*}} {
// CHECK-NOT: llvm.coro.
// CHECK: [[CAPTURE:%[0-9]+]] = call ptr @"{{.*}}AllocZ"(i64 8)
// CHECK: store i64 %3, ptr [[CAPTURE]]
// CHECK: [[ENV:%[0-9]+]] = call ptr @"{{.*}}AllocU"(i64 8)
// CHECK: store ptr [[CAPTURE]], ptr %{{[0-9]+}}
// CHECK: insertvalue { ptr, ptr } { ptr @__llgo_coro_func_descriptor_v2.{{[0-9a-f.]+}}, ptr undef }, ptr [[ENV]], 1

// ARM64-LABEL: define i64 @"main.makeWithFree$1"(ptr swiftself %0, i64 %1){{.*}} {
// AMD64-LABEL: define i64 @"main.makeWithFree$1"(ptr nest %0, i64 %1){{.*}} {
// CHECK: [[CAPTURE_BOX:%[0-9]+]] = load { ptr }, ptr %0
// CHECK: [[CAPTURE_ADDR:%[0-9]+]] = extractvalue { ptr } [[CAPTURE_BOX]], 0
// CHECK: [[CAPTURE_VALUE:%[0-9]+]] = load i64, ptr [[CAPTURE_ADDR]]
// CHECK: [[CAPTURE_RESULT:%[0-9]+]] = add i64 %1, [[CAPTURE_VALUE]]
// CHECK-NEXT: ret i64 [[CAPTURE_RESULT]]

// CHECK-NOT: __llgo_stub
