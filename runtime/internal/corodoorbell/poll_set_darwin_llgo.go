//go:build llgo && darwin && !baremetal

package corodoorbell

import (
	_ "unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

// Darwin's nfds_t is unsigned int, unlike Linux's target-word-sized type.
//
// nativeCPollSet is the executor-owner-only, capacity- and timeout-bounded
// physical readiness wait. The compiler admits it only for the scheduler-owner
// invocation in the compiler-owned raw host-stack island and does not claim
// that the physical poll is noblock. Generic poll(2) remains conservative.
//
//go:linkname nativeCPollSet C.__llgo_coro_doorbell_poll_set_v1
func nativeCPollSet(first *PollFD, count c.Uint, timeout c.Int) uint64

func nativePollSet(first *PollFD, count uint32, timeoutMS int32) (int, int32) {
	return unpackNativeDoorbellResult(nativeCPollSet(first, c.Uint(count), c.Int(timeoutMS)))
}
