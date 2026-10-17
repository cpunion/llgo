//go:build (darwin || linux) && !tinygo.wasm && !baremetal

package syscall

import "unsafe"

type llgoOpenResult = struct {
	R0 int
	R1 error
}

//go:linkname llgoBorrowOpenResult llgo.coroBorrowResult
func llgoBorrowOpenResult(func(*llgoOpenResult), func(*llgoOpenResult, unsafe.Pointer, string, int, uint32)) *llgoOpenResult

// The worker writes the descriptor into the protected result cell before it
// publishes completion. Cancellation need not run a Go continuation to learn
// which descriptor was acquired. Only ordinary borrowed pointers cross C.
//
//go:linkname llgoOpenInto C.__llgo_runtime_posix_open_into_v1
func llgoOpenInto(*int, *byte, int32, uint32) uintptr

func llgoInitializeOpenResult(out *llgoOpenResult) { *out = llgoOpenResult{R0: -1} }

func llgoDropOpenResult(out *llgoOpenResult, _ unsafe.Pointer, _ string, _ int, _ uint32) {
	fd := out.R0
	out.R0 = -1 // A failed or interrupted close must never be retried.
	if fd >= 0 && out.R1 == nil {
		_ = Close(fd)
	}
}

func Open(path string, mode int, perm uint32) (fd int, err error) {
	out := llgoBorrowOpenResult(llgoInitializeOpenResult, llgoDropOpenResult)
	pointer, pathErr := BytePtrFromString(path)
	if pathErr != nil {
		out.R1 = pathErr
		out.R0 = 0 // Preserve the generated syscall wrapper's validation result.
		return out.R0, out.R1
	}
	if errno := llgoOpenInto(&out.R0, pointer, int32(mode), perm); errno != 0 {
		out.R1 = errnoErr(Errno(errno))
	}
	return out.R0, out.R1
}
