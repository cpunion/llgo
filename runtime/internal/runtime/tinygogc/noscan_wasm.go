//go:build wasm && llgo.wasm.gc.linear

package tinygogc

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

type noScanAllocation struct {
	key       uintptr
	indexNext *noScanAllocation
}

var (
	noScanIndex []*noScanAllocation
	noScanCount int
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
	prepareNoScanIndex()
	ptr := AllocRoot(size)

	lock(&gcMutex)
	record.key = encodeNoScanAddress(uintptr(ptr))
	bucket := noScanBucket(record.key, len(noScanIndex))
	record.indexNext = noScanIndex[bucket]
	noScanIndex[bucket] = record
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
	link := &noScanIndex[noScanBucket(key, len(noScanIndex))]
	for *link != nil && (*link).key != key {
		link = &(*link).indexNext
	}
	if *link == nil {
		unlock(&gcMutex)
		gcPanic(c.Str("gc: invalid no-scan root release"))
		return
	}
	record := *link
	*link = record.indexNext
	record.indexNext = nil
	noScanCount--
	unlock(&gcMutex)
	FreeRoot(ptr)
}

func prepareNoScanIndex() {
	for noScanCount >= len(noScanIndex) {
		size := len(noScanIndex) * 2
		if size == 0 {
			size = 16
		}
		index := make([]*noScanAllocation, size)
		lock(&gcMutex)
		if len(index) > len(noScanIndex) {
			for _, head := range noScanIndex {
				for record := head; record != nil; {
					next := record.indexNext
					bucket := noScanBucket(record.key, len(index))
					record.indexNext = index[bucket]
					index[bucket] = record
					record = next
				}
			}
			noScanIndex = index
		}
		unlock(&gcMutex)
	}
}

func shouldScanObject(head uintptr) bool {
	if len(noScanIndex) == 0 {
		return true
	}
	key := encodeNoScanAddress(gcAddressOf(head))
	for record := noScanIndex[noScanBucket(key, len(noScanIndex))]; record != nil; record = record.indexNext {
		if record.key == key {
			return false
		}
	}
	return true
}

func encodeNoScanAddress(address uintptr) uintptr {
	return ^address
}

func noScanBucket(key uintptr, size int) uintptr {
	key /= bytesPerBlock
	key ^= key >> 16
	return key * 0x9e3779b1 & uintptr(size-1)
}
