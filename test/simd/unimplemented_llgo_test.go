//go:build llgo && goexperiment.simd && (amd64 || arm64 || wasm)

package simd_test

import (
	"reflect"
	"simd/archsimd"
	"strings"
	"testing"
	_ "unsafe"
)

//go:linkname simdDiv simd/archsimd.Float32x4.Div
func simdDiv(x, y archsimd.Float32x4) archsimd.Float32x4

func TestUnimplementedSIMD(t *testing.T) {
	var x archsimd.Float32x4
	method := x.Div
	for _, tc := range []struct {
		name   string
		call   func()
		symbol string
	}{
		{"direct", func() { x.Div(x) }, "simd/archsimd.Float32x4.Div"},
		{"method value", func() { method(x) }, "simd/archsimd.Float32x4.Div"},
		{"method expression", func() { indirect(archsimd.Float32x4.Div, x, x) }, "simd/archsimd.Float32x4.Div"},
		{"deferred", func() { defer x.Div(x) }, "simd/archsimd.Float32x4.Div"},
		{"linkname", func() { simdDiv(x, x) }, "simd/archsimd.Float32x4.Div"},
		{"mask result", func() { x.Equal(x) }, "simd/archsimd.Float32x4.Equal"},
		{"package function", func() { archsimd.LoadFloat32x4Array(new([4]float32)) }, "simd/archsimd.LoadFloat32x4Array"},
		{"helper", func() { archsimd.BroadcastFloat32x4(1) }, "simd/archsimd."},
		{"reflected method", func() { reflect.ValueOf(x).MethodByName("Div").Call([]reflect.Value{reflect.ValueOf(x)}) }, "simd/archsimd.Float32x4.Div"},
		{"reflection", func() { reflect.ValueOf(method).Call([]reflect.Value{reflect.ValueOf(x)}) }, "simd/archsimd.Float32x4.Div"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				err, ok := recover().(interface{ Error() string })
				if !ok || !strings.HasPrefix(err.Error(), "runtime error: unimplemented SIMD intrinsic: "+tc.symbol) {
					t.Fatalf("unexpected panic: %v", err)
				}
			}()
			tc.call()
			t.Fatal("unimplemented intrinsic returned")
		})
	}
	// Go helper bodies remain executable; the fallback applies to declarations.
	if x.Len() != 4 {
		t.Fatal("Go helper was replaced by the fallback")
	}
}

func TestVectorReflection(t *testing.T) {
	var x archsimd.Float32x4
	x = x.SetElem(0, 9).SetElem(3, -7)
	fn := reflect.ValueOf(vectorIdentity)
	out := fn.Call([]reflect.Value{reflect.ValueOf(x)})
	if got := out[0].Interface().(archsimd.Float32x4); got.GetElem(0) != 9 || got.GetElem(3) != -7 {
		t.Fatal("reflect.Call vector ABI")
	}
	out = reflect.ValueOf(x).MethodByName("Add").Call([]reflect.Value{reflect.ValueOf(x)})
	if got := out[0].Interface().(archsimd.Float32x4); got.GetElem(0) != 18 || got.GetElem(3) != -14 {
		t.Fatal("reflected SIMD method")
	}
	made := reflect.MakeFunc(fn.Type(), func(args []reflect.Value) []reflect.Value { return args })
	got := made.Interface().(func(archsimd.Float32x4) archsimd.Float32x4)(x)
	if got.GetElem(0) != 9 || got.GetElem(3) != -7 {
		t.Fatal("reflect.MakeFunc vector ABI")
	}
}
