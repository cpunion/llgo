// LITTEST
package main

import (
	"github.com/goplus/lib/c"
	"github.com/goplus/lib/c/sync/atomic"
)

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	var v int64

	// CHECK: store atomic i64 100, ptr [[ADDR:%[0-9]+]] seq_cst, align 8
	atomic.Store(&v, 100)
	// CHECK: load atomic i64, ptr [[ADDR]] seq_cst, align 8
	// CHECK: call i32 @__llgo_coro_os_thread_foreign_call_v1
	c.Printf(c.Str("store: %ld\n"), atomic.Load(&v))
	// CHECK: atomicrmw add ptr [[ADDR]], i64 1 seq_cst, align 8
	ret := atomic.Add(&v, 1)
	c.Printf(c.Str("ret: %ld, v: %ld\n"), ret, v)

	// CHECK: cmpxchg ptr [[ADDR]], i64 100, i64 102 seq_cst seq_cst, align 8
	ret, _ = atomic.CompareAndExchange(&v, 100, 102)
	c.Printf(c.Str("ret: %ld vs 100, v: %ld\n"), ret, v)

	// CHECK: cmpxchg ptr [[ADDR]], i64 101, i64 102 seq_cst seq_cst, align 8
	ret, _ = atomic.CompareAndExchange(&v, 101, 102)
	c.Printf(c.Str("ret: %ld vs 101, v: %ld\n"), ret, v)

	// CHECK: atomicrmw sub ptr [[ADDR]], i64 1 seq_cst, align 8
	ret = atomic.Sub(&v, 1)
	c.Printf(c.Str("ret: %ld, v: %ld\n"), ret, v)
}
