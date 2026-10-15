//go:build coro_gc_observer_test

package runtime

import (
	"testing"
	_ "unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

var gcTestConfiguration, gcTestObservation, gcTestRequest int
var gcTestBusy, gcTestConcurrentCollection bool
var gcTestRejectRequest bool
var coroProgramExecutorHandleV1State = coro.ExecutorHandle{Slot: 1, Generation: 2}

func coroConfigureGCObserverV1(*uint32) { gcTestConfiguration++ }
func coroTargetRequestExecutorV1(handle coro.ExecutorHandle) bool {
	if handle != coroProgramExecutorHandleV1State {
		panic("unexpected coordinator executor")
	}
	gcTestRequest++
	return !gcTestRejectRequest
}

//go:linkname gcTestRawObserver runtime.coroTimerGCObserveV1
func gcTestRawObserver() bool {
	gcTestObservation++
	if gcTestConcurrentCollection {
		gcTestConcurrentCollection = false
		coroGCCompletedV1()
	}
	return !gcTestBusy
}

func TestCoroGCObserverBusyRetainsWakeAndNeverSleeps(t *testing.T) {
	coroGCObservationV1State = coro.GCObservation{}
	gcTestConfiguration, gcTestObservation, gcTestRequest = 0, 0, 0
	gcTestBusy, gcTestConcurrentCollection = true, false
	gcTestRejectRequest = true
	if coroObserveGCV1() || gcTestObservation != 0 {
		t.Fatal("disabled observer performed work")
	}
	EnableGCObserver()
	EnableGCObserver()
	if gcTestConfiguration != 1 {
		t.Fatal("already enabled observer reconfigured the collector")
	}
	coroGCCompletedV1()
	for turn := 1; turn <= 3; turn++ {
		// The returned pending bit is the actual sleep/standby gate; merely
		// requesting a running executor is insufficient because its source scan
		// may consume that advisory request before the observer lock is free.
		if !coroObserveGCV1() || gcTestObservation != turn || gcTestRequest != turn {
			t.Fatal("busy observation spun inline, slept, or lost the retained wake")
		}
	}
	gcTestBusy = false
	gcTestRejectRequest = false
	if coroObserveGCV1() || gcTestObservation != 4 || gcTestRequest != 3 {
		t.Fatal("available observer did not settle the epoch in one bounded turn")
	}
	if coroObserveGCV1() || gcTestObservation != 4 {
		t.Fatal("acknowledged epoch rescanned without a new collection")
	}
	coroGCCompletedV1()
	gcTestConcurrentCollection = true
	if !coroObserveGCV1() || coroObserveGCV1() {
		t.Fatal("collection during observation did not survive for exactly one later turn")
	}
}
