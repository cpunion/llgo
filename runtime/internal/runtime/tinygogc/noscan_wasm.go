//go:build wasm && llgo.wasm.gc.linear

package tinygogc

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

type noScanAllocation struct {
	key  uintptr
	next *noScanAllocation
}

var (
	noScanAllocations *noScanAllocation
	noScanCount       int
)

// AllocNoScanRoot allocates runtime-owned storage whose allocation must stay
// live without recursively tracing its payload. The used portion of the main
// Fiber stack is registered separately, while the system Asyncify buffer is
// covered by the active physical stack and compiler root chains. Scanning the
// complete multi-megabyte capacities instead turns stale stack words into
// roots. Suspended ordinary goroutine stacks remain conservatively scanned as
// a fallback for values live across cooperative scheduling.
func AllocNoScanRoot(size uintptr) unsafe.Pointer {
	record := new(noScanAllocation)
	ptr := AllocRoot(size)

	lock(&gcMutex)
	record.key = encodeNoScanAddress(uintptr(ptr))
	record.next = noScanAllocations
	noScanAllocations = record
	noScanCount++
	unlock(&gcMutex)
	return ptr
}

func FreeNoScanRoot(ptr unsafe.Pointer) {
	if ptr == nil || ptr == unsafe.Pointer(&zeroSizedAlloc) {
		return
	}
	key := encodeNoScanAddress(uintptr(ptr))
	lock(&gcMutex)
	link := &noScanAllocations
	for *link != nil && (*link).key != key {
		link = &(*link).next
	}
	if *link == nil {
		unlock(&gcMutex)
		gcPanic(c.Str("gc: invalid no-scan root release"))
		return
	}
	record := *link
	*link = record.next
	record.next = nil
	noScanCount--
	unlock(&gcMutex)
	FreeRoot(ptr)
}
func shouldScanObject(head uintptr) bool {
	key := encodeNoScanAddress(gcAddressOf(head))
	for record := noScanAllocations; record != nil; record = record.next {
		if record.key == key {
			return false
		}
	}
	return true
}

func encodeNoScanAddress(address uintptr) uintptr {
	return ^address
}
