package nativeprobe

import (
	"sync/atomic"
	"testing"
	"unsafe"
)

//go:linkname getg github.com/xgo-dev/llgo/runtime/internal/runtime.getg
func getg() unsafe.Pointer

var pointerSink unsafe.Pointer
var integerSink int
var global int64

func BenchmarkGetG(b *testing.B) {
	for i := 0; i < b.N; i++ {
		pointerSink = getg()
	}
}

//go:noinline
func directCall(v int) int { return v + 1 }

func BenchmarkDirectCall(b *testing.B) {
	v := 0
	for i := 0; i < b.N; i++ {
		v = directCall(v)
	}
	integerSink = v
}

//go:noinline
func writeGlobal(v int64) { atomic.StoreInt64(&global, v) }

func BenchmarkGlobalWrite(b *testing.B) {
	for i := 0; i < b.N; i++ {
		writeGlobal(int64(i))
	}
}
