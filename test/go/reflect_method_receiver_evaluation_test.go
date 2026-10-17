package gotest

import (
	"reflect"
	"testing"
)

type reflectReceiver struct{ N int }

func (r reflectReceiver) Read() int        { return r.N }
func (r reflectReceiver) Send(ch chan int) { ch <- r.N }

type reflectOtherReceiver struct{ N int }
type reflectZeroReceiver struct{}

func (reflectZeroReceiver) Zero() struct{} { return struct{}{} }

func (r reflectOtherReceiver) Read() int { return r.N + 100 }

type reflectRecoverReceiver struct{ Done chan int }
type reflectOtherRecoverReceiver struct{ Done chan int }

func (r reflectRecoverReceiver) Finish() int {
	if recover() != nil {
		r.Done <- 17
	}
	return 17
}
func (r reflectOtherRecoverReceiver) Finish() int {
	if recover() != nil {
		r.Done <- 23
	}
	return 23
}

func TestReflectBoundMethodReceiverEvaluation(t *testing.T) {
	var zero interface{ Zero() struct{} } = reflectZeroReceiver{}
	empty := reflect.ValueOf(&zero).Elem().MethodByName("Zero").Interface().(func() struct{})
	_ = empty()
	if out := reflect.ValueOf(empty).Call(nil); len(out) != 1 || out[0].Type() != reflect.TypeOf(struct{}{}) {
		t.Fatal("zero-sized reflected method result")
	}
	r := reflectReceiver{17}
	f := reflect.ValueOf(&r).Elem().MethodByName("Read").Interface().(func() int)
	r.N = 99
	if got := f(); got != 99 {
		t.Fatalf("addressable value = %d, want 99", got)
	}
	p := &reflectReceiver{17}
	f = reflect.ValueOf(&p).Elem().MethodByName("Read").Interface().(func() int)
	p = &reflectReceiver{23}
	if got := f(); got != 23 {
		t.Fatalf("addressable pointer = %d, want 23", got)
	}
	var receiver interface{ Read() int } = reflectReceiver{17}
	f = reflect.ValueOf(&receiver).Elem().MethodByName("Read").Interface().(func() int)
	receiver = reflectOtherReceiver{23}
	if got := f(); got != 123 {
		t.Fatalf("addressable interface = %d, want 123", got)
	}
	if got := reflect.ValueOf(f).Call(nil)[0].Int(); got != 123 {
		t.Fatalf("reflected bound interface = %d, want 123", got)
	}
	ch := make(chan int, 1)
	p = &reflectReceiver{17}
	send := reflect.ValueOf(&p).Elem().MethodByName("Send").Interface().(func(chan int))
	func() {
		defer send(ch)
		p = &reflectReceiver{29}
	}()
	if got := <-ch; got != 29 {
		t.Fatalf("deferred addressable pointer = %d, want 29", got)
	}
	var recoverer interface{ Finish() int } = reflectRecoverReceiver{Done: ch}
	finish := reflect.ValueOf(&recoverer).Elem().MethodByName("Finish").Interface().(func() int)
	recoverer = reflectOtherRecoverReceiver{Done: ch}
	func() { defer finish(); panic("bound interface recover") }()
	if got := <-ch; got != 23 {
		t.Fatalf("deferred dynamic receiver recover = %d, want 23", got)
	}
}
