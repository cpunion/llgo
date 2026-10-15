package main

import (
	"reflect"
	"syscall"
)

func expectPanic(f func()) {
	panicked := false
	func() {
		defer func() {
			var message string
			switch value := recover().(type) {
			case error:
				message = value.Error()
			case string:
				message = value
			}
			panicked = message == "bad ABI" || message == "libffi is unsupported on this freestanding target"
		}()
		f()
	}()
	if !panicked {
		panic("unsupported dynamic reflection silently succeeded")
	}
}

func main() {
	expectPanic(func() {
		reflect.ValueOf(func() {}).Call(nil)
	})
	expectPanic(func() {
		reflect.MakeFunc(reflect.TypeOf(func() {}), func([]reflect.Value) []reflect.Value {
			return nil
		})
	})
	const success = "WASI dynamic reflection fails explicitly\n"
	if n, err := syscall.Write(1, []byte(success)); err != nil || n != len(success) {
		panic("reflection failure marker")
	}
}
