//go:build baremetal && !nogc

package tinygogc

import "unsafe"

func AllocNoScanRoot(size uintptr) unsafe.Pointer {
	return AllocRoot(size)
}

func FreeNoScanRoot(ptr unsafe.Pointer) {
	FreeRoot(ptr)
}

func shouldScanObject(uintptr) bool {
	return true
}
