// LITTEST
package main

import "unsafe"

// CHECK: {{^}}@1 = private unnamed_addr constant [14 x i8] c"error type f32", align 1{{$}}
// CHECK: {{^}}@3 = private unnamed_addr constant [14 x i8] c"error bits f32", align 1{{$}}
// CHECK: {{^}}@5 = private unnamed_addr constant [14 x i8] c"error type f64", align 1{{$}}
// CHECK: {{^}}@6 = private unnamed_addr constant [14 x i8] c"error bits f64", align 1{{$}}

const pi = 3.14159265
const pi32bits = 0x40490fdb
const pi64lo = 0x53c8d4f1
const pi64hi = 0x400921fb

type eface struct {
	typ  unsafe.Pointer
	data unsafe.Pointer
}

type u64parts struct {
	lo uint32
	hi uint32
}

func check32(v any) {
	switch v.(type) {
	case float32:
	default:
		panic("error type f32")
	}
	e := *(*eface)(unsafe.Pointer(&v))
	if *(*uint32)(e.data) != pi32bits {
		panic("error bits f32")
	}
}

func check64(v any) {
	switch v.(type) {
	case float64:
	default:
		panic("error type f64")
	}
	e := *(*eface)(unsafe.Pointer(&v))
	bits := *(*u64parts)(e.data)
	if bits.lo != pi64lo || bits.hi != pi64hi {
		panic("error bits f64")
	}
}

func f32() float32 {
	return pi
}

func f64() float64 {
	return pi
}

func main() {
	check32(f32())
	check64(f64())
}

// CHECK-LABEL: define void @main.check32(%"{{.*}}/runtime/internal/runtime.eface" %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %1 = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 16)
// CHECK-NEXT:   store %"{{.*}}/runtime/internal/runtime.eface" %0, ptr %1, align 8
// CHECK-NEXT:   %2 = load %"{{.*}}/runtime/internal/runtime.eface", ptr %1, align 8
// CHECK-NEXT:   %3 = extractvalue %"{{.*}}/runtime/internal/runtime.eface" %2, 0
// CHECK-NEXT:   %4 = icmp eq ptr %3, @_llgo_float32
// CHECK-NEXT:   br i1 %4, label %_llgo_5, label %_llgo_6
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:                                          ; preds = %_llgo_7
// CHECK-NEXT:   %5 = alloca %main.eface, align 8
// CHECK-NEXT:   call void @llvm.memset.p0.i64(ptr %5, i8 0, i64 16, i1 false)
// CHECK-NEXT:   %6 = icmp eq ptr %1, null
// CHECK-NEXT:   br i1 %6, label %18, label %19
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_2:                                          ; preds = %_llgo_7
// CHECK-NEXT:   %7 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 16)
// CHECK-NEXT:   store %"{{.*}}/runtime/internal/runtime.String" { ptr @1, i64 14 }, ptr %7, align 8
// CHECK-NEXT:   %8 = insertvalue %"{{.*}}/runtime/internal/runtime.eface" { ptr @_llgo_string, ptr undef }, ptr %7, 1
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.Panic"(%"{{.*}}/runtime/internal/runtime.eface" %8)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_3:                                          ; preds = %19
// CHECK-NEXT:   %9 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 16)
// CHECK-NEXT:   store %"{{.*}}/runtime/internal/runtime.String" { ptr @3, i64 14 }, ptr %9, align 8
// CHECK-NEXT:   %10 = insertvalue %"{{.*}}/runtime/internal/runtime.eface" { ptr @_llgo_string, ptr undef }, ptr %9, 1
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.Panic"(%"{{.*}}/runtime/internal/runtime.eface" %10)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_4:                                          ; preds = %19
// CHECK-NEXT:   ret void
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_5:                                          ; preds = %_llgo_0
// CHECK-NEXT:   %11 = extractvalue %"{{.*}}/runtime/internal/runtime.eface" %2, 1
// CHECK-NEXT:   %12 = load float, ptr %11, align 4
// CHECK-NEXT:   %13 = insertvalue { float, i1 } undef, float %12, 0
// CHECK-NEXT:   %14 = insertvalue { float, i1 } %13, i1 true, 1
// CHECK-NEXT:   br label %_llgo_7
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_6:                                          ; preds = %_llgo_0
// CHECK-NEXT:   br label %_llgo_7
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_7:                                          ; preds = %_llgo_6, %_llgo_5
// CHECK-NEXT:   %15 = phi { float, i1 } [ %14, %_llgo_5 ], [ zeroinitializer, %_llgo_6 ]
// CHECK-NEXT:   %16 = extractvalue { float, i1 } %15, 0
// CHECK-NEXT:   %17 = extractvalue { float, i1 } %15, 1
// CHECK-NEXT:   br i1 %17, label %_llgo_1, label %_llgo_2
// CHECK-EMPTY:
// CHECK-NEXT: 18:                                               ; preds = %_llgo_1
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 19:                                               ; preds = %_llgo_1
// CHECK-NEXT:   %20 = load %main.eface, ptr %1, align 8
// CHECK-NEXT:   store %main.eface %20, ptr %5, align 8
// CHECK-NEXT:   %21 = getelementptr inbounds %main.eface, ptr %5, i32 0, i32 1
// CHECK-NEXT:   %22 = load ptr, ptr %21, align 8
// CHECK-NEXT:   %23 = load i32, ptr %22, align 4
// CHECK-NEXT:   %24 = icmp ne i32 %23, 1078530011
// CHECK-NEXT:   br i1 %24, label %_llgo_3, label %_llgo_4
// CHECK-NEXT: }

// CHECK-LABEL: define void @main.check64(%"{{.*}}/runtime/internal/runtime.eface" %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %1 = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 16)
// CHECK-NEXT:   store %"{{.*}}/runtime/internal/runtime.eface" %0, ptr %1, align 8
// CHECK-NEXT:   %2 = load %"{{.*}}/runtime/internal/runtime.eface", ptr %1, align 8
// CHECK-NEXT:   %3 = extractvalue %"{{.*}}/runtime/internal/runtime.eface" %2, 0
// CHECK-NEXT:   %4 = icmp eq ptr %3, @_llgo_float64
// CHECK-NEXT:   br i1 %4, label %_llgo_6, label %_llgo_7
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:                                          ; preds = %_llgo_8
// CHECK-NEXT:   %5 = alloca %main.eface, align 8
// CHECK-NEXT:   call void @llvm.memset.p0.i64(ptr %5, i8 0, i64 16, i1 false)
// CHECK-NEXT:   %6 = icmp eq ptr %1, null
// CHECK-NEXT:   br i1 %6, label %21, label %22
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_2:                                          ; preds = %_llgo_8
// CHECK-NEXT:   %7 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 16)
// CHECK-NEXT:   store %"{{.*}}/runtime/internal/runtime.String" { ptr @5, i64 14 }, ptr %7, align 8
// CHECK-NEXT:   %8 = insertvalue %"{{.*}}/runtime/internal/runtime.eface" { ptr @_llgo_string, ptr undef }, ptr %7, 1
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.Panic"(%"{{.*}}/runtime/internal/runtime.eface" %8)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_3:                                          ; preds = %_llgo_5, %29
// CHECK-NEXT:   %9 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 16)
// CHECK-NEXT:   store %"{{.*}}/runtime/internal/runtime.String" { ptr @6, i64 14 }, ptr %9, align 8
// CHECK-NEXT:   %10 = insertvalue %"{{.*}}/runtime/internal/runtime.eface" { ptr @_llgo_string, ptr undef }, ptr %9, 1
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.Panic"(%"{{.*}}/runtime/internal/runtime.eface" %10)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_4:                                          ; preds = %_llgo_5
// CHECK-NEXT:   ret void
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_5:                                          ; preds = %29
// CHECK-NEXT:   %11 = getelementptr inbounds %main.u64parts, ptr %24, i32 0, i32 1
// CHECK-NEXT:   %12 = load i32, ptr %11, align 4
// CHECK-NEXT:   %13 = icmp ne i32 %12, 1074340347
// CHECK-NEXT:   br i1 %13, label %_llgo_3, label %_llgo_4
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_6:                                          ; preds = %_llgo_0
// CHECK-NEXT:   %14 = extractvalue %"{{.*}}/runtime/internal/runtime.eface" %2, 1
// CHECK-NEXT:   %15 = load double, ptr %14, align 8
// CHECK-NEXT:   %16 = insertvalue { double, i1 } undef, double %15, 0
// CHECK-NEXT:   %17 = insertvalue { double, i1 } %16, i1 true, 1
// CHECK-NEXT:   br label %_llgo_8
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_7:                                          ; preds = %_llgo_0
// CHECK-NEXT:   br label %_llgo_8
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_8:                                          ; preds = %_llgo_7, %_llgo_6
// CHECK-NEXT:   %18 = phi { double, i1 } [ %17, %_llgo_6 ], [ zeroinitializer, %_llgo_7 ]
// CHECK-NEXT:   %19 = extractvalue { double, i1 } %18, 0
// CHECK-NEXT:   %20 = extractvalue { double, i1 } %18, 1
// CHECK-NEXT:   br i1 %20, label %_llgo_1, label %_llgo_2
// CHECK-EMPTY:
// CHECK-NEXT: 21:                                               ; preds = %_llgo_1
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 22:                                               ; preds = %_llgo_1
// CHECK-NEXT:   %23 = load %main.eface, ptr %1, align 8
// CHECK-NEXT:   store %main.eface %23, ptr %5, align 8
// CHECK-NEXT:   %24 = alloca %main.u64parts, align 8
// CHECK-NEXT:   call void @llvm.memset.p0.i64(ptr %24, i8 0, i64 8, i1 false)
// CHECK-NEXT:   %25 = getelementptr inbounds %main.eface, ptr %5, i32 0, i32 1
// CHECK-NEXT:   %26 = load ptr, ptr %25, align 8
// CHECK-NEXT:   %27 = icmp eq ptr %26, null
// CHECK-NEXT:   br i1 %27, label %28, label %29
// CHECK-EMPTY:
// CHECK-NEXT: 28:                                               ; preds = %22
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 29:                                               ; preds = %22
// CHECK-NEXT:   %30 = load %main.u64parts, ptr %26, align 4
// CHECK-NEXT:   store %main.u64parts %30, ptr %24, align 4
// CHECK-NEXT:   %31 = getelementptr inbounds %main.u64parts, ptr %24, i32 0, i32 0
// CHECK-NEXT:   %32 = load i32, ptr %31, align 4
// CHECK-NEXT:   %33 = icmp ne i32 %32, 1405670641
// CHECK-NEXT:   br i1 %33, label %_llgo_3, label %_llgo_5
// CHECK-NEXT: }

// CHECK-LABEL: define float @main.f32(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   ret float 0x400921FB60000000
// CHECK-NEXT: }

// CHECK-LABEL: define double @main.f64(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   ret double 0x400921FB53C8D4F1
// CHECK-NEXT: }

// CHECK-LABEL: define void @main.init(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %0 = load i1, ptr @"main.init$guard", align 1
// CHECK-NEXT:   br i1 %0, label %_llgo_2, label %_llgo_1
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:                                          ; preds = %_llgo_0
// CHECK-NEXT:   store i1 true, ptr @"main.init$guard", align 1
// CHECK-NEXT:   br label %_llgo_2
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_2:                                          ; preds = %_llgo_1, %_llgo_0
// CHECK-NEXT:   ret void
// CHECK-NEXT: }

// CHECK-LABEL: define void @main.main(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %0 = call float @main.f32()
// CHECK-NEXT:   %1 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 4)
// CHECK-NEXT:   store float %0, ptr %1, align 4
// CHECK-NEXT:   %2 = insertvalue %"{{.*}}/runtime/internal/runtime.eface" { ptr @_llgo_float32, ptr undef }, ptr %1, 1
// CHECK-NEXT:   call void @main.check32(%"{{.*}}/runtime/internal/runtime.eface" %2)
// CHECK-NEXT:   %3 = call double @main.f64()
// CHECK-NEXT:   %4 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 8)
// CHECK-NEXT:   store double %3, ptr %4, align 8
// CHECK-NEXT:   %5 = insertvalue %"{{.*}}/runtime/internal/runtime.eface" { ptr @_llgo_float64, ptr undef }, ptr %4, 1
// CHECK-NEXT:   call void @main.check64(%"{{.*}}/runtime/internal/runtime.eface" %5)
// CHECK-NEXT:   ret void
// CHECK-NEXT: }
