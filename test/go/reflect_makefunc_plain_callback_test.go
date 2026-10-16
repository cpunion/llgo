package gotest

import (
	"reflect"
	"runtime"
	"testing"
)

var reflectMakeFuncPlainCalls int

// This callback is genuinely plain: no allocation, panic, or suspension. The
// source reflect.Value and its runtime replacement still share one callable
// identity when CHA classifies the callback stored by MakeFunc.
func reflectMakeFuncPlainCallback([]reflect.Value) []reflect.Value {
	reflectMakeFuncPlainCalls++
	return nil
}

func TestReflectMakeFuncPlainAndCoroutineCallbacks(t *testing.T) {
	before := reflectMakeFuncPlainCalls
	coroCalls := 0
	callbacks := []func([]reflect.Value) []reflect.Value{
		reflectMakeFuncPlainCallback,
		func([]reflect.Value) []reflect.Value {
			runtime.Gosched()
			coroCalls++
			return nil
		},
	}
	for _, callback := range callbacks {
		value := reflect.MakeFunc(reflect.TypeOf((func())(nil)), callback)
		fn := value.Interface().(func())
		fn()
		value.Call(nil)
		func() {
			defer fn()
			runtime.Gosched()
		}()
	}
	if got := reflectMakeFuncPlainCalls - before; got != 3 || coroCalls != 3 {
		t.Fatalf("MakeFunc callback counts: plain=%d coroutine=%d, want 3 each", got, coroCalls)
	}
}
