//go:build baremetal

package syscall

// No POSIX file-offset operation exists without a board/embedding adapter.
func Seek(fd int, offset int64, whence int) (newoffset int64, err error) {
	return -1, ENOSYS
}
