//go:build llgo && js && wasm && llgo.wasm.reflect.libffi && !llgo.wasm.emscripten.memory64

package gotest

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestReflectNativeMakeFuncWasm32WordRange(t *testing.T) {
	wide := uintptr(uint64(1) << 40)
	for _, test := range []struct {
		name string
		call func()
	}{
		{"reflected-input", func() {
			newReflectNativeMakeFunc().Call([]reflect.Value{reflect.ValueOf(wide)})
		}},
		{"typed-input", func() {
			newReflectNativeMakeFunc().Interface().(reflectNativeMakeFunc)(wide)
		}},
		{"reflected-result", func() {
			fn := reflect.MakeFunc(reflect.TypeOf(reflectNativeMakeFunc(nil)), func([]reflect.Value) []reflect.Value {
				return []reflect.Value{reflect.ValueOf(wide)}
			})
			fn.Call([]reflect.Value{reflect.ValueOf(uintptr(1))})
		}},
		{"nested-array-input", func() {
			fn := reflect.MakeFunc(reflect.TypeOf(reflectNativeNestedWordArrayFunc(nil)), func([]reflect.Value) []reflect.Value {
				t.Fatal("out-of-range nested array reached the native callback")
				return nil
			})
			fn.Interface().(reflectNativeNestedWordArrayFunc)([2][2]uintptr{{1, 2}, {3, wide}})
		}},
		{"typed-result", func() {
			fn := reflect.MakeFunc(reflect.TypeOf(reflectNativeMakeFunc(nil)), func([]reflect.Value) []reflect.Value {
				return []reflect.Value{reflect.ValueOf(wide)}
			})
			fn.Interface().(reflectNativeMakeFunc)(1)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if message := fmt.Sprint(recover()); !strings.Contains(message, "ABI integer conversion out of range") {
					t.Fatalf("wide native word panic = %q", message)
				}
			}()
			test.call()
			t.Fatal("native wasm32 word silently truncated a Go-width integer")
		})
	}
}
