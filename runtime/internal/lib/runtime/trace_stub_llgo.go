package runtime

import (
	"unsafe"
)

// LLGo's runtimeNano is normalized to nanoseconds on every hosted target,
// including Windows where QueryPerformanceCounter is converted using
// QueryPerformanceFrequency. A fixed divisor therefore gives trace timestamps
// the same 64 ns resolution as Go's high-resolution nanotime path.
const traceTimeDiv = 64

//go:linkname traceAdvance runtime.traceAdvance
func traceAdvance(stopTrace bool) {}

//go:linkname traceClockNow runtime.traceClockNow
func traceClockNow() uint64 { return uint64(runtimeNano()) / traceTimeDiv }

//go:linkname runtime_readTrace runtime/trace.runtime_readTrace
func runtime_readTrace() []byte { return ReadTrace() }

//go:linkname trace_userTaskCreate runtime/trace.userTaskCreate
func trace_userTaskCreate(id, parentID uint64, taskType string) {}

//go:linkname trace_userTaskEnd runtime/trace.userTaskEnd
func trace_userTaskEnd(id uint64) {}

//go:linkname trace_userRegion runtime/trace.userRegion
func trace_userRegion(id, mode uint64, regionType string) {}

//go:linkname trace_userLog runtime/trace.userLog
func trace_userLog(id uint64, category, message string) {}

func SetCgoTraceback(version int, traceback, context, symbolizer unsafe.Pointer) {
}
