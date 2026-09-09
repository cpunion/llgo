//go:build llgo && wasm

package runtime

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

// Compiler instrumentation passes the parts of immutable string literals as
// scalar arguments. Passing Go strings directly uses indirect C ABI arguments
// and reserves aggregate temporaries in every instrumented caller's stack.
// Construct the strings here instead, so those slots are live only during the
// runtime update and disappear before the instrumented call runs.
// These pointers and lengths describe compiler-owned immutable literals.
// Use the existing string-construction intrinsic instead of unsafe.String's
// dynamic bounds checks on every shadow-stack update.
// Keep the ABI boundary under LTO too: inlining would move the aggregate
// argument storage back into each instrumented caller.
//
//go:noinline
func PushCallerLocationFrameWasm(entry uintptr, nameData *byte, nameLen int, fileData *byte, fileLen, line int) int {
	if wasmMemProfileEnabled && !memProfileHasCurrentG() {
		return -1
	}
	MemProfilePause()
	name := c.GoString((*c.Char)(unsafe.Pointer(nameData)), nameLen)
	file := c.GoString((*c.Char)(unsafe.Pointer(fileData)), fileLen)
	mark := PushCallerLocationFrame(entry, name, file, line)
	MemProfileResume()
	return mark
}

//go:noinline
func RecordCallerLocationWasm(entry uintptr, nameData *byte, nameLen int, fileData *byte, fileLen, line int) {
	if wasmMemProfileEnabled && !memProfileHasCurrentG() {
		return
	}
	MemProfilePause()
	name := c.GoString((*c.Char)(unsafe.Pointer(nameData)), nameLen)
	file := c.GoString((*c.Char)(unsafe.Pointer(fileData)), fileLen)
	RecordCallerLocation(entry, name, file, line)
	MemProfileResume()
}

//go:noinline
func RecordPanicLocationWasm(entry uintptr, nameData *byte, nameLen int, fileData *byte, fileLen, line int) {
	if wasmMemProfileEnabled && !memProfileHasCurrentG() {
		return
	}
	MemProfilePause()
	name := c.GoString((*c.Char)(unsafe.Pointer(nameData)), nameLen)
	file := c.GoString((*c.Char)(unsafe.Pointer(fileData)), fileLen)
	RecordPanicLocation(entry, name, file, line)
	MemProfileResume()
}

//go:noinline
func PopCallerLocationFrameWasm(mark int) {
	if wasmMemProfileEnabled && !memProfileHasCurrentG() {
		return
	}
	PopCallerLocationFrame(mark)
}
