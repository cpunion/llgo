package main

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/runtime/tinygogc"
)

var root unsafe.Pointer

func main() {
	for i := uintptr(1); i <= 80; i++ {
		root = tinygogc.Alloc(4 * unsafe.Sizeof(uintptr(0)))
		child := tinygogc.Alloc(97)
		*(*unsafe.Pointer)(root) = unsafe.Add(child, 1)
		*(*byte)(unsafe.Add(child, 1)) = byte(i)
		tinygogc.GC()
		ptr := *(*unsafe.Pointer)(root)
		if *(*byte)(ptr) != byte(i) {
			panic("gc standalone lost an interior root")
		}
	}
	if tinygogc.ReadGCStats().NumGC < 80 {
		panic("gc standalone did not collect")
	}
	println("gc standalone ok")
}
