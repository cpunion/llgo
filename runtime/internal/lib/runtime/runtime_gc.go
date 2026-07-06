//go:build !nogc && !baremetal && !llgo_wasm_gc

package runtime

import (
	"runtime"

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/bdwgc"
	"github.com/goplus/llgo/runtime/internal/coroalloc"
)

func init() {
	// Legacy entry paths initialize the same allocator here. Coroutine entry
	// performs this phase explicitly before any Go/runtime initialization.
	if !coroalloc.Bootstrap() {
		c.Exit(2)
	}
}

func ReadMemStats(m *runtime.MemStats) {
	if m == nil {
		return
	}
	var heapSize, freeBytes, unmappedBytes, bytesSinceGC, totalBytes uintptr
	bdwgc.GetHeapUsageSafe(&heapSize, &freeBytes, &unmappedBytes, &bytesSinceGC, &totalBytes)

	heapSys := heapSize + unmappedBytes
	heapIdle := freeBytes + unmappedBytes
	heapInuse := saturatingSub(heapSys, heapIdle)
	heapAlloc := heapInuse
	*m = runtime.MemStats{
		Alloc:      uint64(heapAlloc),
		TotalAlloc: uint64(totalBytes),
		Sys:        uint64(heapSys),
		HeapAlloc:  uint64(heapAlloc),
		HeapSys:    uint64(heapSys),
		HeapIdle:   uint64(heapIdle),
		HeapInuse:  uint64(heapInuse),
		NumGC:      uint32(bdwgc.GetGCNo()),
	}
}

func GC() {
	bdwgc.ClearStack(nil)
	bdwgc.Gcollect()
	runFinalizers()
	// BDW finalizers are observed on a subsequent collection cycle.
	// Run one extra cycle so weak-pointer cleanup hooks (unique/weak) see
	// finalized state before we trigger map cleanup callbacks.
	bdwgc.ClearStack(nil)
	bdwgc.Gcollect()
	runFinalizers()
	unique_runtime_notifyMapCleanup()
	if poolCleanup != nil {
		poolCleanup()
	}
}

func saturatingSub(x, y uintptr) uintptr {
	if x < y {
		return 0
	}
	return x - y
}
