//go:build (baremetal && !nogc) || (wasm && llgo.wasm.gc.linear)

package runtime

import (
	"runtime"

	llruntime "github.com/xgo-dev/llgo/runtime/internal/runtime"
	"github.com/xgo-dev/llgo/runtime/internal/runtime/tinygogc"
)

func ReadMemStats(m *runtime.MemStats) {
	llruntime.AssertNilDeref(m == nil)
	stats := tinygogc.ReadGCStats()
	m.Alloc = stats.Alloc
	m.TotalAlloc = stats.TotalAlloc
	m.Sys = stats.Sys
	m.Mallocs = stats.Mallocs
	m.Frees = stats.Frees
	m.HeapAlloc = stats.HeapAlloc
	m.HeapSys = stats.HeapSys
	m.HeapIdle = stats.HeapIdle
	m.HeapInuse = stats.HeapInuse
	m.StackInuse = stats.StackInuse
	m.StackSys = stats.StackSys
	m.GCSys = stats.GCSys
}

func GC() {
	tinygogc.GC()
	// weak pointers are cleared by the collection above. Wake unique's map
	// cleanup worker only after the collector has released its internal lock.
	unique_runtime_notifyMapCleanup()
	if poolCleanup != nil {
		poolCleanup()
	}
}
