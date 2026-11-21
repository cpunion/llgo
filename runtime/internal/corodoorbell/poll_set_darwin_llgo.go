//go:build llgo && darwin && !baremetal

package corodoorbell

import (
	_ "unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
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

// nativePollQueue mirrors the three int32_t fields owned by the C wait leaf.
// A zero descriptor selects lazy creation and a negative descriptor disables
// it. Positive descriptors are encoded with an offset of one.
type nativePollQueue struct {
	descriptor int32
	watchedFD  int32
	filter     int32
}

//go:linkname nativeCPollQueueWait C.__llgo_coro_doorbell_kevent_pair_v1
func nativeCPollQueueWait(queue *nativePollQueue, first *PollFD, count uint32, timeoutMS int32) uint64

func (queue *nativePollQueue) wait(first *PollFD, count uint32, timeoutMS int32) (int, int32) {
	return unpackNativeDoorbellResult(nativeCPollQueueWait(queue, first, count, timeoutMS))
}

func (queue *nativePollQueue) close() bool {
	descriptor := queue.descriptor
	*queue = nativePollQueue{}
	return descriptor <= 0 || nativePipeClose(descriptor-1)
}
