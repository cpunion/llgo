//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && linux && !baremetal && !coro_runtime_adapter_test

package runtime

import (
	"unsafe"

	cliteos "github.com/goplus/llgo/runtime/internal/clite/os"
)

//llgo:coro workeraddr 3
//go:linkname libc_fstat_trampoline C.fstat
func libc_fstat_trampoline()

//go:linkname coroPollFstatFuncPCABI0 llgo.funcPCABI0
func coroPollFstatFuncPCABI0(fn any) uintptr

//go:linkname coroPollFstatSyscall3 llgo.syscall32
func coroPollFstatSyscall3(fn, a1, a2, a3 uintptr) (r1, r2, errno uintptr)

func coroPollFstat(fd int32, info *cliteos.StatT) (result, errno uintptr) {
	result, _, errno = coroPollFstatSyscall3(
		coroPollFstatFuncPCABI0(libc_fstat_trampoline),
		uintptr(uint32(fd)),
		uintptr(unsafe.Pointer(info)),
		0,
	)
	return
}
