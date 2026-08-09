// LITTEST
package main

import "github.com/goplus/llgo/cl/_testdata/embedunexport"

// Wrapped embeds *embedunexport.Base to implement embedunexport.Object
// CHECK: {{^}}@0 = private unnamed_addr constant [4 x i8] c"test", align 1{{$}}

type Wrapped struct {
	*embedunexport.Base
}

func main() {
	base := embedunexport.NewBase("test")
	wrapped := &Wrapped{Base: base}

	// This should work: calling unexported method through interface
	var obj embedunexport.Object = wrapped
	embedunexport.Use(obj)

	println(obj.Name())
}

// CHECK-LABEL: define %"{{.*}}/runtime/internal/runtime.String" @main.Wrapped.Name(%main.Wrapped %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %1 = alloca %main.Wrapped, align 8
// CHECK-NEXT:   call void @llvm.memset.p0.i64(ptr %1, i8 0, i64 8, i1 false)
// CHECK-NEXT:   store %main.Wrapped %0, ptr %1, align 8
// CHECK-NEXT:   %2 = getelementptr inbounds %main.Wrapped, ptr %1, i32 0, i32 0
// CHECK-NEXT:   %3 = load ptr, ptr %2, align 8
// CHECK-NEXT:   %4 = call %"{{.*}}/runtime/internal/runtime.String" @"{{.*}}/cl/_testdata/embedunexport.(*Base).Name"(ptr %3)
// CHECK-NEXT:   ret %"{{.*}}/runtime/internal/runtime.String" %4
// CHECK-NEXT: }

// CHECK-LABEL: define void @"main.Wrapped.{{.*}}/cl/_testdata/embedunexport.setName"(%main.Wrapped %0, %"{{.*}}/runtime/internal/runtime.String" %1){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %2 = alloca %main.Wrapped, align 8
// CHECK-NEXT:   call void @llvm.memset.p0.i64(ptr %2, i8 0, i64 8, i1 false)
// CHECK-NEXT:   store %main.Wrapped %0, ptr %2, align 8
// CHECK-NEXT:   %3 = getelementptr inbounds %main.Wrapped, ptr %2, i32 0, i32 0
// CHECK-NEXT:   %4 = load ptr, ptr %3, align 8
// CHECK-NEXT:   call void @"{{.*}}/cl/_testdata/embedunexport.(*Base).setName"(ptr %4, %"{{.*}}/runtime/internal/runtime.String" %1)
// CHECK-NEXT:   ret void
// CHECK-NEXT: }

// CHECK-LABEL: define %"{{.*}}/runtime/internal/runtime.String" @"main.(*Wrapped).Name"(ptr %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %1 = icmp eq ptr %0, null
// CHECK-NEXT:   br i1 %1, label %2, label %3
// CHECK-EMPTY:
// CHECK-NEXT: 2:                                                ; preds = %_llgo_0
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 3:                                                ; preds = %_llgo_0
// CHECK-NEXT:   %4 = getelementptr inbounds %main.Wrapped, ptr %0, i32 0, i32 0
// CHECK-NEXT:   %5 = load ptr, ptr %4, align 8
// CHECK-NEXT:   %6 = call %"{{.*}}/runtime/internal/runtime.String" @"{{.*}}/cl/_testdata/embedunexport.(*Base).Name"(ptr %5)
// CHECK-NEXT:   ret %"{{.*}}/runtime/internal/runtime.String" %6
// CHECK-NEXT: }

// CHECK-LABEL: define void @"main.(*Wrapped).{{.*}}/cl/_testdata/embedunexport.setName"(ptr %0, %"{{.*}}/runtime/internal/runtime.String" %1){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %2 = icmp eq ptr %0, null
// CHECK-NEXT:   br i1 %2, label %3, label %4
// CHECK-EMPTY:
// CHECK-NEXT: 3:                                                ; preds = %_llgo_0
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 4:                                                ; preds = %_llgo_0
// CHECK-NEXT:   %5 = getelementptr inbounds %main.Wrapped, ptr %0, i32 0, i32 0
// CHECK-NEXT:   %6 = load ptr, ptr %5, align 8
// CHECK-NEXT:   call void @"{{.*}}/cl/_testdata/embedunexport.(*Base).setName"(ptr %6, %"{{.*}}/runtime/internal/runtime.String" %1)
// CHECK-NEXT:   ret void
// CHECK-NEXT: }

// CHECK-LABEL: define void @main.init(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %0 = load i1, ptr @"main.init$guard", align 1
// CHECK-NEXT:   br i1 %0, label %_llgo_2, label %_llgo_1
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:                                          ; preds = %_llgo_0
// CHECK-NEXT:   store i1 true, ptr @"main.init$guard", align 1
// CHECK-NEXT:   call void @"{{.*}}/cl/_testdata/embedunexport.init"()
// CHECK-NEXT:   br label %_llgo_2
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_2:                                          ; preds = %_llgo_1, %_llgo_0
// CHECK-NEXT:   ret void
// CHECK-NEXT: }

// CHECK-LABEL: define void @main.main(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %0 = call ptr @"{{.*}}/cl/_testdata/embedunexport.NewBase"(%"{{.*}}/runtime/internal/runtime.String" { ptr @0, i64 4 })
// CHECK-NEXT:   %1 = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 8)
// CHECK-NEXT:   %2 = getelementptr inbounds %main.Wrapped, ptr %1, i32 0, i32 0
// CHECK-NEXT:   store ptr %0, ptr %2, align 8
// CHECK-NEXT:   %3 = call ptr @"{{.*}}/runtime/internal/runtime.NewItab"(ptr @"{{.*}}/cl/_testdata/embedunexport.iface$gGW7PSocDeRlTvk5kuSew8C-TZ8OXQrGkMlj2EUlZ9E", ptr @"*_llgo_main.Wrapped")
// CHECK-NEXT:   %4 = insertvalue %"{{.*}}/runtime/internal/runtime.iface" undef, ptr %3, 0
// CHECK-NEXT:   %5 = insertvalue %"{{.*}}/runtime/internal/runtime.iface" %4, ptr %1, 1
// CHECK-NEXT:   call void @"{{.*}}/cl/_testdata/embedunexport.Use"(%"{{.*}}/runtime/internal/runtime.iface" %5)
// CHECK-NEXT:   %6 = call ptr @"{{.*}}/runtime/internal/runtime.IfacePtrData"(%"{{.*}}/runtime/internal/runtime.iface" %5)
// CHECK-NEXT:   %7 = extractvalue %"{{.*}}/runtime/internal/runtime.iface" %5, 0
// CHECK-NEXT:   %8 = getelementptr ptr, ptr %7, i64 3
// CHECK-NEXT:   %9 = load ptr, ptr %8, align 8
// CHECK-NEXT:   %10 = insertvalue { ptr, ptr } undef, ptr %9, 0
// CHECK-NEXT:   %11 = insertvalue { ptr, ptr } %10, ptr %6, 1
// CHECK-NEXT:   %12 = extractvalue { ptr, ptr } %11, 1
// CHECK-NEXT:   %13 = extractvalue { ptr, ptr } %11, 0
// CHECK-NEXT:   %14 = call %"{{.*}}/runtime/internal/runtime.String" %13(ptr %12)
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintString"(%"{{.*}}/runtime/internal/runtime.String" %14)
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintByte"(i8 10)
// CHECK-NEXT:   ret void
// CHECK-NEXT: }
