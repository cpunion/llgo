// LITTEST
package main

import (
	"sync/atomic"
)

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	var v int64

	// CHECK: store atomic i64 100, ptr [[ADDR:%[0-9]+]] seq_cst, align 8
	// CHECK: [[LOAD:%[0-9]+]] = load atomic i64, ptr [[ADDR]] seq_cst, align 8
	atomic.StoreInt64(&v, 100)
	println("store:", atomic.LoadInt64(&v))

	// CHECK: [[ADDOLD:%[0-9]+]] = atomicrmw add ptr [[ADDR]], i64 1 seq_cst, align 8
	// CHECK: [[ADDNEW:%[0-9]+]] = add i64 [[ADDOLD]], 1
	// CHECK: load i64, ptr [[ADDR]], align 8
	ret := atomic.AddInt64(&v, 1)
	println("ret:", ret, "v:", v)

	// CHECK: [[CAS1:%[0-9]+]] = cmpxchg ptr [[ADDR]], i64 100, i64 102 seq_cst seq_cst, align 8
	// CHECK: extractvalue { i64, i1 } [[CAS1]], 1
	// CHECK: load i64, ptr [[ADDR]], align 8
	swp := atomic.CompareAndSwapInt64(&v, 100, 102)
	println("swp:", swp, "v:", v)

	// CHECK: [[CAS2:%[0-9]+]] = cmpxchg ptr [[ADDR]], i64 101, i64 102 seq_cst seq_cst, align 8
	// CHECK: extractvalue { i64, i1 } [[CAS2]], 1
	// CHECK: load i64, ptr [[ADDR]], align 8
	swp = atomic.CompareAndSwapInt64(&v, 101, 102)
	println("swp:", swp, "v:", v)

	// CHECK: [[SUBOLD:%[0-9]+]] = atomicrmw add ptr [[ADDR]], i64 -1 seq_cst, align 8
	// CHECK: add i64 [[SUBOLD]], -1
	// CHECK: load i64, ptr [[ADDR]], align 8
	ret = atomic.AddInt64(&v, -1)
	println("ret:", ret, "v:", v)
}
