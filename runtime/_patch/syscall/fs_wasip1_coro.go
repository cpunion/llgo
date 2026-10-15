//go:build llgo && llgo_coro && wasip1

package syscall

import "unsafe"

// WASI's raw fd/path boundary owns class 3 of the existing HostOp protocol.
// Keep Go's cwd, preopen resolution, flags, Openat and directory decoding in
// the standard library. The reactor receives exact WASI arguments, not paths
// to reinterpret with libc. All pointers stay typed until exact completion;
// the standard iovec callers retain their backing slices with KeepAlive.
//
// Each 64-bit scalar is split into low/high words on wasm32. path_open uses
// one borrowed [2]uint64 rights record to stay within the nine-word limit.
// This does not turn Preview 1's synchronous host imports into nonblocking
// disk operations; it separates their execution from the managed Go task.

//go:linkname coroWASI_fd_read llgo.coroHostOperation
func coroWASI_fd_read(opcode uintptr, a0 uintptr, a1 unsafe.Pointer, a2 uintptr, a3 unsafe.Pointer) (r1, r2, errno uintptr)

func fd_read(fd int32, iovs *iovec, iovsLen size, nread *size) Errno {
	_, _, errno := coroWASI_fd_read(3<<16|1, uintptr(uint32(fd)), unsafe.Pointer(iovs), uintptr(iovsLen), unsafe.Pointer(nread))
	return Errno(errno)
}

//go:linkname coroWASI_fd_write llgo.coroHostOperation
func coroWASI_fd_write(opcode uintptr, a0 uintptr, a1 unsafe.Pointer, a2 uintptr, a3 unsafe.Pointer) (r1, r2, errno uintptr)

func fd_write(fd int32, iovs *iovec, iovsLen size, nwritten *size) Errno {
	_, _, errno := coroWASI_fd_write(3<<16|2, uintptr(uint32(fd)), unsafe.Pointer(iovs), uintptr(iovsLen), unsafe.Pointer(nwritten))
	return Errno(errno)
}

//go:linkname coroWASI_fd_pread llgo.coroHostOperation
func coroWASI_fd_pread(opcode uintptr, a0 uintptr, a1 unsafe.Pointer, a2 uintptr, a3 uintptr, a4 uintptr, a5 unsafe.Pointer) (r1, r2, errno uintptr)

func fd_pread(fd int32, iovs *iovec, iovsLen size, offset filesize, nread *size) Errno {
	_, _, errno := coroWASI_fd_pread(3<<16|3, uintptr(uint32(fd)), unsafe.Pointer(iovs), uintptr(iovsLen), uintptr(uint32(offset)), uintptr(uint32(offset>>32)), unsafe.Pointer(nread))
	return Errno(errno)
}

//go:linkname coroWASI_fd_pwrite llgo.coroHostOperation
func coroWASI_fd_pwrite(opcode uintptr, a0 uintptr, a1 unsafe.Pointer, a2 uintptr, a3 uintptr, a4 uintptr, a5 unsafe.Pointer) (r1, r2, errno uintptr)

func fd_pwrite(fd int32, iovs *iovec, iovsLen size, offset filesize, nwritten *size) Errno {
	_, _, errno := coroWASI_fd_pwrite(3<<16|4, uintptr(uint32(fd)), unsafe.Pointer(iovs), uintptr(iovsLen), uintptr(uint32(offset)), uintptr(uint32(offset>>32)), unsafe.Pointer(nwritten))
	return Errno(errno)
}

//go:linkname coroWASI_fd_close llgo.coroHostOperation
func coroWASI_fd_close(opcode uintptr, a0 uintptr) (r1, r2, errno uintptr)

func fd_close(fd int32) Errno {
	_, _, errno := coroWASI_fd_close(3<<16|5, uintptr(uint32(fd)))
	return Errno(errno)
}

//go:linkname coroWASI_fd_seek llgo.coroHostOperation
func coroWASI_fd_seek(opcode uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 unsafe.Pointer) (r1, r2, errno uintptr)

func fd_seek(fd int32, offset filedelta, whence uint32, newoffset *filesize) Errno {
	_, _, errno := coroWASI_fd_seek(3<<16|6, uintptr(uint32(fd)), uintptr(uint32(offset)), uintptr(uint32(uint64(offset)>>32)), uintptr(whence), unsafe.Pointer(newoffset))
	return Errno(errno)
}

//go:linkname coroWASI_fd_readdir llgo.coroHostOperation
func coroWASI_fd_readdir(opcode uintptr, a0 uintptr, a1 unsafe.Pointer, a2 uintptr, a3 uintptr, a4 uintptr, a5 unsafe.Pointer) (r1, r2, errno uintptr)

func fd_readdir(fd int32, buf *byte, bufLen size, cookie dircookie, nwritten *size) Errno {
	_, _, errno := coroWASI_fd_readdir(3<<16|7, uintptr(uint32(fd)), unsafe.Pointer(buf), uintptr(bufLen), uintptr(uint32(cookie)), uintptr(uint32(cookie>>32)), unsafe.Pointer(nwritten))
	return Errno(errno)
}

//go:linkname coroWASI_fd_filestat_get llgo.coroHostOperation
func coroWASI_fd_filestat_get(opcode uintptr, a0 uintptr, a1 unsafe.Pointer) (r1, r2, errno uintptr)

func fd_filestat_get(fd int32, buf unsafe.Pointer) Errno {
	_, _, errno := coroWASI_fd_filestat_get(3<<16|8, uintptr(uint32(fd)), buf)
	return Errno(errno)
}

//go:linkname coroWASI_fd_filestat_set_size llgo.coroHostOperation
func coroWASI_fd_filestat_set_size(opcode uintptr, a0 uintptr, a1 uintptr, a2 uintptr) (r1, r2, errno uintptr)

func fd_filestat_set_size(fd int32, set_size filesize) Errno {
	_, _, errno := coroWASI_fd_filestat_set_size(3<<16|9, uintptr(uint32(fd)), uintptr(uint32(set_size)), uintptr(uint32(set_size>>32)))
	return Errno(errno)
}

//go:linkname coroWASI_fd_sync llgo.coroHostOperation
func coroWASI_fd_sync(opcode uintptr, a0 uintptr) (r1, r2, errno uintptr)

func fd_sync(fd int32) Errno {
	_, _, errno := coroWASI_fd_sync(3<<16|10, uintptr(uint32(fd)))
	return Errno(errno)
}

//go:linkname coroWASI_fd_fdstat_set_rights llgo.coroHostOperation
func coroWASI_fd_fdstat_set_rights(opcode uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr) (r1, r2, errno uintptr)

func fd_fdstat_set_rights(fd int32, rightsBase rights, rightsInheriting rights) Errno {
	_, _, errno := coroWASI_fd_fdstat_set_rights(3<<16|11, uintptr(uint32(fd)), uintptr(uint32(rightsBase)), uintptr(uint32(rightsBase>>32)), uintptr(uint32(rightsInheriting)), uintptr(uint32(rightsInheriting>>32)))
	return Errno(errno)
}

//go:linkname coroWASI_path_open llgo.coroHostOperation
func coroWASI_path_open(opcode uintptr, a0 uintptr, a1 uintptr, a2 unsafe.Pointer, a3 uintptr, a4 uintptr, a5 unsafe.Pointer, a6 uintptr, a7 unsafe.Pointer) (r1, r2, errno uintptr)

func path_open(rootFD int32, dirflags lookupflags, path *byte, pathLen size, oflags oflags, fsRightsBase rights, fsRightsInheriting rights, fsFlags fdflags, fd *int32) Errno {
	capabilities := [2]uint64{fsRightsBase, fsRightsInheriting}
	_, _, errno := coroWASI_path_open(3<<16|12, uintptr(uint32(rootFD)), uintptr(dirflags), unsafe.Pointer(path), uintptr(pathLen), uintptr(oflags), unsafe.Pointer(&capabilities), uintptr(fsFlags), unsafe.Pointer(fd))
	return Errno(errno)
}

//go:linkname coroWASI_path_filestat_get llgo.coroHostOperation
func coroWASI_path_filestat_get(opcode uintptr, a0 uintptr, a1 uintptr, a2 unsafe.Pointer, a3 uintptr, a4 unsafe.Pointer) (r1, r2, errno uintptr)

func path_filestat_get(fd int32, flags lookupflags, path *byte, pathLen size, buf unsafe.Pointer) Errno {
	_, _, errno := coroWASI_path_filestat_get(3<<16|13, uintptr(uint32(fd)), uintptr(flags), unsafe.Pointer(path), uintptr(pathLen), buf)
	return Errno(errno)
}

//go:linkname coroWASI_path_filestat_set_times llgo.coroHostOperation
func coroWASI_path_filestat_set_times(opcode uintptr, a0 uintptr, a1 uintptr, a2 unsafe.Pointer, a3 uintptr, a4 uintptr, a5 uintptr, a6 uintptr, a7 uintptr, a8 uintptr) (r1, r2, errno uintptr)

func path_filestat_set_times(fd int32, flags lookupflags, path *byte, pathLen size, atim timestamp, mtim timestamp, fstflags fstflags) Errno {
	_, _, errno := coroWASI_path_filestat_set_times(3<<16|14, uintptr(uint32(fd)), uintptr(flags), unsafe.Pointer(path), uintptr(pathLen), uintptr(uint32(atim)), uintptr(uint32(atim>>32)), uintptr(uint32(mtim)), uintptr(uint32(mtim>>32)), uintptr(fstflags))
	return Errno(errno)
}

//go:linkname coroWASI_path_create_directory llgo.coroHostOperation
func coroWASI_path_create_directory(opcode uintptr, a0 uintptr, a1 unsafe.Pointer, a2 uintptr) (r1, r2, errno uintptr)

func path_create_directory(fd int32, path *byte, pathLen size) Errno {
	_, _, errno := coroWASI_path_create_directory(3<<16|15, uintptr(uint32(fd)), unsafe.Pointer(path), uintptr(pathLen))
	return Errno(errno)
}

//go:linkname coroWASI_path_unlink_file llgo.coroHostOperation
func coroWASI_path_unlink_file(opcode uintptr, a0 uintptr, a1 unsafe.Pointer, a2 uintptr) (r1, r2, errno uintptr)

func path_unlink_file(fd int32, path *byte, pathLen size) Errno {
	_, _, errno := coroWASI_path_unlink_file(3<<16|16, uintptr(uint32(fd)), unsafe.Pointer(path), uintptr(pathLen))
	return Errno(errno)
}

//go:linkname coroWASI_path_remove_directory llgo.coroHostOperation
func coroWASI_path_remove_directory(opcode uintptr, a0 uintptr, a1 unsafe.Pointer, a2 uintptr) (r1, r2, errno uintptr)

func path_remove_directory(fd int32, path *byte, pathLen size) Errno {
	_, _, errno := coroWASI_path_remove_directory(3<<16|17, uintptr(uint32(fd)), unsafe.Pointer(path), uintptr(pathLen))
	return Errno(errno)
}

//go:linkname coroWASI_path_rename llgo.coroHostOperation
func coroWASI_path_rename(opcode uintptr, a0 uintptr, a1 unsafe.Pointer, a2 uintptr, a3 uintptr, a4 unsafe.Pointer, a5 uintptr) (r1, r2, errno uintptr)

func path_rename(oldFd int32, oldPath *byte, oldPathLen size, newFd int32, newPath *byte, newPathLen size) Errno {
	_, _, errno := coroWASI_path_rename(3<<16|18, uintptr(uint32(oldFd)), unsafe.Pointer(oldPath), uintptr(oldPathLen), uintptr(uint32(newFd)), unsafe.Pointer(newPath), uintptr(newPathLen))
	return Errno(errno)
}

//go:linkname coroWASI_path_link llgo.coroHostOperation
func coroWASI_path_link(opcode uintptr, a0 uintptr, a1 uintptr, a2 unsafe.Pointer, a3 uintptr, a4 uintptr, a5 unsafe.Pointer, a6 uintptr) (r1, r2, errno uintptr)

func path_link(oldFd int32, oldFlags lookupflags, oldPath *byte, oldPathLen size, newFd int32, newPath *byte, newPathLen size) Errno {
	_, _, errno := coroWASI_path_link(3<<16|19, uintptr(uint32(oldFd)), uintptr(oldFlags), unsafe.Pointer(oldPath), uintptr(oldPathLen), uintptr(uint32(newFd)), unsafe.Pointer(newPath), uintptr(newPathLen))
	return Errno(errno)
}

//go:linkname coroWASI_path_symlink llgo.coroHostOperation
func coroWASI_path_symlink(opcode uintptr, a0 unsafe.Pointer, a1 uintptr, a2 uintptr, a3 unsafe.Pointer, a4 uintptr) (r1, r2, errno uintptr)

func path_symlink(oldPath *byte, oldPathLen size, fd int32, newPath *byte, newPathLen size) Errno {
	_, _, errno := coroWASI_path_symlink(3<<16|20, unsafe.Pointer(oldPath), uintptr(oldPathLen), uintptr(uint32(fd)), unsafe.Pointer(newPath), uintptr(newPathLen))
	return Errno(errno)
}

//go:linkname coroWASI_path_readlink llgo.coroHostOperation
func coroWASI_path_readlink(opcode uintptr, a0 uintptr, a1 unsafe.Pointer, a2 uintptr, a3 unsafe.Pointer, a4 uintptr, a5 unsafe.Pointer) (r1, r2, errno uintptr)

func path_readlink(fd int32, path *byte, pathLen size, buf *byte, bufLen size, nwritten *size) Errno {
	_, _, errno := coroWASI_path_readlink(3<<16|21, uintptr(uint32(fd)), unsafe.Pointer(path), uintptr(pathLen), unsafe.Pointer(buf), uintptr(bufLen), unsafe.Pointer(nwritten))
	return Errno(errno)
}
