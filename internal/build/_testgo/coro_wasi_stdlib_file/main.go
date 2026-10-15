package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	const directory = "/tmp/llgo-coro-wasi-stdlib-file"
	must(os.Mkdir(directory, 0700))
	root, err := os.OpenRoot(directory)
	must(err)
	file, err := root.OpenFile("first", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	must(err)
	if n, err := io.WriteString(file, "abcdef"); err != nil || n != 6 {
		panic("standard file write")
	}
	if n, err := io.NewOffsetWriter(file, 1).Write([]byte("XY")); err != nil || n != 2 {
		panic("OffsetWriter/WriteAt")
	}
	var contents [6]byte
	if n, err := file.ReadAt(contents[:], 0); err != nil || n != 6 || string(contents[:]) != "aXYdef" {
		panic("standard ReadAt")
	}
	must(file.SetReadDeadline(time.Now().Add(-time.Second)))
	if _, err := file.Read(contents[:]); !os.IsTimeout(err) {
		panic("expired standard file read deadline")
	}
	must(file.SetReadDeadline(time.Time{}))
	if n, err := file.Read(contents[:]); n != 0 || err != io.EOF {
		panic("standard file cursor and EOF")
	}
	must(file.SetWriteDeadline(time.Now().Add(-time.Second)))
	if _, err := file.Write([]byte("!")); !os.IsTimeout(err) {
		panic("expired standard file write deadline")
	}
	must(file.SetWriteDeadline(time.Time{}))
	must(file.Sync())
	must(file.Close())
	must(root.Rename("first", "renamed"))
	info, err := root.Stat("renamed")
	must(err)
	if info.Size() != 6 || !info.Mode().IsRegular() {
		panic("root.Stat")
	}
	entries, err := os.ReadDir(directory)
	must(err)
	if len(entries) != 1 || entries[0].Name() != "renamed" {
		panic("standard directory iteration")
	}
	must(root.Remove("renamed"))
	must(root.Close())
	must(os.Remove(directory))
	fmt.Println("WASI standard file, Root and fmt ok")
}
