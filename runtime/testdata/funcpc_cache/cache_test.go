package runtime

import (
	"sync"
	"testing"
	"unsafe"
)

type Func struct{ pc, symbol uintptr }

func ensureRuntimePCLN() bool { return true }

// A cache miss must resolve its own PC, never a colliding entry's payload.
func funcForPCSlow(pc uintptr) *Func {
	fn := &Func{pc: pc, symbol: pc ^ 0x55aa}
	cacheFuncForPC(pc, fn)
	return fn
}

func collidingPC(index int) uintptr {
	return 0x10 + uintptr(index*funcForPCCacheSets)<<4
}

func TestExactMissEvictionAndWrap(t *testing.T) {
	if funcForPCCacheSets <= 0 || funcForPCCacheSets&(funcForPCCacheSets-1) != 0 ||
		funcForPCCacheWays <= 0 || funcForPCCacheWays&(funcForPCCacheWays-1) != 0 {
		t.Fatal("mask-based indexing requires power-of-two dimensions")
	}
	first := FuncForPC(collidingPC(0))
	if FuncForPC(first.pc) != first {
		t.Fatal("exact hot lookup must retain the cached immutable Func")
	}
	for index := 1; index <= funcForPCCacheWays; index++ {
		pc := collidingPC(index)
		if funcForPCCacheIndex(pc) != funcForPCCacheIndex(first.pc) {
			t.Fatal("fixture did not collide")
		}
		if fn := FuncForPC(pc); fn.pc != pc || fn.symbol != pc^0x55aa {
			t.Fatal("collision returned a different function")
		}
	}
	for _, entry := range funcForPCCache[funcForPCCacheIndex(first.pc)] {
		if cachedFuncForPC(&entry, first.pc) != nil {
			t.Fatal("full-set replacement did not evict the oldest entry")
		}
	}
	if fn := FuncForPC(first.pc); fn == first || *fn != *first {
		t.Fatal("evicted lookup did not reconstruct the same symbol")
	}
	set := funcForPCCacheIndex(first.pc)
	funcForPCCacheNext[set] = ^uint32(0)
	pc := collidingPC(2 * funcForPCCacheWays)
	if fn := FuncForPC(pc); fn.pc != pc || fn.symbol != pc^0x55aa || funcForPCCacheNext[set] != 0 {
		t.Fatal("replacement counter wrap changed lookup semantics")
	}
	// A stale pointer to a different PC is always a miss, not a partial match.
	entry := unsafe.Pointer(first)
	if cachedFuncForPC(&entry, pc) != nil {
		t.Fatal("lookup accepted a stale colliding PC")
	}
}

func TestConcurrentCollidingLookups(t *testing.T) {
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Go(func() {
			for iteration := 0; iteration < 300; iteration++ {
				pc := collidingPC(worker*300 + iteration)
				if fn := FuncForPC(pc); fn.pc != pc || fn.symbol != pc^0x55aa {
					t.Errorf("concurrent replacement returned a different function for %x", pc)
					return
				}
			}
		})
	}
	group.Wait()
}
