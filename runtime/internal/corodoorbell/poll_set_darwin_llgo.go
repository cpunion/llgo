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

//llgo:coro contract foreign.v1 progress=executor-safe affinity=any-thread reentry=none memory=by-value
//go:linkname nativeCPollCloseEpochBump C.__llgo_coro_doorbell_poll_close_epoch_bump_v1
func nativeCPollCloseEpochBump()

//llgo:coro contract foreign.v1 progress=executor-safe affinity=any-thread reentry=none memory=by-value
//go:linkname nativeCOwnsPollFD C.__llgo_coro_doorbell_owns_poll_fd_v1
func nativeCOwnsPollFD(fd int32) int32

func nativePollSet(first *PollFD, count uint32, timeoutMS int32) (int, int32) {
	return unpackNativeDoorbellResult(nativeCPollSet(first, c.Uint(count), c.Int(timeoutMS)))
}

// NotifyDescriptorClosed bumps the Darwin kqueue descriptor-close epoch so
// closed-and-reused descriptor numbers cannot alias stale cached knotes.
func NotifyDescriptorClosed() {
	nativeCPollCloseEpochBump()
}

// OwnsPollDescriptor reports whether fd is one of the persistent kqueue
// descriptors managed by corodoorbell.
func OwnsPollDescriptor(fd uintptr) bool {
	if fd > uintptr(^uint32(0)>>1) {
		return false
	}
	return nativeCOwnsPollFD(int32(fd)) != 0
}
