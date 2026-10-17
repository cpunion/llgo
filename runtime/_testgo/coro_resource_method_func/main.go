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

func verifyReflection(fn reflect.Value, receiver any) {
	before := acquisitions
	values := fn.Call([]reflect.Value{reflect.ValueOf(receiver), reflect.ValueOf(17)})
	if len(values) != 1 || values[0].Int() != 17 || acquisitions != before+1 {
		panic("Method.Func reflection lost its guarded resource result")
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
	verifyReflection(value, Receiver{})
	verifyReflection(pointer, (*PointerReceiver)(nil))
	// A nil pointer-to-value receiver panics before acquisition. Its still
	// empty state must not be passed to a snapshot-dependent drop helper.
	snapshot := method((*SnapshotReceiver)(nil))
	if snapshot.Pointer() == 0 {
		panic("snapshot method lost its code identity")
	}
	before := acquisitions
	func() {
		defer func() {
			if recover() == nil {
				panic("nil value receiver did not panic")
			}
		}()
		snapshot.Call([]reflect.Value{reflect.ValueOf((*SnapshotReceiver)(nil)), reflect.ValueOf(17)})
	}()
	if acquisitions != before {
		panic("nil receiver acquired a resource")
	}
	verifyReflection(snapshot, &SnapshotReceiver{Token: 17})
	plain := method(PlainReceiver{}).Call([]reflect.Value{reflect.ValueOf(PlainReceiver{}), reflect.ValueOf(20)})
	if len(plain) != 1 || plain[0].Int() != 21 {
		panic("ordinary Method.Func acquired false resource ownership")
	}
	// An arbitrary MakeFunc with the exact same Go signature does not inherit
	// the source method's ownership from its type or its returned integer.
	callback := reflect.MakeFunc(value.Type(), func([]reflect.Value) []reflect.Value {
		return []reflect.Value{reflect.ValueOf(23)}
	})
	before = acquisitions
	values := callback.Call([]reflect.Value{reflect.ValueOf(Receiver{}), reflect.ValueOf(0)})
	if len(values) != 1 || values[0].Int() != 23 || acquisitions != before {
		panic("ordinary MakeFunc result acquired false resource ownership")
	}
	println("Method.Func delivery identity and reflection boundary ok")
}
