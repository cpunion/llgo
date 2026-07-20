// LITTEST
package main

import (
	"github.com/goplus/lib/c"
)

// CHECK-LABEL: define void @"{{.*}}/cl/_testrt/unreachable.foo"(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:{{.*}}; No predecessors!
// CHECK-NEXT:   ret void
// CHECK-NEXT: }
func foo() {
	c.Unreachable()
}

// Keep a source Jump and merge Phi after the intrinsic. The unreachable
// lowering must move that tail to a dead physical continuation instead of
// either appending a second terminator or dropping the Phi predecessor.
func unreachableMerge(cond bool, value int) int {
	result := value
	if cond {
		c.Unreachable()
		result = value + 1
	}
	return result
}

// CHECK-LABEL: define void @"{{.*}}/cl/_testrt/unreachable.main"(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   call void @"{{.*}}/cl/_testrt/unreachable.foo"()
// CHECK-NEXT:   %0 = call i32 (ptr, ...) @printf(ptr @0)
// CHECK-NEXT:   ret void
// CHECK-NEXT: }
func main() {
	foo()
	c.Printf(c.Str("Hello\n"))
}
