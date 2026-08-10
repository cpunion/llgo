// LITTEST
// CHECK-LABEL: define ptr @"main.main$coro"
// CHECK: call void @__llgo_coro_same_m_foreign_call_v1
package main

import (
	"github.com/goplus/lib/c"
	"github.com/goplus/lib/py/math"
)

func main() {
	c.Printf(c.Str("pi = %f\n"), math.Pi.Float64())
}
