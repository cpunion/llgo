//go:build !nogc && (baremetal || ((wasm || tinygo.wasm) && llgo_wasm_gc))

package runtime

import (
	"runtime"

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/coroalloc"
	"github.com/goplus/llgo/runtime/internal/runtime/tinygogc"
)

func init() {
	// Coroutine entry performs this before runtime initialization. Keep legacy
	// and embedded entry paths idempotent and fail closed.
	if !coroalloc.Bootstrap() {
		c.Exit(2)
	}
}

func ReadMemStats(m *runtime.MemStats) {
	if m == nil {
		return
	}
	stats := tinygogc.ReadGCStats()
	*m = runtime.MemStats{
		Alloc:      stats.Alloc,
		TotalAlloc: stats.TotalAlloc,
		Sys:        stats.Sys,
		Mallocs:    stats.Mallocs,
		Frees:      stats.Frees,
		NumGC:      stats.NumGC,
		HeapAlloc:  stats.HeapAlloc,
		HeapSys:    stats.HeapSys,
		HeapIdle:   stats.HeapIdle,
		HeapInuse:  stats.HeapInuse,
		StackInuse: stats.StackInuse,
		StackSys:   stats.StackSys,
		GCSys:      stats.GCSys,
	}
}

func GC() {
	tinygogc.GC()
	if poolCleanup != nil {
		poolCleanup()
	}
}
