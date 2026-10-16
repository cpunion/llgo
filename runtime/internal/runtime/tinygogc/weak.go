//go:build baremetal || (!nogc && (wasm || tinygo.wasm) && llgo_wasm_gc)

package tinygogc

import "unsafe"

// Only objects with weak handles need an extra record. Neither the stable
// allocation base nor its interior user pointer is stored as a visible root.
// live keeps the handle cell itself valid until its registration is removed.
type weakRecord struct {
	hiddenBase uintptr
	live       *uintptr
	next       *weakRecord
}

var weakRecords *weakRecord

func WeakHeapBase(p unsafe.Pointer) unsafe.Pointer {
	address := uintptr(p)
	if !isOnHeap(address) {
		return nil
	}
	block := blockFromAddr(address)
	if gcStateOf(block) == blockStateFree {
		return nil
	}
	return gcPointerOf(gcFindHead(block))
}

func RegisterWeak(p unsafe.Pointer, live *uintptr) bool {
	if !isOnHeap(uintptr(p)) {
		return true // static/zero-sized storage is never reclaimed
	}
	// Allocation may collect. The subsequent use of the actual pointer p,
	// rather than a previously hidden integer, keeps the referent live across it.
	record := new(weakRecord)
	base := WeakHeapBase(p)
	if base == nil {
		*live = 0
		return false
	}
	record.hiddenBase = hideWeakBase(base)
	record.live = live
	record.next = weakRecords
	weakRecords = record
	return true
}

// Pointer provenance must end in this synchronous leaf. RegisterWeak may
// allocate and unwind; retaining its raw base as a frame keepalive after the
// hiding operation would accidentally turn the weak record into a strong root.
func hideWeakBase(base unsafe.Pointer) uintptr {
	return ^uintptr(base)
}

// This collector profile has one serialized executor and no concurrent mark.
// A call/alloc/backedge-free read+reveal leaf cannot be preempted halfway. Any
// future concurrent collector must replace this boundary with its own lock or
// read barrier, not merely change Live into an atomic load.
func StrongFromWeak(key uintptr, live *uintptr) unsafe.Pointer {
	if live == nil || *live == 0 {
		return nil
	}
	return unsafe.Pointer(^key)
}

// Clear weak references before resuming mutators or sweeping. The list itself
// is rooted, but each hiddenBase is not; no finalizer/cleanup queue is involved.
//
//llgo:nopreempt
//llgo:nounwind
func clearUnmarkedWeakReferences() {
	link := &weakRecords
	for *link != nil {
		record := *link
		base := ^record.hiddenBase
		if !isOnHeap(base) || gcStateOf(blockFromAddr(base)) != blockStateMark {
			*record.live = 0
			*link = record.next
			record.live = nil
			record.next = nil
			continue
		}
		link = &record.next
	}
}
