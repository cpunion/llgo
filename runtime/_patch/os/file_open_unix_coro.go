//go:build (darwin || linux) && go1.26 && !tinygo.wasm && !baremetal

package os

import (
	"internal/poll"
	"syscall"
	"unsafe"
)

type llgoRawOpenResult = struct {
	R0 int
	R1 poll.SysFile
	R2 error
}

//go:linkname llgoBorrowRawOpen llgo.coroBorrowResult
func llgoBorrowRawOpen(func(*llgoRawOpenResult), func(*llgoRawOpenResult, unsafe.Pointer, string, int, uint32)) *llgoRawOpenResult
func llgoInitializeRawOpen(out *llgoRawOpenResult) { *out = llgoRawOpenResult{R0: -1} }
func llgoDropRawOpen(out *llgoRawOpenResult, _ unsafe.Pointer, _ string, _ int, _ uint32) {
	fd := out.R0
	out.R0 = -1
	if fd >= 0 && out.R2 == nil {
		_ = syscall.Close(fd)
	}
}
func open(path string, flag int, perm uint32) (int, poll.SysFile, error) {
	out := llgoBorrowRawOpen(llgoInitializeRawOpen, llgoDropRawOpen)
	out.R0, out.R2 = syscall.Open(path, flag, perm)
	return out.R0, out.R1, out.R2
}
