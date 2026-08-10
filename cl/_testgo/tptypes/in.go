// LITTEST
package main

// Generic instances keep distinct typed entries but share the same stackless
// call/await protocol. Register allocation and expanded append lowering are not
// part of this fixture's contract.
//
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"main.(*Slice{{\[\[\]int,int\]}}).Append$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK: call ptr @"main.(*Slice{{\[\[\]string,string\]}}).Append$coro"(
// CHECK: call ptr @"main.(*Slice{{\[\[\]int,int\]}}).Append2$coro"(
// CHECK: call ptr @"{{.*}}PrintInt$coro"(
// CHECK: call ptr @"{{.*}}PrintString$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-LABEL: define linkonce ptr @"main.(*Slice{{\[\[\]int,int\]}}).Append$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK-LABEL: define linkonce ptr @"main.(*Slice{{\[\[\]int,int\]}}).Append2$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK-LABEL: define linkonce ptr @"main.(*Slice{{\[\[\]string,string\]}}).Append$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
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
