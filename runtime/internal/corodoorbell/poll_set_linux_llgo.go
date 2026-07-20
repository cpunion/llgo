//go:build llgo && linux && !baremetal

package corodoorbell

import (
	_ "unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

// nativeCPollSet is the executor-owner-only, capacity- and timeout-bounded
// physical readiness wait. schedulerwait admits it only in the scheduler-owner
// case of the compiler-owned raw host-stack island and does not claim that the
// physical poll is noblock. Generic poll(2) remains uncertified.
//
//llgo:coro schedulerwait
//go:linkname nativeCPollSet C.__llgo_coro_doorbell_poll_set_v1
func nativeCPollSet(first *PollFD, count uintptr, timeout c.Int) uint64

func nativePollSet(first *PollFD, count uint32, timeoutMS int32) (int, int32) {
	return unpackNativeDoorbellResult(nativeCPollSet(first, uintptr(count), c.Int(timeoutMS)))
}
