// LITTEST
package main

// CHECK-DAG: private unnamed_addr constant [18 x i8] c"zero-sized capture", align 1
// CHECK-DAG: private unnamed_addr constant [26 x i8] c"zero-sized capture address", align 1
// CHECK-DAG: private unnamed_addr constant [26 x i8] c"zero-sized pointer capture", align 1
// CHECK-DAG: private unnamed_addr constant [25 x i8] c"nil receiver method value", align 1
// CHECK-DAG: private unnamed_addr constant [32 x i8] c"typed-nil interface method value", align 1
// CHECK-DAG: @"__llgo.moduleZeroSizedAlloc$" = linkonce_odr unnamed_addr global i8 0

type nilReceiver struct{}

func (p *nilReceiver) IsNil() bool {
	return p == nil
}

func zeroSizedCapture() func() int {
	captured := struct{}{}
	return func() int {
		if captured != (struct{}{}) {
			return 0
		}
		return 42
	}
}

func zeroSizedAddressCapture() (func() *struct{}, *struct{}) {
	captured := struct{}{}
	return func() *struct{} { return &captured }, &captured
}

func zeroSizedPointerCapture(pointer *struct{}) func() bool {
	return func() bool { return pointer == nil }
}

func main() {
	if zeroSizedCapture()() != 42 {
		panic("zero-sized capture")
	}
	addressClosure, address := zeroSizedAddressCapture()
	if addressClosure() != address {
		panic("zero-sized capture address")
	}
	if !zeroSizedPointerCapture(nil)() {
		panic("zero-sized pointer capture")
	}

	var receiver *nilReceiver
	method := receiver.IsNil
	if !method() {
		panic("nil receiver method value")
	}

	var typedNil interface{ IsNil() bool } = receiver
	interfaceMethod := typedNil.IsNil
	if !interfaceMethod() {
		panic("typed-nil interface method value")
	}
	println("ok")
}

// The dynamic closure calls make main managed, while the source-level API and
// result remain ordinary Go values. expect.txt verifies the complete runtime
// behavior; these checks cover only the representation boundaries.
// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1)
// CHECK: call ptr @"main.zeroSizedCapture$coro"(
// CHECK: call ptr @"main.zeroSizedAddressCapture$coro"(
// CHECK: call ptr @"main.zeroSizedPointerCapture$coro"(

// A nil receiver is a meaningful value and must not be confused with an
// elidable zero-sized lexical environment.
// CHECK-LABEL: define i1 @"main.(*nilReceiver).IsNil"(ptr %0)
// CHECK: icmp eq ptr %0, null

// Capturing the address of a zero-sized local uses the one canonical non-nil
// sentinel both in the returned closure and in the separately returned value.
// No environment allocation is required.
// CHECK-LABEL: define ptr @"main.zeroSizedAddressCapture$coro"(ptr %0, ptr %1)
// CHECK-NOT: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(
// CHECK: { ptr @__llgo_coro_func_descriptor_v1.{{.*}}, ptr null }
// CHECK: store ptr @"__llgo.moduleZeroSizedAlloc$"

// CHECK-LABEL: define ptr @"main.zeroSizedAddressCapture$1"()
// CHECK: ret ptr @"__llgo.moduleZeroSizedAlloc$"

// An all-zero-sized capture has no physical environment parameter. Its load is
// statically backed by the sentinel and must not retain a nil-deref helper.
// CHECK-LABEL: define ptr @"main.zeroSizedCapture$coro"(ptr %0, ptr %1)
// CHECK-NOT: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(
// CHECK: { ptr @__llgo_coro_func_descriptor_v1.{{.*}}, ptr null }

// CHECK-LABEL: define ptr @"main.zeroSizedCapture$1$coro"(ptr %0, ptr %1)
// CHECK-NOT: AssertNilDeref

// Capturing a pointer remains a real one-word environment, even when the
// pointee type has size zero: nil/non-nil is semantically observable.
// CHECK-LABEL: define ptr @"main.zeroSizedPointerCapture$coro"(ptr %0, ptr %1, ptr %2)
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 8)

// CHECK-LABEL: define ptr @"main.zeroSizedPointerCapture$1$coro"(ptr %0, ptr %1, ptr swiftself %2)
// CHECK: load { ptr }, ptr %2
