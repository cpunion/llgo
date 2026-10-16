package runtime

import (
	"sync/atomic"
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
	corort "github.com/xgo-dev/llgo/runtime/internal/runtime"
)

// Object associations exist only in programs using execution domains. Neither
// ordinary heap objects nor WaitGroups gain a header or a scheduler field.
// A collector-owned weak handle prevents retention and address-reuse aliases.
// The owner is strong identity: a later bubble cannot reuse its address while
// an object still carries the old association.
type synctestObjectEntry struct {
	weak  *weakHandle
	owner unsafe.Pointer
	next  *synctestObjectEntry
}

var synctestObjects struct {
	lock    uint32
	buckets [64]*synctestObjectEntry
}

func synctestObjectBucket(key uintptr) **synctestObjectEntry {
	return &synctestObjects.buckets[((key>>3)^(key>>13))&63]
}

func synctestObjectLock() {
	for !atomic.CompareAndSwapUint32(&synctestObjects.lock, 0, 1) {
		coroSchedulerYield()
	}
}

func synctestObjectUnlock() { atomic.StoreUint32(&synctestObjects.lock, 0) }

// Return values match internal/synctest: Unbubbled, CurrentBubble, OtherBubble.
func synctestObjectAssociation(p unsafe.Pointer, add bool) int {
	if !coro.ExecutionDomainEnabled() || p == nil {
		return 0
	}
	owner := corort.CurrentExecutionDomainContext()
	if owner == nil || llgoWeakHeapBase(p) == nil {
		// Package globals cannot acquire heap-associated synctest metadata.
		return 0
	}
	key := llgoWeakPointerKey(p)
	synctestObjectLock()
	bucket := synctestObjectBucket(key)
	link := bucket
	for *link != nil {
		entry := *link
		if atomic.LoadUintptr(&entry.weak.Live) == 0 {
			*link = entry.next
			continue
		}
		if entry.weak.Key == key {
			synctestObjectUnlock()
			if entry.owner == owner {
				return 1
			}
			return 2
		}
		link = &entry.next
	}
	if !add {
		synctestObjectUnlock()
		return 0
	}
	handle := (*weakHandle)(llgoRegisterWeakPointer(p))
	*bucket = &synctestObjectEntry{weak: handle, owner: owner, next: *bucket}
	synctestObjectUnlock()
	return 1
}

func synctestForgetObject(p unsafe.Pointer) {
	if !coro.ExecutionDomainEnabled() || p == nil {
		return
	}
	key := llgoWeakPointerKey(p)
	synctestObjectLock()
	link := synctestObjectBucket(key)
	for *link != nil {
		entry := *link
		if entry.weak.Key == key || atomic.LoadUintptr(&entry.weak.Live) == 0 {
			*link = entry.next
			continue
		}
		link = &entry.next
	}
	synctestObjectUnlock()
}
