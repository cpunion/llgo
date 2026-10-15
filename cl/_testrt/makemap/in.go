// LITTEST
// Scope: common
package main

// Map capacity hints must honor source signedness before entering the async
// map allocator. The result is consumed only after the child coroutine joins.
// CHECK-LABEL: define ptr @"main.fromInt32$coro"(
// CHECK: [[INT_HINT:%[0-9]+]] = sext i32 %{{[0-9]+}} to i64
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.makemap$coro"({{.*}}ptr @"map[_llgo_string]_llgo_int", i64 [[INT_HINT]], ptr null)
// CHECK: call i64 @"{{.*}}/runtime/internal/runtime.MapLen"

// CHECK-LABEL: define ptr @"main.fromUint32$coro"(
// CHECK: [[UINT_HINT:%[0-9]+]] = zext i32 %{{[0-9]+}} to i64
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.makemap$coro"({{.*}}ptr @"map[_llgo_string]_llgo_int", i64 [[UINT_HINT]], ptr null)
// CHECK: call i64 @"{{.*}}/runtime/internal/runtime.MapLen"

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK-DAG: call ptr @"main.make1$coro"
// CHECK-DAG: call ptr @"main.make2$coro"
// CHECK-DAG: call ptr @"main.make3$coro"
// CHECK-DAG: call ptr @"main.make4$coro"
// CHECK-DAG: call ptr @"main.make5$coro"
// CHECK-DAG: call ptr @"main.make6$coro"
// CHECK-DAG: call ptr @"main.make7$coro"

// make1 covers allocation, assignment, access, iteration, deletion, and the
// reverse string-key map through their coroutine-aware runtime entries.
// CHECK-LABEL: define ptr @"main.make1$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.makemap$coro"({{.*}}ptr @"map[_llgo_int]_llgo_string"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_fast64$coro"({{.*}}ptr @"map[_llgo_int]_llgo_string"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1_fast64$coro"({{.*}}ptr @"map[_llgo_int]_llgo_string"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.NewMapIter$coro"({{.*}}ptr @"map[_llgo_int]_llgo_string"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.MapIterNext$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_faststr$coro"({{.*}}ptr @"map[_llgo_string]_llgo_int"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess2_faststr$coro"({{.*}}ptr @"map[_llgo_string]_llgo_int"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapdelete_faststr$coro"({{.*}}ptr @"map[_llgo_string]_llgo_int"

// Interface keys retain their named dynamic descriptor, including a nil-map
// lookup and iteration of the any-key map.
// CHECK-LABEL: define ptr @"main.make2$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1_fast64$coro"({{.*}}ptr @"map[_llgo_int]_llgo_string", ptr null, i64 42)
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.makemap$coro"({{.*}}ptr @"map[_llgo_any]_llgo_int"
// CHECK-DAG: insertvalue %"{{.*}}eface" { ptr @_llgo_main.N1, ptr undef }
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign$coro"({{.*}}ptr @"map[_llgo_any]_llgo_int"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.NewMapIter$coro"({{.*}}ptr @"map[_llgo_any]_llgo_int"

// Array value and pointer interface keys both use dynamic equality and the
// same coroutine-aware generic map assignment path.
// CHECK-LABEL: define ptr @"main.make3$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.EfaceEqual$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign$coro"({{.*}}ptr @"map[_llgo_any]_llgo_int"

// CHECK-LABEL: define ptr @"main.make4$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.EfaceEqual$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign$coro"({{.*}}ptr @"map[_llgo_any]_llgo_int"

// Channel identity remains the key for both interface equality and the
// specialized pointer-key map path.
// CHECK-LABEL: define ptr @"main.make5$coro"(
// CHECK-DAG: call void @"{{.*}}/runtime/internal/runtime.NewChan$outcome"
// CHECK-DAG: insertvalue %"{{.*}}eface" { ptr @"chan _llgo_int", ptr undef }
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.EfaceEqual$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.makemap$coro"({{.*}}ptr @"map[chan _llgo_int]_llgo_int"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_fast64ptr$coro"({{.*}}ptr @"map[chan _llgo_int]_llgo_int"

// Named maps retain their named descriptor at operations; the local named key
// also keeps its concrete descriptor and two-entry allocation hint.
// CHECK-LABEL: define ptr @"main.make6$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_fast64$coro"({{.*}}ptr @_llgo_main.M
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.NewMapIter$coro"({{.*}}ptr @_llgo_main.M

// CHECK-LABEL: define ptr @"main.make7$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.makemap$coro"({{.*}}ptr @"map[_llgo_main.N.7.0]_llgo_string", i64 2
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_fast64$coro"({{.*}}ptr @"map[_llgo_main.N.7.0]_llgo_string"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1_fast64$coro"({{.*}}ptr @"map[_llgo_main.N.7.0]_llgo_string"

func main() {
	make1()
	make2()
	make3()
	make4()
	make5()
	make6()
	make7()
}

func make1() {
	m := make(map[int]string)
	m[1] = "hello"
	m[2] = "world"
	m[3] = "llgo"
	println(m, m[1], m[2], len(m))
	for k, v := range m {
		println(k, ":", v)
	}

	s := make(map[string]int, len(m))
	for k, v := range m {
		s[v] = k
	}

	id, ok := s["llgo"]
	println("llgo", id, ok)

	none, ok := s["go"]
	println("go", none, ok)

	delete(s, "llgo")
	if _, ok := s["llgo"]; ok {
		panic("bad key")
	}
	if len(s) != 2 {
		panic("bad len")
	}
}

type N1 [1]int

func make2() {
	m2 := make(map[int]string)
	println(m2, len(m2), m2 == nil, m2 != nil)
	var m3 map[int]string
	println(m3, len(m3), m3 == nil, m3 != nil)
	if got := m3[42]; got != "" {
		panic("nil map lookup returned a non-zero value")
	}

	n := make(map[any]int)
	n[N1{1}] = 100
	n[N1{2}] = 200
	n[N1{3}] = 300
	n[N1{2}] = -200
	for k, v := range n {
		println(k.(N1)[0], v)
	}
}

type N struct {
	n1 int8
	n2 int8
}
type K [1]N
type K2 [1]*N

func make3() {
	var a any = K{N{1, 2}}
	var b any = K{N{1, 2}}
	println(a == b)

	m := make(map[any]int)
	m[K{N{1, 2}}] = 100
	m[K{N{3, 4}}] = 200
	for k, v := range m {
		println(k.(K)[0].n1, v)
	}
}

func make4() {
	var a any = K2{&N{1, 2}}
	var b any = K2{&N{1, 2}}
	println(a == b)

	m := make(map[any]int)
	m[K2{&N{1, 2}}] = 100
	m[K2{&N{3, 4}}] = 200
	for k, v := range m {
		println(k.(K2)[0].n1, v)
	}
}

func make5() {
	ch := make(chan int)
	var a any = ch
	var b any = ch
	println(a == b)
	m := make(map[chan int]int)
	m[ch] = 100
	m[ch] = 200
	for k, v := range m {
		println(k, v)
	}
}

type M map[int]string

func make6() {
	var m M
	m = make(map[int]string)
	m[1] = "hello"
	for k, v := range m {
		println(k, v)
	}
}

func make7() {
	type N int
	m := map[N]string{
		1: "hello",
		2: "world",
	}
	for k, v := range m {
		println(k, v)
	}
	println(m[1])
}

func fromInt32(n int32) int {
	return len(make(map[string]int, n))
}

func fromUint32(n uint32) int {
	return len(make(map[string]int, n))
}
