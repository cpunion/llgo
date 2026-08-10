package main

import (
	"unsafe"
)

var a int64 = 1<<63 - 1
var b int64 = -1 << 63
var n uint64 = 1<<64 - 1

const (
	uvnan    = 0x7FF8000000000001
	uvinf    = 0x7FF0000000000000
	uvneginf = 0xFFF0000000000000
	uvone    = 0x3FF0000000000000
	mask     = 0x7FF
	shift    = 64 - 11 - 1
	bias     = 1023
	signMask = 1 << 63
	fracMask = 1<<shift - 1
)

func Float64frombits(b uint64) float64 { return *(*float64)(unsafe.Pointer(&b)) }

// Inf returns positive infinity if sign >= 0, negative infinity if sign < 0.
func Inf(sign int) float64 {
	var v uint64
	if sign >= 0 {
		v = uvinf
	} else {
		v = uvneginf
	}
	return Float64frombits(v)
}

func IsNaN(f float64) (is bool) {
	return f != f
}

// NaN returns an IEEE 754 “not-a-number” value.
func NaN() float64 { return Float64frombits(uvnan) }

func demo() {
}

func main() {
	var s = []int{1, 2, 3, 4}
	var a = [...]int{1, 2, 3, 4}
	d := make([]byte, 4, 10)
	println(s, len(s), cap(s))
	println(d, len(d), cap(d))
	println(len(a), cap(a), cap(&a), len(&a))
	println(len([]int{1, 2, 3, 4}), len([4]int{1, 2, 3, 4}))
	println(len(s[1:]), cap(s[1:]), len(s[1:2]), cap(s[1:2]), len(s[1:2:2]), cap(s[1:2:2]))
	println(len(a[1:]), cap(a[1:]), len(a[1:2]), cap(a[1:2]), len(a[1:2:2]), cap(a[1:2:2]))

	println("hello", "hello"[1:], "hello"[1:2], len("hello"[5:]))
	println(append(s, 5, 6, 7, 8))
	data := []byte{'a', 'b', 'c'}
	data = append(data, "def"...)
	println(data)
	fns := []func(){}

	// CHECK-LABEL: define void @"main.main$1"(){{.*}} {
	// CHECK-NEXT: _llgo_0:
	// CHECK-NEXT:   ret void
	// CHECK-NEXT: }

	fns = append(fns, func() {})
	println(fns)
	var i any = 100
	println(true, 0, 100, -100, uint(255), int32(-100), 0.0, 100.5, i, &i, uintptr(unsafe.Pointer(&i)))
	var dst [3]byte
	n := copy(dst[:], data)
	println(n, dst[0], dst[1], dst[2])
	n = copy(dst[1:], "ABCD")
	println(n, dst[0], dst[1], dst[2])

	fn1 := demo

	// CHECK-LABEL: define void @"main.main$2"(){{.*}} {
	// CHECK-NEXT: _llgo_0:
	// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintString"(%"{{.*}}/runtime/internal/runtime.String" { ptr @5, i64 2 })
	// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintByte"(i8 10)
	// CHECK-NEXT:   ret void
	// CHECK-NEXT: }

	fn2 := func() {
		println("fn")
	}

	// CHECK-LABEL: define void @"main.main$3"(ptr {{(nest|swiftself)}} %0){{.*}} {
	// CHECK-NEXT: _llgo_0:
	// CHECK-NEXT:   %1 = load { ptr }, ptr %0, align 8
	// CHECK-NEXT:   %2 = extractvalue { ptr } %1, 0
	// CHECK-NEXT:   %3 = load i64, ptr %2, align 8
	// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintInt"(i64 %3)
	// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintByte"(i8 10)
	// CHECK-NEXT:   ret void
	// CHECK-NEXT: }

	fn3 := func() {
		println(n)
	}
	println(demo, fn1, fn2, fn3)

	for i, v := range "中abcd" {
		println(i, v)
	}

	println(Inf(1), Inf(-1), NaN(), IsNaN(NaN()), IsNaN(1.0))

	data1 := []byte("中abcd")
	data2 := []rune("中abcd")
	println(data1, data2)
	println(string(data1), string(data2), string(data1[3]), string(data2[0]))
	s1 := "abc"
	s2 := "abd"
	println(s1 == "abc", s1 == s2, s1 != s2, s1 < s2, s1 <= s2, s1 > s2, s1 >= s2)
}
