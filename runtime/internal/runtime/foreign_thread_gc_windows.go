//go:build llgo && windows && !nogc && !baremetal

package runtime

import "github.com/goplus/llgo/runtime/internal/clite/bdwgc"

// EnterForeignThread makes a thread created outside the LLGo runtime visible
// to the collector before it allocates or manipulates Go pointers. The return
// value records whether this call owns the matching unregister operation.
func EnterForeignThread() bool {
	if bdwgc.ThreadIsRegistered() != 0 {
		return false
	}
	var base bdwgc.StackBase
	if bdwgc.GetStackBase(&base) != bdwgc.Success {
		panic("runtime: failed to discover foreign thread stack")
	}
	switch status := bdwgc.RegisterMyThread(&base); status {
	case bdwgc.Success:
		return true
	case bdwgc.Duplicate:
		return false
	default:
		panic("runtime: failed to register foreign thread")
	}
}

// ExitForeignThread releases registration only when EnterForeignThread added
// it. Runtime-created threads remain owned by GC_CreateThread.
func ExitForeignThread(registered bool) {
	if registered {
		bdwgc.UnregisterMyThread()
	}
}
