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
	x := math.Gcd(py.Long(60), py.Long(20), py.Long(25))
	c.Printf(c.Str("gcd(60, 20, 25) = %d\n"), x.Long())
}
