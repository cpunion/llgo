//go:build llgo && wasm

package wasmtest

import (
	"reflect"
	"testing"
	"unsafe"
)

//go:linkname pushCallerFrame github.com/xgo-dev/llgo/runtime/internal/runtime.PushCallerLocationFrameWasm
func pushCallerFrame(entry uintptr, nameData *byte, nameLen int, fileData *byte, fileLen, line int) int

//go:linkname recordCallerLocation github.com/xgo-dev/llgo/runtime/internal/runtime.RecordCallerLocationWasm
func recordCallerLocation(entry uintptr, nameData *byte, nameLen int, fileData *byte, fileLen, line int)

//go:linkname recordPanicLocation github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm
func recordPanicLocation(entry uintptr, nameData *byte, nameLen int, fileData *byte, fileLen, line int)

//go:linkname popCallerFrame github.com/xgo-dev/llgo/runtime/internal/runtime.PopCallerLocationFrame
func popCallerFrame(mark int)

//go:noinline
func callerAllocationFunction() func(int) int {
	return func(value int) int { return value + 1 }
}

func TestCallerInstrumentationAllocations(t *testing.T) {
	const name, file = "wasmtest.callerAllocationFunction", "caller_alloc_test.go"
	entry := reflect.ValueOf(callerAllocationFunction).Pointer()
	nameData, fileData := unsafe.StringData(name), unsafe.StringData(file)
	// AllocsPerRun warms up the shadow stack and frame registry before
	// measuring. Updating existing metadata must not allocate string headers.
	if got := testing.AllocsPerRun(100, func() {
		mark := pushCallerFrame(entry, nameData, len(name), fileData, len(file), 1)
		recordCallerLocation(entry, nameData, len(name), fileData, len(file), 2)
		recordPanicLocation(entry, nameData, len(name), fileData, len(file), 3)
		popCallerFrame(mark)
	}); got != 0 {
		t.Fatalf("caller metadata updates allocate %v objects, want 0", got)
	}

	// Also exercise compiler-inserted instrumentation around ordinary
	// no-capture function values, as in GOROOT's closure allocation check.
	if got := testing.AllocsPerRun(100, func() {
		first, second := callerAllocationFunction(), callerAllocationFunction()
		if first(1) != 2 || second(2) != 3 {
			panic("invalid no-capture function result")
		}
	}); got != 0 {
		t.Fatalf("no-capture function calls allocate %v objects, want 0", got)
	}
}
