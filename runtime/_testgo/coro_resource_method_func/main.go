package main

import (
	"reflect"
	"unsafe"
)

type result = struct{ R0 int }

func initialize(out *result)                    { out.R0 = -1 }
func drop(out *result, _ unsafe.Pointer, _ int) { out.R0 = -1 }

//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*result), func(*result, unsafe.Pointer, int)) *result

var acquisitions int

func acquire(value int) int {
	out := borrow(initialize, drop)
	acquisitions++
	out.R0 = value
	return out.R0
}

type Receiver struct{}

func (Receiver) Acquire(value int) int { return acquire(value) }

type PointerReceiver struct{}

func (*PointerReceiver) Acquire(value int) int { return acquire(value) }

type PlainReceiver struct{}

func (PlainReceiver) Acquire(value int) int { return value + 1 }

type SnapshotReceiver struct{ Token int }

func (r SnapshotReceiver) Acquire(int) int { return acquire(r.Token) }

func method(receiver any) reflect.Value {
	m, ok := reflect.TypeOf(receiver).MethodByName("Acquire")
	if !ok {
		panic("missing Method.Func")
	}
	return m.Func
}

func rejectReflection(fn reflect.Value, receiver any) {
	before := acquisitions
	rejected := false
	func() {
		defer func() {
			if got := recover(); got != "llgo: resource result delivery through reflection is not bound" {
				panic("Method.Func lost its resource-delivery capability")
			}
			rejected = true
		}()
		fn.Call([]reflect.Value{reflect.ValueOf(receiver), reflect.ValueOf(17)})
	}()
	if !rejected || acquisitions != before {
		panic("unprotected Method.Func reflection acquired a resource")
	}
}

func main() {
	value := method(Receiver{})
	pointer := method((*PointerReceiver)(nil))
	// A typed Go invocation has the ordinary caller guard and remains valid.
	if got := value.Interface().(func(Receiver, int) int)(Receiver{}, 17); got != 17 {
		panic("receiver-first managed delivery result")
	}
	if got := pointer.Interface().(func(*PointerReceiver, int) int)(nil, 19); got != 19 || acquisitions != 2 {
		panic("pointer receiver managed delivery result")
	}
	rejectReflection(value, Receiver{})
	rejectReflection(pointer, (*PointerReceiver)(nil))
	// PtrToThis metadata must retain an explicitly unavailable entry for
	// this *T-to-T wrapper, rather than break unrelated value-method calls or
	// silently publish an unguarded function. Identity queries remain valid.
	snapshot := method((*SnapshotReceiver)(nil))
	if snapshot.Pointer() == 0 {
		panic("unbound method lost its code identity")
	}
	rejectReflection(snapshot, (*SnapshotReceiver)(nil))
	plain := method(PlainReceiver{}).Call([]reflect.Value{reflect.ValueOf(PlainReceiver{}), reflect.ValueOf(20)})
	if len(plain) != 1 || plain[0].Int() != 21 {
		panic("ordinary Method.Func acquired false resource ownership")
	}
	// An arbitrary MakeFunc with the exact same Go signature does not inherit
	// the source method's ownership from its type or its returned integer.
	callback := reflect.MakeFunc(value.Type(), func([]reflect.Value) []reflect.Value {
		return []reflect.Value{reflect.ValueOf(23)}
	})
	values := callback.Call([]reflect.Value{reflect.ValueOf(Receiver{}), reflect.ValueOf(0)})
	if len(values) != 1 || values[0].Int() != 23 || acquisitions != 2 {
		panic("ordinary MakeFunc result acquired false resource ownership")
	}
	println("Method.Func delivery identity and reflection boundary ok")
}
