//go:build llgo_coro

package runtime

import _ "unsafe"

// The public functions remain ordinary Go wrappers, so taking either function
// as a value keeps the standard callable behavior. Only these private exact
// calls are compiler markers; they acquire the current physical coroutine's G
// explicitly and cannot be materialized as function values.

//go:linkname coroOSThreadLock llgo.coroOSThreadLock
func coroOSThreadLock()

//go:linkname coroOSThreadUnlock llgo.coroOSThreadUnlock
func coroOSThreadUnlock()

func LockOSThread() {
	coroOSThreadLock()
}

func UnlockOSThread() {
	coroOSThreadUnlock()
}
