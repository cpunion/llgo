//go:build !nogc && !baremetal

// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Garbage collector: finalizers and block profiling.

package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/abi"
	"github.com/goplus/llgo/runtime/internal/clite/bdwgc"
	"github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
)

type finalizerClosure struct {
	fn  unsafe.Pointer
	env unsafe.Pointer
}

type finalizerEntry struct {
	fn      any
	cleanup func()
	obj     unsafe.Pointer
	key     uintptr
	tracked bool
	next    unsafe.Pointer // *finalizerEntry; atomic producer/consumer link
	prevFn  bdwgc.FinalizerFunc
	prevCb  unsafe.Pointer
	stop    int32
}

var finalizerState struct {
	// m is managed-owner state. Raw collector callbacks never inspect or
	// mutate it; multi-executor registry serialization remains a separate
	// scheduler-lock concern from the callback ingress queue below.
	m map[uintptr]*finalizerEntry

	// queueHead is the producer end of an intrusive MPSC queue. queueTail is
	// owned by the one managed runFinalizers consumer. The permanent stub lets
	// a raw producer publish with one exchange and one store, without a retry
	// loop, lock, allocation, or scheduler call.
	queueHead unsafe.Pointer // *finalizerEntry
	queueTail *finalizerEntry
	queueStub finalizerEntry
	draining  uint32
}

func initFinalizerState() {
	finalizerState.m = make(map[uintptr]*finalizerEntry)
	stub := &finalizerState.queueStub
	finalizerState.queueHead = unsafe.Pointer(stub)
	finalizerState.queueTail = stub
}

func init() {
	// Runtime initialization completes before managed program tasks can run, so
	// eager construction removes pthread_once and its foreign Go callback from
	// every later SetFinalizer path.
	initFinalizerState()
}

func SetFinalizer(obj any, finalizer any) {
	objFace := (*eface)(unsafe.Pointer(&obj))
	if objFace._type == nil {
		throw("runtime.SetFinalizer: first argument is nil")
	}
	if objFace._type.Kind() != abi.Pointer {
		throw("runtime.SetFinalizer: first argument is " + objFace._type.String() + ", not pointer")
	}
	objPtr := ifacePointerData(objFace)
	if objPtr == nil {
		throw("runtime.SetFinalizer: first argument is nil")
	}

	key := hideFinalizerPtr(objPtr)

	if old := finalizerState.m[key]; old != nil {
		atomic.Store(&old.stop, 1)
		delete(finalizerState.m, key)
		restoreFinalizer(objPtr, old)
	}

	finalizerFace := (*eface)(unsafe.Pointer(&finalizer))
	if finalizerFace._type == nil {
		return
	}
	ft := finalizerFuncType(finalizerFace._type)
	if ft == nil {
		throw("runtime.SetFinalizer: second argument is " + finalizerFace._type.String() + ", not a function")
	}
	if len(ft.In) != 1 || ft.In[0] != objFace._type {
		throw("runtime.SetFinalizer: cannot pass " + objFace._type.String() + " to finalizer " + finalizerFace._type.String())
	}
	entry := &finalizerEntry{fn: finalizer, key: key, tracked: true}
	registerFinalizerEntry(objPtr, entry)

	finalizerState.m[key] = entry
}

func registerFinalizerEntry(ptr unsafe.Pointer, entry *finalizerEntry) {
	// GC_register_finalizer fills these output slots while it still owns the
	// collector lock. Publishing entry as client data and copying local output
	// values only after return would leave a window in which another collector
	// thread could invoke entry before its previous-finalizer chain was visible.
	bdwgc.RegisterFinalizer(
		ptr,
		setFinalizerCallback,
		unsafe.Pointer(entry),
		&entry.prevFn,
		&entry.prevCb,
	)
}

// addCleanupPtr attaches cleanup to ptr while keeping arbitrary Go execution
// out of the collector callback. The callback only chains the previous C
// finalizer and queues entry; runFinalizers invokes cleanup from managed code.
func addCleanupPtr(ptr unsafe.Pointer, cleanup func()) (cancel func()) {
	entry := &finalizerEntry{cleanup: cleanup}
	registerFinalizerEntry(ptr, entry)
	return func() {
		atomic.Store(&entry.stop, 1)
	}
}

func ifacePointerData(e *eface) unsafe.Pointer {
	if e._type.IsDirectIface() {
		return e.data
	}
	return *(*unsafe.Pointer)(e.data)
}

func finalizerFuncType(t *abi.Type) *abi.FuncType {
	if t.IsClosure() {
		st := t.StructType()
		if st == nil || len(st.Fields) == 0 {
			return nil
		}
		return st.Fields[0].Typ.FuncType()
	}
	return t.FuncType()
}

func callFinalizer(fn any, ptr unsafe.Pointer) {
	c := (*finalizerClosure)((*eface)(unsafe.Pointer(&fn)).data)
	f := *(*func(unsafe.Pointer))(unsafe.Pointer(c))
	f(ptr)
}

// enqueueFinalizerEntry is the complete raw-callback publication path. It is
// the wait-free producer half of the intrusive MPSC queue: entry is initialized
// before exchange publishes it as the new producer head, then the release-store
// to previous.next makes the new node visible to the single consumer. A
// consumer that catches the short interval between those two atomics simply
// observes an in-flight producer and retries on a later drain.
func enqueueFinalizerEntry(entry *finalizerEntry) {
	atomic.Store(&entry.next, unsafe.Pointer(nil))
	previous := (*finalizerEntry)(atomic.Exchange(
		&finalizerState.queueHead,
		unsafe.Pointer(entry),
	))
	atomic.Store(&previous.next, unsafe.Pointer(entry))
}

// dequeueFinalizerEntry is called by the one managed consumer. It is the
// standard intrusive MPSC stub-node dequeue: producer publication is FIFO, so
// no batch reversal or allocation is required. nil can mean either empty or a
// producer between its exchange and link store; both are safe because the node
// remains retained by queueHead and becomes visible on a later drain.
func dequeueFinalizerEntry() *finalizerEntry {
	tail := finalizerState.queueTail
	next := (*finalizerEntry)(atomic.Load(&tail.next))
	stub := &finalizerState.queueStub

	if tail == stub {
		if next == nil {
			return nil
		}
		finalizerState.queueTail = next
		tail = next
		next = (*finalizerEntry)(atomic.Load(&tail.next))
	}

	if next != nil {
		finalizerState.queueTail = next
		return tail
	}

	head := (*finalizerEntry)(atomic.Load(&finalizerState.queueHead))
	if tail != head {
		return nil
	}

	// Close the current producer chain with the permanent stub. If another
	// producer races this exchange, its node remains between tail and stub and
	// is observed on this or a later dequeue.
	enqueueFinalizerEntry(stub)
	next = (*finalizerEntry)(atomic.Load(&tail.next))
	if next == nil {
		return nil
	}
	finalizerState.queueTail = next
	return tail
}

func setFinalizerCallback(ptr unsafe.Pointer, cb unsafe.Pointer) {
	entry := (*finalizerEntry)(cb)
	prevFn, prevCb := entry.prevFn, entry.prevCb
	if prevFn != nil {
		prevFn(ptr, prevCb)
	}
	if atomic.Load(&entry.stop) == 1 {
		return
	}

	// Keep the object alive until runFinalizers invokes the Go finalizer or
	// cleanup. Do not allocate, lock, or invoke arbitrary Go code here; BDWGC
	// calls this while collecting.
	entry.obj = ptr
	enqueueFinalizerEntry(entry)
}

func restoreFinalizer(ptr unsafe.Pointer, entry *finalizerEntry) {
	var oldFn bdwgc.FinalizerFunc
	var oldCb unsafe.Pointer
	if entry.prevFn != nil {
		bdwgc.RegisterFinalizer(ptr, entry.prevFn, entry.prevCb, &oldFn, &oldCb)
		return
	}
	bdwgc.RegisterFinalizer(ptr, nil, nil, &oldFn, &oldCb)
}

func releaseFinalizerDrain() {
	atomic.Store(&finalizerState.draining, uint32(0))
}

func runFinalizers() {
	_, acquired := atomic.CompareAndExchange(
		&finalizerState.draining,
		uint32(0),
		uint32(1),
	)
	if !acquired {
		return
	}
	defer releaseFinalizerDrain()

	for {
		entry := dequeueFinalizerEntry()
		if entry == nil {
			return
		}
		atomic.Store(&entry.next, unsafe.Pointer(nil))
		if entry.tracked && finalizerState.m[entry.key] == entry {
			delete(finalizerState.m, entry.key)
		}

		if atomic.Load(&entry.stop) != 1 {
			if entry.cleanup != nil {
				entry.cleanup()
			} else {
				callFinalizer(entry.fn, entry.obj)
			}
		}
		entry.obj = nil
		entry.fn = nil
		entry.cleanup = nil
	}
}

func hideFinalizerPtr(ptr unsafe.Pointer) uintptr {
	return ^uintptr(ptr)
}
