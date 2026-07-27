//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && darwin && !baremetal && !coro_runtime_adapter_test

package runtime

import (
	"unsafe"

	cliteos "github.com/goplus/llgo/runtime/internal/clite/os"
)

//go:linkname libc_fstat64_trampoline C.fstat64
func libc_fstat64_trampoline()

func coroPollFstat(fd int32, info *cliteos.StatT) (result, errno uintptr) {
	result, _, errno = runtimeDarwinSyscall3Int32(
		runtimeDarwinFuncPCABI0(libc_fstat64_trampoline),
		uintptr(uint32(fd)),
		uintptr(unsafe.Pointer(info)),
		0,
	)
	return
}
