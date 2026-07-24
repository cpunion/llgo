//go:build go1.26 && (linux || darwin)

package runtime

import _ "unsafe"

//llgo:managedlink
//go:linkname syscall_runtimeClearenv syscall.runtimeClearenv
func syscall_runtimeClearenv(env map[string]int) {
	for k := range env {
		syscall_runtimeUnsetenv(k)
	}
}
