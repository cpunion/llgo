// LITTEST
package main

// Logical locality is stored in a scheduler-visible package block rather than
// native TLS. Its initializer and value accessors therefore share the normal
// coroutine await protocol, including when values is spawned as a goroutine.
//
// CHECK: @main.__llgo_local_cache = global ptr null
// CHECK-NOT: thread_local
// CHECK-NOT: RegisterLocalRoot
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @ExportedLocality()
// CHECK: call ptr @"{{.*}}EnterLocalContext"(
// CHECK: call ptr @"{{.*}}LocalPackageLogical"(
// CHECK: call void @"{{.*}}LeaveLocalContext"(
// CHECK: ret ptr
// CHECK-LABEL: define ptr @"main.__llgo_local_dispatch_tls_0$coro"(
// CHECK: call ptr @"main.__llgo_local_init_0$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-LABEL: define ptr @"main.__llgo_local_init_0$coro"(
// CHECK: call ptr @main.newPointer()
// CHECK: call ptr @"{{.*}}LocalPackageLogical$coro"(
// CHECK: call ptr @"{{.*}}EnsureLogicalLocalInitializer$coro"(
// CHECK-LABEL: define ptr @"main.init$coro"(
// CHECK: call ptr @main.newPointer()
// CHECK: call ptr @"{{.*}}EnsureLogicalLocalInitializer$coro"(
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"main.values$coro"(
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(ptr %0)
// CHECK: call ptr @"main.values$coro"(
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK-LABEL: define ptr @main.newPointer()
// CHECK: ret ptr @main.backing
// CHECK-LABEL: define ptr @"main.values$coro"(
// CHECK: call ptr @"{{.*}}LocalPackageLogical$coro"(
// CHECK: call ptr @"{{.*}}EnsureLogicalLocalInitializer$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

var backing int

func newPointer() *int {
	return &backing
}

//llgo:tls
var scalar int

//llgo:gls
var pointer *int

//llgo:tls
var initialized = newPointer()

func values() (int, *int, *int) {
	return scalar, pointer, initialized
}

//export ExportedLocality
func ExportedLocality() *int {
	return pointer
}

func main() {
	_, _, _ = values()
	go values()
}
