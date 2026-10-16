// LITTEST
// Scope: common
package main

// Bounds failures are structured coroutine outcomes. Signed and unsigned
// indices retain different predicates and fault kinds without calling the old
// thread-local PanicIndex helpers.

// CHECK-LABEL: define ptr @"main.array$coro"(ptr %0, ptr %1, i64 %2){{.*}} {
// CHECK: [[ARRAY_NEG:%[0-9]+]] = icmp slt i64 %2, 0
// CHECK: [[ARRAY_UPPER:%[0-9]+]] = icmp uge i64 %2, 2
// CHECK: [[ARRAY_OOB:%[0-9]+]] = or i1 [[ARRAY_UPPER]], [[ARRAY_NEG]]
// CHECK: br i1 [[ARRAY_OOB]],
// CHECK: call void @__llgo_coro_fault_prepare_v2({{.*}}i32 13, i64 %2, i64 2)

// CHECK-LABEL: define ptr @"main.array2$coro"(ptr %0, ptr %1, i64 %2){{.*}} {
// CHECK-NOT: icmp slt
// CHECK: [[UARRAY_OOB:%[0-9]+]] = icmp uge i64 %2, 2
// CHECK: br i1 [[UARRAY_OOB]],
// CHECK: call void @__llgo_coro_fault_prepare_v2({{.*}}i32 14, i64 %2, i64 2)

// A non-suspending return-only function is emitted as an outcome helper, not a
// coroutine frame. The narrow signed index is extended before both the check
// and the selected address.
// CHECK-LABEL: define void @"main.narrowArray$outcome"(ptr %0, ptr %1, ptr %2, i8 %3){{.*}} {
// CHECK-NOT: llvm.coro.
// CHECK: [[NARROW:%[0-9]+]] = sext i8 %3 to i64
// CHECK: [[NARROW_NEG:%[0-9]+]] = icmp slt i64 [[NARROW]], 0
// CHECK: [[NARROW_UPPER:%[0-9]+]] = icmp uge i64 [[NARROW]], 2
// CHECK: [[NARROW_OOB:%[0-9]+]] = or i1 [[NARROW_UPPER]], [[NARROW_NEG]]
// CHECK: call void @__llgo_coro_fault_payload_v2(i32 13,
// CHECK: getelementptr inbounds i64, ptr %{{[0-9]+}}, i64 [[NARROW]]

// Slice bounds use the extracted dynamic length in the structured fault.
// CHECK-LABEL: define ptr @"main.slice$coro"(ptr %0, ptr %1, i64 %2){{.*}} {
// CHECK: [[SLICE_LEN:%[0-9]+]] = extractvalue %"{{.*}}Slice" %{{[0-9]+}}, 1
// CHECK-NEXT: [[SLICE_NEG:%[0-9]+]] = icmp slt i64 %2, 0
// CHECK-NEXT: [[SLICE_UPPER:%[0-9]+]] = icmp uge i64 %2, [[SLICE_LEN]]
// CHECK-NEXT: [[SLICE_OOB:%[0-9]+]] = or i1 [[SLICE_UPPER]], [[SLICE_NEG]]
// CHECK: call void @__llgo_coro_fault_prepare_v2({{.*}}i32 13, i64 %2, i64 [[SLICE_LEN]])
func narrowArray(n int8) int {
	return [...]int{1, 2}[n]
}

// The zero-length-array success edge is unreachable at run time but must still
// lower the nil-map lookup without crashing compilation.
// Its caller has a recover boundary, so the fault-capable nil-map lookup is
// emitted as a coroutine and propagates both the bounds fault and the map
// lookup outcome through the same structured channel.
// CHECK-LABEL: define ptr @"main.zeroMapLookup$coro"(ptr %0, ptr %1, i64 %2){{.*}} {
// CHECK: [[ZERO_NEG:%[0-9]+]] = icmp slt i64 %2, 0
// CHECK-NEXT: [[ZERO_UPPER:%[0-9]+]] = icmp uge i64 %2, 0
// CHECK-NEXT: [[ZERO_OOB:%[0-9]+]] = or i1 [[ZERO_UPPER]], [[ZERO_NEG]]
// CHECK: call void @__llgo_coro_fault_prepare_v2({{.*}}i32 13, i64 %2, i64 0)
// CHECK: [[MAP_CORO:%[0-9]+]] = call ptr @"{{.*}}mapaccess1_fast64$coro"({{.*}}ptr @"map[_llgo_int]_llgo_int", ptr null, i64 0)
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[MAP_CORO]],
func zeroMapLookup(n int) int {
	return [0]map[int]int{}[n][0]
}

func checkZeroMapLookup() {
	defer func() {
		if recover() == nil {
			panic("zero map lookup must error")
		}
	}()
	zeroMapLookup(0)
}

func main() {
	if narrowArray(1) != 2 {
		panic("narrow index")
	}
	checkZeroMapLookup()
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("array -1 must error")
		}
	}()
	array(-1)
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("array 2 must error")
		}
	}()
	array(2)
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("array2 must error")
		}
	}()
	array2(2)
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("slice -1 must error")
		}
	}()
	slice(-1)
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("slice 2 must error")
		}
	}()
	slice(2)
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("slice2 2 must error")
		}
	}()
	slice2(2)
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("2 must error")
		}
	}()
	a := [...]int{1, 2}
	var n = -1
	println(a[n])
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("-1 must error")
		}
	}()
	a := [...]int{1, 2}
	var n = 2
	println(a[n])
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("2 must error")
		}
	}()
	a := [...]int{1, 2}
	var n uint = 2
	println(a[n])
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("2 must error")
		}
	}()
	a := []int{1, 2}
	var n = -1
	println(a[n])
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("-1 must error")
		}
	}()
	a := []int{1, 2}
	var n = 2
	println(a[n])
}

func init() {
	defer func() {
		if r := recover(); r == nil {
			panic("2 must error")
		}
	}()
	a := []int{1, 2}
	var n uint = 2
	println(a[n])
}

func array(n int) {
	println([...]int{1, 2}[n])
}

func array2(n uint) {
	println([...]int{1, 2}[n])
}

func slice(n int) {
	println([]int{1, 2}[n])
}

func slice2(n int) {
	println([]int{1, 2}[n])
}
