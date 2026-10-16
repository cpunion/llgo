//go:build llgo && llgo_coro && !coro_runtime_adapter_test

package runtime

import (
	"github.com/xgo-dev/llgo/runtime/internal/coro"
	_ "unsafe"
)

var coroGCObservationV1State coro.GCObservation

// EnableGCObserver is called before publishing the first channel timer. Never
// hold the timer-list lock here: native configuration takes the collector lock.
// The collector retains only a process-lifetime epoch word, never a Go closure.
func EnableGCObserver() {
	if coroGCObservationV1State.Enabled() {
		return
	}
	coroConfigureGCObserverV1(coroGCObservationV1State.EpochWord())
	coroGCObservationV1State.Enable()
}

// This is a Go-to-Go owner call, not a foreign callback. Keeping its exact Go
// identity avoids an exported C entry and redundant native-context setup.
//
//go:linkname coroGCObserveV1 runtime.coroTimerGCObserveV1
func coroGCObserveV1() bool

// coroObserveGCV1 runs only on a clean scheduler/host entry, never in a GC hook
// or during physical coroutine resume. A busy observer retains the epoch and
// requests a later owner turn, including when the host is about to sleep.
func coroObserveGCV1() bool {
	if !coro.GCObservationEnabled() {
		return false
	}
	epoch, seen, pending := coroGCObservationV1State.Pending()
	if !pending {
		return false
	}
	if coroGCObserveV1() {
		coroGCObservationV1State.Acknowledge(epoch, seen)
	}
	if _, _, retry := coroGCObservationV1State.Pending(); retry {
		// This is a program-global observation, not a timer operation. Request
		// the exact adopted executor; its next clean entry observes the retained
		// epoch even if the target could not accept this advisory wake.
		_ = coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State)
		return true
	}
	return false
}

// Tinygc's Go-only tail publishes after weak clearing and sweep. It has no
// lock, allocation, observer call, or scheduler operation, even with gcMutex
// held. The collector uses an exact Go alias, not a synthetic C callback entry.
func coroGCCompletedV1() {
	if coro.GCObservationEnabled() {
		coroGCObservationV1State.Complete()
	}
}
