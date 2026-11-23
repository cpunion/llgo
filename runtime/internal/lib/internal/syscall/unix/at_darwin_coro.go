//go:build darwin && go1.26

package unix

import (
	"syscall"
	"unsafe"
)

// The Go 1.26 internal/syscall/unix at-family routes through syscall's private
// uintptr carriers. These exact alternate declarations certify only the
// path/metadata leaves used by the standard library. Pointer arguments remain
// rooted in their suspended managed callers until the shared worker operation
// publishes completion.

//go:linkname libc_readlinkat_trampoline C.readlinkat
func libc_readlinkat_trampoline()

//go:linkname libc_mkdirat_trampoline C.mkdirat
func libc_mkdirat_trampoline()

//go:linkname libc_fchmodat_trampoline C.fchmodat
func libc_fchmodat_trampoline()

//go:linkname libc_fchownat_trampoline C.fchownat
func libc_fchownat_trampoline()

//go:linkname libc_renameat_trampoline C.renameat
func libc_renameat_trampoline()

//go:linkname libc_linkat_trampoline C.linkat
func libc_linkat_trampoline()

//go:linkname libc_symlinkat_trampoline C.symlinkat
func libc_symlinkat_trampoline()

//go:linkname symlinkatPC llgo.funcPCABI0
func symlinkatPC(fn any) uintptr

//go:linkname symlinkatCall llgo.syscall32
func symlinkatCall(fn, oldpath, newdirfd, newpath uintptr) (uintptr, uintptr, uintptr)

// Symlinkat shares the three-word transport used by the generated x/sys
// wrapper for the same C entry.
func Symlinkat(oldpath string, newdirfd int, newpath string) error {
	oldp, err := syscall.BytePtrFromString(oldpath)
	if err != nil {
		return err
	}
	newp, err := syscall.BytePtrFromString(newpath)
	if err != nil {
		return err
	}
	_, _, errno := symlinkatCall(symlinkatPC(libc_symlinkat_trampoline),
		uintptr(unsafe.Pointer(oldp)), uintptr(newdirfd), uintptr(unsafe.Pointer(newp)))
	if errno != 0 {
		return syscall.Errno(errno)
	}
	return nil
}

//go:linkname libc_faccessat_trampoline C.faccessat
func libc_faccessat_trampoline()
