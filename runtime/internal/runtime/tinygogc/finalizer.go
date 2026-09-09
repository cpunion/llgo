//go:build wasm && llgo.wasm.gc.linear

package tinygogc

import "unsafe"

const (
	finalizerActive uint8 = iota
	finalizerQueued
	finalizerRunning
	finalizerCanceled
	finalizerDone
)

type finalizerKind uint8

const (
	objectFinalizer finalizerKind = iota
	objectCleanup
)

// finalizerRecord deliberately stores an encoded object address while it is
// registered. This collector scans conservatively, so retaining an ordinary
// uintptr containing the address would keep the object alive forever.
type finalizerRecord struct {
	objectKey uintptr
	object    uintptr
	ready     unsafe.Pointer
	callback  func(unsafe.Pointer)
	state     uint8
	kind      finalizerKind
	candidate bool
	blocked   bool
	next      *finalizerRecord
	prev      *finalizerRecord
	indexNext *finalizerRecord
	readyNext *finalizerRecord
}

var (
	finalizers              *finalizerRecord
	finalizerIndex          []*finalizerRecord
	finalizerCount          int
	readyFinalizers         *finalizerRecord
	finalizerWorkerRunning  bool
	finalizerDependencyScan bool
)

// DebugFinalizerState reports collector lifecycle state for the focused R4
// diagnostic workflow. It is deliberately kept off the production branch.
func DebugFinalizerState() (active, ready int, worker bool) {
	lock(&gcMutex)
	active = finalizerCount
	for record := readyFinalizers; record != nil; record = record.readyNext {
		ready++
	}
	worker = finalizerWorkerRunning
	unlock(&gcMutex)
	return
}

// AddFinalizer registers callback for ptr without retaining the object. The
// returned function cancels a callback that has not started. Multiple callbacks
// may be registered for one object.
func AddFinalizer(ptr unsafe.Pointer, callback func(unsafe.Pointer)) (cancel func(), registered bool) {
	return addFinalizer(ptr, callback, objectFinalizer)
}

// AddCleanup registers a cleanup callback. Unlike finalizers, cleanups do not
// impose dependency ordering on one another. When an object has both, its
// cleanup remains pending until its finalizer has run and the object becomes
// unreachable again.
func AddCleanup(ptr unsafe.Pointer, callback func(unsafe.Pointer)) (cancel func(), registered bool) {
	return addFinalizer(ptr, callback, objectCleanup)
}

func addFinalizer(ptr unsafe.Pointer, callback func(unsafe.Pointer), kind finalizerKind) (cancel func(), registered bool) {
	if ptr == nil || callback == nil {
		return func() {}, false
	}

	record := &finalizerRecord{callback: callback, kind: kind}
	prepareFinalizerIndex()
	lock(&gcMutex)
	lazyInit()
	address := uintptr(ptr)
	if !isOnHeap(address) {
		unlock(&gcMutex)
		return func() {}, false
	}
	block := blockFromAddr(address)
	if gcStateOf(block) == blockStateFree {
		unlock(&gcMutex)
		return func() {}, false
	}
	record.objectKey = encodeFinalizerAddress(gcAddressOf(gcFindHead(block)))
	// Store the original (possibly interior) pointer in encoded form until the
	// collector has established that the object is unreachable.
	record.object = encodeFinalizerAddress(address)
	record.next = finalizers
	if finalizers != nil {
		finalizers.prev = record
	}
	finalizers = record
	bucket := finalizerBucket(record.objectKey, len(finalizerIndex))
	record.indexNext = finalizerIndex[bucket]
	finalizerIndex[bucket] = record
	finalizerCount++
	unlock(&gcMutex)

	return func() {
		lock(&gcMutex)
		switch record.state {
		case finalizerActive:
			unlinkFinalizer(record)
			record.state = finalizerCanceled
			record.callback = nil
		case finalizerQueued:
			record.state = finalizerCanceled
			record.ready = nil
			record.callback = nil
		}
		unlock(&gcMutex)
	}, true
}

func encodeFinalizerAddress(address uintptr) uintptr {
	return ^address
}

// Grow only on the mutator path, before taking gcMutex. An allocation here can
// collect using the old, fully published registry. Rebuilding and publication
// under the guard do not allocate, so GC never observes a half-built index.
func prepareFinalizerIndex() {
	for finalizerCount >= len(finalizerIndex) {
		size := len(finalizerIndex) * 2
		if size == 0 {
			size = 16
		}
		index := make([]*finalizerRecord, size)
		lock(&gcMutex)
		if len(index) > len(finalizerIndex) {
			for record := finalizers; record != nil; record = record.next {
				bucket := finalizerBucket(record.objectKey, len(index))
				record.indexNext = index[bucket]
				index[bucket] = record
			}
			finalizerIndex = index
		}
		unlock(&gcMutex)
	}
}

func finalizerBucket(key uintptr, size int) uintptr {
	// Remove object alignment before mixing. Keys remain encoded in records;
	// no decoded address is retained in the index as a conservative root.
	key /= bytesPerBlock
	key ^= key >> 16
	return key * 0x9e3779b1 & uintptr(size-1)
}

func finalizersForObject(key uintptr) *finalizerRecord {
	if len(finalizerIndex) == 0 {
		return nil
	}
	return finalizerIndex[finalizerBucket(key, len(finalizerIndex))]
}

func unlinkFinalizer(record *finalizerRecord) {
	link := &finalizerIndex[finalizerBucket(record.objectKey, len(finalizerIndex))]
	for *link != nil {
		if *link == record {
			*link = record.indexNext
			if record.prev == nil {
				finalizers = record.next
			} else {
				record.prev.next = record.next
			}
			if record.next != nil {
				record.next.prev = record.prev
			}
			record.next = nil
			record.prev = nil
			record.indexNext = nil
			finalizerCount--
			return
		}
		link = &(*link).indexNext
	}
}

// preserveFinalizableObjects runs after ordinary roots have been marked and
// before sweeping. It moves callbacks for newly unreachable objects to the
// ready queue and marks those objects for one more cycle, so a finalizer may
// safely inspect or resurrect its argument.
//
// A finalizer on A must run before a finalizer on B when A can reach B. Extend
// the ordinary mark graph from every finalizable object once, recording each
// candidate reached through an object edge as blocked. Accumulating those
// marks discovers transitive dependencies and cycles without re-marking the
// complete live heap once per finalizer.
func preserveFinalizableObjects() {
	for record := finalizers; record != nil; record = record.next {
		record.candidate = record.state == finalizerActive && finalizerObjectState(record) == blockStateHead
		record.blocked = false
	}

	finalizerDependencyScan = true
	for record := finalizers; record != nil; record = record.next {
		if !record.candidate || record.kind != objectFinalizer {
			continue
		}
		block := finalizerObjectBlock(record)
		if gcStateOf(block) == blockStateHead {
			startMark(block)
		}
	}
	finishMark()
	finalizerDependencyScan = false

	// Visit the registered objects, not every block of the heap. Looking up
	// each heap block in this list multiplies heap size by callback count, even
	// when almost all of the heap has no lifecycle callbacks at all.
	for record := finalizers; record != nil; {
		key := record.objectKey
		// Queueing can unlink several adjacent records for this object. Save
		// a successor for a different object before their next links are cleared.
		next := record.next
		for next != nil && next.objectKey == key {
			next = next.next
		}
		if !record.candidate || finalizerObjectBlocked(key) {
			record = next
			continue
		}
		block := finalizerObjectBlock(record)
		state := gcStateOf(block)
		if hasCandidateFinalizer(key) {
			if !queueCallbacksForObject(key, objectFinalizer) {
				record = next
				continue
			}
		} else {
			if !queueCallbacksForObject(key, objectCleanup) {
				record = next
				continue
			}
		}
		// An object with both a finalizer and cleanups gets only its finalizer
		// this cycle, including when its cleanup records are not adjacent.
		for pending := finalizersForObject(key); pending != nil; pending = pending.indexNext {
			if pending.objectKey == key {
				pending.candidate = false
			}
		}
		if state == blockStateHead {
			startMark(block)
		}
		record = next
	}

	// A cycle of finalizable objects has no valid dependency order. Preserve it
	// instead of freeing the objects while leaving callbacks registered against
	// their now-stale addresses.
	for record := finalizers; record != nil; record = record.next {
		if record.candidate && finalizerObjectState(record) == blockStateHead {
			startMark(finalizerObjectBlock(record))
		}
		record.candidate = false
		record.blocked = false
	}
	finishMark()
}

// noteFinalizerReference is called for heap edges encountered by the marker.
// During dependency discovery, reaching any candidate (including through a
// cycle back to the starting object) means that candidate cannot be finalized
// in this collection.
func noteFinalizerReference(block uintptr) {
	if !finalizerDependencyScan {
		return
	}
	key := encodeFinalizerAddress(gcAddressOf(block))
	if candidateForObject(key) != nil {
		markFinalizerObjectBlocked(key)
	}
}

func finalizerObjectBlock(record *finalizerRecord) uintptr {
	return blockFromAddr(^record.objectKey)
}

func finalizerObjectState(record *finalizerRecord) uint8 {
	return gcStateOf(finalizerObjectBlock(record))
}

// Only inspect the matching bucket while the allocator is stopped. In
// particular, syscall/js can register thousands of short-lived emval handles;
// rescanning the complete registry per object would make collection quadratic.
func candidateForObject(key uintptr) *finalizerRecord {
	for record := finalizersForObject(key); record != nil; record = record.indexNext {
		if record.candidate && record.objectKey == key {
			return record
		}
	}
	return nil
}

func hasCandidateFinalizer(key uintptr) bool {
	for record := finalizersForObject(key); record != nil; record = record.indexNext {
		if record.candidate && record.kind == objectFinalizer && record.objectKey == key {
			return true
		}
	}
	return false
}

func finalizerObjectBlocked(key uintptr) bool {
	for record := finalizersForObject(key); record != nil; record = record.indexNext {
		if record.candidate && record.objectKey == key {
			return record.blocked
		}
	}
	return false
}

func markFinalizerObjectBlocked(key uintptr) {
	for record := finalizersForObject(key); record != nil; record = record.indexNext {
		if record.objectKey == key {
			record.blocked = true
		}
	}
}

func queueCallbacksForObject(key uintptr, kind finalizerKind) bool {
	queued := false
	for record := finalizersForObject(key); record != nil; {
		next := record.indexNext
		if record.objectKey != key || record.kind != kind || record.state != finalizerActive || !record.candidate || record.blocked {
			record = next
			continue
		}
		unlinkFinalizer(record)
		record.state = finalizerQueued
		original := ^record.object
		record.ready = unsafe.Pointer(original)
		record.readyNext = readyFinalizers
		readyFinalizers = record
		queued = true
		record = next
	}
	return queued
}

// scheduleFinalizers starts the one serial finalizer worker when callbacks are
// ready. It must not invoke user code on the allocator's caller: that caller
// may still hold an unrelated runtime lock across an allocation. A ready
// record contains a real pointer and is reachable from a global until claimed,
// keeping the preserved object alive if another collection starts.
func scheduleFinalizers() {
	lock(&gcMutex)
	if finalizerWorkerRunning || readyFinalizers == nil {
		unlock(&gcMutex)
		return
	}
	finalizerWorkerRunning = true
	unlock(&gcMutex)
	go drainFinalizers()
}

func drainFinalizers() {
	for {
		lock(&gcMutex)
		record := readyFinalizers
		if record == nil {
			finalizerWorkerRunning = false
			unlock(&gcMutex)
			return
		}
		readyFinalizers = record.readyNext
		record.readyNext = nil
		if record.state != finalizerQueued {
			unlock(&gcMutex)
			continue
		}
		record.state = finalizerRunning
		object := record.ready
		callback := record.callback
		unlock(&gcMutex)

		callback(object)

		lock(&gcMutex)
		record.state = finalizerDone
		record.ready = nil
		record.callback = nil
		unlock(&gcMutex)
	}
}
