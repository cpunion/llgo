package getgprobe

import (
	"testing"
	"unsafe"
)

//go:linkname getg github.com/xgo-dev/llgo/runtime/internal/runtime.getg
func getg() unsafe.Pointer

var pointerSink unsafe.Pointer
var integerSink int

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
