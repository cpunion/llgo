; ModuleID = 'main'
source_filename = "main"

%"github.com/goplus/llgo/internal/runtime.String" = type { ptr, i64 }
%"github.com/goplus/llgo/internal/runtime.eface" = type { ptr, ptr }
%main.T = type { i64, i64, %"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.eface" }
%"github.com/goplus/llgo/internal/runtime.Slice" = type { ptr, i64, i64 }
%main.N = type {}
%"github.com/goplus/llgo/internal/abi.StructField" = type { %"github.com/goplus/llgo/internal/runtime.String", ptr, i64, %"github.com/goplus/llgo/internal/runtime.String", i1 }

@"main.init$guard" = global i1 false, align 1
@0 = private unnamed_addr constant [6 x i8] c"failed", align 1
@_llgo_string = linkonce global ptr null, align 8
@1 = private unnamed_addr constant [5 x i8] c"hello", align 1
@_llgo_int = linkonce global ptr null, align 8
@2 = private unnamed_addr constant [2 x i8] c"ok", align 1
@"_llgo_struct$n1H8J_3prDN3firMwPxBLVTkE5hJ9Di-AqNvaC9jczw" = linkonce global ptr null, align 8
@3 = private unnamed_addr constant [4 x i8] c"main", align 1
@_llgo_main.T = linkonce global ptr null, align 8
@4 = private unnamed_addr constant [6 x i8] c"main.T", align 1
@"_llgo_struct$5D_KhR3tDEp-wpx9caTiVZca43wS-XW6slE9Bsr8rsk" = linkonce global ptr null, align 8
@5 = private unnamed_addr constant [1 x i8] c"X", align 1
@6 = private unnamed_addr constant [1 x i8] c"Y", align 1
@7 = private unnamed_addr constant [1 x i8] c"Z", align 1
@8 = private unnamed_addr constant [1 x i8] c"V", align 1
@9 = private unnamed_addr constant [1 x i8] c"T", align 1
@_llgo_main.N = linkonce global ptr null, align 8
@10 = private unnamed_addr constant [6 x i8] c"main.N", align 1
@11 = private unnamed_addr constant [1 x i8] c"N", align 1
@"map[_llgo_int]_llgo_string" = linkonce global ptr null, align 8
@12 = private unnamed_addr constant [7 x i8] c"topbits", align 1
@13 = private unnamed_addr constant [4 x i8] c"keys", align 1
@14 = private unnamed_addr constant [5 x i8] c"elems", align 1
@15 = private unnamed_addr constant [8 x i8] c"overflow", align 1
@__llgo_argc = global i32 0, align 4
@__llgo_argv = global ptr null, align 8

define void @main.assert(i1 %0) {
_llgo_0:
  br i1 %0, label %_llgo_2, label %_llgo_1

_llgo_1:                                          ; preds = %_llgo_0
  %1 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1, i32 0, i32 0
  store ptr @0, ptr %2, align 8
  %3 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1, i32 0, i32 1
  store i64 6, ptr %3, align 4
  %4 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1, align 8
  %5 = load ptr, ptr @_llgo_string, align 8
  %6 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %4, ptr %6, align 8
  %7 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %8 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %7, i32 0, i32 0
  store ptr %5, ptr %8, align 8
  %9 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %7, i32 0, i32 1
  store ptr %6, ptr %9, align 8
  %10 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %7, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %10)
  unreachable

_llgo_2:                                          ; preds = %_llgo_0
  ret void
}

define void @main.init() {
_llgo_0:
  %0 = load i1, ptr @"main.init$guard", align 1
  br i1 %0, label %_llgo_2, label %_llgo_1

_llgo_1:                                          ; preds = %_llgo_0
  store i1 true, ptr @"main.init$guard", align 1
  call void @"main.init$after"()
  call void @"main.init#1"()
  call void @"main.init#2"()
  call void @"main.init#3"()
  call void @"main.init#4"()
  call void @"main.init#5"()
  call void @"main.init#6"()
  call void @"main.init#7"()
  br label %_llgo_2

_llgo_2:                                          ; preds = %_llgo_1, %_llgo_0
  ret void
}

define void @"main.init#1"() {
_llgo_0:
  %0 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64 8)
  %1 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %2 = getelementptr inbounds { ptr }, ptr %1, i32 0, i32 0
  store ptr %0, ptr %2, align 8
  %3 = alloca { ptr, ptr }, align 8
  %4 = getelementptr inbounds { ptr, ptr }, ptr %3, i32 0, i32 0
  store ptr @"main.init#1$2", ptr %4, align 8
  %5 = getelementptr inbounds { ptr, ptr }, ptr %3, i32 0, i32 1
  store ptr %1, ptr %5, align 8
  %6 = load { ptr, ptr }, ptr %3, align 8
  call void @main.assert(i1 true)
  call void @main.assert(i1 true)
  call void @main.assert(i1 true)
  call void @main.assert(i1 true)
  call void @main.assert(i1 true)
  call void @main.assert(i1 true)
  %7 = extractvalue { ptr, ptr } %6, 0
  %8 = icmp ne ptr %7, null
  call void @main.assert(i1 %8)
  %9 = extractvalue { ptr, ptr } %6, 0
  %10 = icmp ne ptr null, %9
  call void @main.assert(i1 %10)
  call void @main.assert(i1 true)
  call void @main.assert(i1 true)
  ret void
}

define i64 @"main.init#1$1"(i64 %0, i64 %1) {
_llgo_0:
  %2 = add i64 %0, %1
  ret i64 %2
}

define void @"main.init#1$2"(ptr %0) {
_llgo_0:
  %1 = load { ptr }, ptr %0, align 8
  %2 = extractvalue { ptr } %1, 0
  %3 = load i64, ptr %2, align 4
  call void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64 %3)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  ret void
}

define void @"main.init#2"() {
_llgo_0:
  call void @main.assert(i1 true)
  %0 = alloca [3 x i64], align 8
  %1 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %0, i64 24)
  %2 = getelementptr inbounds i64, ptr %1, i64 0
  %3 = getelementptr inbounds i64, ptr %1, i64 1
  %4 = getelementptr inbounds i64, ptr %1, i64 2
  store i64 1, ptr %2, align 4
  store i64 2, ptr %3, align 4
  store i64 3, ptr %4, align 4
  %5 = alloca [3 x i64], align 8
  %6 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %5, i64 24)
  %7 = getelementptr inbounds i64, ptr %6, i64 0
  %8 = getelementptr inbounds i64, ptr %6, i64 1
  %9 = getelementptr inbounds i64, ptr %6, i64 2
  store i64 1, ptr %7, align 4
  store i64 2, ptr %8, align 4
  store i64 3, ptr %9, align 4
  %10 = load [3 x i64], ptr %1, align 4
  %11 = load [3 x i64], ptr %6, align 4
  %12 = extractvalue [3 x i64] %10, 0
  %13 = extractvalue [3 x i64] %11, 0
  %14 = icmp eq i64 %12, %13
  %15 = and i1 true, %14
  %16 = extractvalue [3 x i64] %10, 1
  %17 = extractvalue [3 x i64] %11, 1
  %18 = icmp eq i64 %16, %17
  %19 = and i1 %15, %18
  %20 = extractvalue [3 x i64] %10, 2
  %21 = extractvalue [3 x i64] %11, 2
  %22 = icmp eq i64 %20, %21
  %23 = and i1 %19, %22
  call void @main.assert(i1 %23)
  %24 = getelementptr inbounds i64, ptr %6, i64 1
  store i64 1, ptr %24, align 4
  %25 = load [3 x i64], ptr %1, align 4
  %26 = load [3 x i64], ptr %6, align 4
  %27 = extractvalue [3 x i64] %25, 0
  %28 = extractvalue [3 x i64] %26, 0
  %29 = icmp eq i64 %27, %28
  %30 = and i1 true, %29
  %31 = extractvalue [3 x i64] %25, 1
  %32 = extractvalue [3 x i64] %26, 1
  %33 = icmp eq i64 %31, %32
  %34 = and i1 %30, %33
  %35 = extractvalue [3 x i64] %25, 2
  %36 = extractvalue [3 x i64] %26, 2
  %37 = icmp eq i64 %35, %36
  %38 = and i1 %34, %37
  %39 = xor i1 %38, true
  call void @main.assert(i1 %39)
  ret void
}

define void @"main.init#3"() {
_llgo_0:
  %0 = alloca %main.T, align 8
  %1 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %0, i64 48)
  %2 = getelementptr inbounds %main.T, ptr %1, i32 0, i32 0
  %3 = getelementptr inbounds %main.T, ptr %1, i32 0, i32 1
  %4 = getelementptr inbounds %main.T, ptr %1, i32 0, i32 2
  %5 = getelementptr inbounds %main.T, ptr %1, i32 0, i32 3
  store i64 10, ptr %2, align 4
  store i64 20, ptr %3, align 4
  %6 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %7 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %6, i32 0, i32 0
  store ptr @1, ptr %7, align 8
  %8 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %6, i32 0, i32 1
  store i64 5, ptr %8, align 4
  %9 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %6, align 8
  store %"github.com/goplus/llgo/internal/runtime.String" %9, ptr %4, align 8
  %10 = load ptr, ptr @_llgo_int, align 8
  %11 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %12 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %11, i32 0, i32 0
  store ptr %10, ptr %12, align 8
  %13 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %11, i32 0, i32 1
  store ptr inttoptr (i64 1 to ptr), ptr %13, align 8
  %14 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %11, align 8
  store %"github.com/goplus/llgo/internal/runtime.eface" %14, ptr %5, align 8
  %15 = alloca %main.T, align 8
  %16 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %15, i64 48)
  %17 = getelementptr inbounds %main.T, ptr %16, i32 0, i32 0
  %18 = getelementptr inbounds %main.T, ptr %16, i32 0, i32 1
  %19 = getelementptr inbounds %main.T, ptr %16, i32 0, i32 2
  %20 = getelementptr inbounds %main.T, ptr %16, i32 0, i32 3
  store i64 10, ptr %17, align 4
  store i64 20, ptr %18, align 4
  %21 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %22 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %21, i32 0, i32 0
  store ptr @1, ptr %22, align 8
  %23 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %21, i32 0, i32 1
  store i64 5, ptr %23, align 4
  %24 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %21, align 8
  store %"github.com/goplus/llgo/internal/runtime.String" %24, ptr %19, align 8
  %25 = load ptr, ptr @_llgo_int, align 8
  %26 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %27 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %26, i32 0, i32 0
  store ptr %25, ptr %27, align 8
  %28 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %26, i32 0, i32 1
  store ptr inttoptr (i64 1 to ptr), ptr %28, align 8
  %29 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %26, align 8
  store %"github.com/goplus/llgo/internal/runtime.eface" %29, ptr %20, align 8
  %30 = alloca %main.T, align 8
  %31 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %30, i64 48)
  %32 = getelementptr inbounds %main.T, ptr %31, i32 0, i32 0
  %33 = getelementptr inbounds %main.T, ptr %31, i32 0, i32 1
  %34 = getelementptr inbounds %main.T, ptr %31, i32 0, i32 2
  %35 = getelementptr inbounds %main.T, ptr %31, i32 0, i32 3
  store i64 10, ptr %32, align 4
  store i64 20, ptr %33, align 4
  %36 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %37 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %36, i32 0, i32 0
  store ptr @1, ptr %37, align 8
  %38 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %36, i32 0, i32 1
  store i64 5, ptr %38, align 4
  %39 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %36, align 8
  store %"github.com/goplus/llgo/internal/runtime.String" %39, ptr %34, align 8
  %40 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %41 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %40, i32 0, i32 0
  store ptr @2, ptr %41, align 8
  %42 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %40, i32 0, i32 1
  store i64 2, ptr %42, align 4
  %43 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %40, align 8
  %44 = load ptr, ptr @_llgo_string, align 8
  %45 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %43, ptr %45, align 8
  %46 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %47 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %46, i32 0, i32 0
  store ptr %44, ptr %47, align 8
  %48 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %46, i32 0, i32 1
  store ptr %45, ptr %48, align 8
  %49 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %46, align 8
  store %"github.com/goplus/llgo/internal/runtime.eface" %49, ptr %35, align 8
  call void @main.assert(i1 true)
  %50 = call i1 @"github.com/goplus/llgo/internal/runtime.StringEqual"(%"github.com/goplus/llgo/internal/runtime.String" zeroinitializer, %"github.com/goplus/llgo/internal/runtime.String" zeroinitializer)
  %51 = and i1 true, %50
  %52 = call i1 @"github.com/goplus/llgo/internal/runtime.EfaceEqual"(%"github.com/goplus/llgo/internal/runtime.eface" zeroinitializer, %"github.com/goplus/llgo/internal/runtime.eface" zeroinitializer)
  %53 = and i1 %51, %52
  call void @main.assert(i1 %53)
  %54 = load %main.T, ptr %1, align 8
  %55 = load %main.T, ptr %16, align 8
  %56 = extractvalue %main.T %54, 0
  %57 = extractvalue %main.T %55, 0
  %58 = icmp eq i64 %56, %57
  %59 = and i1 true, %58
  %60 = extractvalue %main.T %54, 1
  %61 = extractvalue %main.T %55, 1
  %62 = icmp eq i64 %60, %61
  %63 = and i1 %59, %62
  %64 = extractvalue %main.T %54, 2
  %65 = extractvalue %main.T %55, 2
  %66 = call i1 @"github.com/goplus/llgo/internal/runtime.StringEqual"(%"github.com/goplus/llgo/internal/runtime.String" %64, %"github.com/goplus/llgo/internal/runtime.String" %65)
  %67 = and i1 %63, %66
  %68 = extractvalue %main.T %54, 3
  %69 = extractvalue %main.T %55, 3
  %70 = call i1 @"github.com/goplus/llgo/internal/runtime.EfaceEqual"(%"github.com/goplus/llgo/internal/runtime.eface" %68, %"github.com/goplus/llgo/internal/runtime.eface" %69)
  %71 = and i1 %67, %70
  call void @main.assert(i1 %71)
  %72 = load %main.T, ptr %1, align 8
  %73 = load %main.T, ptr %31, align 8
  %74 = extractvalue %main.T %72, 0
  %75 = extractvalue %main.T %73, 0
  %76 = icmp eq i64 %74, %75
  %77 = and i1 true, %76
  %78 = extractvalue %main.T %72, 1
  %79 = extractvalue %main.T %73, 1
  %80 = icmp eq i64 %78, %79
  %81 = and i1 %77, %80
  %82 = extractvalue %main.T %72, 2
  %83 = extractvalue %main.T %73, 2
  %84 = call i1 @"github.com/goplus/llgo/internal/runtime.StringEqual"(%"github.com/goplus/llgo/internal/runtime.String" %82, %"github.com/goplus/llgo/internal/runtime.String" %83)
  %85 = and i1 %81, %84
  %86 = extractvalue %main.T %72, 3
  %87 = extractvalue %main.T %73, 3
  %88 = call i1 @"github.com/goplus/llgo/internal/runtime.EfaceEqual"(%"github.com/goplus/llgo/internal/runtime.eface" %86, %"github.com/goplus/llgo/internal/runtime.eface" %87)
  %89 = and i1 %85, %88
  %90 = xor i1 %89, true
  call void @main.assert(i1 %90)
  %91 = load %main.T, ptr %16, align 8
  %92 = load %main.T, ptr %31, align 8
  %93 = extractvalue %main.T %91, 0
  %94 = extractvalue %main.T %92, 0
  %95 = icmp eq i64 %93, %94
  %96 = and i1 true, %95
  %97 = extractvalue %main.T %91, 1
  %98 = extractvalue %main.T %92, 1
  %99 = icmp eq i64 %97, %98
  %100 = and i1 %96, %99
  %101 = extractvalue %main.T %91, 2
  %102 = extractvalue %main.T %92, 2
  %103 = call i1 @"github.com/goplus/llgo/internal/runtime.StringEqual"(%"github.com/goplus/llgo/internal/runtime.String" %101, %"github.com/goplus/llgo/internal/runtime.String" %102)
  %104 = and i1 %100, %103
  %105 = extractvalue %main.T %91, 3
  %106 = extractvalue %main.T %92, 3
  %107 = call i1 @"github.com/goplus/llgo/internal/runtime.EfaceEqual"(%"github.com/goplus/llgo/internal/runtime.eface" %105, %"github.com/goplus/llgo/internal/runtime.eface" %106)
  %108 = and i1 %104, %107
  %109 = xor i1 %108, true
  call void @main.assert(i1 %109)
  ret void
}

define void @"main.init#4"() {
_llgo_0:
  %0 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64 24)
  %1 = getelementptr inbounds i64, ptr %0, i64 0
  store i64 1, ptr %1, align 4
  %2 = getelementptr inbounds i64, ptr %0, i64 1
  store i64 2, ptr %2, align 4
  %3 = getelementptr inbounds i64, ptr %0, i64 2
  store i64 3, ptr %3, align 4
  %4 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %5 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %4, i32 0, i32 0
  store ptr %0, ptr %5, align 8
  %6 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %4, i32 0, i32 1
  store i64 3, ptr %6, align 4
  %7 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %4, i32 0, i32 2
  store i64 3, ptr %7, align 4
  %8 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %4, align 8
  %9 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64 16)
  %10 = call %"github.com/goplus/llgo/internal/runtime.Slice" @"github.com/goplus/llgo/internal/runtime.NewSlice3"(ptr %9, i64 8, i64 2, i64 0, i64 2, i64 2)
  %11 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64 16)
  %12 = call %"github.com/goplus/llgo/internal/runtime.Slice" @"github.com/goplus/llgo/internal/runtime.NewSlice3"(ptr %11, i64 8, i64 2, i64 0, i64 0, i64 2)
  call void @main.assert(i1 true)
  %13 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %8, 0
  %14 = icmp ne ptr %13, null
  call void @main.assert(i1 %14)
  %15 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %10, 0
  %16 = icmp ne ptr %15, null
  call void @main.assert(i1 %16)
  %17 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %12, 0
  %18 = icmp ne ptr %17, null
  call void @main.assert(i1 %18)
  call void @main.assert(i1 true)
  ret void
}

define void @"main.init#5"() {
_llgo_0:
  %0 = load ptr, ptr @_llgo_int, align 8
  %1 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %2 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %1, i32 0, i32 0
  store ptr %0, ptr %2, align 8
  %3 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %1, i32 0, i32 1
  store ptr inttoptr (i64 100 to ptr), ptr %3, align 8
  %4 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %1, align 8
  %5 = load ptr, ptr @"_llgo_struct$n1H8J_3prDN3firMwPxBLVTkE5hJ9Di-AqNvaC9jczw", align 8
  %6 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  store {} zeroinitializer, ptr %6, align 1
  %7 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %8 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %7, i32 0, i32 0
  store ptr %5, ptr %8, align 8
  %9 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %7, i32 0, i32 1
  store ptr %6, ptr %9, align 8
  %10 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %7, align 8
  %11 = alloca %main.T, align 8
  %12 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %11, i64 48)
  %13 = getelementptr inbounds %main.T, ptr %12, i32 0, i32 0
  %14 = getelementptr inbounds %main.T, ptr %12, i32 0, i32 1
  %15 = getelementptr inbounds %main.T, ptr %12, i32 0, i32 2
  %16 = getelementptr inbounds %main.T, ptr %12, i32 0, i32 3
  store i64 10, ptr %13, align 4
  store i64 20, ptr %14, align 4
  %17 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %18 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %17, i32 0, i32 0
  store ptr @1, ptr %18, align 8
  %19 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %17, i32 0, i32 1
  store i64 5, ptr %19, align 4
  %20 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %17, align 8
  store %"github.com/goplus/llgo/internal/runtime.String" %20, ptr %15, align 8
  %21 = load ptr, ptr @_llgo_int, align 8
  %22 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %23 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %22, i32 0, i32 0
  store ptr %21, ptr %23, align 8
  %24 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %22, i32 0, i32 1
  store ptr inttoptr (i64 1 to ptr), ptr %24, align 8
  %25 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %22, align 8
  store %"github.com/goplus/llgo/internal/runtime.eface" %25, ptr %16, align 8
  %26 = load %main.T, ptr %12, align 8
  %27 = load ptr, ptr @_llgo_main.T, align 8
  %28 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 48)
  store %main.T %26, ptr %28, align 8
  %29 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %30 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %29, i32 0, i32 0
  store ptr %27, ptr %30, align 8
  %31 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %29, i32 0, i32 1
  store ptr %28, ptr %31, align 8
  %32 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %29, align 8
  %33 = alloca %main.T, align 8
  %34 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %33, i64 48)
  %35 = getelementptr inbounds %main.T, ptr %34, i32 0, i32 0
  %36 = getelementptr inbounds %main.T, ptr %34, i32 0, i32 1
  %37 = getelementptr inbounds %main.T, ptr %34, i32 0, i32 2
  %38 = getelementptr inbounds %main.T, ptr %34, i32 0, i32 3
  store i64 10, ptr %35, align 4
  store i64 20, ptr %36, align 4
  %39 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %40 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %39, i32 0, i32 0
  store ptr @1, ptr %40, align 8
  %41 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %39, i32 0, i32 1
  store i64 5, ptr %41, align 4
  %42 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %39, align 8
  store %"github.com/goplus/llgo/internal/runtime.String" %42, ptr %37, align 8
  %43 = load ptr, ptr @_llgo_int, align 8
  %44 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %45 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %44, i32 0, i32 0
  store ptr %43, ptr %45, align 8
  %46 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %44, i32 0, i32 1
  store ptr inttoptr (i64 1 to ptr), ptr %46, align 8
  %47 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %44, align 8
  store %"github.com/goplus/llgo/internal/runtime.eface" %47, ptr %38, align 8
  %48 = alloca %main.T, align 8
  %49 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %48, i64 48)
  %50 = getelementptr inbounds %main.T, ptr %49, i32 0, i32 0
  %51 = getelementptr inbounds %main.T, ptr %49, i32 0, i32 1
  %52 = getelementptr inbounds %main.T, ptr %49, i32 0, i32 2
  %53 = getelementptr inbounds %main.T, ptr %49, i32 0, i32 3
  store i64 10, ptr %50, align 4
  store i64 20, ptr %51, align 4
  %54 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %55 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %54, i32 0, i32 0
  store ptr @1, ptr %55, align 8
  %56 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %54, i32 0, i32 1
  store i64 5, ptr %56, align 4
  %57 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %54, align 8
  store %"github.com/goplus/llgo/internal/runtime.String" %57, ptr %52, align 8
  %58 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %59 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %58, i32 0, i32 0
  store ptr @2, ptr %59, align 8
  %60 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %58, i32 0, i32 1
  store i64 2, ptr %60, align 4
  %61 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %58, align 8
  %62 = load ptr, ptr @_llgo_string, align 8
  %63 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %61, ptr %63, align 8
  %64 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %65 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %64, i32 0, i32 0
  store ptr %62, ptr %65, align 8
  %66 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %64, i32 0, i32 1
  store ptr %63, ptr %66, align 8
  %67 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %64, align 8
  store %"github.com/goplus/llgo/internal/runtime.eface" %67, ptr %53, align 8
  %68 = load ptr, ptr @_llgo_int, align 8
  %69 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %70 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %69, i32 0, i32 0
  store ptr %68, ptr %70, align 8
  %71 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %69, i32 0, i32 1
  store ptr inttoptr (i64 100 to ptr), ptr %71, align 8
  %72 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %69, align 8
  %73 = call i1 @"github.com/goplus/llgo/internal/runtime.EfaceEqual"(%"github.com/goplus/llgo/internal/runtime.eface" %4, %"github.com/goplus/llgo/internal/runtime.eface" %72)
  call void @main.assert(i1 %73)
  %74 = load ptr, ptr @"_llgo_struct$n1H8J_3prDN3firMwPxBLVTkE5hJ9Di-AqNvaC9jczw", align 8
  %75 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  store {} zeroinitializer, ptr %75, align 1
  %76 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %77 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %76, i32 0, i32 0
  store ptr %74, ptr %77, align 8
  %78 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %76, i32 0, i32 1
  store ptr %75, ptr %78, align 8
  %79 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %76, align 8
  %80 = call i1 @"github.com/goplus/llgo/internal/runtime.EfaceEqual"(%"github.com/goplus/llgo/internal/runtime.eface" %10, %"github.com/goplus/llgo/internal/runtime.eface" %79)
  call void @main.assert(i1 %80)
  %81 = load ptr, ptr @_llgo_main.N, align 8
  %82 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  store %main.N zeroinitializer, ptr %82, align 1
  %83 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %84 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %83, i32 0, i32 0
  store ptr %81, ptr %84, align 8
  %85 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %83, i32 0, i32 1
  store ptr %82, ptr %85, align 8
  %86 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %83, align 8
  %87 = call i1 @"github.com/goplus/llgo/internal/runtime.EfaceEqual"(%"github.com/goplus/llgo/internal/runtime.eface" %10, %"github.com/goplus/llgo/internal/runtime.eface" %86)
  %88 = xor i1 %87, true
  call void @main.assert(i1 %88)
  %89 = load %main.T, ptr %34, align 8
  %90 = load ptr, ptr @_llgo_main.T, align 8
  %91 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 48)
  store %main.T %89, ptr %91, align 8
  %92 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %93 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %92, i32 0, i32 0
  store ptr %90, ptr %93, align 8
  %94 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %92, i32 0, i32 1
  store ptr %91, ptr %94, align 8
  %95 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %92, align 8
  %96 = call i1 @"github.com/goplus/llgo/internal/runtime.EfaceEqual"(%"github.com/goplus/llgo/internal/runtime.eface" %32, %"github.com/goplus/llgo/internal/runtime.eface" %95)
  call void @main.assert(i1 %96)
  %97 = load %main.T, ptr %49, align 8
  %98 = load ptr, ptr @_llgo_main.T, align 8
  %99 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 48)
  store %main.T %97, ptr %99, align 8
  %100 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %101 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %100, i32 0, i32 0
  store ptr %98, ptr %101, align 8
  %102 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %100, i32 0, i32 1
  store ptr %99, ptr %102, align 8
  %103 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %100, align 8
  %104 = call i1 @"github.com/goplus/llgo/internal/runtime.EfaceEqual"(%"github.com/goplus/llgo/internal/runtime.eface" %32, %"github.com/goplus/llgo/internal/runtime.eface" %103)
  %105 = xor i1 %104, true
  call void @main.assert(i1 %105)
  ret void
}

define void @"main.init#6"() {
_llgo_0:
  %0 = call ptr @"github.com/goplus/llgo/internal/runtime.NewChan"(i64 8, i64 0)
  %1 = call ptr @"github.com/goplus/llgo/internal/runtime.NewChan"(i64 8, i64 0)
  %2 = icmp eq ptr %0, %0
  call void @main.assert(i1 %2)
  %3 = icmp ne ptr %0, %1
  call void @main.assert(i1 %3)
  %4 = icmp ne ptr %0, null
  call void @main.assert(i1 %4)
  ret void
}

define void @"main.init#7"() {
_llgo_0:
  %0 = load ptr, ptr @_llgo_int, align 8
  %1 = load ptr, ptr @_llgo_string, align 8
  %2 = load ptr, ptr @"map[_llgo_int]_llgo_string", align 8
  %3 = call ptr @"github.com/goplus/llgo/internal/runtime.MakeMap"(ptr %2, i64 0)
  %4 = icmp ne ptr %3, null
  call void @main.assert(i1 %4)
  call void @main.assert(i1 true)
  ret void
}

define i32 @main(i32 %0, ptr %1) {
_llgo_0:
  store i32 %0, ptr @__llgo_argc, align 4
  store ptr %1, ptr @__llgo_argv, align 8
  call void @"github.com/goplus/llgo/internal/runtime.init"()
  call void @main.init()
  ret i32 0
}

define void @main.test() {
_llgo_0:
  ret void
}

define void @"main.init$after"() {
_llgo_0:
  %0 = load ptr, ptr @_llgo_string, align 8
  %1 = icmp eq ptr %0, null
  br i1 %1, label %_llgo_1, label %_llgo_2

_llgo_1:                                          ; preds = %_llgo_0
  %2 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 24)
  store ptr %2, ptr @_llgo_string, align 8
  br label %_llgo_2

_llgo_2:                                          ; preds = %_llgo_1, %_llgo_0
  %3 = load ptr, ptr @_llgo_int, align 8
  %4 = icmp eq ptr %3, null
  br i1 %4, label %_llgo_3, label %_llgo_4

_llgo_3:                                          ; preds = %_llgo_2
  %5 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 34)
  store ptr %5, ptr @_llgo_int, align 8
  br label %_llgo_4

_llgo_4:                                          ; preds = %_llgo_3, %_llgo_2
  %6 = load ptr, ptr @"_llgo_struct$n1H8J_3prDN3firMwPxBLVTkE5hJ9Di-AqNvaC9jczw", align 8
  %7 = icmp eq ptr %6, null
  br i1 %7, label %_llgo_5, label %_llgo_6

_llgo_5:                                          ; preds = %_llgo_4
  %8 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %9 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %8, i32 0, i32 0
  store ptr @3, ptr %9, align 8
  %10 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %8, i32 0, i32 1
  store i64 4, ptr %10, align 4
  %11 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %8, align 8
  %12 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %13 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %14 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %13, i32 0, i32 0
  store ptr %12, ptr %14, align 8
  %15 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %13, i32 0, i32 1
  store i64 0, ptr %15, align 4
  %16 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %13, i32 0, i32 2
  store i64 0, ptr %16, align 4
  %17 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %13, align 8
  %18 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %11, i64 0, %"github.com/goplus/llgo/internal/runtime.Slice" %17)
  store ptr %18, ptr @"_llgo_struct$n1H8J_3prDN3firMwPxBLVTkE5hJ9Di-AqNvaC9jczw", align 8
  br label %_llgo_6

_llgo_6:                                          ; preds = %_llgo_5, %_llgo_4
  %19 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %20 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %19, i32 0, i32 0
  store ptr @4, ptr %20, align 8
  %21 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %19, i32 0, i32 1
  store i64 6, ptr %21, align 4
  %22 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %19, align 8
  %23 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %22, i64 25, i64 48, i64 0, i64 0)
  %24 = load ptr, ptr @_llgo_main.T, align 8
  %25 = icmp eq ptr %24, null
  br i1 %25, label %_llgo_7, label %_llgo_8

_llgo_7:                                          ; preds = %_llgo_6
  store ptr %23, ptr @_llgo_main.T, align 8
  br label %_llgo_8

_llgo_8:                                          ; preds = %_llgo_7, %_llgo_6
  %26 = load ptr, ptr @"_llgo_struct$5D_KhR3tDEp-wpx9caTiVZca43wS-XW6slE9Bsr8rsk", align 8
  %27 = icmp eq ptr %26, null
  br i1 %27, label %_llgo_9, label %_llgo_10

_llgo_9:                                          ; preds = %_llgo_8
  %28 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %29 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %28, i32 0, i32 0
  store ptr @5, ptr %29, align 8
  %30 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %28, i32 0, i32 1
  store i64 1, ptr %30, align 4
  %31 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %28, align 8
  %32 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %33 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %32, i32 0, i32 0
  store ptr null, ptr %33, align 8
  %34 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %32, i32 0, i32 1
  store i64 0, ptr %34, align 4
  %35 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %32, align 8
  %36 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 34)
  %37 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %31, ptr %36, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %35, i1 false)
  %38 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %39 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %38, i32 0, i32 0
  store ptr @6, ptr %39, align 8
  %40 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %38, i32 0, i32 1
  store i64 1, ptr %40, align 4
  %41 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %38, align 8
  %42 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %43 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %42, i32 0, i32 0
  store ptr null, ptr %43, align 8
  %44 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %42, i32 0, i32 1
  store i64 0, ptr %44, align 4
  %45 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %42, align 8
  %46 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 34)
  %47 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %41, ptr %46, i64 8, %"github.com/goplus/llgo/internal/runtime.String" %45, i1 false)
  %48 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %49 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %48, i32 0, i32 0
  store ptr @7, ptr %49, align 8
  %50 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %48, i32 0, i32 1
  store i64 1, ptr %50, align 4
  %51 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %48, align 8
  %52 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %53 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %52, i32 0, i32 0
  store ptr null, ptr %53, align 8
  %54 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %52, i32 0, i32 1
  store i64 0, ptr %54, align 4
  %55 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %52, align 8
  %56 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 24)
  %57 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %51, ptr %56, i64 16, %"github.com/goplus/llgo/internal/runtime.String" %55, i1 false)
  %58 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %59 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %58, i32 0, i32 0
  store ptr @8, ptr %59, align 8
  %60 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %58, i32 0, i32 1
  store i64 1, ptr %60, align 4
  %61 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %58, align 8
  %62 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %63 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %62, i32 0, i32 0
  store ptr null, ptr %63, align 8
  %64 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %62, i32 0, i32 1
  store i64 0, ptr %64, align 4
  %65 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %62, align 8
  %66 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %67 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %68 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %67, i32 0, i32 0
  store ptr %66, ptr %68, align 8
  %69 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %67, i32 0, i32 1
  store i64 0, ptr %69, align 4
  %70 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %67, i32 0, i32 2
  store i64 0, ptr %70, align 4
  %71 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %67, align 8
  %72 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %73 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %72, i32 0, i32 0
  store ptr @3, ptr %73, align 8
  %74 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %72, i32 0, i32 1
  store i64 4, ptr %74, align 4
  %75 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %72, align 8
  %76 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %77 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %76, i32 0, i32 0
  store ptr null, ptr %77, align 8
  %78 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %76, i32 0, i32 1
  store i64 0, ptr %78, align 4
  %79 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %76, align 8
  %80 = call ptr @"github.com/goplus/llgo/internal/runtime.Interface"(%"github.com/goplus/llgo/internal/runtime.String" %75, %"github.com/goplus/llgo/internal/runtime.String" %79, %"github.com/goplus/llgo/internal/runtime.Slice" %71)
  %81 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %61, ptr %80, i64 32, %"github.com/goplus/llgo/internal/runtime.String" %65, i1 false)
  %82 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %83 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %82, i32 0, i32 0
  store ptr @3, ptr %83, align 8
  %84 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %82, i32 0, i32 1
  store i64 4, ptr %84, align 4
  %85 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %82, align 8
  %86 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 224)
  %87 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %86, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %37, ptr %87, align 8
  %88 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %86, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %47, ptr %88, align 8
  %89 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %86, i64 2
  store %"github.com/goplus/llgo/internal/abi.StructField" %57, ptr %89, align 8
  %90 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %86, i64 3
  store %"github.com/goplus/llgo/internal/abi.StructField" %81, ptr %90, align 8
  %91 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %92 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %91, i32 0, i32 0
  store ptr %86, ptr %92, align 8
  %93 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %91, i32 0, i32 1
  store i64 4, ptr %93, align 4
  %94 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %91, i32 0, i32 2
  store i64 4, ptr %94, align 4
  %95 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %91, align 8
  %96 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %85, i64 48, %"github.com/goplus/llgo/internal/runtime.Slice" %95)
  store ptr %96, ptr @"_llgo_struct$5D_KhR3tDEp-wpx9caTiVZca43wS-XW6slE9Bsr8rsk", align 8
  br label %_llgo_10

_llgo_10:                                         ; preds = %_llgo_9, %_llgo_8
  %97 = load ptr, ptr @"_llgo_struct$5D_KhR3tDEp-wpx9caTiVZca43wS-XW6slE9Bsr8rsk", align 8
  br i1 %25, label %_llgo_11, label %_llgo_12

_llgo_11:                                         ; preds = %_llgo_10
  %98 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %99 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %98, i32 0, i32 0
  store ptr @3, ptr %99, align 8
  %100 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %98, i32 0, i32 1
  store i64 4, ptr %100, align 4
  %101 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %98, align 8
  %102 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %103 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %102, i32 0, i32 0
  store ptr @9, ptr %103, align 8
  %104 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %102, i32 0, i32 1
  store i64 1, ptr %104, align 4
  %105 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %102, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %23, %"github.com/goplus/llgo/internal/runtime.String" %101, %"github.com/goplus/llgo/internal/runtime.String" %105, ptr %97, { ptr, i64, i64 } zeroinitializer, { ptr, i64, i64 } zeroinitializer)
  br label %_llgo_12

_llgo_12:                                         ; preds = %_llgo_11, %_llgo_10
  %106 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %107 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %106, i32 0, i32 0
  store ptr @10, ptr %107, align 8
  %108 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %106, i32 0, i32 1
  store i64 6, ptr %108, align 4
  %109 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %106, align 8
  %110 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %109, i64 25, i64 0, i64 0, i64 0)
  %111 = load ptr, ptr @_llgo_main.N, align 8
  %112 = icmp eq ptr %111, null
  br i1 %112, label %_llgo_13, label %_llgo_14

_llgo_13:                                         ; preds = %_llgo_12
  store ptr %110, ptr @_llgo_main.N, align 8
  br label %_llgo_14

_llgo_14:                                         ; preds = %_llgo_13, %_llgo_12
  %113 = load ptr, ptr @"_llgo_struct$n1H8J_3prDN3firMwPxBLVTkE5hJ9Di-AqNvaC9jczw", align 8
  br i1 %112, label %_llgo_15, label %_llgo_16

_llgo_15:                                         ; preds = %_llgo_14
  %114 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %115 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %114, i32 0, i32 0
  store ptr @3, ptr %115, align 8
  %116 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %114, i32 0, i32 1
  store i64 4, ptr %116, align 4
  %117 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %114, align 8
  %118 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %119 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %118, i32 0, i32 0
  store ptr @11, ptr %119, align 8
  %120 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %118, i32 0, i32 1
  store i64 1, ptr %120, align 4
  %121 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %118, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %110, %"github.com/goplus/llgo/internal/runtime.String" %117, %"github.com/goplus/llgo/internal/runtime.String" %121, ptr %113, { ptr, i64, i64 } zeroinitializer, { ptr, i64, i64 } zeroinitializer)
  br label %_llgo_16

_llgo_16:                                         ; preds = %_llgo_15, %_llgo_14
  %122 = load ptr, ptr @"map[_llgo_int]_llgo_string", align 8
  %123 = icmp eq ptr %122, null
  br i1 %123, label %_llgo_17, label %_llgo_18

_llgo_17:                                         ; preds = %_llgo_16
  %124 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 34)
  %125 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 24)
  %126 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %127 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %126, i32 0, i32 0
  store ptr @12, ptr %127, align 8
  %128 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %126, i32 0, i32 1
  store i64 7, ptr %128, align 4
  %129 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %126, align 8
  %130 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %131 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %130, i32 0, i32 0
  store ptr null, ptr %131, align 8
  %132 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %130, i32 0, i32 1
  store i64 0, ptr %132, align 4
  %133 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %130, align 8
  %134 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 40)
  %135 = call ptr @"github.com/goplus/llgo/internal/runtime.ArrayOf"(i64 8, ptr %134)
  %136 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %129, ptr %135, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %133, i1 false)
  %137 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %138 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %137, i32 0, i32 0
  store ptr @13, ptr %138, align 8
  %139 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %137, i32 0, i32 1
  store i64 4, ptr %139, align 4
  %140 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %137, align 8
  %141 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %142 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %141, i32 0, i32 0
  store ptr null, ptr %142, align 8
  %143 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %141, i32 0, i32 1
  store i64 0, ptr %143, align 4
  %144 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %141, align 8
  %145 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 34)
  %146 = call ptr @"github.com/goplus/llgo/internal/runtime.ArrayOf"(i64 8, ptr %145)
  %147 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %140, ptr %146, i64 8, %"github.com/goplus/llgo/internal/runtime.String" %144, i1 false)
  %148 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %149 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %148, i32 0, i32 0
  store ptr @14, ptr %149, align 8
  %150 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %148, i32 0, i32 1
  store i64 5, ptr %150, align 4
  %151 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %148, align 8
  %152 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %153 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %152, i32 0, i32 0
  store ptr null, ptr %153, align 8
  %154 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %152, i32 0, i32 1
  store i64 0, ptr %154, align 4
  %155 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %152, align 8
  %156 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 24)
  %157 = call ptr @"github.com/goplus/llgo/internal/runtime.ArrayOf"(i64 8, ptr %156)
  %158 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %151, ptr %157, i64 72, %"github.com/goplus/llgo/internal/runtime.String" %155, i1 false)
  %159 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %160 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %159, i32 0, i32 0
  store ptr @15, ptr %160, align 8
  %161 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %159, i32 0, i32 1
  store i64 8, ptr %161, align 4
  %162 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %159, align 8
  %163 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %164 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %163, i32 0, i32 0
  store ptr null, ptr %164, align 8
  %165 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %163, i32 0, i32 1
  store i64 0, ptr %165, align 4
  %166 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %163, align 8
  %167 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 58)
  %168 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %162, ptr %167, i64 200, %"github.com/goplus/llgo/internal/runtime.String" %166, i1 false)
  %169 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %170 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %169, i32 0, i32 0
  store ptr @3, ptr %170, align 8
  %171 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %169, i32 0, i32 1
  store i64 4, ptr %171, align 4
  %172 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %169, align 8
  %173 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 224)
  %174 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %173, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %136, ptr %174, align 8
  %175 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %173, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %147, ptr %175, align 8
  %176 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %173, i64 2
  store %"github.com/goplus/llgo/internal/abi.StructField" %158, ptr %176, align 8
  %177 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %173, i64 3
  store %"github.com/goplus/llgo/internal/abi.StructField" %168, ptr %177, align 8
  %178 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %179 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %178, i32 0, i32 0
  store ptr %173, ptr %179, align 8
  %180 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %178, i32 0, i32 1
  store i64 4, ptr %180, align 4
  %181 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %178, i32 0, i32 2
  store i64 4, ptr %181, align 4
  %182 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %178, align 8
  %183 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %172, i64 208, %"github.com/goplus/llgo/internal/runtime.Slice" %182)
  %184 = call ptr @"github.com/goplus/llgo/internal/runtime.MapOf"(ptr %124, ptr %125, ptr %183, i64 4)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %184)
  store ptr %184, ptr @"map[_llgo_int]_llgo_string", align 8
  br label %_llgo_18

_llgo_18:                                         ; preds = %_llgo_17, %_llgo_16
  ret void
}

declare ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64)

declare ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64)

declare void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface")

declare ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64)

declare void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64)

declare void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8)

declare ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr, i64)

declare i1 @"github.com/goplus/llgo/internal/runtime.StringEqual"(%"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.String")

declare i1 @"github.com/goplus/llgo/internal/runtime.EfaceEqual"(%"github.com/goplus/llgo/internal/runtime.eface", %"github.com/goplus/llgo/internal/runtime.eface")

declare %"github.com/goplus/llgo/internal/runtime.Slice" @"github.com/goplus/llgo/internal/runtime.NewSlice3"(ptr, i64, i64, i64, i64, i64)

declare ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String", i64, %"github.com/goplus/llgo/internal/runtime.Slice")

declare %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String", ptr, i64, %"github.com/goplus/llgo/internal/runtime.String", i1)

declare ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String", i64, i64, i64, i64)

declare ptr @"github.com/goplus/llgo/internal/runtime.Interface"(%"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.Slice")

declare void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr, %"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.String", ptr, %"github.com/goplus/llgo/internal/runtime.Slice", %"github.com/goplus/llgo/internal/runtime.Slice")

declare ptr @"github.com/goplus/llgo/internal/runtime.NewChan"(i64, i64)

declare ptr @"github.com/goplus/llgo/internal/runtime.MapOf"(ptr, ptr, ptr, i64)

declare ptr @"github.com/goplus/llgo/internal/runtime.ArrayOf"(i64, ptr)

declare void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr)

declare ptr @"github.com/goplus/llgo/internal/runtime.MakeMap"(ptr, i64)

declare void @"github.com/goplus/llgo/internal/runtime.init"()
