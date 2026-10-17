//go:build (darwin || linux) && go1.26 && !tinygo.wasm && !baremetal

package os

import (
	"internal/testlog"
	"unsafe"
)

type llgoOpenFileResult = struct {
	R0 *File
	R1 error
}

//go:linkname llgoBorrowOpenFile llgo.coroBorrowResult
func llgoBorrowOpenFile(func(*llgoOpenFileResult), func(*llgoOpenFileResult, unsafe.Pointer, string, int, FileMode)) *llgoOpenFileResult
func llgoInitializeOpenFile(out *llgoOpenFileResult) { *out = llgoOpenFileResult{} }
func llgoDropOpenFile(out *llgoOpenFileResult, _ unsafe.Pointer, _ string, _ int, _ FileMode) {
	llgoDropFile(out)
}
func llgoDropFile(out *llgoOpenFileResult) {
	f := out.R0
	out.R0 = nil
	if f != nil && out.R1 == nil && f.pfd.Sysfd >= 0 {
		_ = f.Close()
	}
}
func OpenFile(name string, flag int, perm FileMode) (*File, error) {
	out := llgoBorrowOpenFile(llgoInitializeOpenFile, llgoDropOpenFile)
	testlog.Open(name)
	out.R0, out.R1 = openFileNolog(name, flag, perm)
	if out.R1 == nil {
		out.R0.appendMode = flag&O_APPEND != 0
	}
	return out.R0, out.R1
}
