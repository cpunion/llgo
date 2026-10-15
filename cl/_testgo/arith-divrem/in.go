// LITTEST
// Scope: common
package main

// Integer division must guard LLVM's undefined zero-divisor case and the
// signed minInt/-1 overflow while preserving Go's defined result.

// CHECK-LABEL: define void @"main.signedDiv$outcome"(ptr %0, ptr %1, ptr %2, i64 %3, i64 %4)
// CHECK: [[SD_ZERO:%[0-9]+]] = icmp eq i64 %4, 0
// CHECK-NEXT: br i1 [[SD_ZERO]]
// CHECK: call void @__llgo_coro_fault_payload_v1(i32 11,
// CHECK: [[SD_MIN:%[0-9]+]] = icmp eq i64 %3, -9223372036854775808
// CHECK: [[SD_NEG_ONE:%[0-9]+]] = icmp eq i64 %4, -1
// CHECK: [[SD_OVERFLOW:%[0-9]+]] = and i1 [[SD_MIN]], [[SD_NEG_ONE]]
// CHECK: [[SD_SAFE_X:%[0-9]+]] = select i1 [[SD_OVERFLOW]], i64 0, i64 %3
// CHECK: [[SD_SAFE_Y:%[0-9]+]] = select i1 [[SD_OVERFLOW]], i64 1, i64 %4
// CHECK: [[SD_VALUE:%[0-9]+]] = sdiv i64 [[SD_SAFE_X]], [[SD_SAFE_Y]]
// CHECK: [[SD_RESULT:%[0-9]+]] = select i1 [[SD_OVERFLOW]], i64 %3, i64 [[SD_VALUE]]
// CHECK: store i64 [[SD_RESULT]], ptr
func signedDiv(x, y int64) int64 { return x / y }

// CHECK-LABEL: define void @"main.signedRem$outcome"(ptr %0, ptr %1, ptr %2, i64 %3, i64 %4)
// CHECK: [[SR_ZERO:%[0-9]+]] = icmp eq i64 %4, 0
// CHECK-NEXT: br i1 [[SR_ZERO]]
// CHECK: call void @__llgo_coro_fault_payload_v1(i32 11,
// CHECK: [[SR_MIN:%[0-9]+]] = icmp eq i64 %3, -9223372036854775808
// CHECK: [[SR_NEG_ONE:%[0-9]+]] = icmp eq i64 %4, -1
// CHECK: [[SR_OVERFLOW:%[0-9]+]] = and i1 [[SR_MIN]], [[SR_NEG_ONE]]
// CHECK: [[SR_SAFE_X:%[0-9]+]] = select i1 [[SR_OVERFLOW]], i64 0, i64 %3
// CHECK: [[SR_SAFE_Y:%[0-9]+]] = select i1 [[SR_OVERFLOW]], i64 1, i64 %4
// CHECK: [[SR_VALUE:%[0-9]+]] = srem i64 [[SR_SAFE_X]], [[SR_SAFE_Y]]
// CHECK: [[SR_RESULT:%[0-9]+]] = select i1 [[SR_OVERFLOW]], i64 0, i64 [[SR_VALUE]]
// CHECK: store i64 [[SR_RESULT]], ptr
func signedRem(x, y int64) int64 { return x % y }

// CHECK-LABEL: define void @"main.unsignedDiv$outcome"(ptr %0, ptr %1, ptr %2, i64 %3, i64 %4)
// CHECK: [[UD_ZERO:%[0-9]+]] = icmp eq i64 %4, 0
// CHECK-NEXT: br i1 [[UD_ZERO]]
// CHECK: call void @__llgo_coro_fault_payload_v1(i32 11,
// CHECK: [[UD_RESULT:%[0-9]+]] = udiv i64 %3, %4
// CHECK: store i64 [[UD_RESULT]], ptr
func unsignedDiv(x, y uint64) uint64 { return x / y }

// CHECK-LABEL: define void @"main.unsignedRem$outcome"(ptr %0, ptr %1, ptr %2, i64 %3, i64 %4)
// CHECK: [[UR_ZERO:%[0-9]+]] = icmp eq i64 %4, 0
// CHECK-NEXT: br i1 [[UR_ZERO]]
// CHECK: call void @__llgo_coro_fault_payload_v1(i32 11,
// CHECK: [[UR_RESULT:%[0-9]+]] = urem i64 %3, %4
// CHECK: store i64 [[UR_RESULT]], ptr
func unsignedRem(x, y uint64) uint64 { return x % y }

func main() {
	println(signedDiv(17, 5), signedRem(-17, 5))
	println(unsignedDiv(17, 5), unsignedRem(17, 5))
	min := int64(-1 << 63)
	println(signedDiv(min, -1), signedRem(min, -1))
}
