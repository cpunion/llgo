// LITTEST
// Scope: common
package main

type T string
type A [2]int
type MyBytes []byte

func main() {
	var v any = T("hello")
	println(v.(T))
	s, ok := v.(string)
	println(s, ok)

	var a any = A{1, 2}
	ar, ok := a.(A)
	println(ar[0], ar[1], ok)

	var bytes any = MyBytes{}
	_, ok = bytes.(MyBytes)
	println(ok)
}

// Assertions retain the exact named descriptors after the function is colored
// async by printing/panic paths. The payload layouts remain string, array, and
// three-word slice respectively, while panic and print use managed entries.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call token @llvm.coro.id
// CHECK-DAG: load %"{{.*}}String", ptr {{.*}}
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.PanicTypeAssert$coro"({{.*}}ptr @_llgo_main.T, ptr @_llgo_main.T)
// CHECK-DAG: insertvalue %"{{.*}}eface" { ptr @_llgo_main.A, ptr undef }
// CHECK-DAG: icmp eq ptr %{{.*}}, @_llgo_main.A
// CHECK-DAG: load [2 x i64], ptr %{{.*}}
// CHECK-DAG: insertvalue %"{{.*}}eface" { ptr @_llgo_main.MyBytes, ptr undef }
// CHECK-DAG: icmp eq ptr %{{.*}}, @_llgo_main.MyBytes
// CHECK-DAG: load %"{{.*}}Slice", ptr %{{.*}}
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.PrintBatchV1$coro"
