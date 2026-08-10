package main

import (
	"unsafe"
	_ "unsafe"
)

//go:linkname getsp llgo.stackSave
func getsp() unsafe.Pointer

func probe() bool {
	return getsp() != nil
}

func main() {
	println(probe())
}
