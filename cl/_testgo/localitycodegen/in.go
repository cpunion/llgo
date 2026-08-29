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
// CHECK: call ptr @__llgo_coro_foreign_reentry_acquire_v1(
// CHECK: call ptr @"ExportedLocality$coro"(
// CHECK: call ptr @llvm.coro.promise(
// CHECK: call i32 @__llgo_coro_foreign_reentry_run_v1(
// CHECK-DAG: ret ptr
// CHECK-DAG: call void @__llgo_coro_foreign_reentry_failure_v1(
// CHECK-LABEL: define ptr @"main.__llgo_local_dispatch_tls_0$coro"(
// CHECK: [[LOCAL_INIT_HANDLE:%.*]] = call ptr @"main.__llgo_local_init_0$coro"(
// CHECK-NEXT: ret ptr [[LOCAL_INIT_HANDLE]]
// CHECK-LABEL: define ptr @"main.__llgo_local_init_0$coro"(
// CHECK: call ptr @main.newPointer()
// CHECK: call void @"{{.*}}LocalPackageLogical$outcome"(
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
// CHECK: call void @"{{.*}}LocalPackageLogical$outcome"(
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
