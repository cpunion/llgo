// LITTEST
package main

// Generic instances keep distinct typed coroutine entries. Their bounded
// append bodies and statically known callers use the outcome-plain fast path;
// only genuinely suspending printing keeps the stackless await protocol.
//
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call void @"main.(*Slice{{\[\[\]int,int\]}}).Append$outcome"(
// CHECK: call void @"main.(*Slice{{\[\[\]string,string\]}}).Append$outcome"(
// CHECK: call void @"main.(*Slice{{\[\[\]int,int\]}}).Append$outcome"(
// CHECK: call void @"main.(*Slice{{\[\[\]int,int\]}}).Append2$outcome"(
// CHECK: call ptr @"{{.*}}PrintInt$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK: call ptr @"{{.*}}PrintString$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-LABEL: define linkonce ptr @"main.(*Slice{{\[\[\]int,int\]}}).Append$coro"(
// CHECK: call void @"{{.*}}SliceAppend$outcome"(
// CHECK-LABEL: define linkonce ptr @"main.(*Slice{{\[\[\]int,int\]}}).Append2$coro"(
// CHECK: call void @"{{.*}}SliceAppend$outcome"(
// CHECK-LABEL: define linkonce ptr @"main.(*Slice{{\[\[\]string,string\]}}).Append$coro"(
// CHECK: call void @"{{.*}}SliceAppend$outcome"(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

type Data[T any] struct {
	v T
}

func (p *Data[T]) Set(v T) {
	p.v = v
}

func (p *(Data[T1])) Set2(v T1) {
	p.v = v
}

type sliceOf[E any] interface {
	~[]E
}

type Slice[S sliceOf[T], T any] struct {
	Data S
}

func (p *Slice[S, T]) Append(t ...T) S {
	p.Data = append(p.Data, t...)
	return p.Data
}

func (p *Slice[S1, T1]) Append2(t ...T1) S1 {
	p.Data = append(p.Data, t...)
	return p.Data
}

type (
	DataInt     = Data[int]
	SliceInt    = Slice[[]int, int]
	DataString  = Data[string]
	SliceString = Slice[[]string, string]
)

func main() {
	println(DataInt{1}.v)
	println(DataString{"hello"}.v)
	println(Data[int]{100}.v)
	println(Data[string]{"hello"}.v)

	// TODO
	println(Data[struct {
		X int
		Y int
	}]{}.v.X)

	v1 := SliceInt{}
	v1.Append(100)
	v2 := SliceString{}
	v2.Append("hello")
	v3 := Slice[[]int, int]{}
	v3.Append([]int{1, 2, 3, 4}...)
	v3.Append2([]int{1, 2, 3, 4}...)

	println(v1.Data, v1.Data[0])
	println(v2.Data, v2.Data[0])
	println(v3.Data, v3.Data[0])
}
