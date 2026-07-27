//go:build darwin && amd64 && go1.26

package syscall

import _ "unsafe"

//go:linkname libc_readdir_r_trampoline C.readdir_r$INODE64
func libc_readdir_r_trampoline()
