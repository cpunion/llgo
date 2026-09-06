package gotest

import (
	"reflect"
	"runtime"
	"testing"
)

func TestDiagnosticGoroutineStartupGC(t *testing.T) {
	stopGC := make(chan struct{})
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		for {
			select {
			case <-stopGC:
				return
			default:
				runtime.GC()
			}
		}
	}()

	const n = 100
	done := make(chan *int, n)
	for i := 0; i < n; i++ {
		value := new(int)
		*value = i
		go func(value *int) {
			done <- value
		}(value)
	}
	for i := 0; i < n; i++ {
		if value := <-done; value == nil || *value < 0 || *value >= n {
			t.Fatalf("corrupt goroutine argument: %v", value)
		}
	}
	close(stopGC)
	<-gcDone
}

func TestDiagnosticReflectMakeFuncGoroutineNoExplicitGC(t *testing.T) {
	const n = 100
	done := make(chan *int, n)
	for i := 0; i < n; i++ {
		value := new(int)
		*value = i
		f := reflect.MakeFunc(reflect.TypeOf((func(*int))(nil)), func(args []reflect.Value) []reflect.Value {
			if len(args) != 1 || args[0].IsNil() {
				panic("bad reflect MakeFunc pointer argument")
			}
			done <- args[0].Interface().(*int)
			return nil
		}).Interface().(func(*int))
		go f(value)
	}
	for i := 0; i < n; i++ {
		if value := <-done; value == nil || *value < 0 || *value >= n {
			t.Fatalf("corrupt reflect goroutine argument: %v", value)
		}
	}
}
