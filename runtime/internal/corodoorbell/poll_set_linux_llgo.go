//go:build llgo && linux && !baremetal

package corodoorbell

import (
	_ "unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

// nativeCPollSet is the executor-owner-only, capacity- and timeout-bounded
// physical readiness wait. The compiler admits it only for the scheduler-owner
// invocation in the compiler-owned raw host-stack island and does not claim
// that the physical poll is noblock. Generic poll(2) remains conservative.
//
//go:linkname nativeCPollSet C.__llgo_coro_doorbell_poll_set_v1
func nativeCPollSet(first *PollFD, count uintptr, timeout c.Int) uint64

//go:linkname nativeCPollSetNano C.__llgo_coro_doorbell_poll_set_ns_v1
func nativeCPollSetNano(first *PollFD, count uintptr, timeoutNS int64) uint64

func nativePollSet(first *PollFD, count uint32, timeoutMS int32) (int, int32) {
	return unpackNativeDoorbellResult(nativeCPollSet(first, uintptr(count), c.Int(timeoutMS)))
}

func nativePollSetNano(first *PollFD, count uint32, timeoutNS int64) (int, int32) {
	return unpackNativeDoorbellResult(nativeCPollSetNano(first, uintptr(count), timeoutNS))
}
