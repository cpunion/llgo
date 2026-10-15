//go:build llgo && llgo_coro && js && wasm

package syscall

import (
	"syscall/js"
	_ "unsafe"
)

// Preserve fs_js.go's descriptor map, cwd, offsets and directory decoding.
// Only its asynchronous submission boundary needs a coroutine adapter.
//
//go:linkname coroJSFSCall syscall/js.llgoFSCall
func coroJSFSCall(name string, args ...any) (result, failure js.Value)

func fsCall(name string, args ...any) (js.Value, error) {
	result, failure := coroJSFSCall(name, args...)
	if !failure.IsNull() {
		return result, mapJSError(failure)
	}
	return result, nil
}
