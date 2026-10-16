package runtime

import (
	"runtime"
	_ "unsafe"

	llrt "github.com/xgo-dev/llgo/runtime/internal/runtime"
)

var (
	llgoMaxStack  int
	llgoGCPercent int32 = 100
	llgoMemLimit  int64
)

//go:linkname setTraceback runtime/debug.SetTraceback
func setTraceback(level string) {}

//go:linkname readGCStats runtime/debug.readGCStats
func readGCStats(pauses *[]int64) {
	// time.Duration is an int64, not uint64. Avoid importing time into the
	// runtime replacement and use the same collector snapshot as ReadMemStats.
	var stats runtime.MemStats
	ReadMemStats(&stats)
	count := len(stats.PauseNs)
	if stats.NumGC < uint32(count) {
		count = int(stats.NumGC)
	}
	// Collectors which do not publish pause timestamps have no pause history;
	// do not synthesize zero-duration pause entries from their real cycle count.
	if stats.LastGC == 0 {
		count = 0
	}
	length := 2*count + 3
	if cap(*pauses) < length {
		*pauses = make([]int64, length)
	} else {
		*pauses = (*pauses)[:length]
	}
	buffer := *pauses
	for index := 0; index < count; index++ {
		slot := (uint64(stats.NumGC) - 1 - uint64(index)) % uint64(len(stats.PauseNs))
		buffer[index] = int64(stats.PauseNs[slot])
		buffer[count+index] = int64(stats.PauseEnd[slot])
	}
	buffer[2*count] = int64(stats.LastGC)
	buffer[2*count+1] = int64(stats.NumGC)
	buffer[2*count+2] = int64(stats.PauseTotalNs)
}

//go:linkname freeOSMemory runtime/debug.freeOSMemory
func freeOSMemory() {}

//go:linkname setMaxStack runtime/debug.setMaxStack
func setMaxStack(in int) (out int) {
	out = llgoMaxStack
	llgoMaxStack = in
	return out
}

//go:linkname setGCPercent runtime/debug.setGCPercent
func setGCPercent(in int32) (out int32) {
	out = llgoGCPercent
	llgoGCPercent = in
	return out
}

//go:linkname setPanicOnFault runtime/debug.setPanicOnFault
func setPanicOnFault(in bool) (out bool) {
	return llrt.SetPanicOnFault(in)
}

//go:linkname setMaxThreads runtime/debug.setMaxThreads
func setMaxThreads(in int) (out int) {
	return llgoSetMaxThreads(in)
}

//go:linkname setMemoryLimit runtime/debug.setMemoryLimit
func setMemoryLimit(in int64) (out int64) {
	out = llgoMemLimit
	llgoMemLimit = in
	return out
}

//go:linkname runtime_debug_WriteHeapDump runtime/debug.WriteHeapDump
func runtime_debug_WriteHeapDump(uintptr) {}

//go:linkname debug_modinfo runtime/debug.modinfo
func debug_modinfo() string { return "" }
