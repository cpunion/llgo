//go:build nogc

package runtime

import "unsafe"

// The non-collecting profile never reclaims objects. Its weak handles are
// permanently live, and it has no collector-owned heap identity to expose.
func llgoWeakRegister(unsafe.Pointer, *uintptr) bool { return true }

func llgoWeakStrong(key uintptr, live *uintptr) unsafe.Pointer {
	if live == nil || *live == 0 {
		return nil
	}
	return llgoWeakKeyPointer(key)
}

func llgoWeakHeapBase(unsafe.Pointer) unsafe.Pointer { return nil }
