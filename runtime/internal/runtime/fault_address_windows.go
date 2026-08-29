//go:build windows

package runtime

// PanicOnFault reports whether the current goroutine opted into recovering
// unexpected non-nil memory faults. It avoids creating a runtime context when
// called from a platform exception handler on an unrelated host thread.
func PanicOnFault() bool {
	gp := getgIfPresent()
	return gp != nil && gp.paniconfault
}

// PanicSignalAddr converts an unexpected memory fault enabled through
// runtime/debug.SetPanicOnFault and preserves the best-effort fault address.
func PanicSignalAddr(addr uintptr) {
	panic(errorAddressString{
		msg:  "invalid memory address or nil pointer dereference",
		addr: addr,
	})
}
