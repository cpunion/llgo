package wasmtest

import (
	"reflect"
	"runtime"
	"testing"
)

// Reflection's branch-dependent by-value wrappers copy this aggregate even
// though it is below the separate 64 KiB indirect-return ABI threshold.
type gcReflectionAggregate struct {
	pointer *int
	data    [8192]byte
}

//go:noinline
func (value gcReflectionAggregate) Check() int {
	collectGCAggregate()
	return *value.pointer + int(value.data[0]) + int(value.data[len(value.data)-1])
}

func TestLargeReflectionGCRoots(t *testing.T) {
	value := gcReflectionAggregate{pointer: new(int)}
	*value.pointer = 101
	value.data[0], value.data[len(value.data)-1] = 11, 97
	const want = 101 + 11 + 97
	for i := 0; i < 3; i++ {
		method := reflect.ValueOf(value).Method(0)
		function := reflect.ValueOf(gcReflectionAggregate.Check)
		runtime.GC()
		for _, got := range []int{
			value.Check(),
			int(method.Call(nil)[0].Int()),
			int(function.Call([]reflect.Value{reflect.ValueOf(value)})[0].Int()),
		} {
			if got != want {
				t.Fatalf("reflection snapshot = %d, want %d", got, want)
			}
		}
	}
}
