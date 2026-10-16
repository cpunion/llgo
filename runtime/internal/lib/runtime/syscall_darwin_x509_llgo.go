//go:build darwin

package runtime

import "unsafe"

// The macOS x509 trampoline ABI includes an FP register even when most calls
// pass zero there. Keep that type at the C boundary so the existing typed
// foreign-call lowering can offload it without treating a float as a word.
//
//llgo:type C
type darwinX509Call func(a1, a2, a3, a4, a5 uintptr, f1 float64) uintptr

func darwinX509Syscall(fn, a1, a2, a3, a4, a5 uintptr, f1 float64) uintptr {
	call := *(*darwinX509Call)(unsafe.Pointer(&fn))
	return call(a1, a2, a3, a4, a5, f1)
}
