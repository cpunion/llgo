//go:build baremetal

package debug

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/abi"
	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

type Info struct {
	Fname *c.Char
	Fbase uintptr
	Sname *c.Char
	Saddr uintptr
}

func Address() uintptr {
	panic("not implemented")
}

func Addrinfo(addr uintptr, info *Info) c.Int {
	panic("not implemented")
}

// Baremetal has no dynamic loader. Static LLGo function metadata supplies
// symbol information; a dynamic lookup correctly reports no match.
func Symbol(name *c.Char) abi.Text {
	return abi.Text(nil)
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
