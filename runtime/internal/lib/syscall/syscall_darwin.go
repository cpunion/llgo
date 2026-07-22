//go:build darwin && go1.26

package syscall

import (
	stdsyscall "syscall"

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/os"
)

func Syscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	return RawSyscall(trap, a1, a2, a3)
}

func Syscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	return RawSyscall6(trap, a1, a2, a3, a4, a5, a6)
}

func Syscall9(trap, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	ret := c_syscall(c.Long(trap), a1, a2, a3, a4, a5, a6, a7, a8, a9)
	if ret <= -1 {
		return ^uintptr(0), 0, stdsyscall.Errno(os.Errno())
	}
	return uintptr(ret), 0, 0
}

// RawSyscall accepts an arbitrary runtime trap word. A fixed dispatcher
// address therefore cannot certify worker safety: the operation may be fork,
// exec, exit, thread-affine, no-return, or an unknown host extension. Keep the
// public trap API on its original direct/plain path until the compiler owns an
// exact constant-trap capability proof. Generated libc wrappers use their
// separately certified FuncPCABI0 target catalog instead.
func RawSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	ret := c_syscall(c.Long(trap), a1, a2, a3)
	if ret <= -1 {
		return ^uintptr(0), 0, stdsyscall.Errno(os.Errno())
	}
	return uintptr(ret), 0, 0
}

func RawSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	ret := c_syscall(c.Long(trap), a1, a2, a3, a4, a5, a6)
	if ret <= -1 {
		return ^uintptr(0), 0, stdsyscall.Errno(os.Errno())
	}
	return uintptr(ret), 0, 0
}
