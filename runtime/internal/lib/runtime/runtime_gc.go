//go:build !nogc && !baremetal && !wasm

package runtime

import (
	"runtime"

	"github.com/xgo-dev/llgo/runtime/internal/clite/bdwgc"
)

func init() {
	bdwgc.Init()
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
	collectAndRunFinalizers("first")
	// Run one extra cycle so weak-pointer cleanup hooks (unique/weak) see
	// finalized state before we trigger map cleanup callbacks.
	collectAndRunFinalizers("second")
	unique_runtime_notifyMapCleanup()
	if poolCleanup != nil {
		poolCleanup()
	}
}

func collectAndRunFinalizers(cycle string) {
	start := runtimeNano()
	bdwgc.Gcollect()
	reportSlowGCStage(cycle+" collection", start)
	// GC_gcollect only discovers unreachable finalizable objects. Explicitly
	// drain BDWGC's ready queue so runtime.GC does not depend on a later
	// allocation to invoke the callbacks that feed runFinalizers.
	start = runtimeNano()
	bdwgc.InvokeFinalizers()
	reportSlowGCStage(cycle+" invoke finalizers", start)
	start = runtimeNano()
	runFinalizers()
	reportSlowGCStage(cycle+" run finalizers", start)
}

func reportSlowGCStage(stage string, start int64) {
	elapsed := runtimeNano() - start
	if elapsed >= 100_000_000 {
		print("llgo GC diagnostic: ", stage, " took ", elapsed/1_000_000, "ms\n")
	}
}

func saturatingSub(x, y uintptr) uintptr {
	if x < y {
		return 0
	}
	return x - y
}
