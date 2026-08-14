// LITTEST
package main

import (
	"github.com/goplus/lib/c"
	"github.com/goplus/lib/c/sqlite"
)

// CHECK-LABEL: define ptr @"main.check$coro"(
func check(err sqlite.Errno) {
	if err != sqlite.OK {
		// CHECK: call i32 @__llgo_coro_os_thread_foreign_call_v1
		c.Printf(c.Str("==> Error: (%d) %s\n"), err, err.Errstr())
		c.Exit(1)
	}
}

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	// CHECK: [[OPEN:%[0-9]+]] = call ptr @"github.com/goplus/lib/c/sqlite.OpenV2$coro"({{.*}}ptr @{{[0-9]+}}, i32 130, ptr null)
	// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[OPEN]]
	db, err := sqlite.OpenV2(c.Str(":memory:"), sqlite.OpenReadWrite|sqlite.OpenMemory, nil)
	check(err)

	// CHECK: call i32 @__llgo_coro_os_thread_foreign_call_v1
	db.Close()
}

// CHECK: call ptr @sqlite3_errstr(i32 {{%.*}})
// CHECK: call i32 @sqlite3_close(ptr {{%.*}})
