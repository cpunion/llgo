//go:build llgo && llgo_coro && wasip1

package unix

import (
	"syscall"
	"unsafe"
)

// Reuse the defined syscall adapters, including their inferred suspension
// effect and typed borrowed arguments. There is no second host dispatcher.

//go:linkname path_filestat_get syscall.path_filestat_get
func path_filestat_get(fd int32, flags uint32, path *byte, pathLen size, buf unsafe.Pointer) syscall.Errno

//go:linkname path_filestat_set_times syscall.path_filestat_set_times
func path_filestat_set_times(fd int32, flags uint32, path *byte, pathLen size, atim uint64, mtim uint64, fstflags uint32) syscall.Errno

//go:linkname path_create_directory syscall.path_create_directory
func path_create_directory(fd int32, path *byte, pathLen size) syscall.Errno

//go:linkname path_unlink_file syscall.path_unlink_file
func path_unlink_file(fd int32, path *byte, pathLen size) syscall.Errno

//go:linkname path_remove_directory syscall.path_remove_directory
func path_remove_directory(fd int32, path *byte, pathLen size) syscall.Errno

//go:linkname path_rename syscall.path_rename
func path_rename(oldFd int32, oldPath *byte, oldPathLen size, newFd int32, newPath *byte, newPathLen size) syscall.Errno

//go:linkname path_link syscall.path_link
func path_link(oldFd int32, oldFlags uint32, oldPath *byte, oldPathLen size, newFd int32, newPath *byte, newPathLen size) syscall.Errno

//go:linkname path_symlink syscall.path_symlink
func path_symlink(oldPath *byte, oldPathLen size, fd int32, newPath *byte, newPathLen size) syscall.Errno

//go:linkname path_readlink syscall.path_readlink
func path_readlink(fd int32, path *byte, pathLen size, buf *byte, bufLen size, nwritten *size) syscall.Errno
