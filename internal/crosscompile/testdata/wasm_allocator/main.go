//go:build tinygo.wasm

package main

import "unsafe"

// The named freestanding targets provide these symbols through their
// triple-scoped wasmbuiltins archive. Linknames keep this fixture independent
// of a host C sysroot and make the real llgo -target route exercise that ABI.
//
//llgo:coro sync
//go:linkname malloc C.malloc
func malloc(size uintptr) unsafe.Pointer

//llgo:coro sync
//go:linkname free C.free
func free(ptr unsafe.Pointer)

//llgo:coro noblock
//go:linkname abort C.abort
func abort()

func main() {
	const size = uintptr(64)
	ptr := malloc(size)
	if ptr == nil {
		abort()
	}
	for offset := uintptr(0); offset < size; offset++ {
		*(*byte)(unsafe.Add(ptr, offset)) = byte(offset + 1)
	}
	for offset := uintptr(0); offset < size; offset++ {
		if *(*byte)(unsafe.Add(ptr, offset)) != byte(offset+1) {
			abort()
		}
	}
	free(ptr)
}
