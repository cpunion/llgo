//go:build darwin || linux || windows

package runtime

import (
	_ "unsafe"
)

var procPinOnce nativeOnce
var procPinMu nativeMutex

func initProcPinMu() {
	procPinMu.Init(nil)
}

// LLGo has no Go P to pin a goroutine to. Serialize procPin regions instead,
// preserving the exclusion that sync.Pool and sync/atomic.Value require.
func procPin() int {
	procPinOnce.Do(initProcPinMu)
	procPinMu.Lock()
	return 0
}

func procUnpin() {
	procPinMu.Unlock()
}

//go:linkname sync_runtime_procPin sync.runtime_procPin
func sync_runtime_procPin() int {
	return procPin()
}

//go:linkname sync_runtime_procUnpin sync.runtime_procUnpin
func sync_runtime_procUnpin() {
	procUnpin()
}

//go:linkname sync_atomic_runtime_procPin sync/atomic.runtime_procPin
func sync_atomic_runtime_procPin() int {
	return procPin()
}

//go:linkname sync_atomic_runtime_procUnpin sync/atomic.runtime_procUnpin
func sync_atomic_runtime_procUnpin() {
	procUnpin()
}
