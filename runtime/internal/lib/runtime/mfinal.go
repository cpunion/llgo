//go:build !nogc && !baremetal && !llgo_wasm_gc

// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.
// See LICENSES/Go-BSD-3-Clause.txt at this module root for license terms.

// Garbage collector: finalizers and block profiling.

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/abi"
	"github.com/xgo-dev/llgo/runtime/internal/clite/bdwgc"
	"github.com/xgo-dev/llgo/runtime/internal/clite/sync/atomic"
	"github.com/xgo-dev/llgo/runtime/internal/ffi"
	llruntime "github.com/xgo-dev/llgo/runtime/internal/runtime"
)

type finalizerClosure struct {
	descriptor unsafe.Pointer
	env        unsafe.Pointer
}

type finalizerInterfaceArg struct {
	typeOrItab unsafe.Pointer
	data       unsafe.Pointer
}

const (
	finalizerStopped        int32 = 1
	finalizerInterfaceState       = 2
)

type finalizerEntry struct {
	fn       any
	cleanup  func()
	plainCIF *ffi.Signature
	coroCIF  *ffi.Signature

	resultSize  uintptr
	resultAlign uintptr
	retSize     uintptr
	arg         unsafe.Pointer // object pointer or preallocated interface header

	key     uintptr
	tracked bool
	next    unsafe.Pointer // *finalizerEntry; atomic producer/consumer link
	prevFn  bdwgc.FinalizerFunc
	prevCb  unsafe.Pointer
	state   int32
}

// finalizerRegistryGate is a scheduler-aware managed-owner lock. Its holder
// may be preempted while a map helper or synchronous collector registration is
// running; a contender therefore yields its stackless frame instead of
// blocking the physical executor. Raw collector callbacks never enter it.
type finalizerRegistryGate struct {
	state uint32
}

func (gate *finalizerRegistryGate) Lock() {
	for {
		_, acquired := atomic.CompareAndExchange(&gate.state, uint32(0), uint32(1))
		if acquired {
			return
		}
		coroSchedulerYield()
	}
}

func (gate *finalizerRegistryGate) Unlock() {
	if _, released := atomic.CompareAndExchange(&gate.state, uint32(1), uint32(0)); !released {
		throw("runtime: invalid finalizer registry gate release")
	}
}

var finalizerState struct {
	// registry serializes m in managed code. Raw collector callbacks never
	// inspect either field; their ingress queue remains independent below.
	registry finalizerRegistryGate
	m        map[uintptr]*finalizerEntry

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
	finalizerFace := (*eface)(unsafe.Pointer(&finalizer))
	var entry *finalizerEntry
	if finalizerFace._type != nil {
		ft := finalizerFuncType(finalizerFace._type)
		if ft == nil {
			throw("runtime.SetFinalizer: second argument is " + finalizerFace._type.String() + ", not a function")
		}
		if ft.Variadic() {
			throw("runtime.SetFinalizer: cannot pass " + objFace._type.String() + " to finalizer " + finalizerFace._type.String() + " because dotdotdot")
		}
		if len(ft.In) != 1 {
			throw("runtime.SetFinalizer: cannot pass " + objFace._type.String() + " to finalizer " + finalizerFace._type.String())
		}
		argFFIType, interfaceTypeOrItab, ok := prepareFinalizerArgument(objFace._type, ft.In[0])
		if !ok {
			throw("runtime.SetFinalizer: cannot pass " + objFace._type.String() + " to finalizer " + finalizerFace._type.String())
		}
		plainCIF, coroCIF, resultSize, resultAlign, retSize := newFinalizerDispatchSignatures(ft, argFFIType)
		entry = &finalizerEntry{
			fn: finalizer, plainCIF: plainCIF, coroCIF: coroCIF,
			resultSize: resultSize, resultAlign: resultAlign, retSize: retSize,
			key: key, tracked: true,
		}
		if interfaceTypeOrItab != nil {
			entry.state = finalizerInterfaceState
			entry.arg = unsafe.Pointer(&finalizerInterfaceArg{typeOrItab: interfaceTypeOrItab})
		}
	}

	finalizerState.registry.Lock()
	if old := finalizerState.m[key]; old != nil {
		atomic.Store(&old.state, finalizerStopped)
		delete(finalizerState.m, key)
		restoreFinalizer(objPtr, old)
	}
	if entry != nil {
		registerFinalizerEntry(objPtr, entry)
		finalizerState.m[key] = entry
	}
	finalizerState.registry.Unlock()
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
		atomic.Store(&entry.state, finalizerStopped)
	}
}

func prepareFinalizerArgument(objType, argType *abi.Type) (*ffi.Type, unsafe.Pointer, bool) {
	if argType == objType {
		return ffi.TypePointer, nil, true
	}
	switch argType.Kind() {
	case abi.Pointer:
		if (argType.Uncommon() == nil || objType.Uncommon() == nil) && argType.Elem() == objType.Elem() {
			return ffi.TypePointer, nil, true
		}
	case abi.Interface:
		if llruntime.Implements(argType, objType) {
			interfaceType := argType.InterfaceType()
			typeOrItab := unsafe.Pointer(objType)
			if len(interfaceType.Methods) != 0 {
				typeOrItab = unsafe.Pointer(llruntime.NewItab(interfaceType, objType))
			}
			return ffi.TypeInterface, typeOrItab, true
		}
	}
	return nil, nil, false
}

func ifacePointerData(e *eface) unsafe.Pointer {
	if e._type.IsDirectIface() {
		return e.data
	}
	return *(*unsafe.Pointer)(e.data)
}

func finalizerFuncType(t *abi.Type) *abi.FuncType {
	if !t.IsClosure() {
		return nil
	}
	st := t.StructType()
	if st == nil || len(st.Fields) == 0 {
		return nil
	}
	return st.Fields[0].Typ.FuncType()
}

func callFinalizer(entry *finalizerEntry, hasInterfaceArg bool) {
	face := (*eface)(unsafe.Pointer(&entry.fn))
	closure := (*finalizerClosure)(face.data)
	ft := finalizerFuncType(face._type)
	if closure == nil || closure.descriptor == nil || ft == nil {
		throw("runtime: invalid finalizer function value")
	}
	var ret unsafe.Pointer
	if entry.retSize != 0 {
		ret = llruntime.AllocU(entry.retSize)
	}
	arg := entry.arg
	if !hasInterfaceArg {
		ptr := entry.arg
		arg = unsafe.Pointer(&ptr)
	}
	ffi.CallLLGo(
		entry.plainCIF,
		entry.coroCIF,
		closure.descriptor,
		closure.env,
		unsafe.Pointer(ft),
		ret,
		entry.resultSize,
		entry.resultAlign,
		arg,
	)
	KeepAlive(entry.fn)
	KeepAlive(entry.plainCIF)
	KeepAlive(entry.coroCIF)
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
	state := atomic.Load(&entry.state)
	if state == finalizerStopped {
		return
	}

	// Keep the object alive until runFinalizers invokes the Go finalizer or
	// cleanup. Do not allocate, lock, or invoke arbitrary Go code here; BDWGC
	// calls this while collecting.
	if state == finalizerInterfaceState {
		(*finalizerInterfaceArg)(entry.arg).data = ptr
	} else {
		entry.arg = ptr
	}
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
		if entry.tracked {
			finalizerState.registry.Lock()
			if finalizerState.m[entry.key] == entry {
				delete(finalizerState.m, entry.key)
			}
			finalizerState.registry.Unlock()
		}

		state := atomic.Load(&entry.state)
		if state != finalizerStopped {
			if entry.cleanup != nil {
				entry.cleanup()
			} else {
				callFinalizer(entry, state == finalizerInterfaceState)
			}
		}
		entry.arg = nil
		entry.fn = nil
		entry.cleanup = nil
	}
}

func hideFinalizerPtr(ptr unsafe.Pointer) uintptr {
	return ^uintptr(ptr)
}
