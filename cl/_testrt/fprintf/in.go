package main

import (
	"unsafe"

	"github.com/goplus/lib/c"
)

//
//go:linkname cstr llgo.cstr
func cstr(string) *int8

//go:linkname fprintf C.fprintf
func fprintf(fp unsafe.Pointer, format *int8, __llgo_va_list ...any)

func main() {
	fprintf(unsafe.Pointer(c.Stderr), cstr("Hello %d\n"), 100)
}
