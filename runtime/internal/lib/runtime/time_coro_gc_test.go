//go:build coro_timer_gc_test

package runtime

import (
	"sync/atomic"
	"testing"
)

// Named-source host island: the production observer is compiled unchanged.
// The collector and target request are deterministic test boundaries.
type weakHandle struct{ Live uintptr }
type coroTimeTimerState struct {
	control, ownerRoute uint32
	weak                *weakHandle
	gcNext              *coroTimeTimerState
	channel, gcListed   bool
}

const (
	coroTimerControlPhaseMask        = 3
	coroTimerInactive         uint32 = 1
	coroTimerActive           uint32 = 2
)

func coroTimerControlPhase(v uint32) uint32 { return v & coroTimerControlPhaseMask }
func coroTimerLock(p *uint32) {
	if !atomic.CompareAndSwapUint32(p, 0, 1) {
		panic("test unexpectedly entered managed wait")
	}
}
func coroTimerUnlock(p *uint32) { atomic.StoreUint32(p, 0) }

var gcTestRequestOK uint32
var gcTestRequestCount uint32

func llgoCoroTimerRequestControlledV2(route uint32) uint32 {
	atomic.AddUint32(&gcTestRequestCount, 1)
	return atomic.LoadUint32(&gcTestRequestOK)
}

func resetTimerGC(t *testing.T) {
	t.Helper()
	coroTimerGC.head = nil
	atomic.StoreUint32(&coroTimerGC.lock, 0)
	atomic.StoreUint32(&gcTestRequestOK, 1)
	atomic.StoreUint32(&gcTestRequestCount, 0)
	t.Cleanup(func() { coroTimerGC.head = nil })
}

func gcTestTimer(live uintptr, phase uint32) *coroTimeTimerState {
	return &coroTimeTimerState{control: 4 | phase, ownerRoute: 1, weak: &weakHandle{Live: live}, channel: true}
}

func TestTimerGCObserverPrunesDeadAndInactiveWithoutStrongUpgrade(t *testing.T) {
	resetTimerGC(t)
	live, dead, stopped := gcTestTimer(1, coroTimerActive), gcTestTimer(0, coroTimerActive), gcTestTimer(1, coroTimerInactive)
	coroTimerTrack(live)
	coroTimerTrack(dead)
	coroTimerTrack(stopped)
	if !coroTimerGCObserveV1() {
		t.Fatal("complete scan did not acknowledge epoch")
	}
	if coroTimerGC.head != live || live.gcNext != nil || !live.gcListed || dead.gcListed || stopped.gcListed {
		t.Fatal("incorrect surviving membership")
	}
	if atomic.LoadUint32(&dead.control) != 4|coroTimerInactive || gcTestRequestCount != 1 {
		t.Fatal("dead future timer did not invalidate and wake its exact owner")
	}
	atomic.StoreUint32(&stopped.control, 8|coroTimerActive)
	coroTimerTrack(stopped)
	coroTimerTrack(stopped)
	if coroTimerGC.head != stopped || stopped.gcNext != live {
		t.Fatal("Reset did not re-enroll once after collection")
	}
}

func TestTimerGCObserverBusyDoesNotSpinOrAcknowledge(t *testing.T) {
	resetTimerGC(t)
	dead := gcTestTimer(0, coroTimerActive)
	coroTimerTrack(dead)
	atomic.StoreUint32(&coroTimerGC.lock, 1)
	if coroTimerGCObserveV1() || gcTestRequestCount != 0 || dead.control != 4|coroTimerActive {
		t.Fatal("busy scan consumed epoch or touched protected state")
	}
	atomic.StoreUint32(&coroTimerGC.lock, 0)
	if !coroTimerGCObserveV1() || coroTimerGC.head != nil {
		t.Fatal("retained epoch could not complete on retry")
	}
}

func TestTimerGCObserverRetainsFailedOwnerRequest(t *testing.T) {
	resetTimerGC(t)
	dead := gcTestTimer(0, coroTimerActive)
	coroTimerTrack(dead)
	atomic.StoreUint32(&gcTestRequestOK, 0)
	if coroTimerGCObserveV1() || coroTimerGC.head != dead || !dead.gcListed {
		t.Fatal("failed request lost epoch or membership")
	}
	atomic.StoreUint32(&gcTestRequestOK, 1)
	if !coroTimerGCObserveV1() || coroTimerGC.head != nil || gcTestRequestCount != 2 {
		t.Fatal("inactive dead state did not retry its owner request")
	}
}

func TestTimerGCObserverDeadGenerationCannotOverflow(t *testing.T) {
	resetTimerGC(t)
	dead := gcTestTimer(0, coroTimerActive)
	dead.control = ^uint32(0)&^coroTimerControlPhaseMask | coroTimerActive
	coroTimerTrack(dead)
	if !coroTimerGCObserveV1() || coroTimerControlPhase(dead.control) != coroTimerInactive {
		t.Fatal("dead maximum generation was not safely invalidated")
	}
}

func TestTimerGCObserverCancelsBeforeOwnerPublication(t *testing.T) {
	resetTimerGC(t)
	dead := gcTestTimer(0, coroTimerActive)
	dead.ownerRoute = 0
	coroTimerTrack(dead)
	if !coroTimerGCObserveV1() || coroTimerGC.head != nil || dead.gcListed {
		t.Fatal("unpublished dead timer retained GC membership")
	}
	if dead.control != 4|coroTimerInactive || gcTestRequestCount != 0 {
		t.Fatal("pre-publication cancellation must change control without inventing an owner")
	}
}

func TestTimerGCObserverDoesNotTrackAfterFunc(t *testing.T) {
	resetTimerGC(t)
	callback := &coroTimeTimerState{control: 4 | coroTimerActive}
	coroTimerTrack(callback)
	if !coroTimerGCObserveV1() || coroTimerGC.head != nil || callback.gcListed ||
		callback.control != 4|coroTimerActive || gcTestRequestCount != 0 {
		t.Fatal("AfterFunc callback acquired channel weak ownership")
	}
}
