//go:build darwin || linux || wasm || tinygo.wasm

package runtime

import (
	"unsafe"

	latomic "github.com/goplus/llgo/runtime/internal/lib/sync/atomic"
)

// Standard-library semaphore entry points dispatch to a capability-selected
// legacy or event-driven implementation. notifyList uses the same split while
// retaining the standard runtime ABI and ticket layout below.

// sync_runtime_Semacquire should be an internal detail, but is linknamed.
//
//llgo:managedlink
//go:linkname sync_runtime_Semacquire sync.runtime_Semacquire
func sync_runtime_Semacquire(addr *uint32) {
	semaAcquire(addr)
}

//llgo:managedlink
//go:linkname poll_runtime_Semacquire internal/poll.runtime_Semacquire
func poll_runtime_Semacquire(addr *uint32) {
	semaAcquire(addr)
}

//llgo:managedlink
//go:linkname sync_runtime_Semrelease sync.runtime_Semrelease
func sync_runtime_Semrelease(addr *uint32, _ bool, _ int) {
	semaRelease(addr)
}

//llgo:managedlink
//go:linkname sync_runtime_SemacquireRWMutexR sync.runtime_SemacquireRWMutexR
func sync_runtime_SemacquireRWMutexR(addr *uint32, _ bool, _ int) {
	semaAcquire(addr)
}

//llgo:managedlink
//go:linkname sync_runtime_SemacquireRWMutex sync.runtime_SemacquireRWMutex
func sync_runtime_SemacquireRWMutex(addr *uint32, _ bool, _ int) {
	semaAcquire(addr)
}

//llgo:managedlink
//go:linkname sync_runtime_SemacquireWaitGroup sync.runtime_SemacquireWaitGroup
func sync_runtime_SemacquireWaitGroup(addr *uint32, _ bool) {
	semaAcquire(addr)
}

// runtime_SemacquireMutex is used by internal/sync via linkname.
func runtime_SemacquireMutex(addr *uint32, _ bool, _ int) {
	semaAcquire(addr)
}

// sync_runtime_SemacquireMutex is used by older stdlib sync implementations.
//
//llgo:managedlink
//go:linkname sync_runtime_SemacquireMutex sync.runtime_SemacquireMutex
func sync_runtime_SemacquireMutex(addr *uint32, lifo bool, skipframes int) {
	runtime_SemacquireMutex(addr, lifo, skipframes)
}

// runtime_Semrelease is used by internal/sync via linkname.
func runtime_Semrelease(addr *uint32, _ bool, _ int) {
	semaRelease(addr)
}

//llgo:managedlink
//go:linkname poll_runtime_Semrelease internal/poll.runtime_Semrelease
func poll_runtime_Semrelease(addr *uint32) {
	semaRelease(addr)
}

// runtime_canSpin/runtime_doSpin/runtime_nanotime are used by internal/sync.
func runtime_canSpin(_ int) bool { return false }
func runtime_doSpin()            {}
func runtime_nanotime() int64    { return runtimeNano() }

// sync_runtime_canSpin/sync_runtime_doSpin/sync_runtime_nanotime are used by
// older stdlib sync implementations.
//
//llgo:managedlink
//go:linkname sync_runtime_canSpin sync.runtime_canSpin
func sync_runtime_canSpin(i int) bool { return runtime_canSpin(i) }

//llgo:managedlink
//go:linkname sync_runtime_doSpin sync.runtime_doSpin
func sync_runtime_doSpin() { runtime_doSpin() }

//llgo:managedlink
//go:linkname sync_runtime_nanotime sync.runtime_nanotime
func sync_runtime_nanotime() int64 { return runtime_nanotime() }

// internal/sync uses these runtime hooks on Go 1.24+.
//
//llgo:managedlink
//go:linkname internal_sync_runtime_canSpin internal/sync.runtime_canSpin
func internal_sync_runtime_canSpin(i int) bool { return runtime_canSpin(i) }

//llgo:managedlink
//go:linkname internal_sync_runtime_doSpin internal/sync.runtime_doSpin
func internal_sync_runtime_doSpin() { runtime_doSpin() }

//llgo:managedlink
//go:linkname internal_sync_runtime_nanotime internal/sync.runtime_nanotime
func internal_sync_runtime_nanotime() int64 { return runtime_nanotime() }

//llgo:managedlink
//go:linkname internal_sync_runtime_SemacquireMutex internal/sync.runtime_SemacquireMutex
func internal_sync_runtime_SemacquireMutex(addr *uint32, lifo bool, skipframes int) {
	runtime_SemacquireMutex(addr, lifo, skipframes)
}

//llgo:managedlink
//go:linkname internal_sync_runtime_Semrelease internal/sync.runtime_Semrelease
func internal_sync_runtime_Semrelease(addr *uint32, handoff bool, skipframes int) {
	runtime_Semrelease(addr, handoff, skipframes)
}

//llgo:managedlink
//go:linkname internal_sync_throw internal/sync.throw
func internal_sync_throw(s string) { throw(s) }

//llgo:managedlink
//go:linkname internal_sync_fatal internal/sync.fatal
func internal_sync_fatal(s string) { fatal(s) }

// notifyList matches sync.notifyList size/layout.
type notifyList struct {
	wait   uint32
	notify uint32
	lock   uintptr
	head   unsafe.Pointer
	tail   unsafe.Pointer
}

// notifyListTicketLess is valid while the unwrapped distance is below 2^31,
// matching the standard Go runtime notifyList invariant.
func notifyListTicketLess(a, b uint32) bool {
	return int32(a-b) < 0
}

//llgo:managedlink
//go:linkname sync_runtime_notifyListAdd sync.runtime_notifyListAdd
func sync_runtime_notifyListAdd(l *notifyList) uint32 {
	return latomic.AddUint32(&l.wait, 1) - 1
}

//llgo:managedlink
//go:linkname sync_runtime_notifyListCheck sync.runtime_notifyListCheck
func sync_runtime_notifyListCheck(size uintptr) {
	if size != unsafe.Sizeof(notifyList{}) {
		panic("sync.notifyList size mismatch")
	}
}
