//go:build !nogc && !baremetal && !llgo_wasm_gc

package runtime

import (
	"runtime"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/clite/bdwgc"
	"github.com/xgo-dev/llgo/runtime/internal/coroalloc"
)

func init() {
	// Legacy entry paths initialize the same allocator here. Coroutine entry
	// performs this phase explicitly before any Go/runtime initialization.
	if !coroalloc.Bootstrap() {
		c.Exit(2)
	}
	enableForeignThreadRegistration()
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
		Alloc:        uint64(heapAlloc),
		TotalAlloc:   uint64(totalBytes),
		Sys:          uint64(heapSys),
		HeapAlloc:    uint64(heapAlloc),
		HeapSys:      uint64(heapSys),
		HeapIdle:     uint64(heapIdle),
		HeapInuse:    uint64(heapInuse),
		HeapReleased: uint64(unmappedBytes),
		NumGC:        uint32(bdwgc.GetGCNo()),
	}
}

func GC() {
	collectGarbage(false)
}

func freeOSMemoryForCollector() {
	collectGarbage(true)
}

func collectGarbage(release bool) {
	collectAndRunFinalizers(false)
	// Run one extra cycle so weak-pointer cleanup hooks (unique/weak) see
	// finalized state before we trigger map cleanup callbacks.
	collectAndRunFinalizers(false)
	if release {
		// BDWGC's forced unmap pass still skips blocks reclaimed in the
		// previous cycle. Advance the free blocks through that interval
		// before requesting unmap so this call can release its own garbage.
		collectAndRunFinalizers(true)
	}
	unique_runtime_notifyMapCleanup()
	if poolCleanup != nil {
		poolCleanup()
	}
}

func collectAndRunFinalizers(release bool) {
	// GC_clear_stack only scrubs inaccessible stack space below this frame. It
	// cannot reach dead slots in active callers; compiler-emitted volatile
	// clears handle those. This remains useful as best-effort cleanup for
	// storage vacated before GC was entered.
	bdwgc.ClearStack(nil)
	if release {
		bdwgc.GcollectAndUnmap()
	} else {
		bdwgc.Gcollect()
	}
	// GC_gcollect only discovers unreachable finalizable objects. Explicitly
	// drain BDWGC's ready queue so runtime.GC does not depend on a later
	// allocation to invoke the callbacks that feed runFinalizers.
	bdwgc.InvokeFinalizers()
	runFinalizers()
}

func saturatingSub(x, y uintptr) uintptr {
	if x < y {
		return 0
	}
	return x - y
}
