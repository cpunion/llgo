//go:build !wasm && !tinygo.wasm && !baremetal

package debug

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/abi"
	c "github.com/goplus/llgo/runtime/internal/clite"
)

const (
	LLGoFiles = "$(llvm-config --cflags): _wrap/debug.c"
)

type Info struct {
	Fname *c.Char
	Fbase uintptr
	Sname *c.Char
	Saddr uintptr
}

//go:linkname Address C.llgo_address
func Address() unsafe.Pointer

//llgo:coro noblock
//go:linkname Addrinfo C.llgo_addrinfo
func Addrinfo(addr uintptr, info *Info) c.Int

// Symbol searches only the process' already-loaded image. dlsym may take the
// dynamic-loader lock, but this wrapper performs no application I/O, retains
// no caller buffer, and returns on the calling thread.
//
//llgo:coro sync
//go:linkname Symbol C.llgo_symbol
func Symbol(name *c.Char) abi.Text

type stacktraceFrame struct {
	pc     abi.Text
	offset uintptr
	sp     unsafe.Pointer
	name   *c.Char
}

// stacktrace synchronously snapshots native frame metadata into caller-owned
// storage. The C walker retains nothing and invokes no Go callback, so an
// ordinary Go callback may suspend safely only after this call returns.
//
//llgo:coro sync
//go:linkname stacktrace C.llgo_stacktrace
func stacktrace(skip c.Int, frames *stacktraceFrame, capacity c.Int) c.Int

//go:linkname printStack C.llgo_print_stack
func printStack(skip c.Int)

type Frame struct {
	PC     uintptr
	Offset uintptr
	SP     unsafe.Pointer
	Name   string
}

// StackFrames snapshots native frames without invoking managed code while the
// native stack walker is active. Callers on raw runtime paths can consume the
// returned values with ordinary static Go calls.
func StackFrames(skip int) []Frame {
	const (
		initialCapacity = 64
		maximumCapacity = 16 * 1024
	)
	capacity := initialCapacity
	var snapshot []stacktraceFrame
	for {
		snapshot = make([]stacktraceFrame, capacity)
		count := int(stacktrace(c.Int(1+skip), &snapshot[0], c.Int(capacity)))
		snapshot = snapshot[:count]
		if count < capacity || capacity == maximumCapacity {
			break
		}
		capacity *= 2
	}
	frames := make([]Frame, len(snapshot))
	for i := range snapshot {
		raw := &snapshot[i]
		frames[i] = Frame{uintptr(raw.pc), raw.offset, raw.sp, c.GoString(raw.name)}
	}
	return frames
}

func StackTrace(skip int, fn func(fr *Frame) bool) {
	// Account for this compatibility wrapper when asking StackFrames to skip
	// its caller. The callback runs only after the native snapshot is complete.
	frames := StackFrames(skip + 1)
	for i := range frames {
		if !fn(&frames[i]) {
			return
		}
	}
}

func PrintStack(skip int) {
	// Failure diagnostics are reachable through the raw runtime ABI. Keep the
	// entire walk/print callback in C so that path never constructs a managed Go
	// function descriptor while the program is already unwinding.
	printStack(c.Int(skip + 2))
}
