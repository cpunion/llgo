package main

import "syscall"

const probePath = "/tmp/llgo-coro-syscall-file-probe"

func main() {
	_ = syscall.Unlink(probePath)

	fd, err := syscall.Open(probePath, syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC, 0o600)
	if err != nil {
		panic("open")
	}

	want := [1]byte{0x6d}
	written, err := syscall.Write(fd, want[:])
	if err != nil || written != len(want) {
		panic("write")
	}
	position, err := syscall.Seek(fd, 0, 0)
	if err != nil || position != 0 {
		panic("seek")
	}

	got := [1]byte{}
	read, err := syscall.Read(fd, got[:])
	if err != nil || read != len(got) || got[0] != want[0] {
		panic("read")
	}
	if err = syscall.Close(fd); err != nil {
		panic("close")
	}
	if err = syscall.Unlink(probePath); err != nil {
		panic("unlink")
	}
}
