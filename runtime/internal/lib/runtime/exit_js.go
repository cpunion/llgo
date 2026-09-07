//go:build js && wasm && !baremetal

package runtime

import (
	_ "unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/clite/emscripten"
)

//go:linkname syscall_Exit syscall.Exit
//go:nosplit
func syscall_Exit(code int) {
	// Go's os.Exit terminates the process even with pending host callbacks.
	// Plain exit() can leave the Asyncify keepalive runtime running and omit
	// onExit, losing a failed test's status when the module factory resolves.
	emscripten.ForceExit(c.Int(code))
}
