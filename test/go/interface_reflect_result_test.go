package gotest

import (
	"math/rand"
	"reflect"
	"runtime"
	"testing"
)

type reflectedResultGenerator interface {
	Generate(*rand.Rand, int) reflect.Value
}

type reflectedResultInt int

func (n reflectedResultInt) Generate(r *rand.Rand, size int) reflect.Value {
	if r == nil {
		panic("missing generator argument")
	}
	runtime.Gosched()
	return reflect.ValueOf(int(n) + size)
}

func TestInterfaceMethodPatchedReflectResult(t *testing.T) {
	n := reflectedResultInt(9)
	for _, value := range []reflect.Value{
		reflect.Zero(reflect.TypeOf(n)), reflect.ValueOf(n), reflect.ValueOf(&n),
	} {
		want := 59
		if value.Kind() != reflect.Pointer && value.Int() == 0 {
			want = 50
		}
		runtime.GC()
		generic, ok := reflect.TypeAssert[reflectedResultGenerator](value)
		if !ok {
			t.Fatalf("generic assertion failed for %v", value.Type())
		}
		ordinary, ok := value.Interface().(reflectedResultGenerator)
		if !ok {
			t.Fatalf("ordinary assertion failed for %v", value.Type())
		}
		for _, generator := range []reflectedResultGenerator{generic, ordinary} {
			got := generator.Generate(rand.New(rand.NewSource(4)), 50)
			if got.Interface().(int) != want {
				t.Fatalf("%v generated %v, want %d", value.Type(), got.Interface(), want)
			}
		}
	}
}
