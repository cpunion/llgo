//go:build !baremetal && (darwin || linux || wasm || windows)

package runtime

import (
	"sync/atomic"
	"unsafe"
)

const funcForPCCacheSets = 1024

type funcForPCCacheData struct {
	sets [funcForPCCacheSets][funcForPCCacheWays]unsafe.Pointer
	next [funcForPCCacheSets]uint32
}

// Published once by an existing cache insertion. Cold lookups allocate
// nothing; the complete Go allocation remains visible to the collector.
var funcForPCCache unsafe.Pointer // *funcForPCCacheData

// Keep the PC mask inside the helper so its array access has a local range proof.
func funcForPCReadSet(pc uintptr) *[funcForPCCacheWays]unsafe.Pointer {
	index := (pc >> 4) & (funcForPCCacheSets - 1)
	data := (*funcForPCCacheData)(atomic.LoadPointer(&funcForPCCache))
	if data == nil {
		return nil
	}
	// This dominating branch is recognized by the frontend no-unwind proof;
	// the mask makes its fallback unreachable for every uintptr PC.
	if index < funcForPCCacheSets {
		return &data.sets[index]
	}
	return nil
}

func funcForPCInsertionSet(index uintptr) (*[funcForPCCacheWays]unsafe.Pointer, *uint32) {
	data := (*funcForPCCacheData)(atomic.LoadPointer(&funcForPCCache))
	if data == nil {
		candidate := new(funcForPCCacheData)
		if atomic.CompareAndSwapPointer(&funcForPCCache, nil, unsafe.Pointer(candidate)) {
			data = candidate
		} else {
			data = (*funcForPCCacheData)(atomic.LoadPointer(&funcForPCCache))
		}
	}
	return &data.sets[index], &data.next[index]
}
