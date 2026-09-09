//go:build llgo && wasm

package runtime

import "unsafe"

// Compiler instrumentation passes the parts of immutable string literals as
// scalar arguments. Passing Go strings directly uses indirect C ABI arguments
// and reserves aggregate temporaries in every instrumented caller's stack.
// Construct the strings here instead, so those slots are live only during the
// runtime update and disappear before the instrumented call runs.
// unsafe.String preserves the compiler-owned immutable literals without a copy;
// the C string conversion intrinsic would allocate on every runtime update.
// Keep the ABI boundary under LTO too: inlining would move the aggregate
// argument storage back into each instrumented caller.
//
//go:noinline
func PushCallerLocationFrameWasm(entry uintptr, nameData *byte, nameLen int, fileData *byte, fileLen, line int) int {
	if wasmMemProfileEnabled && !memProfileHasCurrentG() {
		return -1
	}
	MemProfilePause()
	name := unsafe.String(nameData, nameLen)
	file := unsafe.String(fileData, fileLen)
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
	name := unsafe.String(nameData, nameLen)
	file := unsafe.String(fileData, fileLen)
	RecordCallerLocation(entry, name, file, line)
	MemProfileResume()
}

//go:noinline
func RecordPanicLocationWasm(entry uintptr, nameData *byte, nameLen int, fileData *byte, fileLen, line int) {
	if wasmMemProfileEnabled && !memProfileHasCurrentG() {
		return
	}
	MemProfilePause()
	name := unsafe.String(nameData, nameLen)
	file := unsafe.String(fileData, fileLen)
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
