//go:build go1.23 && llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux)) || wasm || tinygo.wasm || baremetal || llgo_coro_host)

package runtime

import latomic "sync/atomic"

var coroTimerGC struct {
	lock uint32
	head *coroTimeTimerState
}

// Tracking is O(1), and inactive entries are pruned once per collection rather
// than searching a global list on every Stop or expiry. The public Timer and
// channel are not reachable through this list. A Reset holds state.lock while
// publishing active control and ensuring membership; the observer never takes
// state.lock, so an owner cannot wait for a preempted managed timer caller.
func coroTimerTrack(state *coroTimeTimerState) {
	if !state.channel {
		return
	}
	coroTimerLock(&coroTimerGC.lock)
	if !state.gcListed {
		state.gcNext = coroTimerGC.head
		state.gcListed = true
		coroTimerGC.head = state
	}
	coroTimerUnlock(&coroTimerGC.lock)
}

// The collector only publishes an epoch and rings the existing owner ingress.
// This observer runs at an executor safe point, outside GC locks. false leaves
// that epoch pending, including when a managed caller holds the list lock or
// an owner notification cannot yet be delivered. It must not allocate, block,
// recover a strong channel, acquire a timer lock, or call user code.
//
//llgo:nopreempt
//llgo:nounwind
func coroTimerGCObserveV1() bool {
	if !latomic.CompareAndSwapUint32(&coroTimerGC.lock, 0, 1) {
		return false
	}
	link := &coroTimerGC.head
	for *link != nil {
		state := *link
		control := latomic.LoadUint32(&state.control)
		dead := latomic.LoadUintptr(&state.weak.Live) == 0
		if dead {
			// A dead channel has no legal future Reset caller. Changing only
			// phase invalidates the exact retained control word without a
			// generation increment (or an overflow panic in this raw hook).
			inactive := control&^coroTimerControlPhaseMask | coroTimerInactive
			if !latomic.CompareAndSwapUint32(&state.control, control, inactive) {
				latomic.StoreUint32(&coroTimerGC.lock, 0)
				return false
			}
			if route := latomic.LoadUint32(&state.ownerRoute); route != 0 &&
				llgoCoroTimerRequestControlledV2(route) == 0 {
				latomic.StoreUint32(&coroTimerGC.lock, 0)
				return false
			}
		}
		if dead || coroTimerControlPhase(control) != coroTimerActive {
			*link, state.gcNext, state.gcListed = state.gcNext, nil, false
			continue
		}
		link = &state.gcNext
	}
	latomic.StoreUint32(&coroTimerGC.lock, 0)
	return true
}
