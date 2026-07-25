// LITTEST
package main

import (
	"github.com/goplus/lib/c"
)

// CHECK-LABEL: define ptr @"{{.*}}/cl/_testlibc/argv.main$coro"(
func main() {
	// CHECK: load i32, ptr @__llgo_argc, align 4
	// CHECK: load ptr, ptr @__llgo_argv, align 8
	// CHECK: call void @__llgo_coro_os_thread_foreign_call_v1
	// CHECK: call void @__llgo_coro_worker_park_v1
	// CHECK: call i32 (ptr, ...) @printf(ptr {{%.*}}, ptr {{%.*}})
	for i := c.Int(0); i < c.Argc; i++ {
		c.Printf(c.Str("%s\n"), c.Index(c.Argv, i))
	}
}
