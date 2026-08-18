//go:build go1.25
// +build go1.25

package runtime

import (
	_ "unsafe"
)

// traceClockUnitsPerSecond estimates the number of trace clock units per
// second that elapse.
//
//go:linkname traceClockUnitsPerSecond runtime/trace.runtime_traceClockUnitsPerSecond
func traceClockUnitsPerSecond() uint64 {
	// Unlike the Go runtime's Windows cputicks path, LLGo's trace clock is
	// already normalized to nanoseconds and needs no dynamic calibration.
	// (trace clock units / nanoseconds) * (1e9 nanoseconds / 1 second)
	return uint64(1.0 / float64(traceTimeDiv) * 1e9)
}
