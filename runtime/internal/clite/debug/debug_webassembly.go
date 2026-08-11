//go:build wasm || tinygo.wasm

package debug

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/abi"
	c "github.com/goplus/llgo/runtime/internal/clite"
)

const (
	LLGoFiles = "_wrap/debug_wasm.c"
)

type Info struct {
	Fname *c.Char
	Fbase uintptr
	Sname *c.Char
	Saddr uintptr
}

func Address() unsafe.Pointer {
	return nil
}

func Addrinfo(addr uintptr, info *Info) c.Int {
	return 0
}

// Symbol lookup through a native dynamic loader does not exist in a core wasm
// module. Static LLGo function metadata remains the authoritative source.
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
	// Native frame walking is not meaningful for stackless wasm. Logical
	// coroutine frames are reported by the runtime metadata path instead.
}

func StackFrames(skip int) []Frame {
	// Native frame walking is not meaningful for stackless wasm. Logical
	// coroutine frames are reported by the runtime metadata path instead.
	return nil
}

func PrintStack(skip int) {
	print_stack(c.Int(skip + 4))
}

//go:linkname print_stack C.llgo_print_stack
func print_stack(skip c.Int)
