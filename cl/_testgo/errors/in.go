// LITTEST
package main

// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define void @"main.New$outcome"(
// CHECK: call ptr @"{{.*}}AllocZ"(
// CHECK: insertvalue %"{{.*}}iface" { ptr @{{.*}}, ptr undef }, ptr
// CHECK: store i32 1
// CHECK: ret void
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define void @"main.(*errorString).Error$outcome"(
// CHECK-NOT: call i8 @llvm.coro.suspend(
// CHECK: load %"{{.*}}String"
// CHECK: store i32 1
// CHECK: ret void
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call void @"main.New$outcome"(
// CHECK: call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK: call ptr %{{[0-9]+}}(ptr %0,
// CHECK: call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

// New returns an error that formats as the given text.
// Each call to New returns a distinct error value even if the text is identical.
func New(text string) error {
	return &errorString{text}
}

// errorString is a trivial implementation of error.
type errorString struct {
	s string
}

func (e *errorString) Error() string {
	return e.s
}

func main() {
	err := New("an error")
	println(err)
	println(err.Error())
}
