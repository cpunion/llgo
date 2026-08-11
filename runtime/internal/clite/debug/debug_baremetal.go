//go:build baremetal

package debug

import (
	"unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

type Info struct {
	Fname *c.Char
	Fbase uintptr
	Sname *c.Char
	Saddr uintptr
}

func Address() unsafe.Pointer {
	panic("not implemented")
}

func Addrinfo(addr uintptr, info *Info) c.Int {
	panic("not implemented")
}

type Frame struct {
	PC     uintptr
	Offset uintptr
	SP     unsafe.Pointer
	Name   string
}

func StackTrace(skip int, fn func(fr *Frame) bool) {
	panic("not implemented")
}

func StackFrames(skip int) []Frame {
	panic("not implemented")
}

func PrintStack(skip int) {
	panic("not implemented")

}
