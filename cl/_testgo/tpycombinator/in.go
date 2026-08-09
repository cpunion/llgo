// LITTEST
package main

// CHECK: {{^}}@0 = private unnamed_addr constant [1 x i8] c"x", align 1{{$}}

func Y[Endo ~func(RecFct) RecFct, RecFct ~func(T) R, T, R any](f Endo) RecFct {
	type internal[RecFct ~func(T) R, T, R any] func(internal[RecFct, T, R]) RecFct

	g := func(h internal[RecFct, T, R]) RecFct {
		return func(t T) R {
			return f(h(h))(t)
		}
	}
	return g(g)
}

func main() {
	factorial := Y(func(recur func(int) int) func(int) int {
		return func(n int) int {
			if n == 0 {
				return 1
			}
			return n * recur(n-1)
		}
	})
	repeat := Y(func(recur func(string) string) func(string) string {
		return func(s string) string {
			if len(s) == 3 {
				return s
			}
			return recur(s + "x")
		}
	})
	println(factorial(10))
	println(repeat(""))
}

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
// CHECK-NEXT:   %0 = call { ptr, ptr } @"main.Y[func(recur func(int) int) func(int) int,func(int) int,int,int]"({ ptr, ptr } { ptr @"main.main$1", ptr null })
// CHECK-NEXT:   %1 = call { ptr, ptr } @"main.Y[func(recur func(string) string) func(string) string,func(string) string,string,string]"({ ptr, ptr } { ptr @"main.main$2", ptr null })
// CHECK-NEXT:   %2 = extractvalue { ptr, ptr } %0, 1
// CHECK-NEXT:   %3 = extractvalue { ptr, ptr } %0, 0
// CHECK-NEXT:   %__llgo_funcval_code = call ptr asm "", "=r,0"(ptr %3)
// CHECK-NEXT:   %4 = call i64 %__llgo_funcval_code(ptr {{(nest|swiftself)}} %2, i64 10)
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintInt"(i64 %4)
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintByte"(i8 10)
// CHECK-NEXT:   %5 = extractvalue { ptr, ptr } %1, 1
// CHECK-NEXT:   %6 = extractvalue { ptr, ptr } %1, 0
// CHECK-NEXT:   %__llgo_funcval_code1 = call ptr asm "", "=r,0"(ptr %6)
// CHECK-NEXT:   %7 = call %"{{.*}}/runtime/internal/runtime.String" %__llgo_funcval_code1(ptr {{(nest|swiftself)}} %5, %"{{.*}}/runtime/internal/runtime.String" zeroinitializer)
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintString"(%"{{.*}}/runtime/internal/runtime.String" %7)
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintByte"(i8 10)
// CHECK-NEXT:   ret void
// CHECK-NEXT: }

// CHECK-LABEL: define { ptr, ptr } @"main.main$1"({ ptr, ptr } %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %1 = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 16)
// CHECK-NEXT:   store { ptr, ptr } %0, ptr %1, align 8
// CHECK-NEXT:   %2 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 8)
// CHECK-NEXT:   %3 = getelementptr inbounds { ptr }, ptr %2, i32 0, i32 0
// CHECK-NEXT:   store ptr %1, ptr %3, align 8
// CHECK-NEXT:   %4 = insertvalue { ptr, ptr } { ptr @"main.main$1$1", ptr undef }, ptr %2, 1
// CHECK-NEXT:   ret { ptr, ptr } %4
// CHECK-NEXT: }

// CHECK-LABEL: define i64 @"main.main$1$1"(ptr {{(nest|swiftself)}} %0, i64 %1){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %2 = load { ptr }, ptr %0, align 8
// CHECK-NEXT:   %3 = icmp eq i64 %1, 0
// CHECK-NEXT:   br i1 %3, label %_llgo_1, label %_llgo_2
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:                                          ; preds = %_llgo_0
// CHECK-NEXT:   ret i64 1
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_2:                                          ; preds = %_llgo_0
// CHECK-NEXT:   %4 = extractvalue { ptr } %2, 0
// CHECK-NEXT:   %5 = icmp eq ptr %4, null
// CHECK-NEXT:   br i1 %5, label %6, label %7
// CHECK-EMPTY:
// CHECK-NEXT: 6:                                                ; preds = %_llgo_2
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 7:                                                ; preds = %_llgo_2
// CHECK-NEXT:   %8 = load { ptr, ptr }, ptr %4, align 8
// CHECK-NEXT:   %9 = sub i64 %1, 1
// CHECK-NEXT:   %10 = extractvalue { ptr, ptr } %8, 1
// CHECK-NEXT:   %11 = extractvalue { ptr, ptr } %8, 0
// CHECK-NEXT:   %__llgo_funcval_code = call ptr asm "", "=r,0"(ptr %11)
// CHECK-NEXT:   %12 = call i64 %__llgo_funcval_code(ptr {{(nest|swiftself)}} %10, i64 %9)
// CHECK-NEXT:   %13 = mul i64 %1, %12
// CHECK-NEXT:   ret i64 %13
// CHECK-NEXT: }

// CHECK-LABEL: define { ptr, ptr } @"main.main$2"({ ptr, ptr } %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %1 = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 16)
// CHECK-NEXT:   store { ptr, ptr } %0, ptr %1, align 8
// CHECK-NEXT:   %2 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 8)
// CHECK-NEXT:   %3 = getelementptr inbounds { ptr }, ptr %2, i32 0, i32 0
// CHECK-NEXT:   store ptr %1, ptr %3, align 8
// CHECK-NEXT:   %4 = insertvalue { ptr, ptr } { ptr @"main.main$2$1", ptr undef }, ptr %2, 1
// CHECK-NEXT:   ret { ptr, ptr } %4
// CHECK-NEXT: }

// CHECK-LABEL: define %"{{.*}}/runtime/internal/runtime.String" @"main.main$2$1"(ptr {{(nest|swiftself)}} %0, %"{{.*}}/runtime/internal/runtime.String" %1){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %2 = load { ptr }, ptr %0, align 8
// CHECK-NEXT:   %3 = extractvalue %"{{.*}}/runtime/internal/runtime.String" %1, 1
// CHECK-NEXT:   %4 = icmp eq i64 %3, 3
// CHECK-NEXT:   br i1 %4, label %_llgo_1, label %_llgo_2
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:                                          ; preds = %_llgo_0
// CHECK-NEXT:   ret %"{{.*}}/runtime/internal/runtime.String" %1
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_2:                                          ; preds = %_llgo_0
// CHECK-NEXT:   %5 = extractvalue { ptr } %2, 0
// CHECK-NEXT:   %6 = icmp eq ptr %5, null
// CHECK-NEXT:   br i1 %6, label %7, label %8
// CHECK-EMPTY:
// CHECK-NEXT: 7:                                                ; preds = %_llgo_2
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 8:                                                ; preds = %_llgo_2
// CHECK-NEXT:   %9 = load { ptr, ptr }, ptr %5, align 8
// CHECK-NEXT:   %10 = call %"{{.*}}/runtime/internal/runtime.String" @"{{.*}}/runtime/internal/runtime.StringCat"(%"{{.*}}/runtime/internal/runtime.String" %1, %"{{.*}}/runtime/internal/runtime.String" { ptr @0, i64 1 })
// CHECK-NEXT:   %11 = extractvalue { ptr, ptr } %9, 1
// CHECK-NEXT:   %12 = extractvalue { ptr, ptr } %9, 0
// CHECK-NEXT:   %__llgo_funcval_code = call ptr asm "", "=r,0"(ptr %12)
// CHECK-NEXT:   %13 = call %"{{.*}}/runtime/internal/runtime.String" %__llgo_funcval_code(ptr {{(nest|swiftself)}} %11, %"{{.*}}/runtime/internal/runtime.String" %10)
// CHECK-NEXT:   ret %"{{.*}}/runtime/internal/runtime.String" %13
// CHECK-NEXT: }

// CHECK-LABEL: define linkonce { ptr, ptr } @"main.Y[func(recur func(int) int) func(int) int,func(int) int,int,int]"({ ptr, ptr } %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %1 = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 16)
// CHECK-NEXT:   store { ptr, ptr } %0, ptr %1, align 8
// CHECK-NEXT:   %2 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 8)
// CHECK-NEXT:   %3 = getelementptr inbounds { ptr }, ptr %2, i32 0, i32 0
// CHECK-NEXT:   store ptr %1, ptr %3, align 8
// CHECK-NEXT:   %4 = insertvalue { ptr, ptr } { ptr @"main.Y$1[func(recur func(int) int) func(int) int,func(int) int,int,int]", ptr undef }, ptr %2, 1
// CHECK-NEXT:   %5 = alloca %"main.internal[func(recur func(int) int) func(int) int,func(int) int,int,int;func(int) int,int,int]", align 8
// CHECK-NEXT:   store { ptr, ptr } %4, ptr %5, align 8
// CHECK-NEXT:   %6 = load %"main.internal[func(recur func(int) int) func(int) int,func(int) int,int,int;func(int) int,int,int]", ptr %5, align 8
// CHECK-NEXT:   %7 = extractvalue { ptr, ptr } %4, 1
// CHECK-NEXT:   %8 = extractvalue { ptr, ptr } %4, 0
// CHECK-NEXT:   %__llgo_funcval_code = call ptr asm "", "=r,0"(ptr %8)
// CHECK-NEXT:   %9 = call { ptr, ptr } %__llgo_funcval_code(ptr {{(nest|swiftself)}} %7, %"main.internal[func(recur func(int) int) func(int) int,func(int) int,int,int;func(int) int,int,int]" %6)
// CHECK-NEXT:   ret { ptr, ptr } %9
// CHECK-NEXT: }

// CHECK-LABEL: define linkonce { ptr, ptr } @"main.Y$1[func(recur func(int) int) func(int) int,func(int) int,int,int]"(ptr {{(nest|swiftself)}} %0, %"main.internal[func(recur func(int) int) func(int) int,func(int) int,int,int;func(int) int,int,int]" %1){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %2 = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 16)
// CHECK-NEXT:   store %"main.internal[func(recur func(int) int) func(int) int,func(int) int,int,int;func(int) int,int,int]" %1, ptr %2, align 8
// CHECK-NEXT:   %3 = load { ptr }, ptr %0, align 8
// CHECK-NEXT:   %4 = extractvalue { ptr } %3, 0
// CHECK-NEXT:   %5 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 16)
// CHECK-NEXT:   %6 = getelementptr inbounds { ptr, ptr }, ptr %5, i32 0, i32 0
// CHECK-NEXT:   store ptr %4, ptr %6, align 8
// CHECK-NEXT:   %7 = getelementptr inbounds { ptr, ptr }, ptr %5, i32 0, i32 1
// CHECK-NEXT:   store ptr %2, ptr %7, align 8
// CHECK-NEXT:   %8 = insertvalue { ptr, ptr } { ptr @"main.Y$1$1[func(recur func(int) int) func(int) int,func(int) int,int,int]", ptr undef }, ptr %5, 1
// CHECK-NEXT:   ret { ptr, ptr } %8
// CHECK-NEXT: }

// CHECK-LABEL: define linkonce i64 @"main.Y$1$1[func(recur func(int) int) func(int) int,func(int) int,int,int]"(ptr {{(nest|swiftself)}} %0, i64 %1){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %2 = load { ptr, ptr }, ptr %0, align 8
// CHECK-NEXT:   %3 = extractvalue { ptr, ptr } %2, 0
// CHECK-NEXT:   %4 = icmp eq ptr %3, null
// CHECK-NEXT:   br i1 %4, label %5, label %6
// CHECK-EMPTY:
// CHECK-NEXT: 5:                                                ; preds = %_llgo_0
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 6:                                                ; preds = %_llgo_0
// CHECK-NEXT:   %7 = load { ptr, ptr }, ptr %3, align 8
// CHECK-NEXT:   %8 = extractvalue { ptr, ptr } %2, 1
// CHECK-NEXT:   %9 = icmp eq ptr %8, null
// CHECK-NEXT:   br i1 %9, label %10, label %11
// CHECK-EMPTY:
// CHECK-NEXT: 10:                                               ; preds = %6
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 11:                                               ; preds = %6
// CHECK-NEXT:   %12 = load %"main.internal[func(recur func(int) int) func(int) int,func(int) int,int,int;func(int) int,int,int]", ptr %8, align 8
// CHECK-NEXT:   %13 = extractvalue { ptr, ptr } %2, 1
// CHECK-NEXT:   %14 = icmp eq ptr %13, null
// CHECK-NEXT:   br i1 %14, label %15, label %16
// CHECK-EMPTY:
// CHECK-NEXT: 15:                                               ; preds = %11
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 16:                                               ; preds = %11
// CHECK-NEXT:   %17 = load %"main.internal[func(recur func(int) int) func(int) int,func(int) int,int,int;func(int) int,int,int]", ptr %13, align 8
// CHECK-NEXT:   %18 = extractvalue %"main.internal[func(recur func(int) int) func(int) int,func(int) int,int,int;func(int) int,int,int]" %12, 1
// CHECK-NEXT:   %19 = extractvalue %"main.internal[func(recur func(int) int) func(int) int,func(int) int,int,int;func(int) int,int,int]" %12, 0
// CHECK-NEXT:   %__llgo_funcval_code = call ptr asm "", "=r,0"(ptr %19)
// CHECK-NEXT:   %20 = call { ptr, ptr } %__llgo_funcval_code(ptr {{(nest|swiftself)}} %18, %"main.internal[func(recur func(int) int) func(int) int,func(int) int,int,int;func(int) int,int,int]" %17)
// CHECK-NEXT:   %21 = extractvalue { ptr, ptr } %7, 1
// CHECK-NEXT:   %22 = extractvalue { ptr, ptr } %7, 0
// CHECK-NEXT:   %__llgo_funcval_code1 = call ptr asm "", "=r,0"(ptr %22)
// CHECK-NEXT:   %23 = call { ptr, ptr } %__llgo_funcval_code1(ptr {{(nest|swiftself)}} %21, { ptr, ptr } %20)
// CHECK-NEXT:   %24 = extractvalue { ptr, ptr } %23, 1
// CHECK-NEXT:   %25 = extractvalue { ptr, ptr } %23, 0
// CHECK-NEXT:   %__llgo_funcval_code2 = call ptr asm "", "=r,0"(ptr %25)
// CHECK-NEXT:   %26 = call i64 %__llgo_funcval_code2(ptr {{(nest|swiftself)}} %24, i64 %1)
// CHECK-NEXT:   ret i64 %26
// CHECK-NEXT: }

// CHECK-LABEL: define linkonce { ptr, ptr } @"main.Y[func(recur func(string) string) func(string) string,func(string) string,string,string]"({ ptr, ptr } %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %1 = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 16)
// CHECK-NEXT:   store { ptr, ptr } %0, ptr %1, align 8
// CHECK-NEXT:   %2 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 8)
// CHECK-NEXT:   %3 = getelementptr inbounds { ptr }, ptr %2, i32 0, i32 0
// CHECK-NEXT:   store ptr %1, ptr %3, align 8
// CHECK-NEXT:   %4 = insertvalue { ptr, ptr } { ptr @"main.Y$1[func(recur func(string) string) func(string) string,func(string) string,string,string]", ptr undef }, ptr %2, 1
// CHECK-NEXT:   %5 = alloca %"main.internal[func(recur func(string) string) func(string) string,func(string) string,string,string;func(string) string,string,string]", align 8
// CHECK-NEXT:   store { ptr, ptr } %4, ptr %5, align 8
// CHECK-NEXT:   %6 = load %"main.internal[func(recur func(string) string) func(string) string,func(string) string,string,string;func(string) string,string,string]", ptr %5, align 8
// CHECK-NEXT:   %7 = extractvalue { ptr, ptr } %4, 1
// CHECK-NEXT:   %8 = extractvalue { ptr, ptr } %4, 0
// CHECK-NEXT:   %__llgo_funcval_code = call ptr asm "", "=r,0"(ptr %8)
// CHECK-NEXT:   %9 = call { ptr, ptr } %__llgo_funcval_code(ptr {{(nest|swiftself)}} %7, %"main.internal[func(recur func(string) string) func(string) string,func(string) string,string,string;func(string) string,string,string]" %6)
// CHECK-NEXT:   ret { ptr, ptr } %9
// CHECK-NEXT: }

// CHECK-LABEL: define linkonce { ptr, ptr } @"main.Y$1[func(recur func(string) string) func(string) string,func(string) string,string,string]"(ptr {{(nest|swiftself)}} %0, %"main.internal[func(recur func(string) string) func(string) string,func(string) string,string,string;func(string) string,string,string]" %1){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %2 = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 16)
// CHECK-NEXT:   store %"main.internal[func(recur func(string) string) func(string) string,func(string) string,string,string;func(string) string,string,string]" %1, ptr %2, align 8
// CHECK-NEXT:   %3 = load { ptr }, ptr %0, align 8
// CHECK-NEXT:   %4 = extractvalue { ptr } %3, 0
// CHECK-NEXT:   %5 = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 16)
// CHECK-NEXT:   %6 = getelementptr inbounds { ptr, ptr }, ptr %5, i32 0, i32 0
// CHECK-NEXT:   store ptr %4, ptr %6, align 8
// CHECK-NEXT:   %7 = getelementptr inbounds { ptr, ptr }, ptr %5, i32 0, i32 1
// CHECK-NEXT:   store ptr %2, ptr %7, align 8
// CHECK-NEXT:   %8 = insertvalue { ptr, ptr } { ptr @"main.Y$1$1[func(recur func(string) string) func(string) string,func(string) string,string,string]", ptr undef }, ptr %5, 1
// CHECK-NEXT:   ret { ptr, ptr } %8
// CHECK-NEXT: }

// CHECK-LABEL: define linkonce %"{{.*}}/runtime/internal/runtime.String" @"main.Y$1$1[func(recur func(string) string) func(string) string,func(string) string,string,string]"(ptr {{(nest|swiftself)}} %0, %"{{.*}}/runtime/internal/runtime.String" %1){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %2 = load { ptr, ptr }, ptr %0, align 8
// CHECK-NEXT:   %3 = extractvalue { ptr, ptr } %2, 0
// CHECK-NEXT:   %4 = icmp eq ptr %3, null
// CHECK-NEXT:   br i1 %4, label %5, label %6
// CHECK-EMPTY:
// CHECK-NEXT: 5:                                                ; preds = %_llgo_0
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 6:                                                ; preds = %_llgo_0
// CHECK-NEXT:   %7 = load { ptr, ptr }, ptr %3, align 8
// CHECK-NEXT:   %8 = extractvalue { ptr, ptr } %2, 1
// CHECK-NEXT:   %9 = icmp eq ptr %8, null
// CHECK-NEXT:   br i1 %9, label %10, label %11
// CHECK-EMPTY:
// CHECK-NEXT: 10:                                               ; preds = %6
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 11:                                               ; preds = %6
// CHECK-NEXT:   %12 = load %"main.internal[func(recur func(string) string) func(string) string,func(string) string,string,string;func(string) string,string,string]", ptr %8, align 8
// CHECK-NEXT:   %13 = extractvalue { ptr, ptr } %2, 1
// CHECK-NEXT:   %14 = icmp eq ptr %13, null
// CHECK-NEXT:   br i1 %14, label %15, label %16
// CHECK-EMPTY:
// CHECK-NEXT: 15:                                               ; preds = %11
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.AssertNilDeref"(i1 true)
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: 16:                                               ; preds = %11
// CHECK-NEXT:   %17 = load %"main.internal[func(recur func(string) string) func(string) string,func(string) string,string,string;func(string) string,string,string]", ptr %13, align 8
// CHECK-NEXT:   %18 = extractvalue %"main.internal[func(recur func(string) string) func(string) string,func(string) string,string,string;func(string) string,string,string]" %12, 1
// CHECK-NEXT:   %19 = extractvalue %"main.internal[func(recur func(string) string) func(string) string,func(string) string,string,string;func(string) string,string,string]" %12, 0
// CHECK-NEXT:   %__llgo_funcval_code = call ptr asm "", "=r,0"(ptr %19)
// CHECK-NEXT:   %20 = call { ptr, ptr } %__llgo_funcval_code(ptr {{(nest|swiftself)}} %18, %"main.internal[func(recur func(string) string) func(string) string,func(string) string,string,string;func(string) string,string,string]" %17)
// CHECK-NEXT:   %21 = extractvalue { ptr, ptr } %7, 1
// CHECK-NEXT:   %22 = extractvalue { ptr, ptr } %7, 0
// CHECK-NEXT:   %__llgo_funcval_code1 = call ptr asm "", "=r,0"(ptr %22)
// CHECK-NEXT:   %23 = call { ptr, ptr } %__llgo_funcval_code1(ptr {{(nest|swiftself)}} %21, { ptr, ptr } %20)
// CHECK-NEXT:   %24 = extractvalue { ptr, ptr } %23, 1
// CHECK-NEXT:   %25 = extractvalue { ptr, ptr } %23, 0
// CHECK-NEXT:   %__llgo_funcval_code2 = call ptr asm "", "=r,0"(ptr %25)
// CHECK-NEXT:   %26 = call %"{{.*}}/runtime/internal/runtime.String" %__llgo_funcval_code2(ptr {{(nest|swiftself)}} %24, %"{{.*}}/runtime/internal/runtime.String" %1)
// CHECK-NEXT:   ret %"{{.*}}/runtime/internal/runtime.String" %26
// CHECK-NEXT: }
