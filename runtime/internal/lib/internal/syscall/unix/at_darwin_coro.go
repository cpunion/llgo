//go:build darwin && go1.26

package unix

import _ "unsafe"

// The Go 1.26 internal/syscall/unix at-family routes through syscall's private
// uintptr carriers. These exact alternate declarations certify only the
// path/metadata leaves used by the standard library. Pointer arguments remain
// rooted in their suspended managed callers until the shared worker operation
// publishes completion.

//llgo:coro workeraddr 6
//go:linkname libc_readlinkat_trampoline C.readlinkat
func libc_readlinkat_trampoline()

//llgo:coro workeraddr 3
//go:linkname libc_mkdirat_trampoline C.mkdirat
func libc_mkdirat_trampoline()

//llgo:coro workeraddr 6
//go:linkname libc_fchmodat_trampoline C.fchmodat
func libc_fchmodat_trampoline()

//llgo:coro workeraddr 6
//go:linkname libc_fchownat_trampoline C.fchownat
func libc_fchownat_trampoline()

//llgo:coro workeraddr 6
//go:linkname libc_renameat_trampoline C.renameat
func libc_renameat_trampoline()

//llgo:coro workeraddr 6
//go:linkname libc_linkat_trampoline C.linkat
func libc_linkat_trampoline()

//llgo:coro workeraddr 6
//go:linkname libc_symlinkat_trampoline C.symlinkat
func libc_symlinkat_trampoline()

//llgo:coro workeraddr 6
//go:linkname libc_faccessat_trampoline C.faccessat
func libc_faccessat_trampoline()
