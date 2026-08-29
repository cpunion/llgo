//go:build windows

package runtime

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

const (
	windowsExceptionAccessViolation uint32 = 0xc0000005
	windowsExceptionInPageError     uint32 = 0xc0000006
	windowsExceptionIntDivideByZero uint32 = 0xc0000094
	windowsExceptionIntOverflow     uint32 = 0xc0000095
	windowsSIGFPE                          = 8
	windowsSIGSEGV                         = 11
	windowsMinPanicOnFaultAddress          = 0x1000
)

// WindowsFaultSnapshotFunc is invoked synchronously from the native Windows
// exception stack. Its single-pointer C transport prevents this raw boundary
// from manufacturing a managed descriptor dispatch that could suspend.
//
//llgo:type C
type WindowsFaultSnapshotFunc func(unsafe.Pointer) bool

// WindowsFaultSnapshot, when set by the public runtime package, first reports
// whether the fault PC belongs to Go text and then records the fault context.
// A false result leaves exceptions raised by native code to Windows' handler
// chain, matching the Go runtime's isgoexception check.
var WindowsFaultSnapshot WindowsFaultSnapshotFunc

//llgo:type C
type windowsFaultCallback func(unsafe.Pointer, uint32, uintptr)

// Registration stores cb for later exception delivery but the registration
// call itself completes synchronously on the initializing thread. The callback
// has a process lifetime and is not a borrowed coroutine-frame value.
//
//llgo:coro sync
//go:linkname installWindowsFaultHandler C.llgo_install_windows_fault_handler
func installWindowsFaultHandler(cb windowsFaultCallback) c.Int

// This only clears the current thread's fault-recursion guard immediately
// before the non-local panic transfer.
//
//llgo:coro noblock
//go:linkname windowsFaultCaptureDone C.llgo_windows_fault_capture_done
func windowsFaultCaptureDone()

func init() {
	if installWindowsFaultHandler(windowsFaultCallback(onWindowsFault)) == 0 {
		panic("runtime: failed to install Windows fault handler")
	}
}

func onWindowsFault(context unsafe.Pointer, code uint32, address uintptr) {
	// The vectored handler is process-wide and may observe a fault on a native
	// thread that never entered Go. Do not manufacture a G from exception
	// context: only faults on a thread already executing Go can become Go
	// panics. Foreign faults must continue through Windows' handler chain.
	if getgIfPresent() == nil {
		return
	}
	memoryFault := code == windowsExceptionAccessViolation || code == windowsExceptionInPageError
	if memoryFault && address >= windowsMinPanicOnFaultAddress && !PanicOnFault() {
		return
	}
	// Snapshot the callback once. Besides making concurrent publication
	// semantics explicit, this gives the raw-C call exactly the value whose
	// non-nil guard dominates it; reloading the global for the call would not
	// be the same SSA value and could not carry that proof.
	snapshot := WindowsFaultSnapshot
	if snapshot != nil && !snapshot(context) {
		return
	}

	// The panic path does not return through the vectored handler, so release
	// its recursion guard before the non-local jump begins.
	windowsFaultCaptureDone()
	panicWindowsException(code, address)
}

func panicWindowsException(code uint32, address uintptr) {
	memoryFault := code == windowsExceptionAccessViolation || code == windowsExceptionInPageError
	if memoryFault && address >= windowsMinPanicOnFaultAddress {
		PanicSignalAddr(address)
	}
	switch code {
	case windowsExceptionAccessViolation, windowsExceptionInPageError:
		PanicSignal(windowsSIGSEGV)
	case windowsExceptionIntDivideByZero:
		PanicSignal(windowsSIGFPE)
	case windowsExceptionIntOverflow:
		PanicErrorString("integer overflow")
	}
}
