//go:build darwin && arm64 && go1.26

package syscall

import _ "unsafe"

//llgo:coro workeraddr 3
//go:linkname libc_readdir_r_trampoline C.readdir_r
func libc_readdir_r_trampoline()
