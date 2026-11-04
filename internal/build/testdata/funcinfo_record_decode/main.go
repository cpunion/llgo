package main

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

type record struct {
	symbolPkg, symbolName, namePkg, nameName, fileRoot, fileName, line uint32
}

type compact struct {
	symbolPkg, symbolName, namePkg, nameName, fileRoot, fileName uint16
	line                                                         uint32
}

//go:linkname decodeRecord runtime.decodeRuntimeFuncInfoRecord
func decodeRecord(unsafe.Pointer, uint8, uintptr) record

//go:linkname compiledWidth __llgo_funcinfo_record_size
var compiledWidth uint8

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "16" && os.Args[1] != "28") {
		panic("expected record width argument")
	}
	if os.Args[1] == "16" && compiledWidth != 16 || os.Args[1] == "28" && compiledWidth != 28 {
		panic("unexpected compiled record width")
	}
	if unsafe.Sizeof(record{}) != 28 || unsafe.Sizeof(compact{}) != 16 {
		panic("unexpected record layouts")
	}
	page := syscall.Getpagesize()
	storage, err := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		panic(err)
	}
	defer syscall.Munmap(storage)
	if err := syscall.Mprotect(storage[page:], syscall.PROT_NONE); err != nil {
		panic(err)
	}
	// The last record ends exactly at an inaccessible page. A decoder must
	// respect both the declared record width and the array stride.
	small := (*[2]compact)(unsafe.Pointer(&storage[page-32]))
	small[0] = compact{0, 1, 65535, 2, 3, 4, 0x80000011}
	small[1] = compact{65535, 0, 1, 2, 3, 65535, ^uint32(0)}
	if got := decodeRecord(unsafe.Pointer(small), 16, 0); got != (record{0, 1, 65535, 2, 3, 4, 0x80000011}) {
		panic("first compact record changed")
	}
	if got := decodeRecord(unsafe.Pointer(small), 16, 1); got != (record{65535, 0, 1, 2, 3, 65535, ^uint32(0)}) {
		panic("last compact record changed")
	}
	wide := (*[2]record)(unsafe.Pointer(&storage[page-56]))
	wide[0] = record{0, 65535, 65536, 65537, 3, 4, 0x80000011}
	wide[1] = record{^uint32(0), 65536, 65535, 0, 1, 2, ^uint32(0)}
	for i := 0; i < 2; i++ {
		if got := decodeRecord(unsafe.Pointer(wide), 28, uintptr(i)); got != wide[i] {
			panic("wide record changed")
		}
	}
	runtime.KeepAlive(storage)
	println("funcinfo record decode ok")
}
