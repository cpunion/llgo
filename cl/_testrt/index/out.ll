; ModuleID = 'main'
source_filename = "main"

%main.point = type { i64, i64 }
%"github.com/goplus/llgo/internal/runtime.String" = type { ptr, i64 }
%"github.com/goplus/llgo/internal/runtime.Slice" = type { ptr, i64, i64 }

@"main.init$guard" = global i1 false, align 1
@__llgo_argc = global i32 0, align 4
@__llgo_argv = global ptr null, align 8
@0 = private unnamed_addr constant [6 x i8] c"123456", align 1

define void @main.init() {
_llgo_0:
  %0 = load i1, ptr @"main.init$guard", align 1
  br i1 %0, label %_llgo_2, label %_llgo_1

_llgo_1:                                          ; preds = %_llgo_0
  store i1 true, ptr @"main.init$guard", align 1
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
  %2 = alloca %main.point, align 8
  %3 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %2, i64 16)
  %4 = alloca [3 x %main.point], align 8
  %5 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %4, i64 48)
  %6 = getelementptr inbounds %main.point, ptr %5, i64 0
  %7 = getelementptr inbounds %main.point, ptr %6, i32 0, i32 0
  %8 = getelementptr inbounds %main.point, ptr %6, i32 0, i32 1
  %9 = getelementptr inbounds %main.point, ptr %5, i64 1
  %10 = getelementptr inbounds %main.point, ptr %9, i32 0, i32 0
  %11 = getelementptr inbounds %main.point, ptr %9, i32 0, i32 1
  %12 = getelementptr inbounds %main.point, ptr %5, i64 2
  %13 = getelementptr inbounds %main.point, ptr %12, i32 0, i32 0
  %14 = getelementptr inbounds %main.point, ptr %12, i32 0, i32 1
  store i64 1, ptr %7, align 4
  store i64 2, ptr %8, align 4
  store i64 3, ptr %10, align 4
  store i64 4, ptr %11, align 4
  store i64 5, ptr %13, align 4
  store i64 6, ptr %14, align 4
  %15 = load [3 x %main.point], ptr %5, align 4
  %16 = getelementptr inbounds %main.point, ptr %5, i64 2
  %17 = load %main.point, ptr %16, align 4
  store %main.point %17, ptr %3, align 4
  %18 = getelementptr inbounds %main.point, ptr %3, i32 0, i32 0
  %19 = load i64, ptr %18, align 4
  %20 = getelementptr inbounds %main.point, ptr %3, i32 0, i32 1
  %21 = load i64, ptr %20, align 4
  call void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64 %19)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 32)
  call void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64 %21)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  %22 = alloca [2 x i64], align 8
  %23 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %22, i64 16)
  %24 = alloca [2 x [2 x i64]], align 8
  %25 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %24, i64 32)
  %26 = getelementptr inbounds [2 x i64], ptr %25, i64 0
  %27 = getelementptr inbounds i64, ptr %26, i64 0
  %28 = getelementptr inbounds i64, ptr %26, i64 1
  %29 = getelementptr inbounds [2 x i64], ptr %25, i64 1
  %30 = getelementptr inbounds i64, ptr %29, i64 0
  %31 = getelementptr inbounds i64, ptr %29, i64 1
  store i64 1, ptr %27, align 4
  store i64 2, ptr %28, align 4
  store i64 3, ptr %30, align 4
  store i64 4, ptr %31, align 4
  %32 = load [2 x [2 x i64]], ptr %25, align 4
  %33 = getelementptr inbounds [2 x i64], ptr %25, i64 1
  %34 = load [2 x i64], ptr %33, align 4
  store [2 x i64] %34, ptr %23, align 4
  %35 = getelementptr inbounds i64, ptr %23, i64 0
  %36 = load i64, ptr %35, align 4
  %37 = getelementptr inbounds i64, ptr %23, i64 1
  %38 = load i64, ptr %37, align 4
  call void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64 %36)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 32)
  call void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64 %38)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  %39 = alloca [5 x i64], align 8
  %40 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %39, i64 40)
  %41 = getelementptr inbounds i64, ptr %40, i64 0
  %42 = getelementptr inbounds i64, ptr %40, i64 1
  %43 = getelementptr inbounds i64, ptr %40, i64 2
  %44 = getelementptr inbounds i64, ptr %40, i64 3
  %45 = getelementptr inbounds i64, ptr %40, i64 4
  store i64 1, ptr %41, align 4
  store i64 2, ptr %42, align 4
  store i64 3, ptr %43, align 4
  store i64 4, ptr %44, align 4
  store i64 5, ptr %45, align 4
  %46 = load [5 x i64], ptr %40, align 4
  %47 = getelementptr inbounds i64, ptr %40, i64 2
  %48 = load i64, ptr %47, align 4
  call void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64 %48)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  %49 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %50 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %49, i32 0, i32 0
  store ptr @0, ptr %50, align 8
  %51 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %49, i32 0, i32 1
  store i64 6, ptr %51, align 4
  %52 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %49, align 8
  %53 = extractvalue %"github.com/goplus/llgo/internal/runtime.String" %52, 0
  %54 = extractvalue %"github.com/goplus/llgo/internal/runtime.String" %52, 1
  %55 = icmp sge i64 2, %54
  call void @"github.com/goplus/llgo/internal/runtime.AssertIndexRange"(i1 %55)
  %56 = getelementptr inbounds i8, ptr %53, i64 2
  %57 = load i8, ptr %56, align 1
  %58 = sext i8 %57 to i32
  %59 = call %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/runtime.StringFromRune"(i32 %58)
  call void @"github.com/goplus/llgo/internal/runtime.PrintString"(%"github.com/goplus/llgo/internal/runtime.String" %59)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  %60 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %61 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %60, i32 0, i32 0
  store ptr @0, ptr %61, align 8
  %62 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %60, i32 0, i32 1
  store i64 6, ptr %62, align 4
  %63 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %60, align 8
  %64 = extractvalue %"github.com/goplus/llgo/internal/runtime.String" %63, 0
  %65 = extractvalue %"github.com/goplus/llgo/internal/runtime.String" %63, 1
  %66 = icmp sge i64 1, %65
  call void @"github.com/goplus/llgo/internal/runtime.AssertIndexRange"(i1 %66)
  %67 = getelementptr inbounds i8, ptr %64, i64 1
  %68 = load i8, ptr %67, align 1
  %69 = sext i8 %68 to i32
  %70 = call %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/runtime.StringFromRune"(i32 %69)
  call void @"github.com/goplus/llgo/internal/runtime.PrintString"(%"github.com/goplus/llgo/internal/runtime.String" %70)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  %71 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64 16)
  %72 = getelementptr inbounds i64, ptr %71, i64 0
  %73 = getelementptr inbounds i64, ptr %71, i64 1
  store i64 1, ptr %72, align 4
  store i64 2, ptr %73, align 4
  %74 = getelementptr inbounds i64, ptr %71, i64 1
  %75 = load i64, ptr %74, align 4
  call void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64 %75)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  %76 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64 32)
  %77 = getelementptr inbounds i64, ptr %76, i64 0
  store i64 1, ptr %77, align 4
  %78 = getelementptr inbounds i64, ptr %76, i64 1
  store i64 2, ptr %78, align 4
  %79 = getelementptr inbounds i64, ptr %76, i64 2
  store i64 3, ptr %79, align 4
  %80 = getelementptr inbounds i64, ptr %76, i64 3
  store i64 4, ptr %80, align 4
  %81 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %82 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %81, i32 0, i32 0
  store ptr %76, ptr %82, align 8
  %83 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %81, i32 0, i32 1
  store i64 4, ptr %83, align 4
  %84 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %81, i32 0, i32 2
  store i64 4, ptr %84, align 4
  %85 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %81, align 8
  %86 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %85, 0
  %87 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %85, 1
  %88 = icmp sge i64 1, %87
  call void @"github.com/goplus/llgo/internal/runtime.AssertIndexRange"(i1 %88)
  %89 = getelementptr inbounds i64, ptr %86, i64 1
  %90 = load i64, ptr %89, align 4
  call void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64 %90)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  call void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64 0)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  ret i32 0
}

declare void @"github.com/goplus/llgo/internal/runtime.init"()

declare ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr, i64)

declare void @"github.com/goplus/llgo/internal/runtime.PrintInt"(i64)

declare void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8)

declare void @"github.com/goplus/llgo/internal/runtime.AssertIndexRange"(i1)

declare %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/runtime.StringFromRune"(i32)

declare void @"github.com/goplus/llgo/internal/runtime.PrintString"(%"github.com/goplus/llgo/internal/runtime.String")

declare ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64)
