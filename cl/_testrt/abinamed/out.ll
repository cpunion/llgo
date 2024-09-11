; ModuleID = 'main'
source_filename = "main"

%main.T = type { ptr, ptr, i64, %"github.com/goplus/llgo/internal/runtime.Slice" }
%"github.com/goplus/llgo/internal/runtime.Slice" = type { ptr, i64, i64 }
%"github.com/goplus/llgo/internal/runtime.eface" = type { ptr, ptr }
%"github.com/goplus/llgo/internal/abi.Type" = type { i64, i64, i32, i8, i8, i8, i8, { ptr, ptr }, ptr, %"github.com/goplus/llgo/internal/runtime.String", ptr }
%"github.com/goplus/llgo/internal/runtime.String" = type { ptr, i64 }
%main.eface = type { ptr, ptr }
%"github.com/goplus/llgo/internal/abi.StructField" = type { %"github.com/goplus/llgo/internal/runtime.String", ptr, i64, %"github.com/goplus/llgo/internal/runtime.String", i1 }
%"github.com/goplus/llgo/internal/abi.StructType" = type { %"github.com/goplus/llgo/internal/abi.Type", %"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.Slice" }
%"github.com/goplus/llgo/internal/abi.Method" = type { %"github.com/goplus/llgo/internal/runtime.String", ptr, ptr, ptr }

@"main.init$guard" = global i1 false, align 1
@__llgo_argc = global i32 0, align 4
@__llgo_argv = global ptr null, align 8
@_llgo_main.T = linkonce global ptr null, align 8
@0 = private unnamed_addr constant [6 x i8] c"main.T", align 1
@"main.struct$FYfyNCnlvkYOztpQWjt-y8D_WY3tpxyt5Qo62CJffTE" = linkonce global ptr null, align 8
@1 = private unnamed_addr constant [40 x i8] c"github.com/goplus/llgo/internal/abi.Type", align 1
@2 = private unnamed_addr constant [1 x i8] c"p", align 1
@3 = private unnamed_addr constant [1 x i8] c"t", align 1
@4 = private unnamed_addr constant [1 x i8] c"n", align 1
@5 = private unnamed_addr constant [1 x i8] c"a", align 1
@6 = private unnamed_addr constant [4 x i8] c"main", align 1
@7 = private unnamed_addr constant [1 x i8] c"T", align 1
@"_llgo_github.com/goplus/llgo/internal/abi.Type" = linkonce global ptr null, align 8
@"main.struct$13P_TvKNXommvK6tKt3eRNnJqTcPEFYrHagFiHeRpb0" = linkonce global ptr null, align 8
@8 = private unnamed_addr constant [41 x i8] c"github.com/goplus/llgo/internal/abi.TFlag", align 1
@_llgo_Pointer = linkonce global ptr null, align 8
@_llgo_bool = linkonce global ptr null, align 8
@9 = private unnamed_addr constant [5 x i8] c"Size_", align 1
@10 = private unnamed_addr constant [8 x i8] c"PtrBytes", align 1
@11 = private unnamed_addr constant [4 x i8] c"Hash", align 1
@12 = private unnamed_addr constant [5 x i8] c"TFlag", align 1
@13 = private unnamed_addr constant [6 x i8] c"Align_", align 1
@14 = private unnamed_addr constant [11 x i8] c"FieldAlign_", align 1
@15 = private unnamed_addr constant [5 x i8] c"Kind_", align 1
@16 = private unnamed_addr constant [5 x i8] c"Equal", align 1
@17 = private unnamed_addr constant [1 x i8] c"f", align 1
@18 = private unnamed_addr constant [4 x i8] c"data", align 1
@19 = private unnamed_addr constant [6 x i8] c"GCData", align 1
@20 = private unnamed_addr constant [4 x i8] c"Str_", align 1
@21 = private unnamed_addr constant [10 x i8] c"PtrToThis_", align 1
@22 = private unnamed_addr constant [5 x i8] c"Align", align 1
@"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA" = linkonce global ptr null, align 8
@_llgo_int = linkonce global ptr null, align 8
@23 = private unnamed_addr constant [9 x i8] c"ArrayType", align 1
@"_llgo_func$CsVqlCxhoEcIvPD5BSBukfSiD9C7Ic5_Gf32MLbCWB4" = linkonce global ptr null, align 8
@"_llgo_github.com/goplus/llgo/internal/abi.ArrayType" = linkonce global ptr null, align 8
@24 = private unnamed_addr constant [45 x i8] c"github.com/goplus/llgo/internal/abi.ArrayType", align 1
@"_llgo_struct$eLreYy_0Tx9Ip-rgTmC6_uCvf27HVl_zBUTfLS0WYaY" = linkonce global ptr null, align 8
@25 = private unnamed_addr constant [4 x i8] c"Type", align 1
@26 = private unnamed_addr constant [4 x i8] c"Elem", align 1
@27 = private unnamed_addr constant [5 x i8] c"Slice", align 1
@28 = private unnamed_addr constant [3 x i8] c"Len", align 1
@29 = private unnamed_addr constant [6 x i8] c"Common", align 1
@"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo" = linkonce global ptr null, align 8
@"*_llgo_github.com/goplus/llgo/internal/abi.Type" = linkonce global ptr null, align 8
@30 = private unnamed_addr constant [10 x i8] c"FieldAlign", align 1
@31 = private unnamed_addr constant [8 x i8] c"FuncType", align 1
@"_llgo_func$DsoxgOnxqV7tLvokF3AA14v1gtHsHaThoC8Q_XGcQww" = linkonce global ptr null, align 8
@"_llgo_github.com/goplus/llgo/internal/abi.FuncType" = linkonce global ptr null, align 8
@32 = private unnamed_addr constant [44 x i8] c"github.com/goplus/llgo/internal/abi.FuncType", align 1
@"_llgo_struct$wRu7InfmQeSkq7akLN3soDNninnS1dQajawdYvmHbzw" = linkonce global ptr null, align 8
@33 = private unnamed_addr constant [2 x i8] c"In", align 1
@34 = private unnamed_addr constant [3 x i8] c"Out", align 1
@35 = private unnamed_addr constant [7 x i8] c"HasName", align 1
@"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk" = linkonce global ptr null, align 8
@36 = private unnamed_addr constant [10 x i8] c"IfaceIndir", align 1
@37 = private unnamed_addr constant [13 x i8] c"InterfaceType", align 1
@"_llgo_func$1QmforOaCy2fBAssC2y1FWCCT6fpq9RKwP2j2HIASY8" = linkonce global ptr null, align 8
@"_llgo_github.com/goplus/llgo/internal/abi.InterfaceType" = linkonce global ptr null, align 8
@38 = private unnamed_addr constant [49 x i8] c"github.com/goplus/llgo/internal/abi.InterfaceType", align 1
@"_llgo_struct$mWxYYevLxpL1wQyiQtAy4OszkqTlHtrmEcPpzW9Air4" = linkonce global ptr null, align 8
@39 = private unnamed_addr constant [43 x i8] c"github.com/goplus/llgo/internal/abi.Imethod", align 1
@40 = private unnamed_addr constant [8 x i8] c"PkgPath_", align 1
@41 = private unnamed_addr constant [7 x i8] c"Methods", align 1
@42 = private unnamed_addr constant [13 x i8] c"IsDirectIface", align 1
@43 = private unnamed_addr constant [4 x i8] c"Kind", align 1
@"_llgo_func$ntUE0UmVAWPS2O7GpCCGszSn-XnjHJntZZ2jYtwbFXI" = linkonce global ptr null, align 8
@"_llgo_github.com/goplus/llgo/internal/abi.Kind" = linkonce global ptr null, align 8
@44 = private unnamed_addr constant [40 x i8] c"github.com/goplus/llgo/internal/abi.Kind", align 1
@_llgo_uint = linkonce global ptr null, align 8
@45 = private unnamed_addr constant [6 x i8] c"String", align 1
@"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to" = linkonce global ptr null, align 8
@_llgo_string = linkonce global ptr null, align 8
@46 = private unnamed_addr constant [35 x i8] c"github.com/goplus/llgo/internal/abi", align 1
@47 = private unnamed_addr constant [7 x i8] c"MapType", align 1
@"_llgo_func$d-NlqnjcQnaMjsBQY7qh2SWQmHb0XIigoceXdiJ8YT4" = linkonce global ptr null, align 8
@"_llgo_github.com/goplus/llgo/internal/abi.MapType" = linkonce global ptr null, align 8
@48 = private unnamed_addr constant [43 x i8] c"github.com/goplus/llgo/internal/abi.MapType", align 1
@"main.struct$Yk42tBqeO4BzIoRAwt__cbPj2UwIDCP07Kg_SR7sBZM" = linkonce global ptr null, align 8
@_llgo_uintptr = linkonce global ptr null, align 8
@49 = private unnamed_addr constant [3 x i8] c"Key", align 1
@50 = private unnamed_addr constant [6 x i8] c"Bucket", align 1
@51 = private unnamed_addr constant [6 x i8] c"Hasher", align 1
@52 = private unnamed_addr constant [7 x i8] c"KeySize", align 1
@53 = private unnamed_addr constant [9 x i8] c"ValueSize", align 1
@54 = private unnamed_addr constant [10 x i8] c"BucketSize", align 1
@55 = private unnamed_addr constant [5 x i8] c"Flags", align 1
@56 = private unnamed_addr constant [14 x i8] c"HashMightPanic", align 1
@57 = private unnamed_addr constant [12 x i8] c"IndirectElem", align 1
@58 = private unnamed_addr constant [11 x i8] c"IndirectKey", align 1
@59 = private unnamed_addr constant [13 x i8] c"NeedKeyUpdate", align 1
@60 = private unnamed_addr constant [8 x i8] c"Pointers", align 1
@61 = private unnamed_addr constant [12 x i8] c"ReflexiveKey", align 1
@62 = private unnamed_addr constant [4 x i8] c"Size", align 1
@"_llgo_func$1kITCsyu7hFLMxHLR7kDlvu4SOra_HtrtdFUQH9P13s" = linkonce global ptr null, align 8
@63 = private unnamed_addr constant [10 x i8] c"StructType", align 1
@"_llgo_func$qiNnn6Cbm3GtDp4gDI4U_DRV3h8zlz91s9jrfOXC--U" = linkonce global ptr null, align 8
@"_llgo_github.com/goplus/llgo/internal/abi.StructType" = linkonce global ptr null, align 8
@64 = private unnamed_addr constant [46 x i8] c"github.com/goplus/llgo/internal/abi.StructType", align 1
@"_llgo_struct$K_cvuhBwc2_5r7UW089ibWfcfsGoDb4pZ7K19IcMTk0" = linkonce global ptr null, align 8
@65 = private unnamed_addr constant [47 x i8] c"github.com/goplus/llgo/internal/abi.StructField", align 1
@66 = private unnamed_addr constant [6 x i8] c"Fields", align 1
@67 = private unnamed_addr constant [8 x i8] c"Uncommon", align 1
@"_llgo_func$DbD4nZv_bjE4tH8hh-VfAjMXMpNfIsMlLJJJPKupp34" = linkonce global ptr null, align 8
@"_llgo_github.com/goplus/llgo/internal/abi.UncommonType" = linkonce global ptr null, align 8
@68 = private unnamed_addr constant [48 x i8] c"github.com/goplus/llgo/internal/abi.UncommonType", align 1
@"_llgo_struct$OKIlItfBJsawrEMnVSc2VQ7pxNxCHIgSoitcM9n4FVI" = linkonce global ptr null, align 8
@69 = private unnamed_addr constant [6 x i8] c"Mcount", align 1
@70 = private unnamed_addr constant [6 x i8] c"Xcount", align 1
@71 = private unnamed_addr constant [4 x i8] c"Moff", align 1
@72 = private unnamed_addr constant [15 x i8] c"ExportedMethods", align 1
@"_llgo_func$r0w3aCNVheLGqjxncuxitGhNtWJagb9gZLqOSrNI7dg" = linkonce global ptr null, align 8
@"[]_llgo_github.com/goplus/llgo/internal/abi.Method" = linkonce global ptr null, align 8
@73 = private unnamed_addr constant [42 x i8] c"github.com/goplus/llgo/internal/abi.Method", align 1
@74 = private unnamed_addr constant [12 x i8] c"UncommonType", align 1
@"*_llgo_github.com/goplus/llgo/internal/abi.UncommonType" = linkonce global ptr null, align 8
@"*_llgo_github.com/goplus/llgo/internal/abi.StructType" = linkonce global ptr null, align 8
@"*_llgo_github.com/goplus/llgo/internal/abi.MapType" = linkonce global ptr null, align 8
@"*_llgo_github.com/goplus/llgo/internal/abi.InterfaceType" = linkonce global ptr null, align 8
@75 = private unnamed_addr constant [8 x i8] c"Variadic", align 1
@"*_llgo_github.com/goplus/llgo/internal/abi.FuncType" = linkonce global ptr null, align 8
@"*_llgo_github.com/goplus/llgo/internal/abi.ArrayType" = linkonce global ptr null, align 8
@76 = private unnamed_addr constant [13 x i8] c"error field 0", align 1
@77 = private unnamed_addr constant [18 x i8] c"error field 0 elem", align 1
@78 = private unnamed_addr constant [13 x i8] c"error field 1", align 1
@79 = private unnamed_addr constant [18 x i8] c"error field 1 elem", align 1
@80 = private unnamed_addr constant [13 x i8] c"error field 2", align 1
@81 = private unnamed_addr constant [13 x i8] c"error field 3", align 1

define void @main.init() {
_llgo_0:
  %0 = load i1, ptr @"main.init$guard", align 1
  br i1 %0, label %_llgo_2, label %_llgo_1

_llgo_1:                                          ; preds = %_llgo_0
  store i1 true, ptr @"main.init$guard", align 1
  call void @"github.com/goplus/llgo/internal/abi.init"()
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
  %2 = load ptr, ptr @_llgo_main.T, align 8
  %3 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 48)
  store %main.T zeroinitializer, ptr %3, align 8
  %4 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %5 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %4, i32 0, i32 0
  store ptr %2, ptr %5, align 8
  %6 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %4, i32 0, i32 1
  store ptr %3, ptr %6, align 8
  %7 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %4, align 8
  %8 = call ptr @main.toEface(%"github.com/goplus/llgo/internal/runtime.eface" %7)
  %9 = load ptr, ptr @"_llgo_github.com/goplus/llgo/internal/abi.Type", align 8
  %10 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 72)
  store %"github.com/goplus/llgo/internal/abi.Type" zeroinitializer, ptr %10, align 8
  %11 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %12 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %11, i32 0, i32 0
  store ptr %9, ptr %12, align 8
  %13 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %11, i32 0, i32 1
  store ptr %10, ptr %13, align 8
  %14 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %11, align 8
  %15 = call ptr @main.toEface(%"github.com/goplus/llgo/internal/runtime.eface" %14)
  %16 = getelementptr inbounds %main.eface, ptr %8, i32 0, i32 0
  %17 = load ptr, ptr %16, align 8
  call void @"github.com/goplus/llgo/internal/runtime.PrintPointer"(ptr %17)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  %18 = getelementptr inbounds %main.eface, ptr %8, i32 0, i32 0
  %19 = load ptr, ptr %18, align 8
  %20 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Type", ptr %19, i32 0, i32 10
  %21 = load ptr, ptr %20, align 8
  call void @"github.com/goplus/llgo/internal/runtime.PrintPointer"(ptr %21)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  %22 = getelementptr inbounds %main.eface, ptr %15, i32 0, i32 0
  %23 = load ptr, ptr %22, align 8
  call void @"github.com/goplus/llgo/internal/runtime.PrintPointer"(ptr %23)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  %24 = getelementptr inbounds %main.eface, ptr %15, i32 0, i32 0
  %25 = load ptr, ptr %24, align 8
  %26 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Type", ptr %25, i32 0, i32 10
  %27 = load ptr, ptr %26, align 8
  call void @"github.com/goplus/llgo/internal/runtime.PrintPointer"(ptr %27)
  call void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8 10)
  %28 = alloca %"github.com/goplus/llgo/internal/abi.StructField", align 8
  %29 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %28, i64 56)
  %30 = getelementptr inbounds %main.eface, ptr %8, i32 0, i32 0
  %31 = load ptr, ptr %30, align 8
  %32 = call ptr @"github.com/goplus/llgo/internal/abi.(*Type).StructType"(ptr %31)
  %33 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructType", ptr %32, i32 0, i32 2
  %34 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %33, align 8
  %35 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %34, 0
  %36 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %34, 1
  %37 = icmp sge i64 0, %36
  call void @"github.com/goplus/llgo/internal/runtime.AssertIndexRange"(i1 %37)
  %38 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %35, i64 0
  %39 = load %"github.com/goplus/llgo/internal/abi.StructField", ptr %38, align 8
  store %"github.com/goplus/llgo/internal/abi.StructField" %39, ptr %29, align 8
  %40 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %29, i32 0, i32 1
  %41 = load ptr, ptr %40, align 8
  %42 = getelementptr inbounds %main.eface, ptr %8, i32 0, i32 0
  %43 = load ptr, ptr %42, align 8
  %44 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Type", ptr %43, i32 0, i32 10
  %45 = load ptr, ptr %44, align 8
  %46 = icmp ne ptr %41, %45
  br i1 %46, label %_llgo_1, label %_llgo_2

_llgo_1:                                          ; preds = %_llgo_0
  %47 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %48 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %47, i32 0, i32 0
  store ptr @76, ptr %48, align 8
  %49 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %47, i32 0, i32 1
  store i64 13, ptr %49, align 4
  %50 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %47, align 8
  %51 = load ptr, ptr @_llgo_string, align 8
  %52 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %50, ptr %52, align 8
  %53 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %54 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %53, i32 0, i32 0
  store ptr %51, ptr %54, align 8
  %55 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %53, i32 0, i32 1
  store ptr %52, ptr %55, align 8
  %56 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %53, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %56)
  unreachable

_llgo_2:                                          ; preds = %_llgo_0
  %57 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %29, i32 0, i32 1
  %58 = load ptr, ptr %57, align 8
  %59 = call ptr @"github.com/goplus/llgo/internal/abi.(*Type).Elem"(ptr %58)
  %60 = getelementptr inbounds %main.eface, ptr %8, i32 0, i32 0
  %61 = load ptr, ptr %60, align 8
  %62 = icmp ne ptr %59, %61
  br i1 %62, label %_llgo_3, label %_llgo_4

_llgo_3:                                          ; preds = %_llgo_2
  %63 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %64 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %63, i32 0, i32 0
  store ptr @77, ptr %64, align 8
  %65 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %63, i32 0, i32 1
  store i64 18, ptr %65, align 4
  %66 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %63, align 8
  %67 = load ptr, ptr @_llgo_string, align 8
  %68 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %66, ptr %68, align 8
  %69 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %70 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %69, i32 0, i32 0
  store ptr %67, ptr %70, align 8
  %71 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %69, i32 0, i32 1
  store ptr %68, ptr %71, align 8
  %72 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %69, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %72)
  unreachable

_llgo_4:                                          ; preds = %_llgo_2
  %73 = alloca %"github.com/goplus/llgo/internal/abi.StructField", align 8
  %74 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %73, i64 56)
  %75 = getelementptr inbounds %main.eface, ptr %8, i32 0, i32 0
  %76 = load ptr, ptr %75, align 8
  %77 = call ptr @"github.com/goplus/llgo/internal/abi.(*Type).StructType"(ptr %76)
  %78 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructType", ptr %77, i32 0, i32 2
  %79 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %78, align 8
  %80 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %79, 0
  %81 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %79, 1
  %82 = icmp sge i64 1, %81
  call void @"github.com/goplus/llgo/internal/runtime.AssertIndexRange"(i1 %82)
  %83 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %80, i64 1
  %84 = load %"github.com/goplus/llgo/internal/abi.StructField", ptr %83, align 8
  store %"github.com/goplus/llgo/internal/abi.StructField" %84, ptr %74, align 8
  %85 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %74, i32 0, i32 1
  %86 = load ptr, ptr %85, align 8
  %87 = getelementptr inbounds %main.eface, ptr %15, i32 0, i32 0
  %88 = load ptr, ptr %87, align 8
  %89 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Type", ptr %88, i32 0, i32 10
  %90 = load ptr, ptr %89, align 8
  %91 = icmp ne ptr %86, %90
  br i1 %91, label %_llgo_5, label %_llgo_6

_llgo_5:                                          ; preds = %_llgo_4
  %92 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %93 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %92, i32 0, i32 0
  store ptr @78, ptr %93, align 8
  %94 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %92, i32 0, i32 1
  store i64 13, ptr %94, align 4
  %95 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %92, align 8
  %96 = load ptr, ptr @_llgo_string, align 8
  %97 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %95, ptr %97, align 8
  %98 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %99 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %98, i32 0, i32 0
  store ptr %96, ptr %99, align 8
  %100 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %98, i32 0, i32 1
  store ptr %97, ptr %100, align 8
  %101 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %98, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %101)
  unreachable

_llgo_6:                                          ; preds = %_llgo_4
  %102 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %74, i32 0, i32 1
  %103 = load ptr, ptr %102, align 8
  %104 = call ptr @"github.com/goplus/llgo/internal/abi.(*Type).Elem"(ptr %103)
  %105 = getelementptr inbounds %main.eface, ptr %15, i32 0, i32 0
  %106 = load ptr, ptr %105, align 8
  %107 = icmp ne ptr %104, %106
  br i1 %107, label %_llgo_7, label %_llgo_8

_llgo_7:                                          ; preds = %_llgo_6
  %108 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %109 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %108, i32 0, i32 0
  store ptr @79, ptr %109, align 8
  %110 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %108, i32 0, i32 1
  store i64 18, ptr %110, align 4
  %111 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %108, align 8
  %112 = load ptr, ptr @_llgo_string, align 8
  %113 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %111, ptr %113, align 8
  %114 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %115 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %114, i32 0, i32 0
  store ptr %112, ptr %115, align 8
  %116 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %114, i32 0, i32 1
  store ptr %113, ptr %116, align 8
  %117 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %114, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %117)
  unreachable

_llgo_8:                                          ; preds = %_llgo_6
  %118 = alloca %"github.com/goplus/llgo/internal/abi.StructField", align 8
  %119 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %118, i64 56)
  %120 = getelementptr inbounds %main.eface, ptr %8, i32 0, i32 0
  %121 = load ptr, ptr %120, align 8
  %122 = call ptr @"github.com/goplus/llgo/internal/abi.(*Type).StructType"(ptr %121)
  %123 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructType", ptr %122, i32 0, i32 2
  %124 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %123, align 8
  %125 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %124, 0
  %126 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %124, 1
  %127 = icmp sge i64 2, %126
  call void @"github.com/goplus/llgo/internal/runtime.AssertIndexRange"(i1 %127)
  %128 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %125, i64 2
  %129 = load %"github.com/goplus/llgo/internal/abi.StructField", ptr %128, align 8
  store %"github.com/goplus/llgo/internal/abi.StructField" %129, ptr %119, align 8
  %130 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %119, i32 0, i32 1
  %131 = load ptr, ptr %130, align 8
  %132 = getelementptr inbounds %main.eface, ptr %15, i32 0, i32 0
  %133 = load ptr, ptr %132, align 8
  %134 = call ptr @"github.com/goplus/llgo/internal/abi.(*Type).StructType"(ptr %133)
  %135 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructType", ptr %134, i32 0, i32 2
  %136 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %135, align 8
  %137 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %136, 0
  %138 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %136, 1
  %139 = icmp sge i64 0, %138
  call void @"github.com/goplus/llgo/internal/runtime.AssertIndexRange"(i1 %139)
  %140 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %137, i64 0
  %141 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %140, i32 0, i32 1
  %142 = load ptr, ptr %141, align 8
  %143 = icmp ne ptr %131, %142
  br i1 %143, label %_llgo_9, label %_llgo_10

_llgo_9:                                          ; preds = %_llgo_8
  %144 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %145 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %144, i32 0, i32 0
  store ptr @80, ptr %145, align 8
  %146 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %144, i32 0, i32 1
  store i64 13, ptr %146, align 4
  %147 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %144, align 8
  %148 = load ptr, ptr @_llgo_string, align 8
  %149 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %147, ptr %149, align 8
  %150 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %151 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %150, i32 0, i32 0
  store ptr %148, ptr %151, align 8
  %152 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %150, i32 0, i32 1
  store ptr %149, ptr %152, align 8
  %153 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %150, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %153)
  unreachable

_llgo_10:                                         ; preds = %_llgo_8
  %154 = alloca %"github.com/goplus/llgo/internal/abi.StructField", align 8
  %155 = call ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr %154, i64 56)
  %156 = getelementptr inbounds %main.eface, ptr %8, i32 0, i32 0
  %157 = load ptr, ptr %156, align 8
  %158 = call ptr @"github.com/goplus/llgo/internal/abi.(*Type).StructType"(ptr %157)
  %159 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructType", ptr %158, i32 0, i32 2
  %160 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %159, align 8
  %161 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %160, 0
  %162 = extractvalue %"github.com/goplus/llgo/internal/runtime.Slice" %160, 1
  %163 = icmp sge i64 3, %162
  call void @"github.com/goplus/llgo/internal/runtime.AssertIndexRange"(i1 %163)
  %164 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %161, i64 3
  %165 = load %"github.com/goplus/llgo/internal/abi.StructField", ptr %164, align 8
  store %"github.com/goplus/llgo/internal/abi.StructField" %165, ptr %155, align 8
  %166 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.StructField", ptr %155, i32 0, i32 1
  %167 = load ptr, ptr %166, align 8
  %168 = call ptr @"github.com/goplus/llgo/internal/abi.(*Type).Elem"(ptr %167)
  %169 = getelementptr inbounds %main.eface, ptr %8, i32 0, i32 0
  %170 = load ptr, ptr %169, align 8
  %171 = icmp ne ptr %168, %170
  br i1 %171, label %_llgo_11, label %_llgo_12

_llgo_11:                                         ; preds = %_llgo_10
  %172 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %173 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %172, i32 0, i32 0
  store ptr @81, ptr %173, align 8
  %174 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %172, i32 0, i32 1
  store i64 13, ptr %174, align 4
  %175 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %172, align 8
  %176 = load ptr, ptr @_llgo_string, align 8
  %177 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.String" %175, ptr %177, align 8
  %178 = alloca %"github.com/goplus/llgo/internal/runtime.eface", align 8
  %179 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %178, i32 0, i32 0
  store ptr %176, ptr %179, align 8
  %180 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.eface", ptr %178, i32 0, i32 1
  store ptr %177, ptr %180, align 8
  %181 = load %"github.com/goplus/llgo/internal/runtime.eface", ptr %178, align 8
  call void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface" %181)
  unreachable

_llgo_12:                                         ; preds = %_llgo_10
  ret i32 0
}

define ptr @main.toEface(%"github.com/goplus/llgo/internal/runtime.eface" %0) {
_llgo_0:
  %1 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64 16)
  store %"github.com/goplus/llgo/internal/runtime.eface" %0, ptr %1, align 8
  ret ptr %1
}

declare void @"github.com/goplus/llgo/internal/abi.init"()

declare void @"github.com/goplus/llgo/internal/runtime.init"()

define void @"main.init$after"() {
_llgo_0:
  %0 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %0, i32 0, i32 0
  store ptr @0, ptr %1, align 8
  %2 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %0, i32 0, i32 1
  store i64 6, ptr %2, align 4
  %3 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %0, align 8
  %4 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %3, i64 25, i64 48, i64 0, i64 0)
  %5 = load ptr, ptr @_llgo_main.T, align 8
  %6 = icmp eq ptr %5, null
  br i1 %6, label %_llgo_1, label %_llgo_2

_llgo_1:                                          ; preds = %_llgo_0
  store ptr %4, ptr @_llgo_main.T, align 8
  br label %_llgo_2

_llgo_2:                                          ; preds = %_llgo_1, %_llgo_0
  %7 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %8 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %7, i32 0, i32 0
  store ptr @0, ptr %8, align 8
  %9 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %7, i32 0, i32 1
  store i64 6, ptr %9, align 4
  %10 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %7, align 8
  %11 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %10, i64 25, i64 48, i64 0, i64 0)
  %12 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %13 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %12, i32 0, i32 0
  store ptr @1, ptr %13, align 8
  %14 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %12, i32 0, i32 1
  store i64 40, ptr %14, align 4
  %15 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %12, align 8
  %16 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %15, i64 25, i64 80, i64 0, i64 18)
  %17 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %18 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %17, i32 0, i32 0
  store ptr @0, ptr %18, align 8
  %19 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %17, i32 0, i32 1
  store i64 6, ptr %19, align 4
  %20 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %17, align 8
  %21 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %20, i64 25, i64 48, i64 0, i64 0)
  %22 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %23 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %22, i32 0, i32 0
  store ptr @2, ptr %23, align 8
  %24 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %22, i32 0, i32 1
  store i64 1, ptr %24, align 4
  %25 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %22, align 8
  %26 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %27 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %26, i32 0, i32 0
  store ptr null, ptr %27, align 8
  %28 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %26, i32 0, i32 1
  store i64 0, ptr %28, align 4
  %29 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %26, align 8
  %30 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %11)
  %31 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %25, ptr %30, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %29, i1 false)
  %32 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %33 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %32, i32 0, i32 0
  store ptr @3, ptr %33, align 8
  %34 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %32, i32 0, i32 1
  store i64 1, ptr %34, align 4
  %35 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %32, align 8
  %36 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %37 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %36, i32 0, i32 0
  store ptr null, ptr %37, align 8
  %38 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %36, i32 0, i32 1
  store i64 0, ptr %38, align 4
  %39 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %36, align 8
  %40 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %16)
  %41 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %35, ptr %40, i64 8, %"github.com/goplus/llgo/internal/runtime.String" %39, i1 false)
  %42 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %43 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %42, i32 0, i32 0
  store ptr @4, ptr %43, align 8
  %44 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %42, i32 0, i32 1
  store i64 1, ptr %44, align 4
  %45 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %42, align 8
  %46 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %47 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %46, i32 0, i32 0
  store ptr null, ptr %47, align 8
  %48 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %46, i32 0, i32 1
  store i64 0, ptr %48, align 4
  %49 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %46, align 8
  %50 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 44)
  %51 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %45, ptr %50, i64 16, %"github.com/goplus/llgo/internal/runtime.String" %49, i1 false)
  %52 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %53 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %52, i32 0, i32 0
  store ptr @5, ptr %53, align 8
  %54 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %52, i32 0, i32 1
  store i64 1, ptr %54, align 4
  %55 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %52, align 8
  %56 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %57 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %56, i32 0, i32 0
  store ptr null, ptr %57, align 8
  %58 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %56, i32 0, i32 1
  store i64 0, ptr %58, align 4
  %59 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %56, align 8
  %60 = call ptr @"github.com/goplus/llgo/internal/runtime.SliceOf"(ptr %21)
  %61 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %55, ptr %60, i64 24, %"github.com/goplus/llgo/internal/runtime.String" %59, i1 false)
  %62 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %63 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %62, i32 0, i32 0
  store ptr @6, ptr %63, align 8
  %64 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %62, i32 0, i32 1
  store i64 4, ptr %64, align 4
  %65 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %62, align 8
  %66 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 224)
  %67 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %66, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %31, ptr %67, align 8
  %68 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %66, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %41, ptr %68, align 8
  %69 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %66, i64 2
  store %"github.com/goplus/llgo/internal/abi.StructField" %51, ptr %69, align 8
  %70 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %66, i64 3
  store %"github.com/goplus/llgo/internal/abi.StructField" %61, ptr %70, align 8
  %71 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %72 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %71, i32 0, i32 0
  store ptr %66, ptr %72, align 8
  %73 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %71, i32 0, i32 1
  store i64 4, ptr %73, align 4
  %74 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %71, i32 0, i32 2
  store i64 4, ptr %74, align 4
  %75 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %71, align 8
  %76 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %65, i64 48, %"github.com/goplus/llgo/internal/runtime.Slice" %75)
  store ptr %76, ptr @"main.struct$FYfyNCnlvkYOztpQWjt-y8D_WY3tpxyt5Qo62CJffTE", align 8
  %77 = load ptr, ptr @"main.struct$FYfyNCnlvkYOztpQWjt-y8D_WY3tpxyt5Qo62CJffTE", align 8
  br i1 %6, label %_llgo_3, label %_llgo_4

_llgo_3:                                          ; preds = %_llgo_2
  %78 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %79 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %78, i32 0, i32 0
  store ptr @6, ptr %79, align 8
  %80 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %78, i32 0, i32 1
  store i64 4, ptr %80, align 4
  %81 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %78, align 8
  %82 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %83 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %82, i32 0, i32 0
  store ptr @7, ptr %83, align 8
  %84 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %82, i32 0, i32 1
  store i64 1, ptr %84, align 4
  %85 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %82, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %4, %"github.com/goplus/llgo/internal/runtime.String" %81, %"github.com/goplus/llgo/internal/runtime.String" %85, ptr %77, { ptr, i64, i64 } zeroinitializer, { ptr, i64, i64 } zeroinitializer)
  br label %_llgo_4

_llgo_4:                                          ; preds = %_llgo_3, %_llgo_2
  %86 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %87 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %86, i32 0, i32 0
  store ptr @1, ptr %87, align 8
  %88 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %86, i32 0, i32 1
  store i64 40, ptr %88, align 4
  %89 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %86, align 8
  %90 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %89, i64 25, i64 80, i64 0, i64 18)
  %91 = load ptr, ptr @"_llgo_github.com/goplus/llgo/internal/abi.Type", align 8
  %92 = icmp eq ptr %91, null
  br i1 %92, label %_llgo_5, label %_llgo_6

_llgo_5:                                          ; preds = %_llgo_4
  store ptr %90, ptr @"_llgo_github.com/goplus/llgo/internal/abi.Type", align 8
  br label %_llgo_6

_llgo_6:                                          ; preds = %_llgo_5, %_llgo_4
  %93 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %94 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %93, i32 0, i32 0
  store ptr @8, ptr %94, align 8
  %95 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %93, i32 0, i32 1
  store i64 41, ptr %95, align 4
  %96 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %93, align 8
  %97 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %96, i64 8, i64 1, i64 0, i64 0)
  %98 = load ptr, ptr @_llgo_Pointer, align 8
  %99 = icmp eq ptr %98, null
  br i1 %99, label %_llgo_7, label %_llgo_8

_llgo_7:                                          ; preds = %_llgo_6
  %100 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 58)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %100)
  store ptr %100, ptr @_llgo_Pointer, align 8
  br label %_llgo_8

_llgo_8:                                          ; preds = %_llgo_7, %_llgo_6
  %101 = load ptr, ptr @_llgo_Pointer, align 8
  %102 = load ptr, ptr @_llgo_Pointer, align 8
  %103 = load ptr, ptr @_llgo_Pointer, align 8
  %104 = load ptr, ptr @_llgo_bool, align 8
  %105 = icmp eq ptr %104, null
  br i1 %105, label %_llgo_9, label %_llgo_10

_llgo_9:                                          ; preds = %_llgo_8
  %106 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 33)
  store ptr %106, ptr @_llgo_bool, align 8
  br label %_llgo_10

_llgo_10:                                         ; preds = %_llgo_9, %_llgo_8
  %107 = load ptr, ptr @_llgo_bool, align 8
  %108 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %109 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %108, i32 0, i32 0
  store ptr @1, ptr %109, align 8
  %110 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %108, i32 0, i32 1
  store i64 40, ptr %110, align 4
  %111 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %108, align 8
  %112 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %111, i64 25, i64 80, i64 0, i64 18)
  %113 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %114 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %113, i32 0, i32 0
  store ptr @9, ptr %114, align 8
  %115 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %113, i32 0, i32 1
  store i64 5, ptr %115, align 4
  %116 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %113, align 8
  %117 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %118 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %117, i32 0, i32 0
  store ptr null, ptr %118, align 8
  %119 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %117, i32 0, i32 1
  store i64 0, ptr %119, align 4
  %120 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %117, align 8
  %121 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 44)
  %122 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %116, ptr %121, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %120, i1 false)
  %123 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %124 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %123, i32 0, i32 0
  store ptr @10, ptr %124, align 8
  %125 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %123, i32 0, i32 1
  store i64 8, ptr %125, align 4
  %126 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %123, align 8
  %127 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %128 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %127, i32 0, i32 0
  store ptr null, ptr %128, align 8
  %129 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %127, i32 0, i32 1
  store i64 0, ptr %129, align 4
  %130 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %127, align 8
  %131 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 44)
  %132 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %126, ptr %131, i64 8, %"github.com/goplus/llgo/internal/runtime.String" %130, i1 false)
  %133 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %134 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %133, i32 0, i32 0
  store ptr @11, ptr %134, align 8
  %135 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %133, i32 0, i32 1
  store i64 4, ptr %135, align 4
  %136 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %133, align 8
  %137 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %138 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %137, i32 0, i32 0
  store ptr null, ptr %138, align 8
  %139 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %137, i32 0, i32 1
  store i64 0, ptr %139, align 4
  %140 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %137, align 8
  %141 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 42)
  %142 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %136, ptr %141, i64 16, %"github.com/goplus/llgo/internal/runtime.String" %140, i1 false)
  %143 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %144 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %143, i32 0, i32 0
  store ptr @12, ptr %144, align 8
  %145 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %143, i32 0, i32 1
  store i64 5, ptr %145, align 4
  %146 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %143, align 8
  %147 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %148 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %147, i32 0, i32 0
  store ptr null, ptr %148, align 8
  %149 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %147, i32 0, i32 1
  store i64 0, ptr %149, align 4
  %150 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %147, align 8
  %151 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %146, ptr %97, i64 20, %"github.com/goplus/llgo/internal/runtime.String" %150, i1 false)
  %152 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %153 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %152, i32 0, i32 0
  store ptr @13, ptr %153, align 8
  %154 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %152, i32 0, i32 1
  store i64 6, ptr %154, align 4
  %155 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %152, align 8
  %156 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %157 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %156, i32 0, i32 0
  store ptr null, ptr %157, align 8
  %158 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %156, i32 0, i32 1
  store i64 0, ptr %158, align 4
  %159 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %156, align 8
  %160 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 40)
  %161 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %155, ptr %160, i64 21, %"github.com/goplus/llgo/internal/runtime.String" %159, i1 false)
  %162 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %163 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %162, i32 0, i32 0
  store ptr @14, ptr %163, align 8
  %164 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %162, i32 0, i32 1
  store i64 11, ptr %164, align 4
  %165 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %162, align 8
  %166 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %167 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %166, i32 0, i32 0
  store ptr null, ptr %167, align 8
  %168 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %166, i32 0, i32 1
  store i64 0, ptr %168, align 4
  %169 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %166, align 8
  %170 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 40)
  %171 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %165, ptr %170, i64 22, %"github.com/goplus/llgo/internal/runtime.String" %169, i1 false)
  %172 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %173 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %172, i32 0, i32 0
  store ptr @15, ptr %173, align 8
  %174 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %172, i32 0, i32 1
  store i64 5, ptr %174, align 4
  %175 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %172, align 8
  %176 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %177 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %176, i32 0, i32 0
  store ptr null, ptr %177, align 8
  %178 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %176, i32 0, i32 1
  store i64 0, ptr %178, align 4
  %179 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %176, align 8
  %180 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 40)
  %181 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %175, ptr %180, i64 23, %"github.com/goplus/llgo/internal/runtime.String" %179, i1 false)
  %182 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %183 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %182, i32 0, i32 0
  store ptr @16, ptr %183, align 8
  %184 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %182, i32 0, i32 1
  store i64 5, ptr %184, align 4
  %185 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %182, align 8
  %186 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %187 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %186, i32 0, i32 0
  store ptr null, ptr %187, align 8
  %188 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %186, i32 0, i32 1
  store i64 0, ptr %188, align 4
  %189 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %186, align 8
  %190 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %191 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %190, i32 0, i32 0
  store ptr @17, ptr %191, align 8
  %192 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %190, i32 0, i32 1
  store i64 1, ptr %192, align 4
  %193 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %190, align 8
  %194 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %195 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %194, i32 0, i32 0
  store ptr null, ptr %195, align 8
  %196 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %194, i32 0, i32 1
  store i64 0, ptr %196, align 4
  %197 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %194, align 8
  %198 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 24)
  %199 = getelementptr ptr, ptr %198, i64 0
  store ptr %101, ptr %199, align 8
  %200 = getelementptr ptr, ptr %198, i64 1
  store ptr %102, ptr %200, align 8
  %201 = getelementptr ptr, ptr %198, i64 2
  store ptr %103, ptr %201, align 8
  %202 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %203 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %202, i32 0, i32 0
  store ptr %198, ptr %203, align 8
  %204 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %202, i32 0, i32 1
  store i64 3, ptr %204, align 4
  %205 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %202, i32 0, i32 2
  store i64 3, ptr %205, align 4
  %206 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %202, align 8
  %207 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %208 = getelementptr ptr, ptr %207, i64 0
  store ptr %107, ptr %208, align 8
  %209 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %210 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %209, i32 0, i32 0
  store ptr %207, ptr %210, align 8
  %211 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %209, i32 0, i32 1
  store i64 1, ptr %211, align 4
  %212 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %209, i32 0, i32 2
  store i64 1, ptr %212, align 4
  %213 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %209, align 8
  %214 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %206, %"github.com/goplus/llgo/internal/runtime.Slice" %213, i1 false)
  %215 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %193, ptr %214, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %197, i1 false)
  %216 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %217 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %216, i32 0, i32 0
  store ptr @18, ptr %217, align 8
  %218 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %216, i32 0, i32 1
  store i64 4, ptr %218, align 4
  %219 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %216, align 8
  %220 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %221 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %220, i32 0, i32 0
  store ptr null, ptr %221, align 8
  %222 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %220, i32 0, i32 1
  store i64 0, ptr %222, align 4
  %223 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %220, align 8
  %224 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 58)
  %225 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %219, ptr %224, i64 8, %"github.com/goplus/llgo/internal/runtime.String" %223, i1 false)
  %226 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %227 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %226, i32 0, i32 0
  store ptr @6, ptr %227, align 8
  %228 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %226, i32 0, i32 1
  store i64 4, ptr %228, align 4
  %229 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %226, align 8
  %230 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 112)
  %231 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %230, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %215, ptr %231, align 8
  %232 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %230, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %225, ptr %232, align 8
  %233 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %234 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %233, i32 0, i32 0
  store ptr %230, ptr %234, align 8
  %235 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %233, i32 0, i32 1
  store i64 2, ptr %235, align 4
  %236 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %233, i32 0, i32 2
  store i64 2, ptr %236, align 4
  %237 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %233, align 8
  %238 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %229, i64 16, %"github.com/goplus/llgo/internal/runtime.Slice" %237)
  %239 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %185, ptr %238, i64 24, %"github.com/goplus/llgo/internal/runtime.String" %189, i1 false)
  %240 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %241 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %240, i32 0, i32 0
  store ptr @19, ptr %241, align 8
  %242 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %240, i32 0, i32 1
  store i64 6, ptr %242, align 4
  %243 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %240, align 8
  %244 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %245 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %244, i32 0, i32 0
  store ptr null, ptr %245, align 8
  %246 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %244, i32 0, i32 1
  store i64 0, ptr %246, align 4
  %247 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %244, align 8
  %248 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 40)
  %249 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %248)
  %250 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %243, ptr %249, i64 40, %"github.com/goplus/llgo/internal/runtime.String" %247, i1 false)
  %251 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %252 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %251, i32 0, i32 0
  store ptr @20, ptr %252, align 8
  %253 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %251, i32 0, i32 1
  store i64 4, ptr %253, align 4
  %254 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %251, align 8
  %255 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %256 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %255, i32 0, i32 0
  store ptr null, ptr %256, align 8
  %257 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %255, i32 0, i32 1
  store i64 0, ptr %257, align 4
  %258 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %255, align 8
  %259 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 24)
  %260 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %254, ptr %259, i64 48, %"github.com/goplus/llgo/internal/runtime.String" %258, i1 false)
  %261 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %262 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %261, i32 0, i32 0
  store ptr @21, ptr %262, align 8
  %263 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %261, i32 0, i32 1
  store i64 10, ptr %263, align 4
  %264 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %261, align 8
  %265 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %266 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %265, i32 0, i32 0
  store ptr null, ptr %266, align 8
  %267 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %265, i32 0, i32 1
  store i64 0, ptr %267, align 4
  %268 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %265, align 8
  %269 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %112)
  %270 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %264, ptr %269, i64 64, %"github.com/goplus/llgo/internal/runtime.String" %268, i1 false)
  %271 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %272 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %271, i32 0, i32 0
  store ptr @6, ptr %272, align 8
  %273 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %271, i32 0, i32 1
  store i64 4, ptr %273, align 4
  %274 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %271, align 8
  %275 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 616)
  %276 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %122, ptr %276, align 8
  %277 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %132, ptr %277, align 8
  %278 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 2
  store %"github.com/goplus/llgo/internal/abi.StructField" %142, ptr %278, align 8
  %279 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 3
  store %"github.com/goplus/llgo/internal/abi.StructField" %151, ptr %279, align 8
  %280 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 4
  store %"github.com/goplus/llgo/internal/abi.StructField" %161, ptr %280, align 8
  %281 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 5
  store %"github.com/goplus/llgo/internal/abi.StructField" %171, ptr %281, align 8
  %282 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 6
  store %"github.com/goplus/llgo/internal/abi.StructField" %181, ptr %282, align 8
  %283 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 7
  store %"github.com/goplus/llgo/internal/abi.StructField" %239, ptr %283, align 8
  %284 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 8
  store %"github.com/goplus/llgo/internal/abi.StructField" %250, ptr %284, align 8
  %285 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 9
  store %"github.com/goplus/llgo/internal/abi.StructField" %260, ptr %285, align 8
  %286 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %275, i64 10
  store %"github.com/goplus/llgo/internal/abi.StructField" %270, ptr %286, align 8
  %287 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %288 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %287, i32 0, i32 0
  store ptr %275, ptr %288, align 8
  %289 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %287, i32 0, i32 1
  store i64 11, ptr %289, align 4
  %290 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %287, i32 0, i32 2
  store i64 11, ptr %290, align 4
  %291 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %287, align 8
  %292 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %274, i64 72, %"github.com/goplus/llgo/internal/runtime.Slice" %291)
  store ptr %292, ptr @"main.struct$13P_TvKNXommvK6tKt3eRNnJqTcPEFYrHagFiHeRpb0", align 8
  %293 = load ptr, ptr @"main.struct$13P_TvKNXommvK6tKt3eRNnJqTcPEFYrHagFiHeRpb0", align 8
  br i1 %92, label %_llgo_11, label %_llgo_12

_llgo_11:                                         ; preds = %_llgo_10
  %294 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %295 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %294, i32 0, i32 0
  store ptr @22, ptr %295, align 8
  %296 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %294, i32 0, i32 1
  store i64 5, ptr %296, align 4
  %297 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %294, align 8
  %298 = load ptr, ptr @_llgo_int, align 8
  %299 = icmp eq ptr %298, null
  br i1 %299, label %_llgo_13, label %_llgo_14

_llgo_12:                                         ; preds = %_llgo_100, %_llgo_10
  ret void

_llgo_13:                                         ; preds = %_llgo_11
  %300 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 34)
  store ptr %300, ptr @_llgo_int, align 8
  br label %_llgo_14

_llgo_14:                                         ; preds = %_llgo_13, %_llgo_11
  %301 = load ptr, ptr @_llgo_int, align 8
  %302 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %303 = icmp eq ptr %302, null
  br i1 %303, label %_llgo_15, label %_llgo_16

_llgo_15:                                         ; preds = %_llgo_14
  %304 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %305 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %306 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %305, i32 0, i32 0
  store ptr %304, ptr %306, align 8
  %307 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %305, i32 0, i32 1
  store i64 0, ptr %307, align 4
  %308 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %305, i32 0, i32 2
  store i64 0, ptr %308, align 4
  %309 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %305, align 8
  %310 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %311 = getelementptr ptr, ptr %310, i64 0
  store ptr %301, ptr %311, align 8
  %312 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %313 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %312, i32 0, i32 0
  store ptr %310, ptr %313, align 8
  %314 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %312, i32 0, i32 1
  store i64 1, ptr %314, align 4
  %315 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %312, i32 0, i32 2
  store i64 1, ptr %315, align 4
  %316 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %312, align 8
  %317 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %309, %"github.com/goplus/llgo/internal/runtime.Slice" %316, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %317)
  store ptr %317, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  br label %_llgo_16

_llgo_16:                                         ; preds = %_llgo_15, %_llgo_14
  %318 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %319 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %320 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %319, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %297, ptr %320, align 8
  %321 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %319, i32 0, i32 1
  store ptr %318, ptr %321, align 8
  %322 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %319, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Align", ptr %322, align 8
  %323 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %319, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Align", ptr %323, align 8
  %324 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %319, align 8
  %325 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %326 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %325, i32 0, i32 0
  store ptr @23, ptr %326, align 8
  %327 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %325, i32 0, i32 1
  store i64 9, ptr %327, align 4
  %328 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %325, align 8
  %329 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %330 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %329, i32 0, i32 0
  store ptr @24, ptr %330, align 8
  %331 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %329, i32 0, i32 1
  store i64 45, ptr %331, align 4
  %332 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %329, align 8
  %333 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %332, i64 25, i64 104, i64 0, i64 16)
  %334 = load ptr, ptr @"_llgo_github.com/goplus/llgo/internal/abi.ArrayType", align 8
  %335 = icmp eq ptr %334, null
  br i1 %335, label %_llgo_17, label %_llgo_18

_llgo_17:                                         ; preds = %_llgo_16
  store ptr %333, ptr @"_llgo_github.com/goplus/llgo/internal/abi.ArrayType", align 8
  br label %_llgo_18

_llgo_18:                                         ; preds = %_llgo_17, %_llgo_16
  %336 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %337 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %336, i32 0, i32 0
  store ptr @1, ptr %337, align 8
  %338 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %336, i32 0, i32 1
  store i64 40, ptr %338, align 4
  %339 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %336, align 8
  %340 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %339, i64 25, i64 80, i64 0, i64 18)
  %341 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %342 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %341, i32 0, i32 0
  store ptr @1, ptr %342, align 8
  %343 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %341, i32 0, i32 1
  store i64 40, ptr %343, align 4
  %344 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %341, align 8
  %345 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %344, i64 25, i64 80, i64 0, i64 18)
  %346 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %347 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %346, i32 0, i32 0
  store ptr @1, ptr %347, align 8
  %348 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %346, i32 0, i32 1
  store i64 40, ptr %348, align 4
  %349 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %346, align 8
  %350 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %349, i64 25, i64 80, i64 0, i64 18)
  %351 = load ptr, ptr @"_llgo_struct$eLreYy_0Tx9Ip-rgTmC6_uCvf27HVl_zBUTfLS0WYaY", align 8
  %352 = icmp eq ptr %351, null
  br i1 %352, label %_llgo_19, label %_llgo_20

_llgo_19:                                         ; preds = %_llgo_18
  %353 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %354 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %353, i32 0, i32 0
  store ptr @25, ptr %354, align 8
  %355 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %353, i32 0, i32 1
  store i64 4, ptr %355, align 4
  %356 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %353, align 8
  %357 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %358 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %357, i32 0, i32 0
  store ptr null, ptr %358, align 8
  %359 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %357, i32 0, i32 1
  store i64 0, ptr %359, align 4
  %360 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %357, align 8
  %361 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %356, ptr %340, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %360, i1 true)
  %362 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %363 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %362, i32 0, i32 0
  store ptr @26, ptr %363, align 8
  %364 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %362, i32 0, i32 1
  store i64 4, ptr %364, align 4
  %365 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %362, align 8
  %366 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %367 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %366, i32 0, i32 0
  store ptr null, ptr %367, align 8
  %368 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %366, i32 0, i32 1
  store i64 0, ptr %368, align 4
  %369 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %366, align 8
  %370 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %345)
  %371 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %365, ptr %370, i64 72, %"github.com/goplus/llgo/internal/runtime.String" %369, i1 false)
  %372 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %373 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %372, i32 0, i32 0
  store ptr @27, ptr %373, align 8
  %374 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %372, i32 0, i32 1
  store i64 5, ptr %374, align 4
  %375 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %372, align 8
  %376 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %377 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %376, i32 0, i32 0
  store ptr null, ptr %377, align 8
  %378 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %376, i32 0, i32 1
  store i64 0, ptr %378, align 4
  %379 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %376, align 8
  %380 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %350)
  %381 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %375, ptr %380, i64 80, %"github.com/goplus/llgo/internal/runtime.String" %379, i1 false)
  %382 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %383 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %382, i32 0, i32 0
  store ptr @28, ptr %383, align 8
  %384 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %382, i32 0, i32 1
  store i64 3, ptr %384, align 4
  %385 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %382, align 8
  %386 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %387 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %386, i32 0, i32 0
  store ptr null, ptr %387, align 8
  %388 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %386, i32 0, i32 1
  store i64 0, ptr %388, align 4
  %389 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %386, align 8
  %390 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 44)
  %391 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %385, ptr %390, i64 88, %"github.com/goplus/llgo/internal/runtime.String" %389, i1 false)
  %392 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %393 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %392, i32 0, i32 0
  store ptr @6, ptr %393, align 8
  %394 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %392, i32 0, i32 1
  store i64 4, ptr %394, align 4
  %395 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %392, align 8
  %396 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 224)
  %397 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %396, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %361, ptr %397, align 8
  %398 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %396, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %371, ptr %398, align 8
  %399 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %396, i64 2
  store %"github.com/goplus/llgo/internal/abi.StructField" %381, ptr %399, align 8
  %400 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %396, i64 3
  store %"github.com/goplus/llgo/internal/abi.StructField" %391, ptr %400, align 8
  %401 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %402 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %401, i32 0, i32 0
  store ptr %396, ptr %402, align 8
  %403 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %401, i32 0, i32 1
  store i64 4, ptr %403, align 4
  %404 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %401, i32 0, i32 2
  store i64 4, ptr %404, align 4
  %405 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %401, align 8
  %406 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %395, i64 96, %"github.com/goplus/llgo/internal/runtime.Slice" %405)
  store ptr %406, ptr @"_llgo_struct$eLreYy_0Tx9Ip-rgTmC6_uCvf27HVl_zBUTfLS0WYaY", align 8
  br label %_llgo_20

_llgo_20:                                         ; preds = %_llgo_19, %_llgo_18
  %407 = load ptr, ptr @"_llgo_struct$eLreYy_0Tx9Ip-rgTmC6_uCvf27HVl_zBUTfLS0WYaY", align 8
  br i1 %335, label %_llgo_21, label %_llgo_22

_llgo_21:                                         ; preds = %_llgo_20
  %408 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %409 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %408, i32 0, i32 0
  store ptr @22, ptr %409, align 8
  %410 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %408, i32 0, i32 1
  store i64 5, ptr %410, align 4
  %411 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %408, align 8
  %412 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %413 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %414 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %413, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %411, ptr %414, align 8
  %415 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %413, i32 0, i32 1
  store ptr %412, ptr %415, align 8
  %416 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %413, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Align", ptr %416, align 8
  %417 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %413, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Align", ptr %417, align 8
  %418 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %413, align 8
  %419 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %420 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %419, i32 0, i32 0
  store ptr @23, ptr %420, align 8
  %421 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %419, i32 0, i32 1
  store i64 9, ptr %421, align 4
  %422 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %419, align 8
  %423 = load ptr, ptr @"_llgo_func$CsVqlCxhoEcIvPD5BSBukfSiD9C7Ic5_Gf32MLbCWB4", align 8
  %424 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %425 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %424, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %422, ptr %425, align 8
  %426 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %424, i32 0, i32 1
  store ptr %423, ptr %426, align 8
  %427 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %424, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).ArrayType", ptr %427, align 8
  %428 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %424, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).ArrayType", ptr %428, align 8
  %429 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %424, align 8
  %430 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %431 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %430, i32 0, i32 0
  store ptr @29, ptr %431, align 8
  %432 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %430, i32 0, i32 1
  store i64 6, ptr %432, align 4
  %433 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %430, align 8
  %434 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %435 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %434, i32 0, i32 0
  store ptr @1, ptr %435, align 8
  %436 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %434, i32 0, i32 1
  store i64 40, ptr %436, align 4
  %437 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %434, align 8
  %438 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %437, i64 25, i64 80, i64 0, i64 18)
  %439 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.Type", align 8
  %440 = icmp eq ptr %439, null
  br i1 %440, label %_llgo_23, label %_llgo_24

_llgo_22:                                         ; preds = %_llgo_96, %_llgo_20
  %441 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %442 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %441, i32 0, i32 0
  store ptr @24, ptr %442, align 8
  %443 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %441, i32 0, i32 1
  store i64 45, ptr %443, align 4
  %444 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %441, align 8
  %445 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %444, i64 25, i64 104, i64 0, i64 16)
  %446 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.ArrayType", align 8
  %447 = icmp eq ptr %446, null
  br i1 %447, label %_llgo_97, label %_llgo_98

_llgo_23:                                         ; preds = %_llgo_21
  %448 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %438)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %448)
  store ptr %448, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.Type", align 8
  br label %_llgo_24

_llgo_24:                                         ; preds = %_llgo_23, %_llgo_21
  %449 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.Type", align 8
  %450 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %451 = icmp eq ptr %450, null
  br i1 %451, label %_llgo_25, label %_llgo_26

_llgo_25:                                         ; preds = %_llgo_24
  %452 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %453 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %454 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %453, i32 0, i32 0
  store ptr %452, ptr %454, align 8
  %455 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %453, i32 0, i32 1
  store i64 0, ptr %455, align 4
  %456 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %453, i32 0, i32 2
  store i64 0, ptr %456, align 4
  %457 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %453, align 8
  %458 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %459 = getelementptr ptr, ptr %458, i64 0
  store ptr %449, ptr %459, align 8
  %460 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %461 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %460, i32 0, i32 0
  store ptr %458, ptr %461, align 8
  %462 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %460, i32 0, i32 1
  store i64 1, ptr %462, align 4
  %463 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %460, i32 0, i32 2
  store i64 1, ptr %463, align 4
  %464 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %460, align 8
  %465 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %457, %"github.com/goplus/llgo/internal/runtime.Slice" %464, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %465)
  store ptr %465, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  br label %_llgo_26

_llgo_26:                                         ; preds = %_llgo_25, %_llgo_24
  %466 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %467 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %468 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %467, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %433, ptr %468, align 8
  %469 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %467, i32 0, i32 1
  store ptr %466, ptr %469, align 8
  %470 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %467, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Common", ptr %470, align 8
  %471 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %467, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Common", ptr %471, align 8
  %472 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %467, align 8
  %473 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %474 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %473, i32 0, i32 0
  store ptr @30, ptr %474, align 8
  %475 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %473, i32 0, i32 1
  store i64 10, ptr %475, align 4
  %476 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %473, align 8
  %477 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %478 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %479 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %478, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %476, ptr %479, align 8
  %480 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %478, i32 0, i32 1
  store ptr %477, ptr %480, align 8
  %481 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %478, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).FieldAlign", ptr %481, align 8
  %482 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %478, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).FieldAlign", ptr %482, align 8
  %483 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %478, align 8
  %484 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %485 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %484, i32 0, i32 0
  store ptr @31, ptr %485, align 8
  %486 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %484, i32 0, i32 1
  store i64 8, ptr %486, align 4
  %487 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %484, align 8
  %488 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %489 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %488, i32 0, i32 0
  store ptr @32, ptr %489, align 8
  %490 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %488, i32 0, i32 1
  store i64 44, ptr %490, align 4
  %491 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %488, align 8
  %492 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %491, i64 25, i64 128, i64 0, i64 19)
  %493 = load ptr, ptr @"_llgo_github.com/goplus/llgo/internal/abi.FuncType", align 8
  %494 = icmp eq ptr %493, null
  br i1 %494, label %_llgo_27, label %_llgo_28

_llgo_27:                                         ; preds = %_llgo_26
  store ptr %492, ptr @"_llgo_github.com/goplus/llgo/internal/abi.FuncType", align 8
  br label %_llgo_28

_llgo_28:                                         ; preds = %_llgo_27, %_llgo_26
  %495 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %496 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %495, i32 0, i32 0
  store ptr @1, ptr %496, align 8
  %497 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %495, i32 0, i32 1
  store i64 40, ptr %497, align 4
  %498 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %495, align 8
  %499 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %498, i64 25, i64 80, i64 0, i64 18)
  %500 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %501 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %500, i32 0, i32 0
  store ptr @1, ptr %501, align 8
  %502 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %500, i32 0, i32 1
  store i64 40, ptr %502, align 4
  %503 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %500, align 8
  %504 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %503, i64 25, i64 80, i64 0, i64 18)
  %505 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %506 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %505, i32 0, i32 0
  store ptr @1, ptr %506, align 8
  %507 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %505, i32 0, i32 1
  store i64 40, ptr %507, align 4
  %508 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %505, align 8
  %509 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %508, i64 25, i64 80, i64 0, i64 18)
  %510 = load ptr, ptr @"_llgo_struct$wRu7InfmQeSkq7akLN3soDNninnS1dQajawdYvmHbzw", align 8
  %511 = icmp eq ptr %510, null
  br i1 %511, label %_llgo_29, label %_llgo_30

_llgo_29:                                         ; preds = %_llgo_28
  %512 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %513 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %512, i32 0, i32 0
  store ptr @25, ptr %513, align 8
  %514 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %512, i32 0, i32 1
  store i64 4, ptr %514, align 4
  %515 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %512, align 8
  %516 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %517 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %516, i32 0, i32 0
  store ptr null, ptr %517, align 8
  %518 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %516, i32 0, i32 1
  store i64 0, ptr %518, align 4
  %519 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %516, align 8
  %520 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %515, ptr %499, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %519, i1 true)
  %521 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %522 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %521, i32 0, i32 0
  store ptr @33, ptr %522, align 8
  %523 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %521, i32 0, i32 1
  store i64 2, ptr %523, align 4
  %524 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %521, align 8
  %525 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %526 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %525, i32 0, i32 0
  store ptr null, ptr %526, align 8
  %527 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %525, i32 0, i32 1
  store i64 0, ptr %527, align 4
  %528 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %525, align 8
  %529 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %504)
  %530 = call ptr @"github.com/goplus/llgo/internal/runtime.SliceOf"(ptr %529)
  %531 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %524, ptr %530, i64 72, %"github.com/goplus/llgo/internal/runtime.String" %528, i1 false)
  %532 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %533 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %532, i32 0, i32 0
  store ptr @34, ptr %533, align 8
  %534 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %532, i32 0, i32 1
  store i64 3, ptr %534, align 4
  %535 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %532, align 8
  %536 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %537 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %536, i32 0, i32 0
  store ptr null, ptr %537, align 8
  %538 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %536, i32 0, i32 1
  store i64 0, ptr %538, align 4
  %539 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %536, align 8
  %540 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %509)
  %541 = call ptr @"github.com/goplus/llgo/internal/runtime.SliceOf"(ptr %540)
  %542 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %535, ptr %541, i64 96, %"github.com/goplus/llgo/internal/runtime.String" %539, i1 false)
  %543 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %544 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %543, i32 0, i32 0
  store ptr @6, ptr %544, align 8
  %545 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %543, i32 0, i32 1
  store i64 4, ptr %545, align 4
  %546 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %543, align 8
  %547 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 168)
  %548 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %547, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %520, ptr %548, align 8
  %549 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %547, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %531, ptr %549, align 8
  %550 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %547, i64 2
  store %"github.com/goplus/llgo/internal/abi.StructField" %542, ptr %550, align 8
  %551 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %552 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %551, i32 0, i32 0
  store ptr %547, ptr %552, align 8
  %553 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %551, i32 0, i32 1
  store i64 3, ptr %553, align 4
  %554 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %551, i32 0, i32 2
  store i64 3, ptr %554, align 4
  %555 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %551, align 8
  %556 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %546, i64 120, %"github.com/goplus/llgo/internal/runtime.Slice" %555)
  store ptr %556, ptr @"_llgo_struct$wRu7InfmQeSkq7akLN3soDNninnS1dQajawdYvmHbzw", align 8
  br label %_llgo_30

_llgo_30:                                         ; preds = %_llgo_29, %_llgo_28
  %557 = load ptr, ptr @"_llgo_struct$wRu7InfmQeSkq7akLN3soDNninnS1dQajawdYvmHbzw", align 8
  br i1 %494, label %_llgo_31, label %_llgo_32

_llgo_31:                                         ; preds = %_llgo_30
  %558 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %559 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %558, i32 0, i32 0
  store ptr @22, ptr %559, align 8
  %560 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %558, i32 0, i32 1
  store i64 5, ptr %560, align 4
  %561 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %558, align 8
  %562 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %563 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %564 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %563, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %561, ptr %564, align 8
  %565 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %563, i32 0, i32 1
  store ptr %562, ptr %565, align 8
  %566 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %563, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Align", ptr %566, align 8
  %567 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %563, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Align", ptr %567, align 8
  %568 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %563, align 8
  %569 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %570 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %569, i32 0, i32 0
  store ptr @23, ptr %570, align 8
  %571 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %569, i32 0, i32 1
  store i64 9, ptr %571, align 4
  %572 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %569, align 8
  %573 = load ptr, ptr @"_llgo_func$CsVqlCxhoEcIvPD5BSBukfSiD9C7Ic5_Gf32MLbCWB4", align 8
  %574 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %575 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %574, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %572, ptr %575, align 8
  %576 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %574, i32 0, i32 1
  store ptr %573, ptr %576, align 8
  %577 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %574, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).ArrayType", ptr %577, align 8
  %578 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %574, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).ArrayType", ptr %578, align 8
  %579 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %574, align 8
  %580 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %581 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %580, i32 0, i32 0
  store ptr @29, ptr %581, align 8
  %582 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %580, i32 0, i32 1
  store i64 6, ptr %582, align 4
  %583 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %580, align 8
  %584 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %585 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %586 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %585, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %583, ptr %586, align 8
  %587 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %585, i32 0, i32 1
  store ptr %584, ptr %587, align 8
  %588 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %585, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Common", ptr %588, align 8
  %589 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %585, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Common", ptr %589, align 8
  %590 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %585, align 8
  %591 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %592 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %591, i32 0, i32 0
  store ptr @26, ptr %592, align 8
  %593 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %591, i32 0, i32 1
  store i64 4, ptr %593, align 4
  %594 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %591, align 8
  %595 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %596 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %597 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %596, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %594, ptr %597, align 8
  %598 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %596, i32 0, i32 1
  store ptr %595, ptr %598, align 8
  %599 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %596, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Elem", ptr %599, align 8
  %600 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %596, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Elem", ptr %600, align 8
  %601 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %596, align 8
  %602 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %603 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %602, i32 0, i32 0
  store ptr @30, ptr %603, align 8
  %604 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %602, i32 0, i32 1
  store i64 10, ptr %604, align 4
  %605 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %602, align 8
  %606 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %607 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %608 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %607, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %605, ptr %608, align 8
  %609 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %607, i32 0, i32 1
  store ptr %606, ptr %609, align 8
  %610 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %607, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).FieldAlign", ptr %610, align 8
  %611 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %607, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).FieldAlign", ptr %611, align 8
  %612 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %607, align 8
  %613 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %614 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %613, i32 0, i32 0
  store ptr @31, ptr %614, align 8
  %615 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %613, i32 0, i32 1
  store i64 8, ptr %615, align 4
  %616 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %613, align 8
  %617 = load ptr, ptr @"_llgo_func$DsoxgOnxqV7tLvokF3AA14v1gtHsHaThoC8Q_XGcQww", align 8
  %618 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %619 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %618, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %616, ptr %619, align 8
  %620 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %618, i32 0, i32 1
  store ptr %617, ptr %620, align 8
  %621 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %618, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).FuncType", ptr %621, align 8
  %622 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %618, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).FuncType", ptr %622, align 8
  %623 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %618, align 8
  %624 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %625 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %624, i32 0, i32 0
  store ptr @35, ptr %625, align 8
  %626 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %624, i32 0, i32 1
  store i64 7, ptr %626, align 4
  %627 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %624, align 8
  %628 = load ptr, ptr @_llgo_bool, align 8
  %629 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %630 = icmp eq ptr %629, null
  br i1 %630, label %_llgo_33, label %_llgo_34

_llgo_32:                                         ; preds = %_llgo_92, %_llgo_30
  %631 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %632 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %631, i32 0, i32 0
  store ptr @32, ptr %632, align 8
  %633 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %631, i32 0, i32 1
  store i64 44, ptr %633, align 4
  %634 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %631, align 8
  %635 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %634, i64 25, i64 128, i64 0, i64 19)
  %636 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.FuncType", align 8
  %637 = icmp eq ptr %636, null
  br i1 %637, label %_llgo_93, label %_llgo_94

_llgo_33:                                         ; preds = %_llgo_31
  %638 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %639 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %640 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %639, i32 0, i32 0
  store ptr %638, ptr %640, align 8
  %641 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %639, i32 0, i32 1
  store i64 0, ptr %641, align 4
  %642 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %639, i32 0, i32 2
  store i64 0, ptr %642, align 4
  %643 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %639, align 8
  %644 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %645 = getelementptr ptr, ptr %644, i64 0
  store ptr %628, ptr %645, align 8
  %646 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %647 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %646, i32 0, i32 0
  store ptr %644, ptr %647, align 8
  %648 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %646, i32 0, i32 1
  store i64 1, ptr %648, align 4
  %649 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %646, i32 0, i32 2
  store i64 1, ptr %649, align 4
  %650 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %646, align 8
  %651 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %643, %"github.com/goplus/llgo/internal/runtime.Slice" %650, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %651)
  store ptr %651, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  br label %_llgo_34

_llgo_34:                                         ; preds = %_llgo_33, %_llgo_31
  %652 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %653 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %654 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %653, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %627, ptr %654, align 8
  %655 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %653, i32 0, i32 1
  store ptr %652, ptr %655, align 8
  %656 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %653, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).HasName", ptr %656, align 8
  %657 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %653, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).HasName", ptr %657, align 8
  %658 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %653, align 8
  %659 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %660 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %659, i32 0, i32 0
  store ptr @36, ptr %660, align 8
  %661 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %659, i32 0, i32 1
  store i64 10, ptr %661, align 4
  %662 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %659, align 8
  %663 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %664 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %665 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %664, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %662, ptr %665, align 8
  %666 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %664, i32 0, i32 1
  store ptr %663, ptr %666, align 8
  %667 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %664, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).IfaceIndir", ptr %667, align 8
  %668 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %664, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).IfaceIndir", ptr %668, align 8
  %669 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %664, align 8
  %670 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %671 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %670, i32 0, i32 0
  store ptr @37, ptr %671, align 8
  %672 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %670, i32 0, i32 1
  store i64 13, ptr %672, align 4
  %673 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %670, align 8
  %674 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %675 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %674, i32 0, i32 0
  store ptr @38, ptr %675, align 8
  %676 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %674, i32 0, i32 1
  store i64 49, ptr %676, align 4
  %677 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %674, align 8
  %678 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %677, i64 25, i64 120, i64 0, i64 18)
  %679 = load ptr, ptr @"_llgo_github.com/goplus/llgo/internal/abi.InterfaceType", align 8
  %680 = icmp eq ptr %679, null
  br i1 %680, label %_llgo_35, label %_llgo_36

_llgo_35:                                         ; preds = %_llgo_34
  store ptr %678, ptr @"_llgo_github.com/goplus/llgo/internal/abi.InterfaceType", align 8
  br label %_llgo_36

_llgo_36:                                         ; preds = %_llgo_35, %_llgo_34
  %681 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %682 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %681, i32 0, i32 0
  store ptr @1, ptr %682, align 8
  %683 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %681, i32 0, i32 1
  store i64 40, ptr %683, align 4
  %684 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %681, align 8
  %685 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %684, i64 25, i64 80, i64 0, i64 18)
  %686 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %687 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %686, i32 0, i32 0
  store ptr @39, ptr %687, align 8
  %688 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %686, i32 0, i32 1
  store i64 43, ptr %688, align 4
  %689 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %686, align 8
  %690 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %689, i64 25, i64 24, i64 0, i64 3)
  %691 = load ptr, ptr @"_llgo_struct$mWxYYevLxpL1wQyiQtAy4OszkqTlHtrmEcPpzW9Air4", align 8
  %692 = icmp eq ptr %691, null
  br i1 %692, label %_llgo_37, label %_llgo_38

_llgo_37:                                         ; preds = %_llgo_36
  %693 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %694 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %693, i32 0, i32 0
  store ptr @25, ptr %694, align 8
  %695 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %693, i32 0, i32 1
  store i64 4, ptr %695, align 4
  %696 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %693, align 8
  %697 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %698 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %697, i32 0, i32 0
  store ptr null, ptr %698, align 8
  %699 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %697, i32 0, i32 1
  store i64 0, ptr %699, align 4
  %700 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %697, align 8
  %701 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %696, ptr %685, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %700, i1 true)
  %702 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %703 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %702, i32 0, i32 0
  store ptr @40, ptr %703, align 8
  %704 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %702, i32 0, i32 1
  store i64 8, ptr %704, align 4
  %705 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %702, align 8
  %706 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %707 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %706, i32 0, i32 0
  store ptr null, ptr %707, align 8
  %708 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %706, i32 0, i32 1
  store i64 0, ptr %708, align 4
  %709 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %706, align 8
  %710 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 24)
  %711 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %705, ptr %710, i64 72, %"github.com/goplus/llgo/internal/runtime.String" %709, i1 false)
  %712 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %713 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %712, i32 0, i32 0
  store ptr @41, ptr %713, align 8
  %714 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %712, i32 0, i32 1
  store i64 7, ptr %714, align 4
  %715 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %712, align 8
  %716 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %717 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %716, i32 0, i32 0
  store ptr null, ptr %717, align 8
  %718 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %716, i32 0, i32 1
  store i64 0, ptr %718, align 4
  %719 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %716, align 8
  %720 = call ptr @"github.com/goplus/llgo/internal/runtime.SliceOf"(ptr %690)
  %721 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %715, ptr %720, i64 88, %"github.com/goplus/llgo/internal/runtime.String" %719, i1 false)
  %722 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %723 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %722, i32 0, i32 0
  store ptr @6, ptr %723, align 8
  %724 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %722, i32 0, i32 1
  store i64 4, ptr %724, align 4
  %725 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %722, align 8
  %726 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 168)
  %727 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %726, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %701, ptr %727, align 8
  %728 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %726, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %711, ptr %728, align 8
  %729 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %726, i64 2
  store %"github.com/goplus/llgo/internal/abi.StructField" %721, ptr %729, align 8
  %730 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %731 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %730, i32 0, i32 0
  store ptr %726, ptr %731, align 8
  %732 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %730, i32 0, i32 1
  store i64 3, ptr %732, align 4
  %733 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %730, i32 0, i32 2
  store i64 3, ptr %733, align 4
  %734 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %730, align 8
  %735 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %725, i64 112, %"github.com/goplus/llgo/internal/runtime.Slice" %734)
  store ptr %735, ptr @"_llgo_struct$mWxYYevLxpL1wQyiQtAy4OszkqTlHtrmEcPpzW9Air4", align 8
  br label %_llgo_38

_llgo_38:                                         ; preds = %_llgo_37, %_llgo_36
  %736 = load ptr, ptr @"_llgo_struct$mWxYYevLxpL1wQyiQtAy4OszkqTlHtrmEcPpzW9Air4", align 8
  br i1 %680, label %_llgo_39, label %_llgo_40

_llgo_39:                                         ; preds = %_llgo_38
  %737 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %738 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %737, i32 0, i32 0
  store ptr @22, ptr %738, align 8
  %739 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %737, i32 0, i32 1
  store i64 5, ptr %739, align 4
  %740 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %737, align 8
  %741 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %742 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %743 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %742, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %740, ptr %743, align 8
  %744 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %742, i32 0, i32 1
  store ptr %741, ptr %744, align 8
  %745 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %742, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Align", ptr %745, align 8
  %746 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %742, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Align", ptr %746, align 8
  %747 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %742, align 8
  %748 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %749 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %748, i32 0, i32 0
  store ptr @23, ptr %749, align 8
  %750 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %748, i32 0, i32 1
  store i64 9, ptr %750, align 4
  %751 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %748, align 8
  %752 = load ptr, ptr @"_llgo_func$CsVqlCxhoEcIvPD5BSBukfSiD9C7Ic5_Gf32MLbCWB4", align 8
  %753 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %754 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %753, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %751, ptr %754, align 8
  %755 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %753, i32 0, i32 1
  store ptr %752, ptr %755, align 8
  %756 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %753, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).ArrayType", ptr %756, align 8
  %757 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %753, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).ArrayType", ptr %757, align 8
  %758 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %753, align 8
  %759 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %760 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %759, i32 0, i32 0
  store ptr @29, ptr %760, align 8
  %761 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %759, i32 0, i32 1
  store i64 6, ptr %761, align 4
  %762 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %759, align 8
  %763 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %764 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %765 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %764, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %762, ptr %765, align 8
  %766 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %764, i32 0, i32 1
  store ptr %763, ptr %766, align 8
  %767 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %764, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Common", ptr %767, align 8
  %768 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %764, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Common", ptr %768, align 8
  %769 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %764, align 8
  %770 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %771 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %770, i32 0, i32 0
  store ptr @26, ptr %771, align 8
  %772 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %770, i32 0, i32 1
  store i64 4, ptr %772, align 4
  %773 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %770, align 8
  %774 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %775 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %776 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %775, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %773, ptr %776, align 8
  %777 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %775, i32 0, i32 1
  store ptr %774, ptr %777, align 8
  %778 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %775, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Elem", ptr %778, align 8
  %779 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %775, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Elem", ptr %779, align 8
  %780 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %775, align 8
  %781 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %782 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %781, i32 0, i32 0
  store ptr @30, ptr %782, align 8
  %783 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %781, i32 0, i32 1
  store i64 10, ptr %783, align 4
  %784 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %781, align 8
  %785 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %786 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %787 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %786, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %784, ptr %787, align 8
  %788 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %786, i32 0, i32 1
  store ptr %785, ptr %788, align 8
  %789 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %786, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).FieldAlign", ptr %789, align 8
  %790 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %786, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).FieldAlign", ptr %790, align 8
  %791 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %786, align 8
  %792 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %793 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %792, i32 0, i32 0
  store ptr @31, ptr %793, align 8
  %794 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %792, i32 0, i32 1
  store i64 8, ptr %794, align 4
  %795 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %792, align 8
  %796 = load ptr, ptr @"_llgo_func$DsoxgOnxqV7tLvokF3AA14v1gtHsHaThoC8Q_XGcQww", align 8
  %797 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %798 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %797, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %795, ptr %798, align 8
  %799 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %797, i32 0, i32 1
  store ptr %796, ptr %799, align 8
  %800 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %797, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).FuncType", ptr %800, align 8
  %801 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %797, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).FuncType", ptr %801, align 8
  %802 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %797, align 8
  %803 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %804 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %803, i32 0, i32 0
  store ptr @35, ptr %804, align 8
  %805 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %803, i32 0, i32 1
  store i64 7, ptr %805, align 4
  %806 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %803, align 8
  %807 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %808 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %809 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %808, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %806, ptr %809, align 8
  %810 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %808, i32 0, i32 1
  store ptr %807, ptr %810, align 8
  %811 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %808, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).HasName", ptr %811, align 8
  %812 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %808, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).HasName", ptr %812, align 8
  %813 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %808, align 8
  %814 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %815 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %814, i32 0, i32 0
  store ptr @36, ptr %815, align 8
  %816 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %814, i32 0, i32 1
  store i64 10, ptr %816, align 4
  %817 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %814, align 8
  %818 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %819 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %820 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %819, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %817, ptr %820, align 8
  %821 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %819, i32 0, i32 1
  store ptr %818, ptr %821, align 8
  %822 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %819, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).IfaceIndir", ptr %822, align 8
  %823 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %819, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).IfaceIndir", ptr %823, align 8
  %824 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %819, align 8
  %825 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %826 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %825, i32 0, i32 0
  store ptr @37, ptr %826, align 8
  %827 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %825, i32 0, i32 1
  store i64 13, ptr %827, align 4
  %828 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %825, align 8
  %829 = load ptr, ptr @"_llgo_func$1QmforOaCy2fBAssC2y1FWCCT6fpq9RKwP2j2HIASY8", align 8
  %830 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %831 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %830, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %828, ptr %831, align 8
  %832 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %830, i32 0, i32 1
  store ptr %829, ptr %832, align 8
  %833 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %830, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).InterfaceType", ptr %833, align 8
  %834 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %830, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).InterfaceType", ptr %834, align 8
  %835 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %830, align 8
  %836 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %837 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %836, i32 0, i32 0
  store ptr @42, ptr %837, align 8
  %838 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %836, i32 0, i32 1
  store i64 13, ptr %838, align 4
  %839 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %836, align 8
  %840 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %841 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %842 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %841, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %839, ptr %842, align 8
  %843 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %841, i32 0, i32 1
  store ptr %840, ptr %843, align 8
  %844 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %841, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).IsDirectIface", ptr %844, align 8
  %845 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %841, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).IsDirectIface", ptr %845, align 8
  %846 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %841, align 8
  %847 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %848 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %847, i32 0, i32 0
  store ptr @43, ptr %848, align 8
  %849 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %847, i32 0, i32 1
  store i64 4, ptr %849, align 4
  %850 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %847, align 8
  %851 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %852 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %851, i32 0, i32 0
  store ptr @44, ptr %852, align 8
  %853 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %851, i32 0, i32 1
  store i64 40, ptr %853, align 4
  %854 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %851, align 8
  %855 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %854, i64 7, i64 8, i64 1, i64 1)
  %856 = load ptr, ptr @"_llgo_github.com/goplus/llgo/internal/abi.Kind", align 8
  %857 = icmp eq ptr %856, null
  br i1 %857, label %_llgo_41, label %_llgo_42

_llgo_40:                                         ; preds = %_llgo_88, %_llgo_38
  %858 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %859 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %858, i32 0, i32 0
  store ptr @38, ptr %859, align 8
  %860 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %858, i32 0, i32 1
  store i64 49, ptr %860, align 4
  %861 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %858, align 8
  %862 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %861, i64 25, i64 120, i64 0, i64 18)
  %863 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.InterfaceType", align 8
  %864 = icmp eq ptr %863, null
  br i1 %864, label %_llgo_89, label %_llgo_90

_llgo_41:                                         ; preds = %_llgo_39
  store ptr %855, ptr @"_llgo_github.com/goplus/llgo/internal/abi.Kind", align 8
  br label %_llgo_42

_llgo_42:                                         ; preds = %_llgo_41, %_llgo_39
  %865 = load ptr, ptr @_llgo_uint, align 8
  %866 = icmp eq ptr %865, null
  br i1 %866, label %_llgo_43, label %_llgo_44

_llgo_43:                                         ; preds = %_llgo_42
  %867 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 39)
  store ptr %867, ptr @_llgo_uint, align 8
  br label %_llgo_44

_llgo_44:                                         ; preds = %_llgo_43, %_llgo_42
  %868 = load ptr, ptr @_llgo_uint, align 8
  br i1 %857, label %_llgo_45, label %_llgo_46

_llgo_45:                                         ; preds = %_llgo_44
  %869 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %870 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %869, i32 0, i32 0
  store ptr @45, ptr %870, align 8
  %871 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %869, i32 0, i32 1
  store i64 6, ptr %871, align 4
  %872 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %869, align 8
  %873 = load ptr, ptr @_llgo_string, align 8
  %874 = icmp eq ptr %873, null
  br i1 %874, label %_llgo_47, label %_llgo_48

_llgo_46:                                         ; preds = %_llgo_50, %_llgo_44
  %875 = load ptr, ptr @"_llgo_github.com/goplus/llgo/internal/abi.Kind", align 8
  %876 = load ptr, ptr @"_llgo_func$ntUE0UmVAWPS2O7GpCCGszSn-XnjHJntZZ2jYtwbFXI", align 8
  %877 = icmp eq ptr %876, null
  br i1 %877, label %_llgo_51, label %_llgo_52

_llgo_47:                                         ; preds = %_llgo_45
  %878 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 24)
  store ptr %878, ptr @_llgo_string, align 8
  br label %_llgo_48

_llgo_48:                                         ; preds = %_llgo_47, %_llgo_45
  %879 = load ptr, ptr @_llgo_string, align 8
  %880 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %881 = icmp eq ptr %880, null
  br i1 %881, label %_llgo_49, label %_llgo_50

_llgo_49:                                         ; preds = %_llgo_48
  %882 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %883 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %884 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %883, i32 0, i32 0
  store ptr %882, ptr %884, align 8
  %885 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %883, i32 0, i32 1
  store i64 0, ptr %885, align 4
  %886 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %883, i32 0, i32 2
  store i64 0, ptr %886, align 4
  %887 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %883, align 8
  %888 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %889 = getelementptr ptr, ptr %888, i64 0
  store ptr %879, ptr %889, align 8
  %890 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %891 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %890, i32 0, i32 0
  store ptr %888, ptr %891, align 8
  %892 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %890, i32 0, i32 1
  store i64 1, ptr %892, align 4
  %893 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %890, i32 0, i32 2
  store i64 1, ptr %893, align 4
  %894 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %890, align 8
  %895 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %887, %"github.com/goplus/llgo/internal/runtime.Slice" %894, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %895)
  store ptr %895, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  br label %_llgo_50

_llgo_50:                                         ; preds = %_llgo_49, %_llgo_48
  %896 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %897 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %898 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %897, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %872, ptr %898, align 8
  %899 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %897, i32 0, i32 1
  store ptr %896, ptr %899, align 8
  %900 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %897, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Kind).String", ptr %900, align 8
  %901 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %897, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Kind).String", ptr %901, align 8
  %902 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %897, align 8
  %903 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %904 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %903, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %872, ptr %904, align 8
  %905 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %903, i32 0, i32 1
  store ptr %896, ptr %905, align 8
  %906 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %903, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Kind).String", ptr %906, align 8
  %907 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %903, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.Kind.String", ptr %907, align 8
  %908 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %903, align 8
  %909 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 40)
  %910 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %909, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %908, ptr %910, align 8
  %911 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %912 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %911, i32 0, i32 0
  store ptr %909, ptr %912, align 8
  %913 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %911, i32 0, i32 1
  store i64 1, ptr %913, align 4
  %914 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %911, i32 0, i32 2
  store i64 1, ptr %914, align 4
  %915 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %911, align 8
  %916 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 40)
  %917 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %916, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %902, ptr %917, align 8
  %918 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %919 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %918, i32 0, i32 0
  store ptr %916, ptr %919, align 8
  %920 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %918, i32 0, i32 1
  store i64 1, ptr %920, align 4
  %921 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %918, i32 0, i32 2
  store i64 1, ptr %921, align 4
  %922 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %918, align 8
  %923 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %924 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %923, i32 0, i32 0
  store ptr @46, ptr %924, align 8
  %925 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %923, i32 0, i32 1
  store i64 35, ptr %925, align 4
  %926 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %923, align 8
  %927 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %928 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %927, i32 0, i32 0
  store ptr @43, ptr %928, align 8
  %929 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %927, i32 0, i32 1
  store i64 4, ptr %929, align 4
  %930 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %927, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %855, %"github.com/goplus/llgo/internal/runtime.String" %926, %"github.com/goplus/llgo/internal/runtime.String" %930, ptr %868, %"github.com/goplus/llgo/internal/runtime.Slice" %915, %"github.com/goplus/llgo/internal/runtime.Slice" %922)
  br label %_llgo_46

_llgo_51:                                         ; preds = %_llgo_46
  %931 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %932 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %933 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %932, i32 0, i32 0
  store ptr %931, ptr %933, align 8
  %934 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %932, i32 0, i32 1
  store i64 0, ptr %934, align 4
  %935 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %932, i32 0, i32 2
  store i64 0, ptr %935, align 4
  %936 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %932, align 8
  %937 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %938 = getelementptr ptr, ptr %937, i64 0
  store ptr %875, ptr %938, align 8
  %939 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %940 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %939, i32 0, i32 0
  store ptr %937, ptr %940, align 8
  %941 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %939, i32 0, i32 1
  store i64 1, ptr %941, align 4
  %942 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %939, i32 0, i32 2
  store i64 1, ptr %942, align 4
  %943 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %939, align 8
  %944 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %936, %"github.com/goplus/llgo/internal/runtime.Slice" %943, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %944)
  store ptr %944, ptr @"_llgo_func$ntUE0UmVAWPS2O7GpCCGszSn-XnjHJntZZ2jYtwbFXI", align 8
  br label %_llgo_52

_llgo_52:                                         ; preds = %_llgo_51, %_llgo_46
  %945 = load ptr, ptr @"_llgo_func$ntUE0UmVAWPS2O7GpCCGszSn-XnjHJntZZ2jYtwbFXI", align 8
  %946 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %947 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %946, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %850, ptr %947, align 8
  %948 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %946, i32 0, i32 1
  store ptr %945, ptr %948, align 8
  %949 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %946, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Kind", ptr %949, align 8
  %950 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %946, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Kind", ptr %950, align 8
  %951 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %946, align 8
  %952 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %953 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %952, i32 0, i32 0
  store ptr @28, ptr %953, align 8
  %954 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %952, i32 0, i32 1
  store i64 3, ptr %954, align 4
  %955 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %952, align 8
  %956 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %957 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %958 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %957, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %955, ptr %958, align 8
  %959 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %957, i32 0, i32 1
  store ptr %956, ptr %959, align 8
  %960 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %957, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Len", ptr %960, align 8
  %961 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %957, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Len", ptr %961, align 8
  %962 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %957, align 8
  %963 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %964 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %963, i32 0, i32 0
  store ptr @47, ptr %964, align 8
  %965 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %963, i32 0, i32 1
  store i64 7, ptr %965, align 4
  %966 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %963, align 8
  %967 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %968 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %967, i32 0, i32 0
  store ptr @48, ptr %968, align 8
  %969 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %967, i32 0, i32 1
  store i64 43, ptr %969, align 4
  %970 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %967, align 8
  %971 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %970, i64 25, i64 136, i64 0, i64 22)
  %972 = load ptr, ptr @"_llgo_github.com/goplus/llgo/internal/abi.MapType", align 8
  %973 = icmp eq ptr %972, null
  br i1 %973, label %_llgo_53, label %_llgo_54

_llgo_53:                                         ; preds = %_llgo_52
  store ptr %971, ptr @"_llgo_github.com/goplus/llgo/internal/abi.MapType", align 8
  br label %_llgo_54

_llgo_54:                                         ; preds = %_llgo_53, %_llgo_52
  %974 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %975 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %974, i32 0, i32 0
  store ptr @1, ptr %975, align 8
  %976 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %974, i32 0, i32 1
  store i64 40, ptr %976, align 4
  %977 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %974, align 8
  %978 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %977, i64 25, i64 80, i64 0, i64 18)
  %979 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %980 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %979, i32 0, i32 0
  store ptr @1, ptr %980, align 8
  %981 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %979, i32 0, i32 1
  store i64 40, ptr %981, align 4
  %982 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %979, align 8
  %983 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %982, i64 25, i64 80, i64 0, i64 18)
  %984 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %985 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %984, i32 0, i32 0
  store ptr @1, ptr %985, align 8
  %986 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %984, i32 0, i32 1
  store i64 40, ptr %986, align 4
  %987 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %984, align 8
  %988 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %987, i64 25, i64 80, i64 0, i64 18)
  %989 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %990 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %989, i32 0, i32 0
  store ptr @1, ptr %990, align 8
  %991 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %989, i32 0, i32 1
  store i64 40, ptr %991, align 4
  %992 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %989, align 8
  %993 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %992, i64 25, i64 80, i64 0, i64 18)
  %994 = load ptr, ptr @_llgo_Pointer, align 8
  %995 = load ptr, ptr @_llgo_Pointer, align 8
  %996 = load ptr, ptr @_llgo_uintptr, align 8
  %997 = icmp eq ptr %996, null
  br i1 %997, label %_llgo_55, label %_llgo_56

_llgo_55:                                         ; preds = %_llgo_54
  %998 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 44)
  store ptr %998, ptr @_llgo_uintptr, align 8
  br label %_llgo_56

_llgo_56:                                         ; preds = %_llgo_55, %_llgo_54
  %999 = load ptr, ptr @_llgo_uintptr, align 8
  %1000 = load ptr, ptr @_llgo_uintptr, align 8
  %1001 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1002 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1001, i32 0, i32 0
  store ptr @25, ptr %1002, align 8
  %1003 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1001, i32 0, i32 1
  store i64 4, ptr %1003, align 4
  %1004 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1001, align 8
  %1005 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1006 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1005, i32 0, i32 0
  store ptr null, ptr %1006, align 8
  %1007 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1005, i32 0, i32 1
  store i64 0, ptr %1007, align 4
  %1008 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1005, align 8
  %1009 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1004, ptr %978, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %1008, i1 true)
  %1010 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1011 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1010, i32 0, i32 0
  store ptr @49, ptr %1011, align 8
  %1012 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1010, i32 0, i32 1
  store i64 3, ptr %1012, align 4
  %1013 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1010, align 8
  %1014 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1015 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1014, i32 0, i32 0
  store ptr null, ptr %1015, align 8
  %1016 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1014, i32 0, i32 1
  store i64 0, ptr %1016, align 4
  %1017 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1014, align 8
  %1018 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %983)
  %1019 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1013, ptr %1018, i64 72, %"github.com/goplus/llgo/internal/runtime.String" %1017, i1 false)
  %1020 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1021 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1020, i32 0, i32 0
  store ptr @26, ptr %1021, align 8
  %1022 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1020, i32 0, i32 1
  store i64 4, ptr %1022, align 4
  %1023 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1020, align 8
  %1024 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1025 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1024, i32 0, i32 0
  store ptr null, ptr %1025, align 8
  %1026 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1024, i32 0, i32 1
  store i64 0, ptr %1026, align 4
  %1027 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1024, align 8
  %1028 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %988)
  %1029 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1023, ptr %1028, i64 80, %"github.com/goplus/llgo/internal/runtime.String" %1027, i1 false)
  %1030 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1031 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1030, i32 0, i32 0
  store ptr @50, ptr %1031, align 8
  %1032 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1030, i32 0, i32 1
  store i64 6, ptr %1032, align 4
  %1033 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1030, align 8
  %1034 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1035 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1034, i32 0, i32 0
  store ptr null, ptr %1035, align 8
  %1036 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1034, i32 0, i32 1
  store i64 0, ptr %1036, align 4
  %1037 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1034, align 8
  %1038 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %993)
  %1039 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1033, ptr %1038, i64 88, %"github.com/goplus/llgo/internal/runtime.String" %1037, i1 false)
  %1040 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1041 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1040, i32 0, i32 0
  store ptr @51, ptr %1041, align 8
  %1042 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1040, i32 0, i32 1
  store i64 6, ptr %1042, align 4
  %1043 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1040, align 8
  %1044 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1045 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1044, i32 0, i32 0
  store ptr null, ptr %1045, align 8
  %1046 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1044, i32 0, i32 1
  store i64 0, ptr %1046, align 4
  %1047 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1044, align 8
  %1048 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1049 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1048, i32 0, i32 0
  store ptr @17, ptr %1049, align 8
  %1050 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1048, i32 0, i32 1
  store i64 1, ptr %1050, align 4
  %1051 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1048, align 8
  %1052 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1053 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1052, i32 0, i32 0
  store ptr null, ptr %1053, align 8
  %1054 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1052, i32 0, i32 1
  store i64 0, ptr %1054, align 4
  %1055 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1052, align 8
  %1056 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 24)
  %1057 = getelementptr ptr, ptr %1056, i64 0
  store ptr %994, ptr %1057, align 8
  %1058 = getelementptr ptr, ptr %1056, i64 1
  store ptr %995, ptr %1058, align 8
  %1059 = getelementptr ptr, ptr %1056, i64 2
  store ptr %999, ptr %1059, align 8
  %1060 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1061 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1060, i32 0, i32 0
  store ptr %1056, ptr %1061, align 8
  %1062 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1060, i32 0, i32 1
  store i64 3, ptr %1062, align 4
  %1063 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1060, i32 0, i32 2
  store i64 3, ptr %1063, align 4
  %1064 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1060, align 8
  %1065 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %1066 = getelementptr ptr, ptr %1065, i64 0
  store ptr %1000, ptr %1066, align 8
  %1067 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1068 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1067, i32 0, i32 0
  store ptr %1065, ptr %1068, align 8
  %1069 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1067, i32 0, i32 1
  store i64 1, ptr %1069, align 4
  %1070 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1067, i32 0, i32 2
  store i64 1, ptr %1070, align 4
  %1071 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1067, align 8
  %1072 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %1064, %"github.com/goplus/llgo/internal/runtime.Slice" %1071, i1 false)
  %1073 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1051, ptr %1072, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %1055, i1 false)
  %1074 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1075 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1074, i32 0, i32 0
  store ptr @18, ptr %1075, align 8
  %1076 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1074, i32 0, i32 1
  store i64 4, ptr %1076, align 4
  %1077 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1074, align 8
  %1078 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1079 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1078, i32 0, i32 0
  store ptr null, ptr %1079, align 8
  %1080 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1078, i32 0, i32 1
  store i64 0, ptr %1080, align 4
  %1081 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1078, align 8
  %1082 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 58)
  %1083 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1077, ptr %1082, i64 8, %"github.com/goplus/llgo/internal/runtime.String" %1081, i1 false)
  %1084 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1085 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1084, i32 0, i32 0
  store ptr @6, ptr %1085, align 8
  %1086 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1084, i32 0, i32 1
  store i64 4, ptr %1086, align 4
  %1087 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1084, align 8
  %1088 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 112)
  %1089 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1088, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %1073, ptr %1089, align 8
  %1090 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1088, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %1083, ptr %1090, align 8
  %1091 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1092 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1091, i32 0, i32 0
  store ptr %1088, ptr %1092, align 8
  %1093 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1091, i32 0, i32 1
  store i64 2, ptr %1093, align 4
  %1094 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1091, i32 0, i32 2
  store i64 2, ptr %1094, align 4
  %1095 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1091, align 8
  %1096 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %1087, i64 16, %"github.com/goplus/llgo/internal/runtime.Slice" %1095)
  %1097 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1043, ptr %1096, i64 96, %"github.com/goplus/llgo/internal/runtime.String" %1047, i1 false)
  %1098 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1099 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1098, i32 0, i32 0
  store ptr @52, ptr %1099, align 8
  %1100 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1098, i32 0, i32 1
  store i64 7, ptr %1100, align 4
  %1101 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1098, align 8
  %1102 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1103 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1102, i32 0, i32 0
  store ptr null, ptr %1103, align 8
  %1104 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1102, i32 0, i32 1
  store i64 0, ptr %1104, align 4
  %1105 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1102, align 8
  %1106 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 40)
  %1107 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1101, ptr %1106, i64 112, %"github.com/goplus/llgo/internal/runtime.String" %1105, i1 false)
  %1108 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1109 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1108, i32 0, i32 0
  store ptr @53, ptr %1109, align 8
  %1110 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1108, i32 0, i32 1
  store i64 9, ptr %1110, align 4
  %1111 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1108, align 8
  %1112 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1113 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1112, i32 0, i32 0
  store ptr null, ptr %1113, align 8
  %1114 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1112, i32 0, i32 1
  store i64 0, ptr %1114, align 4
  %1115 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1112, align 8
  %1116 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 40)
  %1117 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1111, ptr %1116, i64 113, %"github.com/goplus/llgo/internal/runtime.String" %1115, i1 false)
  %1118 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1119 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1118, i32 0, i32 0
  store ptr @54, ptr %1119, align 8
  %1120 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1118, i32 0, i32 1
  store i64 10, ptr %1120, align 4
  %1121 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1118, align 8
  %1122 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1123 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1122, i32 0, i32 0
  store ptr null, ptr %1123, align 8
  %1124 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1122, i32 0, i32 1
  store i64 0, ptr %1124, align 4
  %1125 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1122, align 8
  %1126 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 41)
  %1127 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1121, ptr %1126, i64 114, %"github.com/goplus/llgo/internal/runtime.String" %1125, i1 false)
  %1128 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1129 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1128, i32 0, i32 0
  store ptr @55, ptr %1129, align 8
  %1130 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1128, i32 0, i32 1
  store i64 5, ptr %1130, align 4
  %1131 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1128, align 8
  %1132 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1133 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1132, i32 0, i32 0
  store ptr null, ptr %1133, align 8
  %1134 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1132, i32 0, i32 1
  store i64 0, ptr %1134, align 4
  %1135 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1132, align 8
  %1136 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 42)
  %1137 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1131, ptr %1136, i64 116, %"github.com/goplus/llgo/internal/runtime.String" %1135, i1 false)
  %1138 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1139 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1138, i32 0, i32 0
  store ptr @6, ptr %1139, align 8
  %1140 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1138, i32 0, i32 1
  store i64 4, ptr %1140, align 4
  %1141 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1138, align 8
  %1142 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 504)
  %1143 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1142, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %1009, ptr %1143, align 8
  %1144 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1142, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %1019, ptr %1144, align 8
  %1145 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1142, i64 2
  store %"github.com/goplus/llgo/internal/abi.StructField" %1029, ptr %1145, align 8
  %1146 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1142, i64 3
  store %"github.com/goplus/llgo/internal/abi.StructField" %1039, ptr %1146, align 8
  %1147 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1142, i64 4
  store %"github.com/goplus/llgo/internal/abi.StructField" %1097, ptr %1147, align 8
  %1148 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1142, i64 5
  store %"github.com/goplus/llgo/internal/abi.StructField" %1107, ptr %1148, align 8
  %1149 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1142, i64 6
  store %"github.com/goplus/llgo/internal/abi.StructField" %1117, ptr %1149, align 8
  %1150 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1142, i64 7
  store %"github.com/goplus/llgo/internal/abi.StructField" %1127, ptr %1150, align 8
  %1151 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1142, i64 8
  store %"github.com/goplus/llgo/internal/abi.StructField" %1137, ptr %1151, align 8
  %1152 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1153 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1152, i32 0, i32 0
  store ptr %1142, ptr %1153, align 8
  %1154 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1152, i32 0, i32 1
  store i64 9, ptr %1154, align 4
  %1155 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1152, i32 0, i32 2
  store i64 9, ptr %1155, align 4
  %1156 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1152, align 8
  %1157 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %1141, i64 120, %"github.com/goplus/llgo/internal/runtime.Slice" %1156)
  store ptr %1157, ptr @"main.struct$Yk42tBqeO4BzIoRAwt__cbPj2UwIDCP07Kg_SR7sBZM", align 8
  %1158 = load ptr, ptr @"main.struct$Yk42tBqeO4BzIoRAwt__cbPj2UwIDCP07Kg_SR7sBZM", align 8
  br i1 %973, label %_llgo_57, label %_llgo_58

_llgo_57:                                         ; preds = %_llgo_56
  %1159 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1160 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1159, i32 0, i32 0
  store ptr @22, ptr %1160, align 8
  %1161 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1159, i32 0, i32 1
  store i64 5, ptr %1161, align 4
  %1162 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1159, align 8
  %1163 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %1164 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1165 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1164, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1162, ptr %1165, align 8
  %1166 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1164, i32 0, i32 1
  store ptr %1163, ptr %1166, align 8
  %1167 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1164, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Align", ptr %1167, align 8
  %1168 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1164, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Align", ptr %1168, align 8
  %1169 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1164, align 8
  %1170 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1171 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1170, i32 0, i32 0
  store ptr @23, ptr %1171, align 8
  %1172 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1170, i32 0, i32 1
  store i64 9, ptr %1172, align 4
  %1173 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1170, align 8
  %1174 = load ptr, ptr @"_llgo_func$CsVqlCxhoEcIvPD5BSBukfSiD9C7Ic5_Gf32MLbCWB4", align 8
  %1175 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1176 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1175, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1173, ptr %1176, align 8
  %1177 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1175, i32 0, i32 1
  store ptr %1174, ptr %1177, align 8
  %1178 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1175, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).ArrayType", ptr %1178, align 8
  %1179 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1175, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).ArrayType", ptr %1179, align 8
  %1180 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1175, align 8
  %1181 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1182 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1181, i32 0, i32 0
  store ptr @29, ptr %1182, align 8
  %1183 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1181, i32 0, i32 1
  store i64 6, ptr %1183, align 4
  %1184 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1181, align 8
  %1185 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %1186 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1187 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1186, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1184, ptr %1187, align 8
  %1188 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1186, i32 0, i32 1
  store ptr %1185, ptr %1188, align 8
  %1189 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1186, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Common", ptr %1189, align 8
  %1190 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1186, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Common", ptr %1190, align 8
  %1191 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1186, align 8
  %1192 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1193 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1192, i32 0, i32 0
  store ptr @30, ptr %1193, align 8
  %1194 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1192, i32 0, i32 1
  store i64 10, ptr %1194, align 4
  %1195 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1192, align 8
  %1196 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %1197 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1198 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1197, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1195, ptr %1198, align 8
  %1199 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1197, i32 0, i32 1
  store ptr %1196, ptr %1199, align 8
  %1200 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1197, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).FieldAlign", ptr %1200, align 8
  %1201 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1197, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).FieldAlign", ptr %1201, align 8
  %1202 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1197, align 8
  %1203 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1204 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1203, i32 0, i32 0
  store ptr @31, ptr %1204, align 8
  %1205 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1203, i32 0, i32 1
  store i64 8, ptr %1205, align 4
  %1206 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1203, align 8
  %1207 = load ptr, ptr @"_llgo_func$DsoxgOnxqV7tLvokF3AA14v1gtHsHaThoC8Q_XGcQww", align 8
  %1208 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1209 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1208, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1206, ptr %1209, align 8
  %1210 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1208, i32 0, i32 1
  store ptr %1207, ptr %1210, align 8
  %1211 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1208, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).FuncType", ptr %1211, align 8
  %1212 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1208, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).FuncType", ptr %1212, align 8
  %1213 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1208, align 8
  %1214 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1215 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1214, i32 0, i32 0
  store ptr @35, ptr %1215, align 8
  %1216 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1214, i32 0, i32 1
  store i64 7, ptr %1216, align 4
  %1217 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1214, align 8
  %1218 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1219 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1220 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1219, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1217, ptr %1220, align 8
  %1221 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1219, i32 0, i32 1
  store ptr %1218, ptr %1221, align 8
  %1222 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1219, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).HasName", ptr %1222, align 8
  %1223 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1219, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).HasName", ptr %1223, align 8
  %1224 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1219, align 8
  %1225 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1226 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1225, i32 0, i32 0
  store ptr @56, ptr %1226, align 8
  %1227 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1225, i32 0, i32 1
  store i64 14, ptr %1227, align 4
  %1228 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1225, align 8
  %1229 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1230 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1231 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1230, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1228, ptr %1231, align 8
  %1232 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1230, i32 0, i32 1
  store ptr %1229, ptr %1232, align 8
  %1233 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1230, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).HashMightPanic", ptr %1233, align 8
  %1234 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1230, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).HashMightPanic", ptr %1234, align 8
  %1235 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1230, align 8
  %1236 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1237 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1236, i32 0, i32 0
  store ptr @36, ptr %1237, align 8
  %1238 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1236, i32 0, i32 1
  store i64 10, ptr %1238, align 4
  %1239 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1236, align 8
  %1240 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1241 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1242 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1241, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1239, ptr %1242, align 8
  %1243 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1241, i32 0, i32 1
  store ptr %1240, ptr %1243, align 8
  %1244 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1241, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).IfaceIndir", ptr %1244, align 8
  %1245 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1241, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).IfaceIndir", ptr %1245, align 8
  %1246 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1241, align 8
  %1247 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1248 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1247, i32 0, i32 0
  store ptr @57, ptr %1248, align 8
  %1249 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1247, i32 0, i32 1
  store i64 12, ptr %1249, align 4
  %1250 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1247, align 8
  %1251 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1252 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1253 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1252, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1250, ptr %1253, align 8
  %1254 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1252, i32 0, i32 1
  store ptr %1251, ptr %1254, align 8
  %1255 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1252, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).IndirectElem", ptr %1255, align 8
  %1256 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1252, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).IndirectElem", ptr %1256, align 8
  %1257 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1252, align 8
  %1258 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1259 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1258, i32 0, i32 0
  store ptr @58, ptr %1259, align 8
  %1260 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1258, i32 0, i32 1
  store i64 11, ptr %1260, align 4
  %1261 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1258, align 8
  %1262 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1263 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1264 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1263, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1261, ptr %1264, align 8
  %1265 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1263, i32 0, i32 1
  store ptr %1262, ptr %1265, align 8
  %1266 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1263, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).IndirectKey", ptr %1266, align 8
  %1267 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1263, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).IndirectKey", ptr %1267, align 8
  %1268 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1263, align 8
  %1269 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1270 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1269, i32 0, i32 0
  store ptr @37, ptr %1270, align 8
  %1271 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1269, i32 0, i32 1
  store i64 13, ptr %1271, align 4
  %1272 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1269, align 8
  %1273 = load ptr, ptr @"_llgo_func$1QmforOaCy2fBAssC2y1FWCCT6fpq9RKwP2j2HIASY8", align 8
  %1274 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1275 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1274, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1272, ptr %1275, align 8
  %1276 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1274, i32 0, i32 1
  store ptr %1273, ptr %1276, align 8
  %1277 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1274, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).InterfaceType", ptr %1277, align 8
  %1278 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1274, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).InterfaceType", ptr %1278, align 8
  %1279 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1274, align 8
  %1280 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1281 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1280, i32 0, i32 0
  store ptr @42, ptr %1281, align 8
  %1282 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1280, i32 0, i32 1
  store i64 13, ptr %1282, align 4
  %1283 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1280, align 8
  %1284 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1285 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1286 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1285, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1283, ptr %1286, align 8
  %1287 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1285, i32 0, i32 1
  store ptr %1284, ptr %1287, align 8
  %1288 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1285, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).IsDirectIface", ptr %1288, align 8
  %1289 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1285, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).IsDirectIface", ptr %1289, align 8
  %1290 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1285, align 8
  %1291 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1292 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1291, i32 0, i32 0
  store ptr @43, ptr %1292, align 8
  %1293 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1291, i32 0, i32 1
  store i64 4, ptr %1293, align 4
  %1294 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1291, align 8
  %1295 = load ptr, ptr @"_llgo_func$ntUE0UmVAWPS2O7GpCCGszSn-XnjHJntZZ2jYtwbFXI", align 8
  %1296 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1297 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1296, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1294, ptr %1297, align 8
  %1298 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1296, i32 0, i32 1
  store ptr %1295, ptr %1298, align 8
  %1299 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1296, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Kind", ptr %1299, align 8
  %1300 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1296, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Kind", ptr %1300, align 8
  %1301 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1296, align 8
  %1302 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1303 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1302, i32 0, i32 0
  store ptr @28, ptr %1303, align 8
  %1304 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1302, i32 0, i32 1
  store i64 3, ptr %1304, align 4
  %1305 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1302, align 8
  %1306 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %1307 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1308 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1307, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1305, ptr %1308, align 8
  %1309 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1307, i32 0, i32 1
  store ptr %1306, ptr %1309, align 8
  %1310 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1307, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Len", ptr %1310, align 8
  %1311 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1307, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Len", ptr %1311, align 8
  %1312 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1307, align 8
  %1313 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1314 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1313, i32 0, i32 0
  store ptr @47, ptr %1314, align 8
  %1315 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1313, i32 0, i32 1
  store i64 7, ptr %1315, align 4
  %1316 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1313, align 8
  %1317 = load ptr, ptr @"_llgo_func$d-NlqnjcQnaMjsBQY7qh2SWQmHb0XIigoceXdiJ8YT4", align 8
  %1318 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1319 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1318, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1316, ptr %1319, align 8
  %1320 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1318, i32 0, i32 1
  store ptr %1317, ptr %1320, align 8
  %1321 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1318, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).MapType", ptr %1321, align 8
  %1322 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1318, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).MapType", ptr %1322, align 8
  %1323 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1318, align 8
  %1324 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1325 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1324, i32 0, i32 0
  store ptr @59, ptr %1325, align 8
  %1326 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1324, i32 0, i32 1
  store i64 13, ptr %1326, align 4
  %1327 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1324, align 8
  %1328 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1329 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1330 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1329, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1327, ptr %1330, align 8
  %1331 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1329, i32 0, i32 1
  store ptr %1328, ptr %1331, align 8
  %1332 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1329, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).NeedKeyUpdate", ptr %1332, align 8
  %1333 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1329, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).NeedKeyUpdate", ptr %1333, align 8
  %1334 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1329, align 8
  %1335 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1336 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1335, i32 0, i32 0
  store ptr @60, ptr %1336, align 8
  %1337 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1335, i32 0, i32 1
  store i64 8, ptr %1337, align 4
  %1338 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1335, align 8
  %1339 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1340 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1341 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1340, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1338, ptr %1341, align 8
  %1342 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1340, i32 0, i32 1
  store ptr %1339, ptr %1342, align 8
  %1343 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1340, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Pointers", ptr %1343, align 8
  %1344 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1340, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Pointers", ptr %1344, align 8
  %1345 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1340, align 8
  %1346 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1347 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1346, i32 0, i32 0
  store ptr @61, ptr %1347, align 8
  %1348 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1346, i32 0, i32 1
  store i64 12, ptr %1348, align 4
  %1349 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1346, align 8
  %1350 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1351 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1352 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1351, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1349, ptr %1352, align 8
  %1353 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1351, i32 0, i32 1
  store ptr %1350, ptr %1353, align 8
  %1354 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1351, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).ReflexiveKey", ptr %1354, align 8
  %1355 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1351, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).ReflexiveKey", ptr %1355, align 8
  %1356 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1351, align 8
  %1357 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1358 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1357, i32 0, i32 0
  store ptr @62, ptr %1358, align 8
  %1359 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1357, i32 0, i32 1
  store i64 4, ptr %1359, align 4
  %1360 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1357, align 8
  %1361 = load ptr, ptr @_llgo_uintptr, align 8
  %1362 = load ptr, ptr @"_llgo_func$1kITCsyu7hFLMxHLR7kDlvu4SOra_HtrtdFUQH9P13s", align 8
  %1363 = icmp eq ptr %1362, null
  br i1 %1363, label %_llgo_59, label %_llgo_60

_llgo_58:                                         ; preds = %_llgo_84, %_llgo_56
  %1364 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1365 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1364, i32 0, i32 0
  store ptr @48, ptr %1365, align 8
  %1366 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1364, i32 0, i32 1
  store i64 43, ptr %1366, align 4
  %1367 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1364, align 8
  %1368 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %1367, i64 25, i64 136, i64 0, i64 22)
  %1369 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.MapType", align 8
  %1370 = icmp eq ptr %1369, null
  br i1 %1370, label %_llgo_85, label %_llgo_86

_llgo_59:                                         ; preds = %_llgo_57
  %1371 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %1372 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1373 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1372, i32 0, i32 0
  store ptr %1371, ptr %1373, align 8
  %1374 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1372, i32 0, i32 1
  store i64 0, ptr %1374, align 4
  %1375 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1372, i32 0, i32 2
  store i64 0, ptr %1375, align 4
  %1376 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1372, align 8
  %1377 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %1378 = getelementptr ptr, ptr %1377, i64 0
  store ptr %1361, ptr %1378, align 8
  %1379 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1380 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1379, i32 0, i32 0
  store ptr %1377, ptr %1380, align 8
  %1381 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1379, i32 0, i32 1
  store i64 1, ptr %1381, align 4
  %1382 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1379, i32 0, i32 2
  store i64 1, ptr %1382, align 4
  %1383 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1379, align 8
  %1384 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %1376, %"github.com/goplus/llgo/internal/runtime.Slice" %1383, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %1384)
  store ptr %1384, ptr @"_llgo_func$1kITCsyu7hFLMxHLR7kDlvu4SOra_HtrtdFUQH9P13s", align 8
  br label %_llgo_60

_llgo_60:                                         ; preds = %_llgo_59, %_llgo_57
  %1385 = load ptr, ptr @"_llgo_func$1kITCsyu7hFLMxHLR7kDlvu4SOra_HtrtdFUQH9P13s", align 8
  %1386 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1387 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1386, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1360, ptr %1387, align 8
  %1388 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1386, i32 0, i32 1
  store ptr %1385, ptr %1388, align 8
  %1389 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1386, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Size", ptr %1389, align 8
  %1390 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1386, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Size", ptr %1390, align 8
  %1391 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1386, align 8
  %1392 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1393 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1392, i32 0, i32 0
  store ptr @45, ptr %1393, align 8
  %1394 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1392, i32 0, i32 1
  store i64 6, ptr %1394, align 4
  %1395 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1392, align 8
  %1396 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %1397 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1398 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1397, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1395, ptr %1398, align 8
  %1399 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1397, i32 0, i32 1
  store ptr %1396, ptr %1399, align 8
  %1400 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1397, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).String", ptr %1400, align 8
  %1401 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1397, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).String", ptr %1401, align 8
  %1402 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1397, align 8
  %1403 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1404 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1403, i32 0, i32 0
  store ptr @63, ptr %1404, align 8
  %1405 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1403, i32 0, i32 1
  store i64 10, ptr %1405, align 4
  %1406 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1403, align 8
  %1407 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1408 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1407, i32 0, i32 0
  store ptr @64, ptr %1408, align 8
  %1409 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1407, i32 0, i32 1
  store i64 46, ptr %1409, align 4
  %1410 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1407, align 8
  %1411 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %1410, i64 25, i64 120, i64 0, i64 18)
  %1412 = load ptr, ptr @"_llgo_github.com/goplus/llgo/internal/abi.StructType", align 8
  %1413 = icmp eq ptr %1412, null
  br i1 %1413, label %_llgo_61, label %_llgo_62

_llgo_61:                                         ; preds = %_llgo_60
  store ptr %1411, ptr @"_llgo_github.com/goplus/llgo/internal/abi.StructType", align 8
  br label %_llgo_62

_llgo_62:                                         ; preds = %_llgo_61, %_llgo_60
  %1414 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1415 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1414, i32 0, i32 0
  store ptr @1, ptr %1415, align 8
  %1416 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1414, i32 0, i32 1
  store i64 40, ptr %1416, align 4
  %1417 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1414, align 8
  %1418 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %1417, i64 25, i64 80, i64 0, i64 18)
  %1419 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1420 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1419, i32 0, i32 0
  store ptr @65, ptr %1420, align 8
  %1421 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1419, i32 0, i32 1
  store i64 47, ptr %1421, align 4
  %1422 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1419, align 8
  %1423 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %1422, i64 25, i64 56, i64 0, i64 2)
  %1424 = load ptr, ptr @"_llgo_struct$K_cvuhBwc2_5r7UW089ibWfcfsGoDb4pZ7K19IcMTk0", align 8
  %1425 = icmp eq ptr %1424, null
  br i1 %1425, label %_llgo_63, label %_llgo_64

_llgo_63:                                         ; preds = %_llgo_62
  %1426 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1427 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1426, i32 0, i32 0
  store ptr @25, ptr %1427, align 8
  %1428 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1426, i32 0, i32 1
  store i64 4, ptr %1428, align 4
  %1429 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1426, align 8
  %1430 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1431 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1430, i32 0, i32 0
  store ptr null, ptr %1431, align 8
  %1432 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1430, i32 0, i32 1
  store i64 0, ptr %1432, align 4
  %1433 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1430, align 8
  %1434 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1429, ptr %1418, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %1433, i1 true)
  %1435 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1436 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1435, i32 0, i32 0
  store ptr @40, ptr %1436, align 8
  %1437 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1435, i32 0, i32 1
  store i64 8, ptr %1437, align 4
  %1438 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1435, align 8
  %1439 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1440 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1439, i32 0, i32 0
  store ptr null, ptr %1440, align 8
  %1441 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1439, i32 0, i32 1
  store i64 0, ptr %1441, align 4
  %1442 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1439, align 8
  %1443 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 24)
  %1444 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1438, ptr %1443, i64 72, %"github.com/goplus/llgo/internal/runtime.String" %1442, i1 false)
  %1445 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1446 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1445, i32 0, i32 0
  store ptr @66, ptr %1446, align 8
  %1447 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1445, i32 0, i32 1
  store i64 6, ptr %1447, align 4
  %1448 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1445, align 8
  %1449 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1450 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1449, i32 0, i32 0
  store ptr null, ptr %1450, align 8
  %1451 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1449, i32 0, i32 1
  store i64 0, ptr %1451, align 4
  %1452 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1449, align 8
  %1453 = call ptr @"github.com/goplus/llgo/internal/runtime.SliceOf"(ptr %1423)
  %1454 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1448, ptr %1453, i64 88, %"github.com/goplus/llgo/internal/runtime.String" %1452, i1 false)
  %1455 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1456 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1455, i32 0, i32 0
  store ptr @6, ptr %1456, align 8
  %1457 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1455, i32 0, i32 1
  store i64 4, ptr %1457, align 4
  %1458 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1455, align 8
  %1459 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 168)
  %1460 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1459, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %1434, ptr %1460, align 8
  %1461 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1459, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %1444, ptr %1461, align 8
  %1462 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1459, i64 2
  store %"github.com/goplus/llgo/internal/abi.StructField" %1454, ptr %1462, align 8
  %1463 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1464 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1463, i32 0, i32 0
  store ptr %1459, ptr %1464, align 8
  %1465 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1463, i32 0, i32 1
  store i64 3, ptr %1465, align 4
  %1466 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1463, i32 0, i32 2
  store i64 3, ptr %1466, align 4
  %1467 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1463, align 8
  %1468 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %1458, i64 112, %"github.com/goplus/llgo/internal/runtime.Slice" %1467)
  store ptr %1468, ptr @"_llgo_struct$K_cvuhBwc2_5r7UW089ibWfcfsGoDb4pZ7K19IcMTk0", align 8
  br label %_llgo_64

_llgo_64:                                         ; preds = %_llgo_63, %_llgo_62
  %1469 = load ptr, ptr @"_llgo_struct$K_cvuhBwc2_5r7UW089ibWfcfsGoDb4pZ7K19IcMTk0", align 8
  br i1 %1413, label %_llgo_65, label %_llgo_66

_llgo_65:                                         ; preds = %_llgo_64
  %1470 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1471 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1470, i32 0, i32 0
  store ptr @22, ptr %1471, align 8
  %1472 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1470, i32 0, i32 1
  store i64 5, ptr %1472, align 4
  %1473 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1470, align 8
  %1474 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %1475 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1476 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1475, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1473, ptr %1476, align 8
  %1477 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1475, i32 0, i32 1
  store ptr %1474, ptr %1477, align 8
  %1478 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1475, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Align", ptr %1478, align 8
  %1479 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1475, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Align", ptr %1479, align 8
  %1480 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1475, align 8
  %1481 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1482 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1481, i32 0, i32 0
  store ptr @23, ptr %1482, align 8
  %1483 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1481, i32 0, i32 1
  store i64 9, ptr %1483, align 4
  %1484 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1481, align 8
  %1485 = load ptr, ptr @"_llgo_func$CsVqlCxhoEcIvPD5BSBukfSiD9C7Ic5_Gf32MLbCWB4", align 8
  %1486 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1487 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1486, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1484, ptr %1487, align 8
  %1488 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1486, i32 0, i32 1
  store ptr %1485, ptr %1488, align 8
  %1489 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1486, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).ArrayType", ptr %1489, align 8
  %1490 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1486, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).ArrayType", ptr %1490, align 8
  %1491 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1486, align 8
  %1492 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1493 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1492, i32 0, i32 0
  store ptr @29, ptr %1493, align 8
  %1494 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1492, i32 0, i32 1
  store i64 6, ptr %1494, align 4
  %1495 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1492, align 8
  %1496 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %1497 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1498 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1497, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1495, ptr %1498, align 8
  %1499 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1497, i32 0, i32 1
  store ptr %1496, ptr %1499, align 8
  %1500 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1497, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Common", ptr %1500, align 8
  %1501 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1497, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Common", ptr %1501, align 8
  %1502 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1497, align 8
  %1503 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1504 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1503, i32 0, i32 0
  store ptr @26, ptr %1504, align 8
  %1505 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1503, i32 0, i32 1
  store i64 4, ptr %1505, align 4
  %1506 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1503, align 8
  %1507 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %1508 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1509 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1508, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1506, ptr %1509, align 8
  %1510 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1508, i32 0, i32 1
  store ptr %1507, ptr %1510, align 8
  %1511 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1508, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Elem", ptr %1511, align 8
  %1512 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1508, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Elem", ptr %1512, align 8
  %1513 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1508, align 8
  %1514 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1515 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1514, i32 0, i32 0
  store ptr @30, ptr %1515, align 8
  %1516 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1514, i32 0, i32 1
  store i64 10, ptr %1516, align 4
  %1517 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1514, align 8
  %1518 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %1519 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1520 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1519, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1517, ptr %1520, align 8
  %1521 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1519, i32 0, i32 1
  store ptr %1518, ptr %1521, align 8
  %1522 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1519, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).FieldAlign", ptr %1522, align 8
  %1523 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1519, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).FieldAlign", ptr %1523, align 8
  %1524 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1519, align 8
  %1525 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1526 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1525, i32 0, i32 0
  store ptr @31, ptr %1526, align 8
  %1527 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1525, i32 0, i32 1
  store i64 8, ptr %1527, align 4
  %1528 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1525, align 8
  %1529 = load ptr, ptr @"_llgo_func$DsoxgOnxqV7tLvokF3AA14v1gtHsHaThoC8Q_XGcQww", align 8
  %1530 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1531 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1530, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1528, ptr %1531, align 8
  %1532 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1530, i32 0, i32 1
  store ptr %1529, ptr %1532, align 8
  %1533 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1530, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).FuncType", ptr %1533, align 8
  %1534 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1530, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).FuncType", ptr %1534, align 8
  %1535 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1530, align 8
  %1536 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1537 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1536, i32 0, i32 0
  store ptr @35, ptr %1537, align 8
  %1538 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1536, i32 0, i32 1
  store i64 7, ptr %1538, align 4
  %1539 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1536, align 8
  %1540 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1541 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1542 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1541, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1539, ptr %1542, align 8
  %1543 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1541, i32 0, i32 1
  store ptr %1540, ptr %1543, align 8
  %1544 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1541, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).HasName", ptr %1544, align 8
  %1545 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1541, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).HasName", ptr %1545, align 8
  %1546 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1541, align 8
  %1547 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1548 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1547, i32 0, i32 0
  store ptr @36, ptr %1548, align 8
  %1549 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1547, i32 0, i32 1
  store i64 10, ptr %1549, align 4
  %1550 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1547, align 8
  %1551 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1552 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1553 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1552, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1550, ptr %1553, align 8
  %1554 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1552, i32 0, i32 1
  store ptr %1551, ptr %1554, align 8
  %1555 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1552, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).IfaceIndir", ptr %1555, align 8
  %1556 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1552, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).IfaceIndir", ptr %1556, align 8
  %1557 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1552, align 8
  %1558 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1559 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1558, i32 0, i32 0
  store ptr @37, ptr %1559, align 8
  %1560 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1558, i32 0, i32 1
  store i64 13, ptr %1560, align 4
  %1561 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1558, align 8
  %1562 = load ptr, ptr @"_llgo_func$1QmforOaCy2fBAssC2y1FWCCT6fpq9RKwP2j2HIASY8", align 8
  %1563 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1564 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1563, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1561, ptr %1564, align 8
  %1565 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1563, i32 0, i32 1
  store ptr %1562, ptr %1565, align 8
  %1566 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1563, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).InterfaceType", ptr %1566, align 8
  %1567 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1563, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).InterfaceType", ptr %1567, align 8
  %1568 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1563, align 8
  %1569 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1570 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1569, i32 0, i32 0
  store ptr @42, ptr %1570, align 8
  %1571 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1569, i32 0, i32 1
  store i64 13, ptr %1571, align 4
  %1572 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1569, align 8
  %1573 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1574 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1575 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1574, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1572, ptr %1575, align 8
  %1576 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1574, i32 0, i32 1
  store ptr %1573, ptr %1576, align 8
  %1577 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1574, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).IsDirectIface", ptr %1577, align 8
  %1578 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1574, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).IsDirectIface", ptr %1578, align 8
  %1579 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1574, align 8
  %1580 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1581 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1580, i32 0, i32 0
  store ptr @43, ptr %1581, align 8
  %1582 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1580, i32 0, i32 1
  store i64 4, ptr %1582, align 4
  %1583 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1580, align 8
  %1584 = load ptr, ptr @"_llgo_func$ntUE0UmVAWPS2O7GpCCGszSn-XnjHJntZZ2jYtwbFXI", align 8
  %1585 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1586 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1585, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1583, ptr %1586, align 8
  %1587 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1585, i32 0, i32 1
  store ptr %1584, ptr %1587, align 8
  %1588 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1585, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Kind", ptr %1588, align 8
  %1589 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1585, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Kind", ptr %1589, align 8
  %1590 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1585, align 8
  %1591 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1592 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1591, i32 0, i32 0
  store ptr @28, ptr %1592, align 8
  %1593 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1591, i32 0, i32 1
  store i64 3, ptr %1593, align 4
  %1594 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1591, align 8
  %1595 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %1596 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1597 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1596, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1594, ptr %1597, align 8
  %1598 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1596, i32 0, i32 1
  store ptr %1595, ptr %1598, align 8
  %1599 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1596, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Len", ptr %1599, align 8
  %1600 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1596, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Len", ptr %1600, align 8
  %1601 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1596, align 8
  %1602 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1603 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1602, i32 0, i32 0
  store ptr @47, ptr %1603, align 8
  %1604 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1602, i32 0, i32 1
  store i64 7, ptr %1604, align 4
  %1605 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1602, align 8
  %1606 = load ptr, ptr @"_llgo_func$d-NlqnjcQnaMjsBQY7qh2SWQmHb0XIigoceXdiJ8YT4", align 8
  %1607 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1608 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1607, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1605, ptr %1608, align 8
  %1609 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1607, i32 0, i32 1
  store ptr %1606, ptr %1609, align 8
  %1610 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1607, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).MapType", ptr %1610, align 8
  %1611 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1607, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).MapType", ptr %1611, align 8
  %1612 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1607, align 8
  %1613 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1614 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1613, i32 0, i32 0
  store ptr @60, ptr %1614, align 8
  %1615 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1613, i32 0, i32 1
  store i64 8, ptr %1615, align 4
  %1616 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1613, align 8
  %1617 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1618 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1619 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1618, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1616, ptr %1619, align 8
  %1620 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1618, i32 0, i32 1
  store ptr %1617, ptr %1620, align 8
  %1621 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1618, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Pointers", ptr %1621, align 8
  %1622 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1618, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Pointers", ptr %1622, align 8
  %1623 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1618, align 8
  %1624 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1625 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1624, i32 0, i32 0
  store ptr @62, ptr %1625, align 8
  %1626 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1624, i32 0, i32 1
  store i64 4, ptr %1626, align 4
  %1627 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1624, align 8
  %1628 = load ptr, ptr @"_llgo_func$1kITCsyu7hFLMxHLR7kDlvu4SOra_HtrtdFUQH9P13s", align 8
  %1629 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1630 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1629, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1627, ptr %1630, align 8
  %1631 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1629, i32 0, i32 1
  store ptr %1628, ptr %1631, align 8
  %1632 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1629, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Size", ptr %1632, align 8
  %1633 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1629, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Size", ptr %1633, align 8
  %1634 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1629, align 8
  %1635 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1636 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1635, i32 0, i32 0
  store ptr @45, ptr %1636, align 8
  %1637 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1635, i32 0, i32 1
  store i64 6, ptr %1637, align 4
  %1638 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1635, align 8
  %1639 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %1640 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1641 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1640, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1638, ptr %1641, align 8
  %1642 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1640, i32 0, i32 1
  store ptr %1639, ptr %1642, align 8
  %1643 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1640, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).String", ptr %1643, align 8
  %1644 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1640, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).String", ptr %1644, align 8
  %1645 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1640, align 8
  %1646 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1647 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1646, i32 0, i32 0
  store ptr @63, ptr %1647, align 8
  %1648 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1646, i32 0, i32 1
  store i64 10, ptr %1648, align 4
  %1649 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1646, align 8
  %1650 = load ptr, ptr @"_llgo_func$qiNnn6Cbm3GtDp4gDI4U_DRV3h8zlz91s9jrfOXC--U", align 8
  %1651 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1652 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1651, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1649, ptr %1652, align 8
  %1653 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1651, i32 0, i32 1
  store ptr %1650, ptr %1653, align 8
  %1654 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1651, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).StructType", ptr %1654, align 8
  %1655 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1651, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).StructType", ptr %1655, align 8
  %1656 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1651, align 8
  %1657 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1658 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1657, i32 0, i32 0
  store ptr @67, ptr %1658, align 8
  %1659 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1657, i32 0, i32 1
  store i64 8, ptr %1659, align 4
  %1660 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1657, align 8
  %1661 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1662 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1661, i32 0, i32 0
  store ptr @68, ptr %1662, align 8
  %1663 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1661, i32 0, i32 1
  store i64 48, ptr %1663, align 4
  %1664 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1661, align 8
  %1665 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %1664, i64 25, i64 24, i64 0, i64 2)
  %1666 = load ptr, ptr @"_llgo_github.com/goplus/llgo/internal/abi.UncommonType", align 8
  %1667 = icmp eq ptr %1666, null
  br i1 %1667, label %_llgo_67, label %_llgo_68

_llgo_66:                                         ; preds = %_llgo_80, %_llgo_64
  %1668 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1669 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1668, i32 0, i32 0
  store ptr @64, ptr %1669, align 8
  %1670 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1668, i32 0, i32 1
  store i64 46, ptr %1670, align 4
  %1671 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1668, align 8
  %1672 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %1671, i64 25, i64 120, i64 0, i64 18)
  %1673 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.StructType", align 8
  %1674 = icmp eq ptr %1673, null
  br i1 %1674, label %_llgo_81, label %_llgo_82

_llgo_67:                                         ; preds = %_llgo_65
  store ptr %1665, ptr @"_llgo_github.com/goplus/llgo/internal/abi.UncommonType", align 8
  br label %_llgo_68

_llgo_68:                                         ; preds = %_llgo_67, %_llgo_65
  %1675 = load ptr, ptr @"_llgo_struct$OKIlItfBJsawrEMnVSc2VQ7pxNxCHIgSoitcM9n4FVI", align 8
  %1676 = icmp eq ptr %1675, null
  br i1 %1676, label %_llgo_69, label %_llgo_70

_llgo_69:                                         ; preds = %_llgo_68
  %1677 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1678 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1677, i32 0, i32 0
  store ptr @40, ptr %1678, align 8
  %1679 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1677, i32 0, i32 1
  store i64 8, ptr %1679, align 4
  %1680 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1677, align 8
  %1681 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1682 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1681, i32 0, i32 0
  store ptr null, ptr %1682, align 8
  %1683 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1681, i32 0, i32 1
  store i64 0, ptr %1683, align 4
  %1684 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1681, align 8
  %1685 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 24)
  %1686 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1680, ptr %1685, i64 0, %"github.com/goplus/llgo/internal/runtime.String" %1684, i1 false)
  %1687 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1688 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1687, i32 0, i32 0
  store ptr @69, ptr %1688, align 8
  %1689 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1687, i32 0, i32 1
  store i64 6, ptr %1689, align 4
  %1690 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1687, align 8
  %1691 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1692 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1691, i32 0, i32 0
  store ptr null, ptr %1692, align 8
  %1693 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1691, i32 0, i32 1
  store i64 0, ptr %1693, align 4
  %1694 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1691, align 8
  %1695 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 41)
  %1696 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1690, ptr %1695, i64 16, %"github.com/goplus/llgo/internal/runtime.String" %1694, i1 false)
  %1697 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1698 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1697, i32 0, i32 0
  store ptr @70, ptr %1698, align 8
  %1699 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1697, i32 0, i32 1
  store i64 6, ptr %1699, align 4
  %1700 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1697, align 8
  %1701 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1702 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1701, i32 0, i32 0
  store ptr null, ptr %1702, align 8
  %1703 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1701, i32 0, i32 1
  store i64 0, ptr %1703, align 4
  %1704 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1701, align 8
  %1705 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 41)
  %1706 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1700, ptr %1705, i64 18, %"github.com/goplus/llgo/internal/runtime.String" %1704, i1 false)
  %1707 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1708 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1707, i32 0, i32 0
  store ptr @71, ptr %1708, align 8
  %1709 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1707, i32 0, i32 1
  store i64 4, ptr %1709, align 4
  %1710 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1707, align 8
  %1711 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1712 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1711, i32 0, i32 0
  store ptr null, ptr %1712, align 8
  %1713 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1711, i32 0, i32 1
  store i64 0, ptr %1713, align 4
  %1714 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1711, align 8
  %1715 = call ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64 42)
  %1716 = call %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String" %1710, ptr %1715, i64 20, %"github.com/goplus/llgo/internal/runtime.String" %1714, i1 false)
  %1717 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1718 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1717, i32 0, i32 0
  store ptr @6, ptr %1718, align 8
  %1719 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1717, i32 0, i32 1
  store i64 4, ptr %1719, align 4
  %1720 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1717, align 8
  %1721 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 224)
  %1722 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1721, i64 0
  store %"github.com/goplus/llgo/internal/abi.StructField" %1686, ptr %1722, align 8
  %1723 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1721, i64 1
  store %"github.com/goplus/llgo/internal/abi.StructField" %1696, ptr %1723, align 8
  %1724 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1721, i64 2
  store %"github.com/goplus/llgo/internal/abi.StructField" %1706, ptr %1724, align 8
  %1725 = getelementptr %"github.com/goplus/llgo/internal/abi.StructField", ptr %1721, i64 3
  store %"github.com/goplus/llgo/internal/abi.StructField" %1716, ptr %1725, align 8
  %1726 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1727 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1726, i32 0, i32 0
  store ptr %1721, ptr %1727, align 8
  %1728 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1726, i32 0, i32 1
  store i64 4, ptr %1728, align 4
  %1729 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1726, i32 0, i32 2
  store i64 4, ptr %1729, align 4
  %1730 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1726, align 8
  %1731 = call ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String" %1720, i64 24, %"github.com/goplus/llgo/internal/runtime.Slice" %1730)
  store ptr %1731, ptr @"_llgo_struct$OKIlItfBJsawrEMnVSc2VQ7pxNxCHIgSoitcM9n4FVI", align 8
  br label %_llgo_70

_llgo_70:                                         ; preds = %_llgo_69, %_llgo_68
  %1732 = load ptr, ptr @"_llgo_struct$OKIlItfBJsawrEMnVSc2VQ7pxNxCHIgSoitcM9n4FVI", align 8
  br i1 %1667, label %_llgo_71, label %_llgo_72

_llgo_71:                                         ; preds = %_llgo_70
  %1733 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1734 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1733, i32 0, i32 0
  store ptr @72, ptr %1734, align 8
  %1735 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1733, i32 0, i32 1
  store i64 15, ptr %1735, align 4
  %1736 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1733, align 8
  %1737 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1738 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1737, i32 0, i32 0
  store ptr @73, ptr %1738, align 8
  %1739 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1737, i32 0, i32 1
  store i64 42, ptr %1739, align 4
  %1740 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1737, align 8
  %1741 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %1740, i64 25, i64 40, i64 0, i64 3)
  %1742 = load ptr, ptr @"[]_llgo_github.com/goplus/llgo/internal/abi.Method", align 8
  %1743 = icmp eq ptr %1742, null
  br i1 %1743, label %_llgo_73, label %_llgo_74

_llgo_72:                                         ; preds = %_llgo_76, %_llgo_70
  %1744 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1745 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1744, i32 0, i32 0
  store ptr @68, ptr %1745, align 8
  %1746 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1744, i32 0, i32 1
  store i64 48, ptr %1746, align 4
  %1747 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1744, align 8
  %1748 = call ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String" %1747, i64 25, i64 24, i64 0, i64 2)
  %1749 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.UncommonType", align 8
  %1750 = icmp eq ptr %1749, null
  br i1 %1750, label %_llgo_77, label %_llgo_78

_llgo_73:                                         ; preds = %_llgo_71
  %1751 = call ptr @"github.com/goplus/llgo/internal/runtime.SliceOf"(ptr %1741)
  store ptr %1751, ptr @"[]_llgo_github.com/goplus/llgo/internal/abi.Method", align 8
  br label %_llgo_74

_llgo_74:                                         ; preds = %_llgo_73, %_llgo_71
  %1752 = load ptr, ptr @"[]_llgo_github.com/goplus/llgo/internal/abi.Method", align 8
  %1753 = load ptr, ptr @"_llgo_func$r0w3aCNVheLGqjxncuxitGhNtWJagb9gZLqOSrNI7dg", align 8
  %1754 = icmp eq ptr %1753, null
  br i1 %1754, label %_llgo_75, label %_llgo_76

_llgo_75:                                         ; preds = %_llgo_74
  %1755 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %1756 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1757 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1756, i32 0, i32 0
  store ptr %1755, ptr %1757, align 8
  %1758 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1756, i32 0, i32 1
  store i64 0, ptr %1758, align 4
  %1759 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1756, i32 0, i32 2
  store i64 0, ptr %1759, align 4
  %1760 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1756, align 8
  %1761 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %1762 = getelementptr ptr, ptr %1761, i64 0
  store ptr %1752, ptr %1762, align 8
  %1763 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1764 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1763, i32 0, i32 0
  store ptr %1761, ptr %1764, align 8
  %1765 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1763, i32 0, i32 1
  store i64 1, ptr %1765, align 4
  %1766 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1763, i32 0, i32 2
  store i64 1, ptr %1766, align 4
  %1767 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1763, align 8
  %1768 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %1760, %"github.com/goplus/llgo/internal/runtime.Slice" %1767, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %1768)
  store ptr %1768, ptr @"_llgo_func$r0w3aCNVheLGqjxncuxitGhNtWJagb9gZLqOSrNI7dg", align 8
  br label %_llgo_76

_llgo_76:                                         ; preds = %_llgo_75, %_llgo_74
  %1769 = load ptr, ptr @"_llgo_func$r0w3aCNVheLGqjxncuxitGhNtWJagb9gZLqOSrNI7dg", align 8
  %1770 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1771 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1770, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1736, ptr %1771, align 8
  %1772 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1770, i32 0, i32 1
  store ptr %1769, ptr %1772, align 8
  %1773 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1770, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*UncommonType).ExportedMethods", ptr %1773, align 8
  %1774 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1770, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*UncommonType).ExportedMethods", ptr %1774, align 8
  %1775 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1770, align 8
  %1776 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1777 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1776, i32 0, i32 0
  store ptr @41, ptr %1777, align 8
  %1778 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1776, i32 0, i32 1
  store i64 7, ptr %1778, align 4
  %1779 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1776, align 8
  %1780 = load ptr, ptr @"_llgo_func$r0w3aCNVheLGqjxncuxitGhNtWJagb9gZLqOSrNI7dg", align 8
  %1781 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1782 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1781, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1779, ptr %1782, align 8
  %1783 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1781, i32 0, i32 1
  store ptr %1780, ptr %1783, align 8
  %1784 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1781, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*UncommonType).Methods", ptr %1784, align 8
  %1785 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1781, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*UncommonType).Methods", ptr %1785, align 8
  %1786 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1781, align 8
  %1787 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 80)
  %1788 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1787, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %1775, ptr %1788, align 8
  %1789 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1787, i64 1
  store %"github.com/goplus/llgo/internal/abi.Method" %1786, ptr %1789, align 8
  %1790 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1791 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1790, i32 0, i32 0
  store ptr %1787, ptr %1791, align 8
  %1792 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1790, i32 0, i32 1
  store i64 2, ptr %1792, align 4
  %1793 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1790, i32 0, i32 2
  store i64 2, ptr %1793, align 4
  %1794 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1790, align 8
  %1795 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1796 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1795, i32 0, i32 0
  store ptr @46, ptr %1796, align 8
  %1797 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1795, i32 0, i32 1
  store i64 35, ptr %1797, align 4
  %1798 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1795, align 8
  %1799 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1800 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1799, i32 0, i32 0
  store ptr @74, ptr %1800, align 8
  %1801 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1799, i32 0, i32 1
  store i64 12, ptr %1801, align 4
  %1802 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1799, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %1665, %"github.com/goplus/llgo/internal/runtime.String" %1798, %"github.com/goplus/llgo/internal/runtime.String" %1802, ptr %1732, { ptr, i64, i64 } zeroinitializer, %"github.com/goplus/llgo/internal/runtime.Slice" %1794)
  br label %_llgo_72

_llgo_77:                                         ; preds = %_llgo_72
  %1803 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %1748)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %1803)
  store ptr %1803, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.UncommonType", align 8
  br label %_llgo_78

_llgo_78:                                         ; preds = %_llgo_77, %_llgo_72
  %1804 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.UncommonType", align 8
  %1805 = load ptr, ptr @"_llgo_func$DbD4nZv_bjE4tH8hh-VfAjMXMpNfIsMlLJJJPKupp34", align 8
  %1806 = icmp eq ptr %1805, null
  br i1 %1806, label %_llgo_79, label %_llgo_80

_llgo_79:                                         ; preds = %_llgo_78
  %1807 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %1808 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1809 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1808, i32 0, i32 0
  store ptr %1807, ptr %1809, align 8
  %1810 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1808, i32 0, i32 1
  store i64 0, ptr %1810, align 4
  %1811 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1808, i32 0, i32 2
  store i64 0, ptr %1811, align 4
  %1812 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1808, align 8
  %1813 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %1814 = getelementptr ptr, ptr %1813, i64 0
  store ptr %1804, ptr %1814, align 8
  %1815 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1816 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1815, i32 0, i32 0
  store ptr %1813, ptr %1816, align 8
  %1817 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1815, i32 0, i32 1
  store i64 1, ptr %1817, align 4
  %1818 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1815, i32 0, i32 2
  store i64 1, ptr %1818, align 4
  %1819 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1815, align 8
  %1820 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %1812, %"github.com/goplus/llgo/internal/runtime.Slice" %1819, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %1820)
  store ptr %1820, ptr @"_llgo_func$DbD4nZv_bjE4tH8hh-VfAjMXMpNfIsMlLJJJPKupp34", align 8
  br label %_llgo_80

_llgo_80:                                         ; preds = %_llgo_79, %_llgo_78
  %1821 = load ptr, ptr @"_llgo_func$DbD4nZv_bjE4tH8hh-VfAjMXMpNfIsMlLJJJPKupp34", align 8
  %1822 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1823 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1822, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1660, ptr %1823, align 8
  %1824 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1822, i32 0, i32 1
  store ptr %1821, ptr %1824, align 8
  %1825 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1822, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Uncommon", ptr %1825, align 8
  %1826 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1822, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Uncommon", ptr %1826, align 8
  %1827 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1822, align 8
  %1828 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 720)
  %1829 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %1480, ptr %1829, align 8
  %1830 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 1
  store %"github.com/goplus/llgo/internal/abi.Method" %1491, ptr %1830, align 8
  %1831 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 2
  store %"github.com/goplus/llgo/internal/abi.Method" %1502, ptr %1831, align 8
  %1832 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 3
  store %"github.com/goplus/llgo/internal/abi.Method" %1513, ptr %1832, align 8
  %1833 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 4
  store %"github.com/goplus/llgo/internal/abi.Method" %1524, ptr %1833, align 8
  %1834 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 5
  store %"github.com/goplus/llgo/internal/abi.Method" %1535, ptr %1834, align 8
  %1835 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 6
  store %"github.com/goplus/llgo/internal/abi.Method" %1546, ptr %1835, align 8
  %1836 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 7
  store %"github.com/goplus/llgo/internal/abi.Method" %1557, ptr %1836, align 8
  %1837 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 8
  store %"github.com/goplus/llgo/internal/abi.Method" %1568, ptr %1837, align 8
  %1838 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 9
  store %"github.com/goplus/llgo/internal/abi.Method" %1579, ptr %1838, align 8
  %1839 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 10
  store %"github.com/goplus/llgo/internal/abi.Method" %1590, ptr %1839, align 8
  %1840 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 11
  store %"github.com/goplus/llgo/internal/abi.Method" %1601, ptr %1840, align 8
  %1841 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 12
  store %"github.com/goplus/llgo/internal/abi.Method" %1612, ptr %1841, align 8
  %1842 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 13
  store %"github.com/goplus/llgo/internal/abi.Method" %1623, ptr %1842, align 8
  %1843 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 14
  store %"github.com/goplus/llgo/internal/abi.Method" %1634, ptr %1843, align 8
  %1844 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 15
  store %"github.com/goplus/llgo/internal/abi.Method" %1645, ptr %1844, align 8
  %1845 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 16
  store %"github.com/goplus/llgo/internal/abi.Method" %1656, ptr %1845, align 8
  %1846 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1828, i64 17
  store %"github.com/goplus/llgo/internal/abi.Method" %1827, ptr %1846, align 8
  %1847 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1848 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1847, i32 0, i32 0
  store ptr %1828, ptr %1848, align 8
  %1849 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1847, i32 0, i32 1
  store i64 18, ptr %1849, align 4
  %1850 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1847, i32 0, i32 2
  store i64 18, ptr %1850, align 4
  %1851 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1847, align 8
  %1852 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1853 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1852, i32 0, i32 0
  store ptr @46, ptr %1853, align 8
  %1854 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1852, i32 0, i32 1
  store i64 35, ptr %1854, align 4
  %1855 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1852, align 8
  %1856 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1857 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1856, i32 0, i32 0
  store ptr @63, ptr %1857, align 8
  %1858 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1856, i32 0, i32 1
  store i64 10, ptr %1858, align 4
  %1859 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1856, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %1411, %"github.com/goplus/llgo/internal/runtime.String" %1855, %"github.com/goplus/llgo/internal/runtime.String" %1859, ptr %1469, { ptr, i64, i64 } zeroinitializer, %"github.com/goplus/llgo/internal/runtime.Slice" %1851)
  br label %_llgo_66

_llgo_81:                                         ; preds = %_llgo_66
  %1860 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %1672)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %1860)
  store ptr %1860, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.StructType", align 8
  br label %_llgo_82

_llgo_82:                                         ; preds = %_llgo_81, %_llgo_66
  %1861 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.StructType", align 8
  %1862 = load ptr, ptr @"_llgo_func$qiNnn6Cbm3GtDp4gDI4U_DRV3h8zlz91s9jrfOXC--U", align 8
  %1863 = icmp eq ptr %1862, null
  br i1 %1863, label %_llgo_83, label %_llgo_84

_llgo_83:                                         ; preds = %_llgo_82
  %1864 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %1865 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1866 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1865, i32 0, i32 0
  store ptr %1864, ptr %1866, align 8
  %1867 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1865, i32 0, i32 1
  store i64 0, ptr %1867, align 4
  %1868 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1865, i32 0, i32 2
  store i64 0, ptr %1868, align 4
  %1869 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1865, align 8
  %1870 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %1871 = getelementptr ptr, ptr %1870, i64 0
  store ptr %1861, ptr %1871, align 8
  %1872 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1873 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1872, i32 0, i32 0
  store ptr %1870, ptr %1873, align 8
  %1874 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1872, i32 0, i32 1
  store i64 1, ptr %1874, align 4
  %1875 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1872, i32 0, i32 2
  store i64 1, ptr %1875, align 4
  %1876 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1872, align 8
  %1877 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %1869, %"github.com/goplus/llgo/internal/runtime.Slice" %1876, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %1877)
  store ptr %1877, ptr @"_llgo_func$qiNnn6Cbm3GtDp4gDI4U_DRV3h8zlz91s9jrfOXC--U", align 8
  br label %_llgo_84

_llgo_84:                                         ; preds = %_llgo_83, %_llgo_82
  %1878 = load ptr, ptr @"_llgo_func$qiNnn6Cbm3GtDp4gDI4U_DRV3h8zlz91s9jrfOXC--U", align 8
  %1879 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1880 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1879, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1406, ptr %1880, align 8
  %1881 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1879, i32 0, i32 1
  store ptr %1878, ptr %1881, align 8
  %1882 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1879, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).StructType", ptr %1882, align 8
  %1883 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1879, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).StructType", ptr %1883, align 8
  %1884 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1879, align 8
  %1885 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1886 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1885, i32 0, i32 0
  store ptr @67, ptr %1886, align 8
  %1887 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1885, i32 0, i32 1
  store i64 8, ptr %1887, align 4
  %1888 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1885, align 8
  %1889 = load ptr, ptr @"_llgo_func$DbD4nZv_bjE4tH8hh-VfAjMXMpNfIsMlLJJJPKupp34", align 8
  %1890 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1891 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1890, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1888, ptr %1891, align 8
  %1892 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1890, i32 0, i32 1
  store ptr %1889, ptr %1892, align 8
  %1893 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1890, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Uncommon", ptr %1893, align 8
  %1894 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1890, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Uncommon", ptr %1894, align 8
  %1895 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1890, align 8
  %1896 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 880)
  %1897 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %1169, ptr %1897, align 8
  %1898 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 1
  store %"github.com/goplus/llgo/internal/abi.Method" %1180, ptr %1898, align 8
  %1899 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 2
  store %"github.com/goplus/llgo/internal/abi.Method" %1191, ptr %1899, align 8
  %1900 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 3
  store %"github.com/goplus/llgo/internal/abi.Method" %1202, ptr %1900, align 8
  %1901 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 4
  store %"github.com/goplus/llgo/internal/abi.Method" %1213, ptr %1901, align 8
  %1902 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 5
  store %"github.com/goplus/llgo/internal/abi.Method" %1224, ptr %1902, align 8
  %1903 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 6
  store %"github.com/goplus/llgo/internal/abi.Method" %1235, ptr %1903, align 8
  %1904 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 7
  store %"github.com/goplus/llgo/internal/abi.Method" %1246, ptr %1904, align 8
  %1905 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 8
  store %"github.com/goplus/llgo/internal/abi.Method" %1257, ptr %1905, align 8
  %1906 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 9
  store %"github.com/goplus/llgo/internal/abi.Method" %1268, ptr %1906, align 8
  %1907 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 10
  store %"github.com/goplus/llgo/internal/abi.Method" %1279, ptr %1907, align 8
  %1908 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 11
  store %"github.com/goplus/llgo/internal/abi.Method" %1290, ptr %1908, align 8
  %1909 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 12
  store %"github.com/goplus/llgo/internal/abi.Method" %1301, ptr %1909, align 8
  %1910 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 13
  store %"github.com/goplus/llgo/internal/abi.Method" %1312, ptr %1910, align 8
  %1911 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 14
  store %"github.com/goplus/llgo/internal/abi.Method" %1323, ptr %1911, align 8
  %1912 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 15
  store %"github.com/goplus/llgo/internal/abi.Method" %1334, ptr %1912, align 8
  %1913 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 16
  store %"github.com/goplus/llgo/internal/abi.Method" %1345, ptr %1913, align 8
  %1914 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 17
  store %"github.com/goplus/llgo/internal/abi.Method" %1356, ptr %1914, align 8
  %1915 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 18
  store %"github.com/goplus/llgo/internal/abi.Method" %1391, ptr %1915, align 8
  %1916 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 19
  store %"github.com/goplus/llgo/internal/abi.Method" %1402, ptr %1916, align 8
  %1917 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 20
  store %"github.com/goplus/llgo/internal/abi.Method" %1884, ptr %1917, align 8
  %1918 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %1896, i64 21
  store %"github.com/goplus/llgo/internal/abi.Method" %1895, ptr %1918, align 8
  %1919 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1920 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1919, i32 0, i32 0
  store ptr %1896, ptr %1920, align 8
  %1921 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1919, i32 0, i32 1
  store i64 22, ptr %1921, align 4
  %1922 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1919, i32 0, i32 2
  store i64 22, ptr %1922, align 4
  %1923 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1919, align 8
  %1924 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1925 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1924, i32 0, i32 0
  store ptr @46, ptr %1925, align 8
  %1926 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1924, i32 0, i32 1
  store i64 35, ptr %1926, align 4
  %1927 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1924, align 8
  %1928 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1929 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1928, i32 0, i32 0
  store ptr @47, ptr %1929, align 8
  %1930 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1928, i32 0, i32 1
  store i64 7, ptr %1930, align 4
  %1931 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1928, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %971, %"github.com/goplus/llgo/internal/runtime.String" %1927, %"github.com/goplus/llgo/internal/runtime.String" %1931, ptr %1158, { ptr, i64, i64 } zeroinitializer, %"github.com/goplus/llgo/internal/runtime.Slice" %1923)
  br label %_llgo_58

_llgo_85:                                         ; preds = %_llgo_58
  %1932 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %1368)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %1932)
  store ptr %1932, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.MapType", align 8
  br label %_llgo_86

_llgo_86:                                         ; preds = %_llgo_85, %_llgo_58
  %1933 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.MapType", align 8
  %1934 = load ptr, ptr @"_llgo_func$d-NlqnjcQnaMjsBQY7qh2SWQmHb0XIigoceXdiJ8YT4", align 8
  %1935 = icmp eq ptr %1934, null
  br i1 %1935, label %_llgo_87, label %_llgo_88

_llgo_87:                                         ; preds = %_llgo_86
  %1936 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %1937 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1938 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1937, i32 0, i32 0
  store ptr %1936, ptr %1938, align 8
  %1939 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1937, i32 0, i32 1
  store i64 0, ptr %1939, align 4
  %1940 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1937, i32 0, i32 2
  store i64 0, ptr %1940, align 4
  %1941 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1937, align 8
  %1942 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %1943 = getelementptr ptr, ptr %1942, i64 0
  store ptr %1933, ptr %1943, align 8
  %1944 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %1945 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1944, i32 0, i32 0
  store ptr %1942, ptr %1945, align 8
  %1946 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1944, i32 0, i32 1
  store i64 1, ptr %1946, align 4
  %1947 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1944, i32 0, i32 2
  store i64 1, ptr %1947, align 4
  %1948 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %1944, align 8
  %1949 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %1941, %"github.com/goplus/llgo/internal/runtime.Slice" %1948, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %1949)
  store ptr %1949, ptr @"_llgo_func$d-NlqnjcQnaMjsBQY7qh2SWQmHb0XIigoceXdiJ8YT4", align 8
  br label %_llgo_88

_llgo_88:                                         ; preds = %_llgo_87, %_llgo_86
  %1950 = load ptr, ptr @"_llgo_func$d-NlqnjcQnaMjsBQY7qh2SWQmHb0XIigoceXdiJ8YT4", align 8
  %1951 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1952 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1951, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %966, ptr %1952, align 8
  %1953 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1951, i32 0, i32 1
  store ptr %1950, ptr %1953, align 8
  %1954 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1951, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).MapType", ptr %1954, align 8
  %1955 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1951, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).MapType", ptr %1955, align 8
  %1956 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1951, align 8
  %1957 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1958 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1957, i32 0, i32 0
  store ptr @60, ptr %1958, align 8
  %1959 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1957, i32 0, i32 1
  store i64 8, ptr %1959, align 4
  %1960 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1957, align 8
  %1961 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %1962 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1963 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1962, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1960, ptr %1963, align 8
  %1964 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1962, i32 0, i32 1
  store ptr %1961, ptr %1964, align 8
  %1965 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1962, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Pointers", ptr %1965, align 8
  %1966 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1962, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Pointers", ptr %1966, align 8
  %1967 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1962, align 8
  %1968 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1969 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1968, i32 0, i32 0
  store ptr @62, ptr %1969, align 8
  %1970 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1968, i32 0, i32 1
  store i64 4, ptr %1970, align 4
  %1971 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1968, align 8
  %1972 = load ptr, ptr @"_llgo_func$1kITCsyu7hFLMxHLR7kDlvu4SOra_HtrtdFUQH9P13s", align 8
  %1973 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1974 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1973, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1971, ptr %1974, align 8
  %1975 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1973, i32 0, i32 1
  store ptr %1972, ptr %1975, align 8
  %1976 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1973, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Size", ptr %1976, align 8
  %1977 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1973, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Size", ptr %1977, align 8
  %1978 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1973, align 8
  %1979 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1980 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1979, i32 0, i32 0
  store ptr @45, ptr %1980, align 8
  %1981 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1979, i32 0, i32 1
  store i64 6, ptr %1981, align 4
  %1982 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1979, align 8
  %1983 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %1984 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1985 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1984, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1982, ptr %1985, align 8
  %1986 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1984, i32 0, i32 1
  store ptr %1983, ptr %1986, align 8
  %1987 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1984, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).String", ptr %1987, align 8
  %1988 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1984, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).String", ptr %1988, align 8
  %1989 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1984, align 8
  %1990 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %1991 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1990, i32 0, i32 0
  store ptr @63, ptr %1991, align 8
  %1992 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %1990, i32 0, i32 1
  store i64 10, ptr %1992, align 4
  %1993 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %1990, align 8
  %1994 = load ptr, ptr @"_llgo_func$qiNnn6Cbm3GtDp4gDI4U_DRV3h8zlz91s9jrfOXC--U", align 8
  %1995 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %1996 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1995, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %1993, ptr %1996, align 8
  %1997 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1995, i32 0, i32 1
  store ptr %1994, ptr %1997, align 8
  %1998 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1995, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).StructType", ptr %1998, align 8
  %1999 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %1995, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).StructType", ptr %1999, align 8
  %2000 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %1995, align 8
  %2001 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2002 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2001, i32 0, i32 0
  store ptr @67, ptr %2002, align 8
  %2003 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2001, i32 0, i32 1
  store i64 8, ptr %2003, align 4
  %2004 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2001, align 8
  %2005 = load ptr, ptr @"_llgo_func$DbD4nZv_bjE4tH8hh-VfAjMXMpNfIsMlLJJJPKupp34", align 8
  %2006 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2007 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2006, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2004, ptr %2007, align 8
  %2008 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2006, i32 0, i32 1
  store ptr %2005, ptr %2008, align 8
  %2009 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2006, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Uncommon", ptr %2009, align 8
  %2010 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2006, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Uncommon", ptr %2010, align 8
  %2011 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2006, align 8
  %2012 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 720)
  %2013 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %747, ptr %2013, align 8
  %2014 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 1
  store %"github.com/goplus/llgo/internal/abi.Method" %758, ptr %2014, align 8
  %2015 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 2
  store %"github.com/goplus/llgo/internal/abi.Method" %769, ptr %2015, align 8
  %2016 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 3
  store %"github.com/goplus/llgo/internal/abi.Method" %780, ptr %2016, align 8
  %2017 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 4
  store %"github.com/goplus/llgo/internal/abi.Method" %791, ptr %2017, align 8
  %2018 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 5
  store %"github.com/goplus/llgo/internal/abi.Method" %802, ptr %2018, align 8
  %2019 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 6
  store %"github.com/goplus/llgo/internal/abi.Method" %813, ptr %2019, align 8
  %2020 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 7
  store %"github.com/goplus/llgo/internal/abi.Method" %824, ptr %2020, align 8
  %2021 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 8
  store %"github.com/goplus/llgo/internal/abi.Method" %835, ptr %2021, align 8
  %2022 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 9
  store %"github.com/goplus/llgo/internal/abi.Method" %846, ptr %2022, align 8
  %2023 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 10
  store %"github.com/goplus/llgo/internal/abi.Method" %951, ptr %2023, align 8
  %2024 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 11
  store %"github.com/goplus/llgo/internal/abi.Method" %962, ptr %2024, align 8
  %2025 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 12
  store %"github.com/goplus/llgo/internal/abi.Method" %1956, ptr %2025, align 8
  %2026 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 13
  store %"github.com/goplus/llgo/internal/abi.Method" %1967, ptr %2026, align 8
  %2027 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 14
  store %"github.com/goplus/llgo/internal/abi.Method" %1978, ptr %2027, align 8
  %2028 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 15
  store %"github.com/goplus/llgo/internal/abi.Method" %1989, ptr %2028, align 8
  %2029 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 16
  store %"github.com/goplus/llgo/internal/abi.Method" %2000, ptr %2029, align 8
  %2030 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2012, i64 17
  store %"github.com/goplus/llgo/internal/abi.Method" %2011, ptr %2030, align 8
  %2031 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %2032 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2031, i32 0, i32 0
  store ptr %2012, ptr %2032, align 8
  %2033 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2031, i32 0, i32 1
  store i64 18, ptr %2033, align 4
  %2034 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2031, i32 0, i32 2
  store i64 18, ptr %2034, align 4
  %2035 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2031, align 8
  %2036 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2037 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2036, i32 0, i32 0
  store ptr @46, ptr %2037, align 8
  %2038 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2036, i32 0, i32 1
  store i64 35, ptr %2038, align 4
  %2039 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2036, align 8
  %2040 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2041 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2040, i32 0, i32 0
  store ptr @37, ptr %2041, align 8
  %2042 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2040, i32 0, i32 1
  store i64 13, ptr %2042, align 4
  %2043 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2040, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %678, %"github.com/goplus/llgo/internal/runtime.String" %2039, %"github.com/goplus/llgo/internal/runtime.String" %2043, ptr %736, { ptr, i64, i64 } zeroinitializer, %"github.com/goplus/llgo/internal/runtime.Slice" %2035)
  br label %_llgo_40

_llgo_89:                                         ; preds = %_llgo_40
  %2044 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %862)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %2044)
  store ptr %2044, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.InterfaceType", align 8
  br label %_llgo_90

_llgo_90:                                         ; preds = %_llgo_89, %_llgo_40
  %2045 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.InterfaceType", align 8
  %2046 = load ptr, ptr @"_llgo_func$1QmforOaCy2fBAssC2y1FWCCT6fpq9RKwP2j2HIASY8", align 8
  %2047 = icmp eq ptr %2046, null
  br i1 %2047, label %_llgo_91, label %_llgo_92

_llgo_91:                                         ; preds = %_llgo_90
  %2048 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %2049 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %2050 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2049, i32 0, i32 0
  store ptr %2048, ptr %2050, align 8
  %2051 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2049, i32 0, i32 1
  store i64 0, ptr %2051, align 4
  %2052 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2049, i32 0, i32 2
  store i64 0, ptr %2052, align 4
  %2053 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2049, align 8
  %2054 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %2055 = getelementptr ptr, ptr %2054, i64 0
  store ptr %2045, ptr %2055, align 8
  %2056 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %2057 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2056, i32 0, i32 0
  store ptr %2054, ptr %2057, align 8
  %2058 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2056, i32 0, i32 1
  store i64 1, ptr %2058, align 4
  %2059 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2056, i32 0, i32 2
  store i64 1, ptr %2059, align 4
  %2060 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2056, align 8
  %2061 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %2053, %"github.com/goplus/llgo/internal/runtime.Slice" %2060, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %2061)
  store ptr %2061, ptr @"_llgo_func$1QmforOaCy2fBAssC2y1FWCCT6fpq9RKwP2j2HIASY8", align 8
  br label %_llgo_92

_llgo_92:                                         ; preds = %_llgo_91, %_llgo_90
  %2062 = load ptr, ptr @"_llgo_func$1QmforOaCy2fBAssC2y1FWCCT6fpq9RKwP2j2HIASY8", align 8
  %2063 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2064 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2063, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %673, ptr %2064, align 8
  %2065 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2063, i32 0, i32 1
  store ptr %2062, ptr %2065, align 8
  %2066 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2063, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).InterfaceType", ptr %2066, align 8
  %2067 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2063, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).InterfaceType", ptr %2067, align 8
  %2068 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2063, align 8
  %2069 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2070 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2069, i32 0, i32 0
  store ptr @42, ptr %2070, align 8
  %2071 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2069, i32 0, i32 1
  store i64 13, ptr %2071, align 4
  %2072 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2069, align 8
  %2073 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2074 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2075 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2074, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2072, ptr %2075, align 8
  %2076 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2074, i32 0, i32 1
  store ptr %2073, ptr %2076, align 8
  %2077 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2074, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).IsDirectIface", ptr %2077, align 8
  %2078 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2074, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).IsDirectIface", ptr %2078, align 8
  %2079 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2074, align 8
  %2080 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2081 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2080, i32 0, i32 0
  store ptr @43, ptr %2081, align 8
  %2082 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2080, i32 0, i32 1
  store i64 4, ptr %2082, align 4
  %2083 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2080, align 8
  %2084 = load ptr, ptr @"_llgo_func$ntUE0UmVAWPS2O7GpCCGszSn-XnjHJntZZ2jYtwbFXI", align 8
  %2085 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2086 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2085, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2083, ptr %2086, align 8
  %2087 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2085, i32 0, i32 1
  store ptr %2084, ptr %2087, align 8
  %2088 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2085, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Kind", ptr %2088, align 8
  %2089 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2085, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Kind", ptr %2089, align 8
  %2090 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2085, align 8
  %2091 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2092 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2091, i32 0, i32 0
  store ptr @28, ptr %2092, align 8
  %2093 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2091, i32 0, i32 1
  store i64 3, ptr %2093, align 4
  %2094 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2091, align 8
  %2095 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %2096 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2097 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2096, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2094, ptr %2097, align 8
  %2098 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2096, i32 0, i32 1
  store ptr %2095, ptr %2098, align 8
  %2099 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2096, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Len", ptr %2099, align 8
  %2100 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2096, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Len", ptr %2100, align 8
  %2101 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2096, align 8
  %2102 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2103 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2102, i32 0, i32 0
  store ptr @47, ptr %2103, align 8
  %2104 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2102, i32 0, i32 1
  store i64 7, ptr %2104, align 4
  %2105 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2102, align 8
  %2106 = load ptr, ptr @"_llgo_func$d-NlqnjcQnaMjsBQY7qh2SWQmHb0XIigoceXdiJ8YT4", align 8
  %2107 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2108 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2107, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2105, ptr %2108, align 8
  %2109 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2107, i32 0, i32 1
  store ptr %2106, ptr %2109, align 8
  %2110 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2107, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).MapType", ptr %2110, align 8
  %2111 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2107, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).MapType", ptr %2111, align 8
  %2112 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2107, align 8
  %2113 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2114 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2113, i32 0, i32 0
  store ptr @60, ptr %2114, align 8
  %2115 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2113, i32 0, i32 1
  store i64 8, ptr %2115, align 4
  %2116 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2113, align 8
  %2117 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2118 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2119 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2118, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2116, ptr %2119, align 8
  %2120 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2118, i32 0, i32 1
  store ptr %2117, ptr %2120, align 8
  %2121 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2118, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Pointers", ptr %2121, align 8
  %2122 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2118, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Pointers", ptr %2122, align 8
  %2123 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2118, align 8
  %2124 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2125 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2124, i32 0, i32 0
  store ptr @62, ptr %2125, align 8
  %2126 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2124, i32 0, i32 1
  store i64 4, ptr %2126, align 4
  %2127 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2124, align 8
  %2128 = load ptr, ptr @"_llgo_func$1kITCsyu7hFLMxHLR7kDlvu4SOra_HtrtdFUQH9P13s", align 8
  %2129 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2130 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2129, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2127, ptr %2130, align 8
  %2131 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2129, i32 0, i32 1
  store ptr %2128, ptr %2131, align 8
  %2132 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2129, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Size", ptr %2132, align 8
  %2133 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2129, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Size", ptr %2133, align 8
  %2134 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2129, align 8
  %2135 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2136 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2135, i32 0, i32 0
  store ptr @45, ptr %2136, align 8
  %2137 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2135, i32 0, i32 1
  store i64 6, ptr %2137, align 4
  %2138 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2135, align 8
  %2139 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %2140 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2141 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2140, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2138, ptr %2141, align 8
  %2142 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2140, i32 0, i32 1
  store ptr %2139, ptr %2142, align 8
  %2143 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2140, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).String", ptr %2143, align 8
  %2144 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2140, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).String", ptr %2144, align 8
  %2145 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2140, align 8
  %2146 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2147 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2146, i32 0, i32 0
  store ptr @63, ptr %2147, align 8
  %2148 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2146, i32 0, i32 1
  store i64 10, ptr %2148, align 4
  %2149 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2146, align 8
  %2150 = load ptr, ptr @"_llgo_func$qiNnn6Cbm3GtDp4gDI4U_DRV3h8zlz91s9jrfOXC--U", align 8
  %2151 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2152 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2151, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2149, ptr %2152, align 8
  %2153 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2151, i32 0, i32 1
  store ptr %2150, ptr %2153, align 8
  %2154 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2151, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).StructType", ptr %2154, align 8
  %2155 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2151, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).StructType", ptr %2155, align 8
  %2156 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2151, align 8
  %2157 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2158 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2157, i32 0, i32 0
  store ptr @67, ptr %2158, align 8
  %2159 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2157, i32 0, i32 1
  store i64 8, ptr %2159, align 4
  %2160 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2157, align 8
  %2161 = load ptr, ptr @"_llgo_func$DbD4nZv_bjE4tH8hh-VfAjMXMpNfIsMlLJJJPKupp34", align 8
  %2162 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2163 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2162, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2160, ptr %2163, align 8
  %2164 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2162, i32 0, i32 1
  store ptr %2161, ptr %2164, align 8
  %2165 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2162, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Uncommon", ptr %2165, align 8
  %2166 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2162, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Uncommon", ptr %2166, align 8
  %2167 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2162, align 8
  %2168 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2169 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2168, i32 0, i32 0
  store ptr @75, ptr %2169, align 8
  %2170 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2168, i32 0, i32 1
  store i64 8, ptr %2170, align 4
  %2171 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2168, align 8
  %2172 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2173 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2174 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2173, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2171, ptr %2174, align 8
  %2175 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2173, i32 0, i32 1
  store ptr %2172, ptr %2175, align 8
  %2176 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2173, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Variadic", ptr %2176, align 8
  %2177 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2173, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Variadic", ptr %2177, align 8
  %2178 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2173, align 8
  %2179 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 760)
  %2180 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %568, ptr %2180, align 8
  %2181 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 1
  store %"github.com/goplus/llgo/internal/abi.Method" %579, ptr %2181, align 8
  %2182 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 2
  store %"github.com/goplus/llgo/internal/abi.Method" %590, ptr %2182, align 8
  %2183 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 3
  store %"github.com/goplus/llgo/internal/abi.Method" %601, ptr %2183, align 8
  %2184 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 4
  store %"github.com/goplus/llgo/internal/abi.Method" %612, ptr %2184, align 8
  %2185 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 5
  store %"github.com/goplus/llgo/internal/abi.Method" %623, ptr %2185, align 8
  %2186 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 6
  store %"github.com/goplus/llgo/internal/abi.Method" %658, ptr %2186, align 8
  %2187 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 7
  store %"github.com/goplus/llgo/internal/abi.Method" %669, ptr %2187, align 8
  %2188 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 8
  store %"github.com/goplus/llgo/internal/abi.Method" %2068, ptr %2188, align 8
  %2189 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 9
  store %"github.com/goplus/llgo/internal/abi.Method" %2079, ptr %2189, align 8
  %2190 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 10
  store %"github.com/goplus/llgo/internal/abi.Method" %2090, ptr %2190, align 8
  %2191 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 11
  store %"github.com/goplus/llgo/internal/abi.Method" %2101, ptr %2191, align 8
  %2192 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 12
  store %"github.com/goplus/llgo/internal/abi.Method" %2112, ptr %2192, align 8
  %2193 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 13
  store %"github.com/goplus/llgo/internal/abi.Method" %2123, ptr %2193, align 8
  %2194 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 14
  store %"github.com/goplus/llgo/internal/abi.Method" %2134, ptr %2194, align 8
  %2195 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 15
  store %"github.com/goplus/llgo/internal/abi.Method" %2145, ptr %2195, align 8
  %2196 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 16
  store %"github.com/goplus/llgo/internal/abi.Method" %2156, ptr %2196, align 8
  %2197 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 17
  store %"github.com/goplus/llgo/internal/abi.Method" %2167, ptr %2197, align 8
  %2198 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2179, i64 18
  store %"github.com/goplus/llgo/internal/abi.Method" %2178, ptr %2198, align 8
  %2199 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %2200 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2199, i32 0, i32 0
  store ptr %2179, ptr %2200, align 8
  %2201 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2199, i32 0, i32 1
  store i64 19, ptr %2201, align 4
  %2202 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2199, i32 0, i32 2
  store i64 19, ptr %2202, align 4
  %2203 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2199, align 8
  %2204 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2205 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2204, i32 0, i32 0
  store ptr @46, ptr %2205, align 8
  %2206 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2204, i32 0, i32 1
  store i64 35, ptr %2206, align 4
  %2207 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2204, align 8
  %2208 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2209 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2208, i32 0, i32 0
  store ptr @31, ptr %2209, align 8
  %2210 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2208, i32 0, i32 1
  store i64 8, ptr %2210, align 4
  %2211 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2208, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %492, %"github.com/goplus/llgo/internal/runtime.String" %2207, %"github.com/goplus/llgo/internal/runtime.String" %2211, ptr %557, { ptr, i64, i64 } zeroinitializer, %"github.com/goplus/llgo/internal/runtime.Slice" %2203)
  br label %_llgo_32

_llgo_93:                                         ; preds = %_llgo_32
  %2212 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %635)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %2212)
  store ptr %2212, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.FuncType", align 8
  br label %_llgo_94

_llgo_94:                                         ; preds = %_llgo_93, %_llgo_32
  %2213 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.FuncType", align 8
  %2214 = load ptr, ptr @"_llgo_func$DsoxgOnxqV7tLvokF3AA14v1gtHsHaThoC8Q_XGcQww", align 8
  %2215 = icmp eq ptr %2214, null
  br i1 %2215, label %_llgo_95, label %_llgo_96

_llgo_95:                                         ; preds = %_llgo_94
  %2216 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %2217 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %2218 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2217, i32 0, i32 0
  store ptr %2216, ptr %2218, align 8
  %2219 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2217, i32 0, i32 1
  store i64 0, ptr %2219, align 4
  %2220 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2217, i32 0, i32 2
  store i64 0, ptr %2220, align 4
  %2221 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2217, align 8
  %2222 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %2223 = getelementptr ptr, ptr %2222, i64 0
  store ptr %2213, ptr %2223, align 8
  %2224 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %2225 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2224, i32 0, i32 0
  store ptr %2222, ptr %2225, align 8
  %2226 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2224, i32 0, i32 1
  store i64 1, ptr %2226, align 4
  %2227 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2224, i32 0, i32 2
  store i64 1, ptr %2227, align 4
  %2228 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2224, align 8
  %2229 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %2221, %"github.com/goplus/llgo/internal/runtime.Slice" %2228, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %2229)
  store ptr %2229, ptr @"_llgo_func$DsoxgOnxqV7tLvokF3AA14v1gtHsHaThoC8Q_XGcQww", align 8
  br label %_llgo_96

_llgo_96:                                         ; preds = %_llgo_95, %_llgo_94
  %2230 = load ptr, ptr @"_llgo_func$DsoxgOnxqV7tLvokF3AA14v1gtHsHaThoC8Q_XGcQww", align 8
  %2231 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2232 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2231, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %487, ptr %2232, align 8
  %2233 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2231, i32 0, i32 1
  store ptr %2230, ptr %2233, align 8
  %2234 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2231, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).FuncType", ptr %2234, align 8
  %2235 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2231, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).FuncType", ptr %2235, align 8
  %2236 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2231, align 8
  %2237 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2238 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2237, i32 0, i32 0
  store ptr @35, ptr %2238, align 8
  %2239 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2237, i32 0, i32 1
  store i64 7, ptr %2239, align 4
  %2240 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2237, align 8
  %2241 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2242 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2243 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2242, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2240, ptr %2243, align 8
  %2244 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2242, i32 0, i32 1
  store ptr %2241, ptr %2244, align 8
  %2245 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2242, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).HasName", ptr %2245, align 8
  %2246 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2242, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).HasName", ptr %2246, align 8
  %2247 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2242, align 8
  %2248 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2249 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2248, i32 0, i32 0
  store ptr @36, ptr %2249, align 8
  %2250 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2248, i32 0, i32 1
  store i64 10, ptr %2250, align 4
  %2251 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2248, align 8
  %2252 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2253 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2254 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2253, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2251, ptr %2254, align 8
  %2255 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2253, i32 0, i32 1
  store ptr %2252, ptr %2255, align 8
  %2256 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2253, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).IfaceIndir", ptr %2256, align 8
  %2257 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2253, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).IfaceIndir", ptr %2257, align 8
  %2258 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2253, align 8
  %2259 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2260 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2259, i32 0, i32 0
  store ptr @37, ptr %2260, align 8
  %2261 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2259, i32 0, i32 1
  store i64 13, ptr %2261, align 4
  %2262 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2259, align 8
  %2263 = load ptr, ptr @"_llgo_func$1QmforOaCy2fBAssC2y1FWCCT6fpq9RKwP2j2HIASY8", align 8
  %2264 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2265 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2264, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2262, ptr %2265, align 8
  %2266 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2264, i32 0, i32 1
  store ptr %2263, ptr %2266, align 8
  %2267 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2264, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).InterfaceType", ptr %2267, align 8
  %2268 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2264, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).InterfaceType", ptr %2268, align 8
  %2269 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2264, align 8
  %2270 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2271 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2270, i32 0, i32 0
  store ptr @42, ptr %2271, align 8
  %2272 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2270, i32 0, i32 1
  store i64 13, ptr %2272, align 4
  %2273 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2270, align 8
  %2274 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2275 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2276 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2275, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2273, ptr %2276, align 8
  %2277 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2275, i32 0, i32 1
  store ptr %2274, ptr %2277, align 8
  %2278 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2275, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).IsDirectIface", ptr %2278, align 8
  %2279 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2275, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).IsDirectIface", ptr %2279, align 8
  %2280 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2275, align 8
  %2281 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2282 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2281, i32 0, i32 0
  store ptr @43, ptr %2282, align 8
  %2283 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2281, i32 0, i32 1
  store i64 4, ptr %2283, align 4
  %2284 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2281, align 8
  %2285 = load ptr, ptr @"_llgo_func$ntUE0UmVAWPS2O7GpCCGszSn-XnjHJntZZ2jYtwbFXI", align 8
  %2286 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2287 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2286, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2284, ptr %2287, align 8
  %2288 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2286, i32 0, i32 1
  store ptr %2285, ptr %2288, align 8
  %2289 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2286, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Kind", ptr %2289, align 8
  %2290 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2286, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Kind", ptr %2290, align 8
  %2291 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2286, align 8
  %2292 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2293 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2292, i32 0, i32 0
  store ptr @47, ptr %2293, align 8
  %2294 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2292, i32 0, i32 1
  store i64 7, ptr %2294, align 4
  %2295 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2292, align 8
  %2296 = load ptr, ptr @"_llgo_func$d-NlqnjcQnaMjsBQY7qh2SWQmHb0XIigoceXdiJ8YT4", align 8
  %2297 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2298 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2297, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2295, ptr %2298, align 8
  %2299 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2297, i32 0, i32 1
  store ptr %2296, ptr %2299, align 8
  %2300 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2297, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).MapType", ptr %2300, align 8
  %2301 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2297, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).MapType", ptr %2301, align 8
  %2302 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2297, align 8
  %2303 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2304 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2303, i32 0, i32 0
  store ptr @60, ptr %2304, align 8
  %2305 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2303, i32 0, i32 1
  store i64 8, ptr %2305, align 4
  %2306 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2303, align 8
  %2307 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2308 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2309 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2308, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2306, ptr %2309, align 8
  %2310 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2308, i32 0, i32 1
  store ptr %2307, ptr %2310, align 8
  %2311 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2308, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Pointers", ptr %2311, align 8
  %2312 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2308, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Pointers", ptr %2312, align 8
  %2313 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2308, align 8
  %2314 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2315 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2314, i32 0, i32 0
  store ptr @62, ptr %2315, align 8
  %2316 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2314, i32 0, i32 1
  store i64 4, ptr %2316, align 4
  %2317 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2314, align 8
  %2318 = load ptr, ptr @"_llgo_func$1kITCsyu7hFLMxHLR7kDlvu4SOra_HtrtdFUQH9P13s", align 8
  %2319 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2320 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2319, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2317, ptr %2320, align 8
  %2321 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2319, i32 0, i32 1
  store ptr %2318, ptr %2321, align 8
  %2322 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2319, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Size", ptr %2322, align 8
  %2323 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2319, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Size", ptr %2323, align 8
  %2324 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2319, align 8
  %2325 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2326 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2325, i32 0, i32 0
  store ptr @45, ptr %2326, align 8
  %2327 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2325, i32 0, i32 1
  store i64 6, ptr %2327, align 4
  %2328 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2325, align 8
  %2329 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %2330 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2331 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2330, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2328, ptr %2331, align 8
  %2332 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2330, i32 0, i32 1
  store ptr %2329, ptr %2332, align 8
  %2333 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2330, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).String", ptr %2333, align 8
  %2334 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2330, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).String", ptr %2334, align 8
  %2335 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2330, align 8
  %2336 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2337 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2336, i32 0, i32 0
  store ptr @63, ptr %2337, align 8
  %2338 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2336, i32 0, i32 1
  store i64 10, ptr %2338, align 4
  %2339 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2336, align 8
  %2340 = load ptr, ptr @"_llgo_func$qiNnn6Cbm3GtDp4gDI4U_DRV3h8zlz91s9jrfOXC--U", align 8
  %2341 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2342 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2341, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2339, ptr %2342, align 8
  %2343 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2341, i32 0, i32 1
  store ptr %2340, ptr %2343, align 8
  %2344 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2341, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).StructType", ptr %2344, align 8
  %2345 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2341, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).StructType", ptr %2345, align 8
  %2346 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2341, align 8
  %2347 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2348 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2347, i32 0, i32 0
  store ptr @67, ptr %2348, align 8
  %2349 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2347, i32 0, i32 1
  store i64 8, ptr %2349, align 4
  %2350 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2347, align 8
  %2351 = load ptr, ptr @"_llgo_func$DbD4nZv_bjE4tH8hh-VfAjMXMpNfIsMlLJJJPKupp34", align 8
  %2352 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2353 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2352, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2350, ptr %2353, align 8
  %2354 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2352, i32 0, i32 1
  store ptr %2351, ptr %2354, align 8
  %2355 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2352, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Uncommon", ptr %2355, align 8
  %2356 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2352, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Uncommon", ptr %2356, align 8
  %2357 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2352, align 8
  %2358 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 640)
  %2359 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %418, ptr %2359, align 8
  %2360 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 1
  store %"github.com/goplus/llgo/internal/abi.Method" %429, ptr %2360, align 8
  %2361 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 2
  store %"github.com/goplus/llgo/internal/abi.Method" %472, ptr %2361, align 8
  %2362 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 3
  store %"github.com/goplus/llgo/internal/abi.Method" %483, ptr %2362, align 8
  %2363 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 4
  store %"github.com/goplus/llgo/internal/abi.Method" %2236, ptr %2363, align 8
  %2364 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 5
  store %"github.com/goplus/llgo/internal/abi.Method" %2247, ptr %2364, align 8
  %2365 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 6
  store %"github.com/goplus/llgo/internal/abi.Method" %2258, ptr %2365, align 8
  %2366 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 7
  store %"github.com/goplus/llgo/internal/abi.Method" %2269, ptr %2366, align 8
  %2367 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 8
  store %"github.com/goplus/llgo/internal/abi.Method" %2280, ptr %2367, align 8
  %2368 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 9
  store %"github.com/goplus/llgo/internal/abi.Method" %2291, ptr %2368, align 8
  %2369 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 10
  store %"github.com/goplus/llgo/internal/abi.Method" %2302, ptr %2369, align 8
  %2370 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 11
  store %"github.com/goplus/llgo/internal/abi.Method" %2313, ptr %2370, align 8
  %2371 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 12
  store %"github.com/goplus/llgo/internal/abi.Method" %2324, ptr %2371, align 8
  %2372 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 13
  store %"github.com/goplus/llgo/internal/abi.Method" %2335, ptr %2372, align 8
  %2373 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 14
  store %"github.com/goplus/llgo/internal/abi.Method" %2346, ptr %2373, align 8
  %2374 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2358, i64 15
  store %"github.com/goplus/llgo/internal/abi.Method" %2357, ptr %2374, align 8
  %2375 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %2376 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2375, i32 0, i32 0
  store ptr %2358, ptr %2376, align 8
  %2377 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2375, i32 0, i32 1
  store i64 16, ptr %2377, align 4
  %2378 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2375, i32 0, i32 2
  store i64 16, ptr %2378, align 4
  %2379 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2375, align 8
  %2380 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2381 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2380, i32 0, i32 0
  store ptr @46, ptr %2381, align 8
  %2382 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2380, i32 0, i32 1
  store i64 35, ptr %2382, align 4
  %2383 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2380, align 8
  %2384 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2385 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2384, i32 0, i32 0
  store ptr @23, ptr %2385, align 8
  %2386 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2384, i32 0, i32 1
  store i64 9, ptr %2386, align 4
  %2387 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2384, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %333, %"github.com/goplus/llgo/internal/runtime.String" %2383, %"github.com/goplus/llgo/internal/runtime.String" %2387, ptr %407, { ptr, i64, i64 } zeroinitializer, %"github.com/goplus/llgo/internal/runtime.Slice" %2379)
  br label %_llgo_22

_llgo_97:                                         ; preds = %_llgo_22
  %2388 = call ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr %445)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %2388)
  store ptr %2388, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.ArrayType", align 8
  br label %_llgo_98

_llgo_98:                                         ; preds = %_llgo_97, %_llgo_22
  %2389 = load ptr, ptr @"*_llgo_github.com/goplus/llgo/internal/abi.ArrayType", align 8
  %2390 = load ptr, ptr @"_llgo_func$CsVqlCxhoEcIvPD5BSBukfSiD9C7Ic5_Gf32MLbCWB4", align 8
  %2391 = icmp eq ptr %2390, null
  br i1 %2391, label %_llgo_99, label %_llgo_100

_llgo_99:                                         ; preds = %_llgo_98
  %2392 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 0)
  %2393 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %2394 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2393, i32 0, i32 0
  store ptr %2392, ptr %2394, align 8
  %2395 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2393, i32 0, i32 1
  store i64 0, ptr %2395, align 4
  %2396 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2393, i32 0, i32 2
  store i64 0, ptr %2396, align 4
  %2397 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2393, align 8
  %2398 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 8)
  %2399 = getelementptr ptr, ptr %2398, i64 0
  store ptr %2389, ptr %2399, align 8
  %2400 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %2401 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2400, i32 0, i32 0
  store ptr %2398, ptr %2401, align 8
  %2402 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2400, i32 0, i32 1
  store i64 1, ptr %2402, align 4
  %2403 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2400, i32 0, i32 2
  store i64 1, ptr %2403, align 4
  %2404 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2400, align 8
  %2405 = call ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice" %2397, %"github.com/goplus/llgo/internal/runtime.Slice" %2404, i1 false)
  call void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr %2405)
  store ptr %2405, ptr @"_llgo_func$CsVqlCxhoEcIvPD5BSBukfSiD9C7Ic5_Gf32MLbCWB4", align 8
  br label %_llgo_100

_llgo_100:                                        ; preds = %_llgo_99, %_llgo_98
  %2406 = load ptr, ptr @"_llgo_func$CsVqlCxhoEcIvPD5BSBukfSiD9C7Ic5_Gf32MLbCWB4", align 8
  %2407 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2408 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2407, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %328, ptr %2408, align 8
  %2409 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2407, i32 0, i32 1
  store ptr %2406, ptr %2409, align 8
  %2410 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2407, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).ArrayType", ptr %2410, align 8
  %2411 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2407, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).ArrayType", ptr %2411, align 8
  %2412 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2407, align 8
  %2413 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2414 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2413, i32 0, i32 0
  store ptr @29, ptr %2414, align 8
  %2415 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2413, i32 0, i32 1
  store i64 6, ptr %2415, align 4
  %2416 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2413, align 8
  %2417 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %2418 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2419 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2418, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2416, ptr %2419, align 8
  %2420 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2418, i32 0, i32 1
  store ptr %2417, ptr %2420, align 8
  %2421 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2418, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Common", ptr %2421, align 8
  %2422 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2418, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Common", ptr %2422, align 8
  %2423 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2418, align 8
  %2424 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2425 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2424, i32 0, i32 0
  store ptr @26, ptr %2425, align 8
  %2426 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2424, i32 0, i32 1
  store i64 4, ptr %2426, align 4
  %2427 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2424, align 8
  %2428 = load ptr, ptr @"_llgo_func$4-mqItKfDlL0CgVKnUxoresYgh6zW1WSlZYZSsVzLRo", align 8
  %2429 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2430 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2429, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2427, ptr %2430, align 8
  %2431 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2429, i32 0, i32 1
  store ptr %2428, ptr %2431, align 8
  %2432 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2429, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Elem", ptr %2432, align 8
  %2433 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2429, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Elem", ptr %2433, align 8
  %2434 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2429, align 8
  %2435 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2436 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2435, i32 0, i32 0
  store ptr @30, ptr %2436, align 8
  %2437 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2435, i32 0, i32 1
  store i64 10, ptr %2437, align 4
  %2438 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2435, align 8
  %2439 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %2440 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2441 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2440, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2438, ptr %2441, align 8
  %2442 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2440, i32 0, i32 1
  store ptr %2439, ptr %2442, align 8
  %2443 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2440, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).FieldAlign", ptr %2443, align 8
  %2444 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2440, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).FieldAlign", ptr %2444, align 8
  %2445 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2440, align 8
  %2446 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2447 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2446, i32 0, i32 0
  store ptr @31, ptr %2447, align 8
  %2448 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2446, i32 0, i32 1
  store i64 8, ptr %2448, align 4
  %2449 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2446, align 8
  %2450 = load ptr, ptr @"_llgo_func$DsoxgOnxqV7tLvokF3AA14v1gtHsHaThoC8Q_XGcQww", align 8
  %2451 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2452 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2451, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2449, ptr %2452, align 8
  %2453 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2451, i32 0, i32 1
  store ptr %2450, ptr %2453, align 8
  %2454 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2451, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).FuncType", ptr %2454, align 8
  %2455 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2451, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).FuncType", ptr %2455, align 8
  %2456 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2451, align 8
  %2457 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2458 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2457, i32 0, i32 0
  store ptr @35, ptr %2458, align 8
  %2459 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2457, i32 0, i32 1
  store i64 7, ptr %2459, align 4
  %2460 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2457, align 8
  %2461 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2462 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2463 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2462, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2460, ptr %2463, align 8
  %2464 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2462, i32 0, i32 1
  store ptr %2461, ptr %2464, align 8
  %2465 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2462, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).HasName", ptr %2465, align 8
  %2466 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2462, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).HasName", ptr %2466, align 8
  %2467 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2462, align 8
  %2468 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2469 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2468, i32 0, i32 0
  store ptr @36, ptr %2469, align 8
  %2470 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2468, i32 0, i32 1
  store i64 10, ptr %2470, align 4
  %2471 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2468, align 8
  %2472 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2473 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2474 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2473, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2471, ptr %2474, align 8
  %2475 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2473, i32 0, i32 1
  store ptr %2472, ptr %2475, align 8
  %2476 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2473, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).IfaceIndir", ptr %2476, align 8
  %2477 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2473, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).IfaceIndir", ptr %2477, align 8
  %2478 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2473, align 8
  %2479 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2480 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2479, i32 0, i32 0
  store ptr @37, ptr %2480, align 8
  %2481 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2479, i32 0, i32 1
  store i64 13, ptr %2481, align 4
  %2482 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2479, align 8
  %2483 = load ptr, ptr @"_llgo_func$1QmforOaCy2fBAssC2y1FWCCT6fpq9RKwP2j2HIASY8", align 8
  %2484 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2485 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2484, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2482, ptr %2485, align 8
  %2486 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2484, i32 0, i32 1
  store ptr %2483, ptr %2486, align 8
  %2487 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2484, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).InterfaceType", ptr %2487, align 8
  %2488 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2484, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).InterfaceType", ptr %2488, align 8
  %2489 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2484, align 8
  %2490 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2491 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2490, i32 0, i32 0
  store ptr @42, ptr %2491, align 8
  %2492 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2490, i32 0, i32 1
  store i64 13, ptr %2492, align 4
  %2493 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2490, align 8
  %2494 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2495 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2496 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2495, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2493, ptr %2496, align 8
  %2497 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2495, i32 0, i32 1
  store ptr %2494, ptr %2497, align 8
  %2498 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2495, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).IsDirectIface", ptr %2498, align 8
  %2499 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2495, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).IsDirectIface", ptr %2499, align 8
  %2500 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2495, align 8
  %2501 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2502 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2501, i32 0, i32 0
  store ptr @43, ptr %2502, align 8
  %2503 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2501, i32 0, i32 1
  store i64 4, ptr %2503, align 4
  %2504 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2501, align 8
  %2505 = load ptr, ptr @"_llgo_func$ntUE0UmVAWPS2O7GpCCGszSn-XnjHJntZZ2jYtwbFXI", align 8
  %2506 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2507 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2506, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2504, ptr %2507, align 8
  %2508 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2506, i32 0, i32 1
  store ptr %2505, ptr %2508, align 8
  %2509 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2506, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Kind", ptr %2509, align 8
  %2510 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2506, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Kind", ptr %2510, align 8
  %2511 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2506, align 8
  %2512 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2513 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2512, i32 0, i32 0
  store ptr @28, ptr %2513, align 8
  %2514 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2512, i32 0, i32 1
  store i64 3, ptr %2514, align 4
  %2515 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2512, align 8
  %2516 = load ptr, ptr @"_llgo_func$ETeB8WwW04JEq0ztcm-XPTJtuYvtpkjIsAc0-2NT9zA", align 8
  %2517 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2518 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2517, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2515, ptr %2518, align 8
  %2519 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2517, i32 0, i32 1
  store ptr %2516, ptr %2519, align 8
  %2520 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2517, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Len", ptr %2520, align 8
  %2521 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2517, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Len", ptr %2521, align 8
  %2522 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2517, align 8
  %2523 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2524 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2523, i32 0, i32 0
  store ptr @47, ptr %2524, align 8
  %2525 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2523, i32 0, i32 1
  store i64 7, ptr %2525, align 4
  %2526 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2523, align 8
  %2527 = load ptr, ptr @"_llgo_func$d-NlqnjcQnaMjsBQY7qh2SWQmHb0XIigoceXdiJ8YT4", align 8
  %2528 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2529 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2528, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2526, ptr %2529, align 8
  %2530 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2528, i32 0, i32 1
  store ptr %2527, ptr %2530, align 8
  %2531 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2528, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).MapType", ptr %2531, align 8
  %2532 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2528, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).MapType", ptr %2532, align 8
  %2533 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2528, align 8
  %2534 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2535 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2534, i32 0, i32 0
  store ptr @60, ptr %2535, align 8
  %2536 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2534, i32 0, i32 1
  store i64 8, ptr %2536, align 4
  %2537 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2534, align 8
  %2538 = load ptr, ptr @"_llgo_func$YHeRw3AOvQtzv982-ZO3Yn8vh3Fx89RM3VvI8E4iKVk", align 8
  %2539 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2540 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2539, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2537, ptr %2540, align 8
  %2541 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2539, i32 0, i32 1
  store ptr %2538, ptr %2541, align 8
  %2542 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2539, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Pointers", ptr %2542, align 8
  %2543 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2539, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Pointers", ptr %2543, align 8
  %2544 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2539, align 8
  %2545 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2546 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2545, i32 0, i32 0
  store ptr @62, ptr %2546, align 8
  %2547 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2545, i32 0, i32 1
  store i64 4, ptr %2547, align 4
  %2548 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2545, align 8
  %2549 = load ptr, ptr @"_llgo_func$1kITCsyu7hFLMxHLR7kDlvu4SOra_HtrtdFUQH9P13s", align 8
  %2550 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2551 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2550, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2548, ptr %2551, align 8
  %2552 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2550, i32 0, i32 1
  store ptr %2549, ptr %2552, align 8
  %2553 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2550, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Size", ptr %2553, align 8
  %2554 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2550, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Size", ptr %2554, align 8
  %2555 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2550, align 8
  %2556 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2557 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2556, i32 0, i32 0
  store ptr @45, ptr %2557, align 8
  %2558 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2556, i32 0, i32 1
  store i64 6, ptr %2558, align 4
  %2559 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2556, align 8
  %2560 = load ptr, ptr @"_llgo_func$zNDVRsWTIpUPKouNUS805RGX--IV9qVK8B31IZbg5to", align 8
  %2561 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2562 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2561, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2559, ptr %2562, align 8
  %2563 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2561, i32 0, i32 1
  store ptr %2560, ptr %2563, align 8
  %2564 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2561, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).String", ptr %2564, align 8
  %2565 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2561, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).String", ptr %2565, align 8
  %2566 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2561, align 8
  %2567 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2568 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2567, i32 0, i32 0
  store ptr @63, ptr %2568, align 8
  %2569 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2567, i32 0, i32 1
  store i64 10, ptr %2569, align 4
  %2570 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2567, align 8
  %2571 = load ptr, ptr @"_llgo_func$qiNnn6Cbm3GtDp4gDI4U_DRV3h8zlz91s9jrfOXC--U", align 8
  %2572 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2573 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2572, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2570, ptr %2573, align 8
  %2574 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2572, i32 0, i32 1
  store ptr %2571, ptr %2574, align 8
  %2575 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2572, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).StructType", ptr %2575, align 8
  %2576 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2572, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).StructType", ptr %2576, align 8
  %2577 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2572, align 8
  %2578 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2579 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2578, i32 0, i32 0
  store ptr @67, ptr %2579, align 8
  %2580 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2578, i32 0, i32 1
  store i64 8, ptr %2580, align 4
  %2581 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2578, align 8
  %2582 = load ptr, ptr @"_llgo_func$DbD4nZv_bjE4tH8hh-VfAjMXMpNfIsMlLJJJPKupp34", align 8
  %2583 = alloca %"github.com/goplus/llgo/internal/abi.Method", align 8
  %2584 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2583, i32 0, i32 0
  store %"github.com/goplus/llgo/internal/runtime.String" %2581, ptr %2584, align 8
  %2585 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2583, i32 0, i32 1
  store ptr %2582, ptr %2585, align 8
  %2586 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2583, i32 0, i32 2
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Uncommon", ptr %2586, align 8
  %2587 = getelementptr inbounds %"github.com/goplus/llgo/internal/abi.Method", ptr %2583, i32 0, i32 3
  store ptr @"github.com/goplus/llgo/internal/abi.(*Type).Uncommon", ptr %2587, align 8
  %2588 = load %"github.com/goplus/llgo/internal/abi.Method", ptr %2583, align 8
  %2589 = call ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64 720)
  %2590 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 0
  store %"github.com/goplus/llgo/internal/abi.Method" %324, ptr %2590, align 8
  %2591 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 1
  store %"github.com/goplus/llgo/internal/abi.Method" %2412, ptr %2591, align 8
  %2592 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 2
  store %"github.com/goplus/llgo/internal/abi.Method" %2423, ptr %2592, align 8
  %2593 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 3
  store %"github.com/goplus/llgo/internal/abi.Method" %2434, ptr %2593, align 8
  %2594 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 4
  store %"github.com/goplus/llgo/internal/abi.Method" %2445, ptr %2594, align 8
  %2595 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 5
  store %"github.com/goplus/llgo/internal/abi.Method" %2456, ptr %2595, align 8
  %2596 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 6
  store %"github.com/goplus/llgo/internal/abi.Method" %2467, ptr %2596, align 8
  %2597 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 7
  store %"github.com/goplus/llgo/internal/abi.Method" %2478, ptr %2597, align 8
  %2598 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 8
  store %"github.com/goplus/llgo/internal/abi.Method" %2489, ptr %2598, align 8
  %2599 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 9
  store %"github.com/goplus/llgo/internal/abi.Method" %2500, ptr %2599, align 8
  %2600 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 10
  store %"github.com/goplus/llgo/internal/abi.Method" %2511, ptr %2600, align 8
  %2601 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 11
  store %"github.com/goplus/llgo/internal/abi.Method" %2522, ptr %2601, align 8
  %2602 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 12
  store %"github.com/goplus/llgo/internal/abi.Method" %2533, ptr %2602, align 8
  %2603 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 13
  store %"github.com/goplus/llgo/internal/abi.Method" %2544, ptr %2603, align 8
  %2604 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 14
  store %"github.com/goplus/llgo/internal/abi.Method" %2555, ptr %2604, align 8
  %2605 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 15
  store %"github.com/goplus/llgo/internal/abi.Method" %2566, ptr %2605, align 8
  %2606 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 16
  store %"github.com/goplus/llgo/internal/abi.Method" %2577, ptr %2606, align 8
  %2607 = getelementptr %"github.com/goplus/llgo/internal/abi.Method", ptr %2589, i64 17
  store %"github.com/goplus/llgo/internal/abi.Method" %2588, ptr %2607, align 8
  %2608 = alloca %"github.com/goplus/llgo/internal/runtime.Slice", align 8
  %2609 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2608, i32 0, i32 0
  store ptr %2589, ptr %2609, align 8
  %2610 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2608, i32 0, i32 1
  store i64 18, ptr %2610, align 4
  %2611 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2608, i32 0, i32 2
  store i64 18, ptr %2611, align 4
  %2612 = load %"github.com/goplus/llgo/internal/runtime.Slice", ptr %2608, align 8
  %2613 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2614 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2613, i32 0, i32 0
  store ptr @46, ptr %2614, align 8
  %2615 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2613, i32 0, i32 1
  store i64 35, ptr %2615, align 4
  %2616 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2613, align 8
  %2617 = alloca %"github.com/goplus/llgo/internal/runtime.String", align 8
  %2618 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2617, i32 0, i32 0
  store ptr @25, ptr %2618, align 8
  %2619 = getelementptr inbounds %"github.com/goplus/llgo/internal/runtime.String", ptr %2617, i32 0, i32 1
  store i64 4, ptr %2619, align 4
  %2620 = load %"github.com/goplus/llgo/internal/runtime.String", ptr %2617, align 8
  call void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr %90, %"github.com/goplus/llgo/internal/runtime.String" %2616, %"github.com/goplus/llgo/internal/runtime.String" %2620, ptr %293, { ptr, i64, i64 } zeroinitializer, %"github.com/goplus/llgo/internal/runtime.Slice" %2612)
  br label %_llgo_12
}

declare ptr @"github.com/goplus/llgo/internal/runtime.NewNamed"(%"github.com/goplus/llgo/internal/runtime.String", i64, i64, i64, i64)

declare ptr @"github.com/goplus/llgo/internal/runtime.Struct"(%"github.com/goplus/llgo/internal/runtime.String", i64, %"github.com/goplus/llgo/internal/runtime.Slice")

declare %"github.com/goplus/llgo/internal/abi.StructField" @"github.com/goplus/llgo/internal/runtime.StructField"(%"github.com/goplus/llgo/internal/runtime.String", ptr, i64, %"github.com/goplus/llgo/internal/runtime.String", i1)

declare ptr @"github.com/goplus/llgo/internal/runtime.PointerTo"(ptr)

declare ptr @"github.com/goplus/llgo/internal/runtime.Basic"(i64)

declare ptr @"github.com/goplus/llgo/internal/runtime.SliceOf"(ptr)

declare ptr @"github.com/goplus/llgo/internal/runtime.AllocU"(i64)

declare void @"github.com/goplus/llgo/internal/runtime.InitNamed"(ptr, %"github.com/goplus/llgo/internal/runtime.String", %"github.com/goplus/llgo/internal/runtime.String", ptr, %"github.com/goplus/llgo/internal/runtime.Slice", %"github.com/goplus/llgo/internal/runtime.Slice")

declare void @"github.com/goplus/llgo/internal/runtime.SetDirectIface"(ptr)

declare ptr @"github.com/goplus/llgo/internal/runtime.Func"(%"github.com/goplus/llgo/internal/runtime.Slice", %"github.com/goplus/llgo/internal/runtime.Slice", i1)

declare i64 @"github.com/goplus/llgo/internal/abi.(*Type).Align"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*ArrayType).Align"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).ArrayType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Common"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*ArrayType).FieldAlign"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*FuncType).Align"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).ArrayType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Common"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Elem"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*FuncType).FieldAlign"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).FuncType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*FuncType).HasName"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*FuncType).IfaceIndir"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Align"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).ArrayType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Common"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Elem"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*InterfaceType).FieldAlign"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).FuncType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*InterfaceType).HasName"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*InterfaceType).IfaceIndir"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).InterfaceType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*InterfaceType).IsDirectIface"(ptr)

declare %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/abi.(*Kind).String"(ptr)

declare %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/abi.Kind.String"(i64)

declare i64 @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Kind"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Len"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*MapType).Align"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*MapType).ArrayType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Common"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*MapType).FieldAlign"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*MapType).FuncType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*MapType).HasName"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*MapType).HashMightPanic"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*MapType).IfaceIndir"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*MapType).IndirectElem"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*MapType).IndirectKey"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*MapType).InterfaceType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*MapType).IsDirectIface"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*MapType).Kind"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*MapType).Len"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*MapType).MapType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*MapType).NeedKeyUpdate"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*MapType).Pointers"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*MapType).ReflexiveKey"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*MapType).Size"(ptr)

declare %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/abi.(*MapType).String"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*StructType).Align"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*StructType).ArrayType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Common"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Elem"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*StructType).FieldAlign"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*StructType).FuncType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*StructType).HasName"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*StructType).IfaceIndir"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*StructType).InterfaceType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*StructType).IsDirectIface"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*StructType).Kind"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*StructType).Len"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*StructType).MapType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*StructType).Pointers"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*StructType).Size"(ptr)

declare %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/abi.(*StructType).String"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*StructType).StructType"(ptr)

declare %"github.com/goplus/llgo/internal/runtime.Slice" @"github.com/goplus/llgo/internal/abi.(*UncommonType).ExportedMethods"(ptr)

declare %"github.com/goplus/llgo/internal/runtime.Slice" @"github.com/goplus/llgo/internal/abi.(*UncommonType).Methods"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*StructType).Uncommon"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*MapType).StructType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*MapType).Uncommon"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).MapType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Pointers"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Size"(ptr)

declare %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/abi.(*InterfaceType).String"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).StructType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*InterfaceType).Uncommon"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).InterfaceType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*FuncType).IsDirectIface"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*FuncType).Kind"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*FuncType).Len"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).MapType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*FuncType).Pointers"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*FuncType).Size"(ptr)

declare %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/abi.(*FuncType).String"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).StructType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*FuncType).Uncommon"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*FuncType).Variadic"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).FuncType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*ArrayType).HasName"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*ArrayType).IfaceIndir"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).InterfaceType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*ArrayType).IsDirectIface"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*ArrayType).Kind"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).MapType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*ArrayType).Pointers"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*ArrayType).Size"(ptr)

declare %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/abi.(*ArrayType).String"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).StructType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*ArrayType).Uncommon"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*Type).ArrayType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*Type).Common"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*Type).Elem"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*Type).FieldAlign"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*Type).FuncType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*Type).HasName"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*Type).IfaceIndir"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*Type).InterfaceType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*Type).IsDirectIface"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*Type).Kind"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*Type).Len"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*Type).MapType"(ptr)

declare i1 @"github.com/goplus/llgo/internal/abi.(*Type).Pointers"(ptr)

declare i64 @"github.com/goplus/llgo/internal/abi.(*Type).Size"(ptr)

declare %"github.com/goplus/llgo/internal/runtime.String" @"github.com/goplus/llgo/internal/abi.(*Type).String"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*Type).StructType"(ptr)

declare ptr @"github.com/goplus/llgo/internal/abi.(*Type).Uncommon"(ptr)

declare void @"github.com/goplus/llgo/internal/runtime.PrintPointer"(ptr)

declare void @"github.com/goplus/llgo/internal/runtime.PrintByte"(i8)

declare ptr @"github.com/goplus/llgo/internal/runtime.Zeroinit"(ptr, i64)

declare void @"github.com/goplus/llgo/internal/runtime.AssertIndexRange"(i1)

declare void @"github.com/goplus/llgo/internal/runtime.Panic"(%"github.com/goplus/llgo/internal/runtime.eface")

declare ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64)
