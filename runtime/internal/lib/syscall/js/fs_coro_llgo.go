//go:build js && wasm && llgo && llgo_coro

package js

import "unsafe"

//go:linkname coroJSFSOperation llgo.coroHostOperation
func coroJSFSOperation(opcode uintptr, name unsafe.Pointer, nameLen uintptr, args unsafe.Pointer, count uintptr) (result, failure, reserved uintptr)

//go:linkname coroJSFSOpenOperation llgo.coroHostOperation
func coroJSFSOpenOperation(opcode uintptr, name unsafe.Pointer, nameLen uintptr, args unsafe.Pointer, count uintptr, output *int32) (result, failure, failed uintptr)

// llgoFSCall is private to syscall's source adapter. The host copies the name
// and JS argument values on submission; typed operands retain their Go owners
// through the exact source-generation completion. No public FuncOf callback
// or synchronous JS-to-Go reentry is involved.
func llgoFSCall(name string, args ...any) (Value, Value) {
	values := make([]Value, len(args))
	for index, arg := range args {
		values[index] = ValueOf(arg)
	}
	result, failure, reserved := coroJSFSOperation(4<<16|1,
		unsafe.Pointer(unsafe.StringData(name)), uintptr(len(name)),
		unsafe.Pointer(unsafe.SliceData(values)), uintptr(len(values)))
	if reserved != 0 {
		panic("syscall/js: invalid filesystem host response")
	}
	return Value{ref: result}, Value{ref: failure}
}

// llgoFSOpenInto is an internal acquisition primitive, not the public Open
// result-delivery contract. The caller must initialize output to -1 and
// register its ordinary deferred close BEFORE calling. The host borrows only
// this typed POD slot until physical completion. On success it writes the FD
// before Post; after that point even a canceled resume leaves the caller's
// guard responsible. Before Post, canceled acquisition is closed by the host.
func llgoFSOpenInto(output *int32, path string, flags int, perm uint32) Value {
	if output == nil || *output != -1 {
		panic("syscall/js: open requires an empty borrowed descriptor slot")
	}
	const name = "open"
	values := []Value{ValueOf(path), ValueOf(flags), ValueOf(perm)}
	result, failure, failed := coroJSFSOpenOperation(4<<16|2,
		unsafe.Pointer(unsafe.StringData(name)), uintptr(len(name)),
		unsafe.Pointer(unsafe.SliceData(values)), uintptr(len(values)), output)
	err := Value{ref: failure}
	if result != 0 || failed > 1 || (failed == 0) != err.IsNull() {
		panic("syscall/js: invalid borrowed open response")
	}
	return err
}
