package main

import "syscall"

func main() {
	const path = "/tmp/llgo-coro-host-positional"
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_TRUNC|syscall.O_RDWR, 0o600)
	if err != nil {
		panic("positional open")
	}
	if n, err := syscall.Write(fd, []byte("abcde")); n != 5 || err != nil {
		panic("positional initial write")
	}
	if n, err := syscall.Pwrite(fd, []byte("XY"), 1); n != 2 || err != nil {
		panic("positional write")
	}
	var buffer [5]byte
	if n, err := syscall.Pread(fd, buffer[:], 0); n != 5 || err != nil || string(buffer[:]) != "aXYde" {
		panic("positional read")
	}
	if n, err := syscall.Pread(fd, buffer[:], 3); n != 2 || err != nil || string(buffer[:2]) != "de" {
		panic("positional short read")
	}
	if n, err := syscall.Pread(fd, buffer[:], 1<<32|3); n != 0 || err != nil {
		panic("positional 64-bit offset")
	}
	if n, err := syscall.Pread(fd, nil, 0); n != 0 || err != nil {
		panic("positional zero-length read")
	}
	if n, err := syscall.Pread(fd, buffer[:], -1); n != 0 || err != syscall.EINVAL {
		panic("positional negative read offset")
	}
	if n, err := syscall.Pwrite(fd, buffer[:], -1); n != 0 || err != syscall.EINVAL {
		panic("positional negative write offset")
	}
	if position, err := syscall.Seek(fd, 0, 1); position != 5 || err != nil {
		panic("positional I/O changed descriptor cursor")
	}
	if err := syscall.Close(fd); err != nil {
		panic("positional close")
	}
	if n, err := syscall.Pread(fd, buffer[:], 0); n != 0 || err != syscall.EBADF {
		panic("positional read after close")
	}
	if n, err := syscall.Pwrite(fd, buffer[:], 0); n != 0 || err != syscall.EBADF {
		panic("positional write after close")
	}
	if err := syscall.Unlink(path); err != nil {
		panic("positional unlink")
	}
}
