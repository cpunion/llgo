; ModuleID = 'main'
source_filename = "main"

%main.S = type { %"github.com/goplus/llgo/internal/runtime.iface" }
%"github.com/goplus/llgo/internal/runtime.iface" = type { ptr, ptr }
%"github.com/goplus/llgo/internal/runtime.String" = type { ptr, i64 }
%main.impl = type {}
%"github.com/goplus/llgo/internal/runtime.eface" = type { ptr, ptr }
%"github.com/goplus/llgo/internal/runtime.Slice" = type { ptr, i64, i64 }
%"github.com/goplus/llgo/internal/abi.Method" = type { %"github.com/goplus/llgo/internal/runtime.String", ptr, ptr, ptr }
%"github.com/goplus/llgo/internal/abi.Imethod" = type { %"github.com/goplus/llgo/internal/runtime.String", ptr }
%"github.com/goplus/llgo/internal/abi.StructField" = type { %"github.com/goplus/llgo/internal/runtime.String", ptr, i64, %"github.com/goplus/llgo/internal/runtime.String", i1 }

@"main.init$guard" = global i1 false, align 1
@0 = private unnamed_addr constant [3 x i8] c"two", align 1
@__llgo_argc = global i32 0, align 4
@__llgo_argv = global ptr null, align 8
@_llgo_main.impl = linkonce global ptr null, align 8
@1 = private unnamed_addr constant [9 x i8] c"main.impl", align 1
@"_llgo_struct$n1H8J_3prDN3firMwPxBLVTkE5hJ9Di-AqNvaC9jczw" = linkonce global ptr null, align 8
@2 = private unnamed_addr constant [4 x i8] c"main", align 1
@3 = private unnamed_addr constant [3 x i8] c"one", align 1
@4 = private unnamed_addr constant [8 x i8] c"main.one", align 1
@"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA" = linkonce global ptr null, align 8
@_llgo_int = linkonce global ptr null, align 8
@5 = private unnamed_addr constant [8 x i8] c"main.two", align 1
@"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to" = linkonce global ptr null, align 8
@_llgo_string = linkonce global ptr null, align 8
@6 = private unnamed_addr constant [4 x i8] c"impl", align 1
@"main.iface$zZ89tENb5h_KNjvpxf1TXPfaWFYn0IZrZwyVf42lRtA" = linkonce global ptr null, align 8
@_llgo_main.I = linkonce global ptr null, align 8
@7 = private unnamed_addr constant [6 x i8] c"main.I", align 1
@8 = private unnamed_addr constant [21 x i8] c"type assertion failed", align 1
@9 = private unnamed_addr constant [4 x i8] c"pass", align 1

define i64 @main.S.one(%main.S %0) {
_llgo_0:
  %1 = alloca %main.S, align 8
  %2 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %1, i64 16)
  store %main.S %0, ptr %2, align 8
  %3 = getelementptr inbounds %main.S, ptr %2, i32 0, i32 0
  %4 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %3, align 8
  %5 = call ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface" %4)
  %6 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %4, 0
  %7 = getelementptr ptr, ptr %6, i64 3
  %8 = load ptr, ptr %7, align 8
  %9 = alloca { ptr, ptr }, align 8
  %10 = getelementptr inbounds { ptr, ptr }, ptr %9, i32 0, i32 0
  store ptr %8, ptr %10, align 8
  %11 = getelementptr inbounds { ptr, ptr }, ptr %9, i32 0, i32 1
  store ptr %5, ptr %11, align 8
  %12 = load { ptr, ptr }, ptr %9, align 8
  %13 = extractvalue { ptr, ptr } %12, 1
  %14 = extractvalue { ptr, ptr } %12, 0
  %15 = call i64 %14(ptr %13)
  ret i64 %15
}

define %"github.com/goplus/llgo/internal/runtime.String" @main.S.two(%main.S %0) {
_llgo_0:
  %1 = alloca %main.S, align 8
  %2 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %1, i64 16)
  store %main.S %0, ptr %2, align 8
  %3 = getelementptr inbounds %main.S, ptr %2, i32 0, i32 0
  %4 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %3, align 8
  %5 = call ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface" %4)
  %6 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %4, 0
  %7 = getelementptr ptr, ptr %6, i64 4
  %8 = load ptr, ptr %7, align 8
  %9 = alloca { ptr, ptr }, align 8
  %10 = getelementptr inbounds { ptr, ptr }, ptr %9, i32 0, i32 0
  store ptr %8, ptr %10, align 8
  %11 = getelementptr inbounds { ptr, ptr }, ptr %9, i32 0, i32 1
  store ptr %5, ptr %11, align 8
  %12 = load { ptr, ptr }, ptr %9, align 8
  %13 = extractvalue { ptr, ptr } %12, 1
  %14 = extractvalue { ptr, ptr } %12, 0
  %15 = call %"github.com/goplus/llgo/internal/runtime.String" %14(ptr %13)
  ret %"github.com/goplus/llgo/internal/runtime.String" %15
}

define i64 @"main.(*S).one"(ptr %0) {
_llgo_0:
  %1 = getelementptr inbounds %main.S, ptr %0, i32 0, i32 0
  %2 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %1, align 8
  %3 = call ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface" %2)
  %4 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %2, 0
  %5 = getelementptr ptr, ptr %4, i64 3
  %6 = load ptr, ptr %5, align 8
  %7 = alloca { ptr, ptr }, align 8
  %8 = getelementptr inbounds { ptr, ptr }, ptr %7, i32 0, i32 0
  store ptr %6, ptr %8, align 8
  %9 = getelementptr inbounds { ptr, ptr }, ptr %7, i32 0, i32 1
  store ptr %3, ptr %9, align 8
  %10 = load { ptr, ptr }, ptr %7, align 8
  %11 = extractvalue { ptr, ptr } %10, 1
  %12 = extractvalue { ptr, ptr } %10, 0
  %13 = call i64 %12(ptr %11)
  ret i64 %13
}

define %"github.com/goplus/llgo/internal/runtime.String" @"main.(*S).two"(ptr %0) {
_llgo_0:
  %1 = getelementptr inbounds %main.S, ptr %0, i32 0, i32 0
  %2 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %1, align 8
  %3 = call ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface" %2)
  %4 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %2, 0
  %5 = getelementptr ptr, ptr %4, i64 4
  %6 = load ptr, ptr %5, align 8
  %7 = alloca { ptr, ptr }, align 8
  %8 = getelementptr inbounds { ptr, ptr }, ptr %7, i32 0, i32 0
  store ptr %6, ptr %8, align 8
  %9 = getelementptr inbounds { ptr, ptr }, ptr %7, i32 0, i32 1
  store ptr %3, ptr %9, align 8
  %10 = load { ptr, ptr }, ptr %7, align 8
  %11 = extractvalue { ptr, ptr } %10, 1
  %12 = extractvalue { ptr, ptr } %10, 0
  %13 = call %"github.com/goplus/llgo/internal/runtime.String" %12(ptr %11)
  ret %"github.com/goplus/llgo/internal/runtime.String" %13
}

define i64 @main.impl.one(%main.impl %0) {
_llgo_0:
  ret i64 1
}

define %"github.com/goplus/llgo/internal/runtime.String" @main.impl.two(%main.impl %0) {
_llgo_0:
  %1 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1, i32 0, i32 0
  store ptr @0, ptr %2, align 8
  %3 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1, i32 0, i32 1
  store i64 3, ptr %3, align 4
  %4 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1, align 8
  ret %"github.com/goplus/llgo/internal/runtime.String" %4
}

define i64 @"main.(*impl).one"(ptr %0) {
_llgo_0:
  %1 = load %main.impl, ptr %0, align 1
  %2 = call i64 @main.impl.one(%main.impl %1)
  ret i64 %2
}

define %"github.com/goplus/llgo/internal/runtime.String" @"main.(*impl).two"(ptr %0) {
_llgo_0:
  %1 = load %main.impl, ptr %0, align 1
  %2 = call %"github.com/goplus/llgo/internal/runtime.String" @main.impl.two(%main.impl %1)
  ret %"github.com/goplus/llgo/internal/runtime.String" %2
}

define void @main.init() {
_llgo_0:
  %0 = load i1, ptr @"main.init$guard", align 1
  br i1 %0, label %_llgo_2, label %_llgo_1

_llgo_1:                                          ; preds = %_llgo_0
  store i1 true, ptr @"main.init$guard", align 1
  call void @"main.init$after"()
  br label %_llgo_2

_llgo_2:                                          ; preds = %_llgo_1, %_llgo_0
  ret void
}

define i32 @main(i32 %0, ptr %1) {
_llgo_0:
  store i32 %0, ptr @__llgo_argc, align 4
  store ptr %1, ptr @__llgo_argv, align 8
  call void @"github.com/goplus/llgo/internal/runtime.init"()
  call void @main.init()
  %2 = alloca %main.S, align 8
  %3 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %2, i64 16)
  %4 = getelementptr inbounds %main.S, ptr %3, i32 0, i32 0
  %5 = load ptr, ptr @_llgo_main.impl, align 8
  %6 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  store %main.impl zeroinitializer, ptr %6, align 1
  %7 = load ptr, ptr @"main.iface$zZ89tENb5h_KNjvpxf1TXPfaWFYn0IZrZwyVf42lRtA", align 8
  %8 = call ptr @"github.com/goplus/llgo/internal/runtime.NewItab"(ptr %7, ptr %5)
  %9 = alloca %"github.com/goplus/llgo/internal/runtime.iface", align 8
  %10 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.iface", ptr %9, i32 0, i32 0
  store ptr %8, ptr %10, align 8
  %11 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.iface", ptr %9, i32 0, i32 1
  store ptr %6, ptr %11, align 8
  %12 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %9, align 8
  store %"github.com/goplus/llgo/internal/runtime.iface" %12, ptr %4, align 8
  %13 = getelementptr inbounds %main.S, ptr %3, i32 0, i32 0
  %14 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %13, align 8
  %15 = call ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface" %14)
  %16 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %14, 0
  %17 = getelementptr ptr, ptr %16, i64 3
  %18 = load ptr, ptr %17, align 8
  %19 = alloca { ptr, ptr }, align 8
  %20 = getelementptr inbounds { ptr, ptr }, ptr %19, i32 0, i32 0
  store ptr %18, ptr %20, align 8
  %21 = getelementptr inbounds { ptr, ptr }, ptr %19, i32 0, i32 1
  store ptr %15, ptr %21, align 8
  %22 = load { ptr, ptr }, ptr %19, align 8
  %23 = extractvalue { ptr, ptr } %22, 1
  %24 = extractvalue { ptr, ptr } %22, 0
  %25 = call i64 %24(ptr %23)
  %26 = icmp ne i64 %25, 1
  br i1 %26, label %_llgo_1, label %_llgo_2

_llgo_1:                                          ; preds = %_llgo_0
  %27 = load ptr, ptr @_llgo_int, align 8
  %28 = inttoptr i64 %25 to ptr
  %29 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %30 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %29, i32 0, i32 0
  store ptr %27, ptr %30, align 8
  %31 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %29, i32 0, i32 1
  store ptr %28, ptr %31, align 8
  %32 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %29, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %32)
  unreachable

_llgo_2:                                          ; preds = %_llgo_0
  %33 = load %main.S, ptr %3, align 8
  %34 = extractvalue %main.S %33, 0
  %35 = call ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface" %34)
  %36 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %34, 0
  %37 = getelementptr ptr, ptr %36, i64 3
  %38 = load ptr, ptr %37, align 8
  %39 = alloca { ptr, ptr }, align 8
  %40 = getelementptr inbounds { ptr, ptr }, ptr %39, i32 0, i32 0
  store ptr %38, ptr %40, align 8
  %41 = getelementptr inbounds { ptr, ptr }, ptr %39, i32 0, i32 1
  store ptr %35, ptr %41, align 8
  %42 = load { ptr, ptr }, ptr %39, align 8
  %43 = extractvalue { ptr, ptr } %42, 1
  %44 = extractvalue { ptr, ptr } %42, 0
  %45 = call i64 %44(ptr %43)
  %46 = icmp ne i64 %45, 1
  br i1 %46, label %_llgo_3, label %_llgo_4

_llgo_3:                                          ; preds = %_llgo_2
  %47 = load ptr, ptr @_llgo_int, align 8
  %48 = inttoptr i64 %45 to ptr
  %49 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %50 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %49, i32 0, i32 0
  store ptr %47, ptr %50, align 8
  %51 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %49, i32 0, i32 1
  store ptr %48, ptr %51, align 8
  %52 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %49, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %52)
  unreachable

_llgo_4:                                          ; preds = %_llgo_2
  %53 = getelementptr inbounds %main.S, ptr %3, i32 0, i32 0
  %54 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %53, align 8
  %55 = call ptr @"github.com/goplus/llgo/internal/runtime.IfaceType"(%"github.com/goplus/llgo/internal/runtime.iface" %54)
  %56 = load ptr, ptr @_llgo_main.I, align 8
  %57 = call i1 @"github.com/goplus/llgo/internal/runtime.Implements"(ptr %56, ptr %55)
  br i1 %57, label %_llgo_17, label %_llgo_18

_llgo_5:                                          ; preds = %_llgo_17
  %58 = load ptr, ptr @_llgo_int, align 8
  %59 = inttoptr i64 %166 to ptr
  %60 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %61 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %60, i32 0, i32 0
  store ptr %58, ptr %61, align 8
  %62 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %60, i32 0, i32 1
  store ptr %59, ptr %62, align 8
  %63 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %60, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %63)
  unreachable

_llgo_6:                                          ; preds = %_llgo_17
  %64 = load %main.S, ptr %3, align 8
  %65 = extractvalue %main.S %64, 0
  %66 = call ptr @"github.com/goplus/llgo/internal/runtime.IfaceType"(%"github.com/goplus/llgo/internal/runtime.iface" %65)
  %67 = load ptr, ptr @_llgo_main.I, align 8
  %68 = call i1 @"github.com/goplus/llgo/internal/runtime.Implements"(ptr %67, ptr %66)
  br i1 %68, label %_llgo_19, label %_llgo_20

_llgo_7:                                          ; preds = %_llgo_19
  %69 = load ptr, ptr @_llgo_int, align 8
  %70 = inttoptr i64 %193 to ptr
  %71 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %72 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %71, i32 0, i32 0
  store ptr %69, ptr %72, align 8
  %73 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %71, i32 0, i32 1
  store ptr %70, ptr %73, align 8
  %74 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %71, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %74)
  unreachable

_llgo_8:                                          ; preds = %_llgo_19
  %75 = getelementptr inbounds %main.S, ptr %3, i32 0, i32 0
  %76 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %75, align 8
  %77 = call ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface" %76)
  %78 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %76, 0
  %79 = getelementptr ptr, ptr %78, i64 4
  %80 = load ptr, ptr %79, align 8
  %81 = alloca { ptr, ptr }, align 8
  %82 = getelementptr inbounds { ptr, ptr }, ptr %81, i32 0, i32 0
  store ptr %80, ptr %82, align 8
  %83 = getelementptr inbounds { ptr, ptr }, ptr %81, i32 0, i32 1
  store ptr %77, ptr %83, align 8
  %84 = load { ptr, ptr }, ptr %81, align 8
  %85 = extractvalue { ptr, ptr } %84, 1
  %86 = extractvalue { ptr, ptr } %84, 0
  %87 = call %"github.com/goplus/llgo/internal/runtime.String" %86(ptr %85)
  %88 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %89 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %88, i32 0, i32 0
  store ptr @0, ptr %89, align 8
  %90 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %88, i32 0, i32 1
  store i64 3, ptr %90, align 4
  %91 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %88, align 8
  %92 = call i1 @"github.com/goplus/llgo/internal/runtime.StringEqual"(%"github.com/goplus/llgo/internal/runtime.String" %87, %"github.com/goplus/llgo/internal/runtime.String" %91)
  %93 = xor i1 %92, true
  br i1 %93, label %_llgo_9, label %_llgo_10

_llgo_9:                                          ; preds = %_llgo_8
  %94 = load ptr, ptr @_llgo_string, align 8
  %95 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %87, ptr %95, align 8
  %96 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %97 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %96, i32 0, i32 0
  store ptr %94, ptr %97, align 8
  %98 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %96, i32 0, i32 1
  store ptr %95, ptr %98, align 8
  %99 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %96, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %99)
  unreachable

_llgo_10:                                         ; preds = %_llgo_8
  %100 = load %main.S, ptr %3, align 8
  %101 = extractvalue %main.S %100, 0
  %102 = call ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface" %101)
  %103 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %101, 0
  %104 = getelementptr ptr, ptr %103, i64 4
  %105 = load ptr, ptr %104, align 8
  %106 = alloca { ptr, ptr }, align 8
  %107 = getelementptr inbounds { ptr, ptr }, ptr %106, i32 0, i32 0
  store ptr %105, ptr %107, align 8
  %108 = getelementptr inbounds { ptr, ptr }, ptr %106, i32 0, i32 1
  store ptr %102, ptr %108, align 8
  %109 = load { ptr, ptr }, ptr %106, align 8
  %110 = extractvalue { ptr, ptr } %109, 1
  %111 = extractvalue { ptr, ptr } %109, 0
  %112 = call %"github.com/goplus/llgo/internal/runtime.String" %111(ptr %110)
  %113 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %114 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %113, i32 0, i32 0
  store ptr @0, ptr %114, align 8
  %115 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %113, i32 0, i32 1
  store i64 3, ptr %115, align 4
  %116 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %113, align 8
  %117 = call i1 @"github.com/goplus/llgo/internal/runtime.StringEqual"(%"github.com/goplus/llgo/internal/runtime.String" %112, %"github.com/goplus/llgo/internal/runtime.String" %116)
  %118 = xor i1 %117, true
  br i1 %118, label %_llgo_11, label %_llgo_12

_llgo_11:                                         ; preds = %_llgo_10
  %119 = load ptr, ptr @_llgo_string, align 8
  %120 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %112, ptr %120, align 8
  %121 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %122 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %121, i32 0, i32 0
  store ptr %119, ptr %122, align 8
  %123 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %121, i32 0, i32 1
  store ptr %120, ptr %123, align 8
  %124 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %121, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %124)
  unreachable

_llgo_12:                                         ; preds = %_llgo_10
  %125 = getelementptr inbounds %main.S, ptr %3, i32 0, i32 0
  %126 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %125, align 8
  %127 = call ptr @"github.com/goplus/llgo/internal/runtime.IfaceType"(%"github.com/goplus/llgo/internal/runtime.iface" %126)
  %128 = load ptr, ptr @_llgo_main.I, align 8
  %129 = call i1 @"github.com/goplus/llgo/internal/runtime.Implements"(ptr %128, ptr %127)
  br i1 %129, label %_llgo_21, label %_llgo_22

_llgo_13:                                         ; preds = %_llgo_21
  %130 = load ptr, ptr @_llgo_string, align 8
  %131 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %220, ptr %131, align 8
  %132 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %133 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %132, i32 0, i32 0
  store ptr %130, ptr %133, align 8
  %134 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %132, i32 0, i32 1
  store ptr %131, ptr %134, align 8
  %135 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %132, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %135)
  unreachable

_llgo_14:                                         ; preds = %_llgo_21
  %136 = load %main.S, ptr %3, align 8
  %137 = extractvalue %main.S %136, 0
  %138 = call ptr @"github.com/goplus/llgo/internal/runtime.IfaceType"(%"github.com/goplus/llgo/internal/runtime.iface" %137)
  %139 = load ptr, ptr @_llgo_main.I, align 8
  %140 = call i1 @"github.com/goplus/llgo/internal/runtime.Implements"(ptr %139, ptr %138)
  br i1 %140, label %_llgo_23, label %_llgo_24

_llgo_15:                                         ; preds = %_llgo_23
  %141 = load ptr, ptr @_llgo_string, align 8
  %142 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %252, ptr %142, align 8
  %143 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %144 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %143, i32 0, i32 0
  store ptr %141, ptr %144, align 8
  %145 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %143, i32 0, i32 1
  store ptr %142, ptr %145, align 8
  %146 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %143, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %146)
  unreachable

_llgo_16:                                         ; preds = %_llgo_23
  %147 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %148 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %147, i32 0, i32 0
  store ptr @9, ptr %148, align 8
  %149 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %147, i32 0, i32 1
  store i64 4, ptr %149, align 4
  %150 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %147, align 8
  call void @"github.com/goplus/llgo/internal/runtime.PrintString"(%"github.com/goplus/llgo/internal/runtime.String" %150)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  ret i32 0

_llgo_17:                                         ; preds = %_llgo_4
  %151 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %54, 1
  %152 = load ptr, ptr @"main.iface$zZ89tENb5h_KNjvpxf1TXPfaWFYn0IZrZwyVf42lRtA", align 8
  %153 = call ptr @"github.com/goplus/llgo/internal/runtime.NewItab"(ptr %152, ptr %55)
  %154 = alloca %"github.com/goplus/llgo/internal/runtime.iface", align 8
  %155 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.iface", ptr %154, i32 0, i32 0
  store ptr %153, ptr %155, align 8
  %156 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.iface", ptr %154, i32 0, i32 1
  store ptr %151, ptr %156, align 8
  %157 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %154, align 8
  %158 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  %159 = getelementptr inbounds { %"github.com/goplus/llgo/internal/runtime.iface" }, ptr %158, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.iface" %54, ptr %159, align 8
  %160 = alloca { ptr, ptr }, align 8
  %161 = getelementptr inbounds { ptr, ptr }, ptr %160, i32 0, i32 0
  store ptr @"main.one$bound", ptr %161, align 8
  %162 = getelementptr inbounds { ptr, ptr }, ptr %160, i32 0, i32 1
  store ptr %158, ptr %162, align 8
  %163 = load { ptr, ptr }, ptr %160, align 8
  %164 = extractvalue { ptr, ptr } %163, 1
  %165 = extractvalue { ptr, ptr } %163, 0
  %166 = call i64 %165(ptr %164)
  %167 = icmp ne i64 %166, 1
  br i1 %167, label %_llgo_5, label %_llgo_6

_llgo_18:                                         ; preds = %_llgo_4
  %168 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %169 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %168, i32 0, i32 0
  store ptr @8, ptr %169, align 8
  %170 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %168, i32 0, i32 1
  store i64 21, ptr %170, align 4
  %171 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %168, align 8
  %172 = load ptr, ptr @_llgo_string, align 8
  %173 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %171, ptr %173, align 8
  %174 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %175 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %174, i32 0, i32 0
  store ptr %172, ptr %175, align 8
  %176 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %174, i32 0, i32 1
  store ptr %173, ptr %176, align 8
  %177 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %174, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %177)
  unreachable

_llgo_19:                                         ; preds = %_llgo_6
  %178 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %65, 1
  %179 = load ptr, ptr @"main.iface$zZ89tENb5h_KNjvpxf1TXPfaWFYn0IZrZwyVf42lRtA", align 8
  %180 = call ptr @"github.com/goplus/llgo/internal/runtime.NewItab"(ptr %179, ptr %66)
  %181 = alloca %"github.com/goplus/llgo/internal/runtime.iface", align 8
  %182 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.iface", ptr %181, i32 0, i32 0
  store ptr %180, ptr %182, align 8
  %183 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.iface", ptr %181, i32 0, i32 1
  store ptr %178, ptr %183, align 8
  %184 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %181, align 8
  %185 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  %186 = getelementptr inbounds { %"github.com/goplus/llgo/internal/runtime.iface" }, ptr %185, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.iface" %65, ptr %186, align 8
  %187 = alloca { ptr, ptr }, align 8
  %188 = getelementptr inbounds { ptr, ptr }, ptr %187, i32 0, i32 0
  store ptr @"main.one$bound", ptr %188, align 8
  %189 = getelementptr inbounds { ptr, ptr }, ptr %187, i32 0, i32 1
  store ptr %185, ptr %189, align 8
  %190 = load { ptr, ptr }, ptr %187, align 8
  %191 = extractvalue { ptr, ptr } %190, 1
  %192 = extractvalue { ptr, ptr } %190, 0
  %193 = call i64 %192(ptr %191)
  %194 = icmp ne i64 %193, 1
  br i1 %194, label %_llgo_7, label %_llgo_8

_llgo_20:                                         ; preds = %_llgo_6
  %195 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %196 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %195, i32 0, i32 0
  store ptr @8, ptr %196, align 8
  %197 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %195, i32 0, i32 1
  store i64 21, ptr %197, align 4
  %198 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %195, align 8
  %199 = load ptr, ptr @_llgo_string, align 8
  %200 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %198, ptr %200, align 8
  %201 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %202 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %201, i32 0, i32 0
  store ptr %199, ptr %202, align 8
  %203 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %201, i32 0, i32 1
  store ptr %200, ptr %203, align 8
  %204 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %201, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %204)
  unreachable

_llgo_21:                                         ; preds = %_llgo_12
  %205 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %126, 1
  %206 = load ptr, ptr @"main.iface$zZ89tENb5h_KNjvpxf1TXPfaWFYn0IZrZwyVf42lRtA", align 8
  %207 = call ptr @"github.com/goplus/llgo/internal/runtime.NewItab"(ptr %206, ptr %127)
  %208 = alloca %"github.com/goplus/llgo/internal/runtime.iface", align 8
  %209 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.iface", ptr %208, i32 0, i32 0
  store ptr %207, ptr %209, align 8
  %210 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.iface", ptr %208, i32 0, i32 1
  store ptr %205, ptr %210, align 8
  %211 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %208, align 8
  %212 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  %213 = getelementptr inbounds { %"github.com/goplus/llgo/internal/runtime.iface" }, ptr %212, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.iface" %126, ptr %213, align 8
  %214 = alloca { ptr, ptr }, align 8
  %215 = getelementptr inbounds { ptr, ptr }, ptr %214, i32 0, i32 0
  store ptr @"main.two$bound", ptr %215, align 8
  %216 = getelementptr inbounds { ptr, ptr }, ptr %214, i32 0, i32 1
  store ptr %212, ptr %216, align 8
  %217 = load { ptr, ptr }, ptr %214, align 8
  %218 = extractvalue { ptr, ptr } %217, 1
  %219 = extractvalue { ptr, ptr } %217, 0
  %220 = call %"github.com/goplus/llgo/internal/runtime.String" %219(ptr %218)
  %221 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %222 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %221, i32 0, i32 0
  store ptr @0, ptr %222, align 8
  %223 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %221, i32 0, i32 1
  store i64 3, ptr %223, align 4
  %224 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %221, align 8
  %225 = call i1 @"github.com/goplus/llgo/internal/runtime.StringEqual"(%"github.com/goplus/llgo/internal/runtime.String" %220, %"github.com/goplus/llgo/internal/runtime.String" %224)
  %226 = xor i1 %225, true
  br i1 %226, label %_llgo_13, label %_llgo_14

_llgo_22:                                         ; preds = %_llgo_12
  %227 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %228 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %227, i32 0, i32 0
  store ptr @8, ptr %228, align 8
  %229 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %227, i32 0, i32 1
  store i64 21, ptr %229, align 4
  %230 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %227, align 8
  %231 = load ptr, ptr @_llgo_string, align 8
  %232 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %230, ptr %232, align 8
  %233 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %234 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %233, i32 0, i32 0
  store ptr %231, ptr %234, align 8
  %235 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %233, i32 0, i32 1
  store ptr %232, ptr %235, align 8
  %236 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %233, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %236)
  unreachable

_llgo_23:                                         ; preds = %_llgo_14
  %237 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %137, 1
  %238 = load ptr, ptr @"main.iface$zZ89tENb5h_KNjvpxf1TXPfaWFYn0IZrZwyVf42lRtA", align 8
  %239 = call ptr @"github.com/goplus/llgo/internal/runtime.NewItab"(ptr %238, ptr %138)
  %240 = alloca %"github.com/goplus/llgo/internal/runtime.iface", align 8
  %241 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.iface", ptr %240, i32 0, i32 0
  store ptr %239, ptr %241, align 8
  %242 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.iface", ptr %240, i32 0, i32 1
  store ptr %237, ptr %242, align 8
  %243 = load %"github.com/goplus/llgo/internal/runtime.iface", ptr %240, align 8
  %244 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  %245 = getelementptr inbounds { %"github.com/goplus/llgo/internal/runtime.iface" }, ptr %244, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.iface" %137, ptr %245, align 8
  %246 = alloca { ptr, ptr }, align 8
  %247 = getelementptr inbounds { ptr, ptr }, ptr %246, i32 0, i32 0
  store ptr @"main.two$bound", ptr %247, align 8
  %248 = getelementptr inbounds { ptr, ptr }, ptr %246, i32 0, i32 1
  store ptr %244, ptr %248, align 8
  %249 = load { ptr, ptr }, ptr %246, align 8
  %250 = extractvalue { ptr, ptr } %249, 1
  %251 = extractvalue { ptr, ptr } %249, 0
  %252 = call %"github.com/goplus/llgo/internal/runtime.String" %251(ptr %250)
  %253 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %254 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %253, i32 0, i32 0
  store ptr @0, ptr %254, align 8
  %255 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %253, i32 0, i32 1
  store i64 3, ptr %255, align 4
  %256 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %253, align 8
  %257 = call i1 @"github.com/goplus/llgo/internal/runtime.StringEqual"(%"github.com/goplus/llgo/internal/runtime.String" %252, %"github.com/goplus/llgo/internal/runtime.String" %256)
  %258 = xor i1 %257, true
  br i1 %258, label %_llgo_15, label %_llgo_16

_llgo_24:                                         ; preds = %_llgo_14
  %259 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %260 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %259, i32 0, i32 0
  store ptr @8, ptr %260, align 8
  %261 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %259, i32 0, i32 1
  store i64 21, ptr %261, align 4
  %262 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %259, align 8
  %263 = load ptr, ptr @_llgo_string, align 8
  %264 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %262, ptr %264, align 8
  %265 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %266 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %265, i32 0, i32 0
  store ptr %263, ptr %266, align 8
  %267 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %265, i32 0, i32 1
  store ptr %264, ptr %267, align 8
  %268 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %265, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %268)
  unreachable
}

declare ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr, i64)

declare ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface")

declare void @"github.com/goplus/llgo/internal/runtime.init"()

define void @"main.init$after"() {
_llgo_0:
  %0 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %0, i32 0, i32 0
  store ptr @1, ptr %1, align 8
  %2 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %0, i32 0, i32 1
  store i64 9, ptr %2, align 4
  %3 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %0, align 8
  %4 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %3, i64 25, i64 0, i64 2, i64 2)
  store ptr %4, ptr @_llgo_main.impl, align 8
  %5 = load ptr, ptr @"_llgo_struct$n1H8J_3prDN3firMwPxBLVTkE5hJ9Di-AqNvaC9jczw", align 8
  %6 = icmp eq ptr %5, null
  br i1 %6, label %_llgo_1, label %_llgo_2

_llgo_1:                                          ; preds = %_llgo_0
  %7 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %8 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %7, i32 0, i32 0
  store ptr @2, ptr %8, align 8
  %9 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %7, i32 0, i32 1
  store i64 4, ptr %9, align 4
  %10 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %7, align 8
  %11 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %12 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %13 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %12, i32 0, i32 0
  store ptr %11, ptr %13, align 8
  %14 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %12, i32 0, i32 1
  store i64 0, ptr %14, align 4
  %15 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %12, i32 0, i32 2
  store i64 0, ptr %15, align 4
  %16 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %12, align 8
  %17 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %10, i64 0, %"github.com/goplus/llgo/internal/runtime.Slice" %16)
  store ptr %17, ptr @"_llgo_struct$n1H8J_3prDN3firMwPxBLVTkE5hJ9Di-AqNvaC9jczw", align 8
  br label %_llgo_2

_llgo_2:                                          ; preds = %_llgo_1, %_llgo_0
  %18 = load ptr, ptr @"_llgo_struct$n1H8J_3prDN3firMwPxBLVTkE5hJ9Di-AqNvaC9jczw", align 8
  %19 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %20 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %19, i32 0, i32 0
  store ptr @3, ptr %20, align 8
  %21 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %19, i32 0, i32 1
  store i64 3, ptr %21, align 4
  %22 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %19, align 8
  %23 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %24 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %23, i32 0, i32 0
  store ptr @4, ptr %24, align 8
  %25 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %23, i32 0, i32 1
  store i64 8, ptr %25, align 4
  %26 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %23, align 8
  %27 = load ptr, ptr @_llgo_int, align 8
  %28 = icmp eq ptr %27, null
  br i1 %28, label %_llgo_3, label %_llgo_4

_llgo_3:                                          ; preds = %_llgo_2
  %29 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 34)
  store ptr %29, ptr @_llgo_int, align 8
  br label %_llgo_4

_llgo_4:                                          ; preds = %_llgo_3, %_llgo_2
  %30 = load ptr, ptr @_llgo_int, align 8
  %31 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %32 = icmp eq ptr %31, null
  br i1 %32, label %_llgo_5, label %_llgo_6

_llgo_5:                                          ; preds = %_llgo_4
  %33 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %34 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %35 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %34, i32 0, i32 0
  store ptr %33, ptr %35, align 8
  %36 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %34, i32 0, i32 1
  store i64 0, ptr %36, align 4
  %37 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %34, i32 0, i32 2
  store i64 0, ptr %37, align 4
  %38 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %34, align 8
  %39 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %40 = getelementptr ptr, ptr %39, i64 0
  store ptr %30, ptr %40, align 8
  %41 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %42 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %41, i32 0, i32 0
  store ptr %39, ptr %42, align 8
  %43 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %41, i32 0, i32 1
  store i64 1, ptr %43, align 4
  %44 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %41, i32 0, i32 2
  store i64 1, ptr %44, align 4
  %45 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %41, align 8
  %46 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %38, %"github.com/goplus/llgo/internal/runtime.Slice" %45, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %46)
  store ptr %46, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  br label %_llgo_6

_llgo_6:                                          ; preds = %_llgo_5, %_llgo_4
  %47 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %48 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %49 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %48, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %26, ptr %49, align 8
  %50 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %48, i32 0, i32 1
  store ptr %47, ptr %50, align 8
  %51 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %48, i32 0, i32 2
  store ptr @"main.(*impl).one", ptr %51, align 8
  %52 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %48, i32 0, i32 3
  store ptr @"main.(*impl).one", ptr %52, align 8
  %53 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %48, align 8
  %54 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %55 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %54, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %26, ptr %55, align 8
  %56 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %54, i32 0, i32 1
  store ptr %47, ptr %56, align 8
  %57 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %54, i32 0, i32 2
  store ptr @"main.(*impl).one", ptr %57, align 8
  %58 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %54, i32 0, i32 3
  store ptr @main.impl.one, ptr %58, align 8
  %59 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %54, align 8
  %60 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %61 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %60, i32 0, i32 0
  store ptr @0, ptr %61, align 8
  %62 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %60, i32 0, i32 1
  store i64 3, ptr %62, align 4
  %63 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %60, align 8
  %64 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %65 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %64, i32 0, i32 0
  store ptr @5, ptr %65, align 8
  %66 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %64, i32 0, i32 1
  store i64 8, ptr %66, align 4
  %67 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %64, align 8
  %68 = load ptr, ptr @_llgo_string, align 8
  %69 = icmp eq ptr %68, null
  br i1 %69, label %_llgo_7, label %_llgo_8

_llgo_7:                                          ; preds = %_llgo_6
  %70 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 24)
  store ptr %70, ptr @_llgo_string, align 8
  br label %_llgo_8

_llgo_8:                                          ; preds = %_llgo_7, %_llgo_6
  %71 = load ptr, ptr @_llgo_string, align 8
  %72 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %73 = icmp eq ptr %72, null
  br i1 %73, label %_llgo_9, label %_llgo_10

_llgo_9:                                          ; preds = %_llgo_8
  %74 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %75 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %76 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %75, i32 0, i32 0
  store ptr %74, ptr %76, align 8
  %77 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %75, i32 0, i32 1
  store i64 0, ptr %77, align 4
  %78 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %75, i32 0, i32 2
  store i64 0, ptr %78, align 4
  %79 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %75, align 8
  %80 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %81 = getelementptr ptr, ptr %80, i64 0
  store ptr %71, ptr %81, align 8
  %82 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %83 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %82, i32 0, i32 0
  store ptr %80, ptr %83, align 8
  %84 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %82, i32 0, i32 1
  store i64 1, ptr %84, align 4
  %85 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %82, i32 0, i32 2
  store i64 1, ptr %85, align 4
  %86 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %82, align 8
  %87 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %79, %"github.com/goplus/llgo/internal/runtime.Slice" %86, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %87)
  store ptr %87, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  br label %_llgo_10

_llgo_10:                                         ; preds = %_llgo_9, %_llgo_8
  %88 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %89 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %90 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %89, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %67, ptr %90, align 8
  %91 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %89, i32 0, i32 1
  store ptr %88, ptr %91, align 8
  %92 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %89, i32 0, i32 2
  store ptr @"main.(*impl).two", ptr %92, align 8
  %93 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %89, i32 0, i32 3
  store ptr @"main.(*impl).two", ptr %93, align 8
  %94 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %89, align 8
  %95 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %96 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %95, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %67, ptr %96, align 8
  %97 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %95, i32 0, i32 1
  store ptr %88, ptr %97, align 8
  %98 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %95, i32 0, i32 2
  store ptr @"main.(*impl).two", ptr %98, align 8
  %99 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %95, i32 0, i32 3
  store ptr @main.impl.two, ptr %99, align 8
  %100 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %95, align 8
  %101 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 80)
  %102 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %101, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %59, ptr %102, align 8
  %103 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %101, i64 1
  store %"github.com/goplus/llgo/internal/abi.Method" %100, ptr %103, align 8
  %104 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %105 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %104, i32 0, i32 0
  store ptr %101, ptr %105, align 8
  %106 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %104, i32 0, i32 1
  store i64 2, ptr %106, align 4
  %107 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %104, i32 0, i32 2
  store i64 2, ptr %107, align 4
  %108 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %104, align 8
  %109 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 80)
  %110 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %109, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %53, ptr %110, align 8
  %111 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %109, i64 1
  store %"github.com/goplus/llgo/internal/abi.Method" %94, ptr %111, align 8
  %112 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %113 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %112, i32 0, i32 0
  store ptr %109, ptr %113, align 8
  %114 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %112, i32 0, i32 1
  store i64 2, ptr %114, align 4
  %115 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %112, i32 0, i32 2
  store i64 2, ptr %115, align 4
  %116 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %112, align 8
  %117 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %118 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %117, i32 0, i32 0
  store ptr @2, ptr %118, align 8
  %119 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %117, i32 0, i32 1
  store i64 4, ptr %119, align 4
  %120 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %117, align 8
  %121 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %122 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %121, i32 0, i32 0
  store ptr @6, ptr %122, align 8
  %123 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %121, i32 0, i32 1
  store i64 4, ptr %123, align 4
  %124 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %121, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %4, %"github.com/goplus/llgo/internal/runtime.String" %120, %"github.com/goplus/llgo/internal/runtime.String" %124, ptr %18, %"github.com/goplus/llgo/internal/runtime.Slice" %108, %"github.com/goplus/llgo/internal/runtime.Slice" %116)
  %125 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %126 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %127 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %128 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %127, i32 0, i32 0
  store ptr @4, ptr %128, align 8
  %129 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %127, i32 0, i32 1
  store i64 8, ptr %129, align 4
  %130 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %127, align 8
  %131 = alloca %"github.com/goplus/llgo/internal/abi.Imethod", align 8
  %132 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Imethod", ptr %131, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %130, ptr %132, align 8
  %133 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Imethod", ptr %131, i32 0, i32 1
  store ptr %125, ptr %133, align 8
  %134 = load %"github.com/goplus/llgo/internal/abi.Imethod", ptr %131, align 8
  %135 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %136 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %135, i32 0, i32 0
  store ptr @5, ptr %136, align 8
  %137 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %135, i32 0, i32 1
  store i64 8, ptr %137, align 4
  %138 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %135, align 8
  %139 = alloca %"github.com/goplus/llgo/internal/abi.Imethod", align 8
  %140 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Imethod", ptr %139, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %138, ptr %140, align 8
  %141 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Imethod", ptr %139, i32 0, i32 1
  store ptr %126, ptr %141, align 8
  %142 = load %"github.com/goplus/llgo/internal/abi.Imethod", ptr %139, align 8
  %143 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 48)
  %144 = getelementptr %"github.com/goplus/llgo/internal/abi.Imethod", ptr %143, i64 0
  store %"github.com/goplus/llgo/internal/abi.Imethod" %134, ptr %144, align 8
  %145 = getelementptr %"github.com/goplus/llgo/internal/abi.Imethod", ptr %143, i64 1
  store %"github.com/goplus/llgo/internal/abi.Imethod" %142, ptr %145, align 8
  %146 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %147 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %146, i32 0, i32 0
  store ptr %143, ptr %147, align 8
  %148 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %146, i32 0, i32 1
  store i64 2, ptr %148, align 4
  %149 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %146, i32 0, i32 2
  store i64 2, ptr %149, align 4
  %150 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %146, align 8
  %151 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %152 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %151, i32 0, i32 0
  store ptr @2, ptr %152, align 8
  %153 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %151, i32 0, i32 1
  store i64 4, ptr %153, align 4
  %154 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %151, align 8
  %155 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %156 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %155, i32 0, i32 0
  store ptr null, ptr %156, align 8
  %157 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %155, i32 0, i32 1
  store i64 0, ptr %157, align 4
  %158 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %155, align 8
  %159 = call ptr @"github.com/goplus/llgo/internal/runtime.Interface"(%"github.com/goplus/llgo/internal/runtime.String" %154, %"github.com/goplus/llgo/internal/runtime.String" %158, %"github.com/goplus/llgo/internal/runtime.Slice" %150)
  store ptr %159, ptr @"main.iface$zZ89tENb5h_KNjvpxf1TXPfaWFYn0IZrZwyVf42lRtA", align 8
  %160 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %161 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %162 = load ptr, ptr @_llgo_main.I, align 8
  %163 = icmp eq ptr %162, null
  br i1 %163, label %_llgo_11, label %_llgo_12

_llgo_11:                                         ; preds = %_llgo_10
  %164 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %165 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %164, i32 0, i32 0
  store ptr @4, ptr %165, align 8
  %166 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %164, i32 0, i32 1
  store i64 8, ptr %166, align 4
  %167 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %164, align 8
  %168 = alloca %"github.com/goplus/llgo/internal/abi.Imethod", align 8
  %169 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Imethod", ptr %168, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %167, ptr %169, align 8
  %170 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Imethod", ptr %168, i32 0, i32 1
  store ptr %160, ptr %170, align 8
  %171 = load %"github.com/goplus/llgo/internal/abi.Imethod", ptr %168, align 8
  %172 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %173 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %172, i32 0, i32 0
  store ptr @5, ptr %173, align 8
  %174 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %172, i32 0, i32 1
  store i64 8, ptr %174, align 4
  %175 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %172, align 8
  %176 = alloca %"github.com/goplus/llgo/internal/abi.Imethod", align 8
  %177 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Imethod", ptr %176, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %175, ptr %177, align 8
  %178 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Imethod", ptr %176, i32 0, i32 1
  store ptr %161, ptr %178, align 8
  %179 = load %"github.com/goplus/llgo/internal/abi.Imethod", ptr %176, align 8
  %180 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 48)
  %181 = getelementptr %"github.com/goplus/llgo/internal/abi.Imethod", ptr %180, i64 0
  store %"github.com/goplus/llgo/internal/abi.Imethod" %171, ptr %181, align 8
  %182 = getelementptr %"github.com/goplus/llgo/internal/abi.Imethod", ptr %180, i64 1
  store %"github.com/goplus/llgo/internal/abi.Imethod" %179, ptr %182, align 8
  %183 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %184 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %183, i32 0, i32 0
  store ptr %180, ptr %184, align 8
  %185 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %183, i32 0, i32 1
  store i64 2, ptr %185, align 4
  %186 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %183, i32 0, i32 2
  store i64 2, ptr %186, align 4
  %187 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %183, align 8
  %188 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %189 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %188, i32 0, i32 0
  store ptr @2, ptr %189, align 8
  %190 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %188, i32 0, i32 1
  store i64 4, ptr %190, align 4
  %191 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %188, align 8
  %192 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %193 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %192, i32 0, i32 0
  store ptr @7, ptr %193, align 8
  %194 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %192, i32 0, i32 1
  store i64 6, ptr %194, align 4
  %195 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %192, align 8
  %196 = call ptr @"github.com/goplus/llgo/internal/runtime.Interface"(%"github.com/goplus/llgo/internal/runtime.String" %191, %"github.com/goplus/llgo/internal/runtime.String" %195, %"github.com/goplus/llgo/internal/runtime.Slice" %187)
  store ptr %196, ptr @_llgo_main.I, align 8
  br label %_llgo_12

_llgo_12:                                         ; preds = %_llgo_11, %_llgo_10
  ret void
}

declare ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String", i64, i64, i64, i64)

declare ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String", i64, %"github.com/goplus/llgo/internal/runtime.Slice")

declare %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String", ptr, i64, %"github.com/goplus/llgo/internal/runtime.String", i1)

declare ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64)

declare void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr, %"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.String", ptr, %"github.com/goplus/llgo/internal/runtime.Slice", %"github.com/goplus/llgo/internal/runtime.Slice")

declare ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64)

declare ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice", %"github.com/goplus/llgo/internal/runtime.Slice", i1)

declare void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr)

declare ptr @"github.com/goplus/llgo/internal/runtime.Interface"(%"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.Slice")

declare ptr @"github.com/goplus/llgo/internal/runtime.NewItab"(ptr, ptr)

declare void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface")

declare ptr @"github.com/goplus/llgo/internal/runtime.IfaceType"(%"github.com/goplus/llgo/internal/runtime.iface")

declare i1 @"github.com/goplus/llgo/internal/runtime.Implements"(ptr, ptr)

define i64 @"main.one$bound"(ptr %0) {
_llgo_0:
  %1 = load { %"github.com/goplus/llgo/internal/runtime.iface" }, ptr %0, align 8
  %2 = extractvalue { %"github.com/goplus/llgo/internal/runtime.iface" } %1, 0
  %3 = call ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface" %2)
  %4 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %2, 0
  %5 = getelementptr ptr, ptr %4, i64 3
  %6 = load ptr, ptr %5, align 8
  %7 = alloca { ptr, ptr }, align 8
  %8 = getelementptr inbounds { ptr, ptr }, ptr %7, i32 0, i32 0
  store ptr %6, ptr %8, align 8
  %9 = getelementptr inbounds { ptr, ptr }, ptr %7, i32 0, i32 1
  store ptr %3, ptr %9, align 8
  %10 = load { ptr, ptr }, ptr %7, align 8
  %11 = extractvalue { ptr, ptr } %10, 1
  %12 = extractvalue { ptr, ptr } %10, 0
  %13 = call i64 %12(ptr %11)
  ret i64 %13
}

declare i1 @"github.com/goplus/llgo/internal/runtime.StringEqual"(%"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.String")

define %"github.com/goplus/llgo/internal/runtime.String" @"main.two$bound"(ptr %0) {
_llgo_0:
  %1 = load { %"github.com/goplus/llgo/internal/runtime.iface" }, ptr %0, align 8
  %2 = extractvalue { %"github.com/goplus/llgo/internal/runtime.iface" } %1, 0
  %3 = call ptr @"github.com/goplus/llgo/internal/runtime.IfacePtrData"(%"github.com/goplus/llgo/internal/runtime.iface" %2)
  %4 = extractvalue %"github.com/goplus/llgo/internal/runtime.iface" %2, 0
  %5 = getelementptr ptr, ptr %4, i64 4
  %6 = load ptr, ptr %5, align 8
  %7 = alloca { ptr, ptr }, align 8
  %8 = getelementptr inbounds { ptr, ptr }, ptr %7, i32 0, i32 0
  store ptr %6, ptr %8, align 8
  %9 = getelementptr inbounds { ptr, ptr }, ptr %7, i32 0, i32 1
  store ptr %3, ptr %9, align 8
  %10 = load { ptr, ptr }, ptr %7, align 8
  %11 = extractvalue { ptr, ptr } %10, 1
  %12 = extractvalue { ptr, ptr } %10, 0
  %13 = call %"github.com/goplus/llgo/internal/runtime.String" %12(ptr %11)
  ret %"github.com/goplus/llgo/internal/runtime.String" %13
}

declare void @"github.com/goplus/llgo/internal/runtime.PrintString"(%"github.com/goplus/llgo/internal/runtime.String")

declare void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8)
