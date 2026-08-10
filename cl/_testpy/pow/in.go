// LITTEST
// CHECK-LABEL: define ptr @"main.main$coro"
// CHECK: call void @__llgo_coro_same_m_foreign_call_v1
package main

import (
	"github.com/goplus/lib/c"
	"github.com/goplus/lib/py"
	"github.com/goplus/lib/py/math"
)

func main() {
	x := math.Pow(py.Float(2), py.Float(3))
	c.Printf(c.Str("pow(2, 3) = %f\n"), x.Float64())
}
