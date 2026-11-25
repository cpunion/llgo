//go:build llgo && darwin && !baremetal

package corodoorbell

import (
	"sync/atomic"
	_ "unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

const darwinPollOwnedSlotCount = 8

var (
	darwinPollStateBound uint32
	darwinPollCloseEpoch uint32
	darwinOwnedPollFDs   [darwinPollOwnedSlotCount]uint32
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

//go:linkname nativeCPollSetNano C.__llgo_coro_doorbell_poll_set_ns_v1
func nativeCPollSetNano(first *PollFD, count c.Uint, timeoutNS int64) uint64

//go:linkname nativeCPollBindState C.__llgo_coro_doorbell_poll_bind_state_v1
func nativeCPollBindState(closeEpoch *uint32, ownedFDs *uint32)

func ensureDarwinPollStateBound() {
	if atomic.LoadUint32(&darwinPollStateBound) == 0 {
		nativeCPollBindState(&darwinPollCloseEpoch, &darwinOwnedPollFDs[0])
		atomic.StoreUint32(&darwinPollStateBound, 1)
	}
}

func nativePollSet(first *PollFD, count uint32, timeoutMS int32) (int, int32) {
	ensureDarwinPollStateBound()
	return unpackNativeDoorbellResult(nativeCPollSet(first, c.Uint(count), c.Int(timeoutMS)))
}

func nativePollSetNano(first *PollFD, count uint32, timeoutNS int64) (int, int32) {
	ensureDarwinPollStateBound()
	return unpackNativeDoorbellResult(nativeCPollSetNano(first, c.Uint(count), timeoutNS))
}

// NotifyDescriptorClosed bumps the Darwin kqueue descriptor-close epoch so
// closed-and-reused descriptor numbers cannot alias stale cached knotes.
func NotifyDescriptorClosed() {
	atomic.AddUint32(&darwinPollCloseEpoch, 1)
}

// OwnsPollDescriptor reports whether fd is one of the persistent kqueue
// descriptors managed by corodoorbell.
func OwnsPollDescriptor(fd uintptr) bool {
	if fd > uintptr(^uint32(0)-1) {
		return false
	}
	target := uint32(fd) + 1
	for i := range darwinOwnedPollFDs {
		if atomic.LoadUint32(&darwinOwnedPollFDs[i]) == target {
			return true
		}
	}
	return false
}
