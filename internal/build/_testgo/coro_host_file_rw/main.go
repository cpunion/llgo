package main

import "syscall"

func main() {
	const path = "/llgo-host/roundtrip.txt"
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_TRUNC|syscall.O_RDWR, 0o600)
	if err != nil {
		panic(err)
	}
	want := []byte{0x5a}
	if n, err := syscall.Write(fd, want); err != nil || n != len(want) {
		panic("host file write failed")
	}
	if offset, err := syscall.Seek(fd, 0, 0); err != nil || offset != 0 {
		panic("host file seek failed")
	}
	got := []byte{0}
	if n, err := syscall.Read(fd, got); err != nil || n != len(got) || got[0] != want[0] {
		panic("host file read failed")
	}
	if err := syscall.Close(fd); err != nil {
		panic(err)
	}
	if err := syscall.Unlink(path); err != nil {
		panic(err)
	}
}
