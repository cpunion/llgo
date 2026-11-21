//go:build (darwin || (linux && !baremetal && !tinygo.wasm)) && !(llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && !baremetal && !coro_runtime_adapter_test)

package runtime

import _ "unsafe"

//go:linkname os_sigpipe os.sigpipe
func os_sigpipe() {}
