//go:build baremetal && (darwin || linux || wasm || windows)

package runtime

import "unsafe"

// Cache misses still resolve through the same metadata and symbol tables.
// Keep the allocation-free four-way cache, but do not reserve a desktop-sized
// lookup cache on a small board. This does not limit functions or goroutines.
const funcForPCCacheSets = 32

// Bare-metal profiles retain their fixed allocation-free cache and symbols.
var funcForPCCache [funcForPCCacheSets][funcForPCCacheWays]unsafe.Pointer
var funcForPCCacheNext [funcForPCCacheSets]uint32

// Keep the PC mask inside the helper so its array access has a local range proof.
func funcForPCReadSet(pc uintptr) *[funcForPCCacheWays]unsafe.Pointer {
	index := (pc >> 4) & (funcForPCCacheSets - 1)
	// This dominating branch is recognized by the frontend no-unwind proof;
	// the mask makes its fallback unreachable for every uintptr PC.
	if index < funcForPCCacheSets {
		return &funcForPCCache[index]
	}
	return nil
}

func funcForPCInsertionSet(index uintptr) (*[funcForPCCacheWays]unsafe.Pointer, *uint32) {
	return &funcForPCCache[index], &funcForPCCacheNext[index]
}
