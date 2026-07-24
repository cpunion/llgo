package gotest

import (
	"reflect"
	"testing"
	"time"
)

func TestReflectCallCapturedCoroutine(t *testing.T) {
	captured := 7
	fn := func(value int, text string) (int, string) {
		time.Sleep(time.Millisecond)
		return captured + value, text + "!"
	}

	out := reflect.ValueOf(fn).Call([]reflect.Value{
		reflect.ValueOf(5),
		reflect.ValueOf("ok"),
	})
	if got := out[0].Int(); got != 12 {
		t.Fatalf("integer result = %d, want 12", got)
	}
	if got := out[1].String(); got != "ok!" {
		t.Fatalf("string result = %q, want ok!", got)
	}
}

type namedReflectMakeFunc func(int8) int8

func TestReflectCallNamedMakeFuncCoroutine(t *testing.T) {
	value := reflect.MakeFunc(
		reflect.TypeOf((namedReflectMakeFunc)(nil)),
		func(args []reflect.Value) []reflect.Value {
			time.Sleep(time.Millisecond)
			return []reflect.Value{reflect.ValueOf(int8(args[0].Int() + 1))}
		},
	)
	out := value.Call([]reflect.Value{reflect.ValueOf(int8(41))})
	if got := out[0].Int(); got != 42 {
		t.Fatalf("named MakeFunc result = %d, want 42", got)
	}
}
