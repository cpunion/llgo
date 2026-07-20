package runtime

import (
	"sync/atomic"
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/atomiccache"
)

type weakHandle = atomiccache.WeakHandle

var weakState atomiccache.WeakTable

func llgoRegisterWeakPointer(p unsafe.Pointer) unsafe.Pointer {
	if p == nil {
		return nil
	}

	key := llgoWeakPointerKey(p)
	candidate := &weakHandle{Key: key, Live: 1}
	h, published := weakState.InternWeak(candidate)
	if !published {
		return unsafe.Pointer(h)
	}

	addCleanupPtr(p, func() {
		// BDWGC only queues this closure. The managed finalizer drain performs
		// the tombstone store without a lock, allocation, or scheduler lookup.
		atomic.StoreUint32(&h.Live, 0)
	})
	return unsafe.Pointer(h)
}

func llgoMakeStrongFromWeak(u unsafe.Pointer) unsafe.Pointer {
	h := (*weakHandle)(u)
	if h == nil {
		return nil
	}
	if atomic.LoadUint32(&h.Live) == 0 {
		weakState.PruneWeak(h.Key)
		return nil
	}
	return llgoWeakKeyPointer(h.Key)
}

// llgoWeakPointerKey is the exact runtime boundary that hides a managed
// pointer as the non-GC-visible identity stored by a weak handle. It must stay
// a synchronous leaf: retaining p as a coroutine-frame keepalive after this
// conversion would make the weak reference strong. Registration may suspend
// only after this leaf has returned the deliberately untraced scalar key.
func llgoWeakPointerKey(p unsafe.Pointer) uintptr {
	return uintptr(p)
}

// llgoWeakKeyPointer is the exact runtime boundary that reconstructs a strong
// pointer from the deliberately non-GC-visible address held by a live weak
// handle. Keep this leaf separate from llgoMakeStrongFromWeak: the latter may
// suspend while pruning a tombstone, so treating its hidden uintptr as normal
// coroutine-frame pointer provenance would keep the referent alive and break
// weak reachability. This leaf has no call, allocation, or backedge and the
// returned pointer becomes an ordinary traced strong reference immediately.
func llgoWeakKeyPointer(key uintptr) unsafe.Pointer {
	return unsafe.Pointer(key)
}

//go:linkname weak_runtime_registerWeakPointer weak.runtime_registerWeakPointer
func weak_runtime_registerWeakPointer(p unsafe.Pointer) unsafe.Pointer {
	return llgoRegisterWeakPointer(p)
}

//go:linkname weak_runtime_makeStrongFromWeak weak.runtime_makeStrongFromWeak
func weak_runtime_makeStrongFromWeak(u unsafe.Pointer) unsafe.Pointer {
	return llgoMakeStrongFromWeak(u)
}

//go:linkname internal_weak_runtime_registerWeakPointer internal/weak.runtime_registerWeakPointer
func internal_weak_runtime_registerWeakPointer(p unsafe.Pointer) unsafe.Pointer {
	return llgoRegisterWeakPointer(p)
}

//go:linkname internal_weak_runtime_makeStrongFromWeak internal/weak.runtime_makeStrongFromWeak
func internal_weak_runtime_makeStrongFromWeak(u unsafe.Pointer) unsafe.Pointer {
	return llgoMakeStrongFromWeak(u)
}
