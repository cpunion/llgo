package main

import (
	"github.com/goplus/lib/c"
)

func genInts(n int, gen func() c.Int) []c.Int {
	a := make([]c.Int, n)
	for i := range a {
		a[i] = gen()
	}
	return a
}

func (g *generator) next() c.Int {
	g.val++
	return g.val
}

type generator struct {
	val c.Int
}

func main() {
	for _, v := range genInts(5, c.Rand) {

		c.Printf(c.Str("%d\n"), v)
	}

	initVal := c.Int(1)
	ints := genInts(5, func() c.Int {
		// CHECK-LABEL: define i32 @"main.main$1"(ptr {{(nest|swiftself)}} %0){{.*}} {
		// CHECK-NEXT: _llgo_0:
		// CHECK-NEXT:   %1 = load { ptr }, ptr %0, align 8
		// CHECK-NEXT:   %2 = extractvalue { ptr } %1, 0
		// CHECK-NEXT:   %3 = load i32, ptr %2, align 4
		// CHECK-NEXT:   %4 = mul i32 %3, 2
		// CHECK-NEXT:   %5 = extractvalue { ptr } %1, 0
		// CHECK-NEXT:   store i32 %4, ptr %5, align 4
		// CHECK-NEXT:   %6 = extractvalue { ptr } %1, 0
		// CHECK-NEXT:   %7 = load i32, ptr %6, align 4
		// CHECK-NEXT:   ret i32 %7
		// CHECK-NEXT: }
		initVal *= 2
		return initVal
	})
	for _, v := range ints {
		c.Printf(c.Str("%d\n"), v)
	}

	g := &generator{val: 1}
	for _, v := range genInts(5, g.next) {
		c.Printf(c.Str("%d\n"), v)
	}
}
