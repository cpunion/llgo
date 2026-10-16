package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	const directory = "/tmp/llgo-coro-js-stdlib-file"
	original, err := os.Getwd()
	must(err)
	must(os.Mkdir(directory, 0700))
	must(os.Chdir(directory))
	file, err := os.OpenFile("first", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	must(err)
	if n, err := io.WriteString(file, "abcdef"); err != nil || n != 6 {
		panic("Node standard file write")
	}
	if _, err := file.Seek(1, io.SeekStart); err != nil {
		panic("Node standard file seek")
	}
	if n, err := io.NewOffsetWriter(file, 3).Write([]byte("XY")); err != nil || n != 2 {
		panic("Node OffsetWriter")
	}
	var data [8]byte
	if n, err := file.Read(data[:]); err != nil || n != 5 || string(data[:n]) != "bcXYf" {
		panic("Node Go-owned seek offset")
	}
	if n, err := file.Read(data[:]); n != 0 || err != io.EOF {
		panic("Node EOF")
	}
	if n, err := file.ReadAt(data[:6], 0); err != nil || n != 6 || string(data[:n]) != "abcXYf" {
		panic("Node positional read")
	}
	must(file.Sync())
	must(file.Close())
	if _, err := file.Read(data[:]); !errors.Is(err, os.ErrClosed) {
		panic("Node closed file result")
	}
	if _, err := os.Stat("missing"); !os.IsNotExist(err) {
		panic("Node error mapping")
	}
	if _, err := syscall.Read(-1, data[:]); err != syscall.EBADF {
		panic("Node Go descriptor table")
	}
	must(os.Rename("first", "renamed"))
	entries, err := os.ReadDir(".")
	must(err)
	if len(entries) != 1 || entries[0].Name() != "renamed" {
		panic("Node Go directory entries")
	}
	must(os.Remove("renamed"))
	must(os.Chdir(original))
	must(os.Remove(directory))
	fmt.Println("Node standard filesystem and Go bookkeeping ok")
}
