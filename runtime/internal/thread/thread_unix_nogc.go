//go:build !windows && (nogc || baremetal || llgo_wasm_gc)

package thread

import (
	_ "unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

//go:linkname create C.pthread_create
func create(thread *nativeThread, attr *nativeAttr, routine RoutineFunc, arg c.Pointer) c.Int

//go:linkname join C.pthread_join
func join(thread nativeThread, retval *c.Pointer) c.Int
