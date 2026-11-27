//go:build go1.23 && llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux)) || wasm || tinygo.wasm || baremetal || llgo_coro_host)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package runtime

import (
	latomic "sync/atomic"
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/abi"
	corort "github.com/xgo-dev/llgo/runtime/internal/runtime"
)

const (
	coroTimerControlPhaseBits = 2
	coroTimerControlPhaseMask = 1<<coroTimerControlPhaseBits - 1
	coroTimerControlMaxGen    = ^uint32(0) >> coroTimerControlPhaseBits

	coroTimerInactive uint32 = 1
	coroTimerActive   uint32 = 2

	coroTimerOutcomeCompleted uint32 = 1
	coroTimerOutcomeCanceled  uint32 = 2

	coroTimerHubCount     = 4
	coroTimerHubMask      = coroTimerHubCount - 1
	coroTimerHubInlineCap = 16
)

// timeTimer matches the public prefix of time.Timer and time.Ticker. Everything
// after init is private runtime state. Channel timers detach state so the
// collector can reclaim unreferenced Timer/Ticker objects and their channels
// before deadline; AfterFunc timers keep their callback live until expiry or
// Stop, so they co-allocate timeTimer and coroTimeTimerState in one object.
type timeTimer struct {
	c    unsafe.Pointer
	init bool

	state *coroTimeTimerState
}

type coroAfterFuncTimer struct {
	timer timeTimer
	state coroTimeTimerState
}

// coroTimerHub multiplexes all active standard timers assigned to one shard
// onto a single binary min-heap and at most one managed stackless G
// (coroTimerManager). NewTimer, Stop, and Reset manipulate the min-heap in
// O(log N) on a zero-frame plain fast path without spawning per-timer
// goroutines or churning the executor's TimerRegistrationTable unless the
// earliest deadline moves earlier or the shard transitions between empty and
// non-empty.
//
// Global lock ordering: state.sendLock (when state.channel is true) MUST
// always be acquired before hub.lock. coroTimerGCObserveV1 never takes either
// lock and communicates solely via atomics (state.control, state.gcListed,
// hub.gcDirty, hub.control, and hub.ownerRoute).
type coroTimerHub struct {
	lock           uint32
	control        uint32
	ownerRoute     uint32
	manager        uint32
	gcDirty        uint32
	parkedDeadline int64
	heap           []*coroTimeTimerState
	inlineHeap     [coroTimerHubInlineCap]*coroTimeTimerState
	_              [64]byte
}

var coroTimerHubs [coroTimerHubCount]coroTimerHub

type coroTimeTimerState struct {
	sendLock      uint32
	control       uint32
	heapIndex     int32
	gcListed      uint32
	maybeBuffered uint32

	hub       *coroTimerHub
	when      int64
	period    int64
	f         func(any, uintptr, int64)
	arg       any
	cb        func()
	weak      *weakHandle
	gcNext    *coroTimeTimerState
	channel   bool
	afterFunc bool
	synctest  bool
}

//go:linkname llgoCoroControlledTimerWaitV2 llgo.coroControlledTimerWait
func llgoCoroControlledTimerWaitV2(
	controller unsafe.Pointer,
	control, ownerRoute *uint32,
	expected uint32,
	deadline int64,
) uint32

//go:linkname llgoCoroTimerRequestControlledV2 C.__llgo_coro_timer_request_controlled_v2
func llgoCoroTimerRequestControlledV2(route uint32) uint32

func coroTimerControl(generation, phase uint32) uint32 {
	return generation<<coroTimerControlPhaseBits | phase
}

func coroTimerControlGeneration(control uint32) uint32 {
	return control >> coroTimerControlPhaseBits
}

func coroTimerControlPhase(control uint32) uint32 {
	return control & coroTimerControlPhaseMask
}

func coroTimerNextControl(control, phase uint32) uint32 {
	generation := coroTimerControlGeneration(control) + 1
	if generation == 0 || generation > coroTimerControlMaxGen {
		panic("time: timer generation exhausted")
	}
	return coroTimerControl(generation, phase)
}

//llgo:nopreempt
//llgo:nounwind
func coroTimerHubBumpControl(hub *coroTimerHub) {
	for {
		old := latomic.LoadUint32(&hub.control)
		next := old + (1 << coroTimerControlPhaseBits)
		if next>>coroTimerControlPhaseBits == 0 {
			next = (1 << coroTimerControlPhaseBits) | coroTimerActive
		} else {
			next = (next &^ coroTimerControlPhaseMask) | coroTimerActive
		}
		if latomic.CompareAndSwapUint32(&hub.control, old, next) {
			return
		}
	}
}

func coroTimerHubForCurrentP() *coroTimerHub {
	return &coroTimerHubs[corort.CoroCurrentTaskRoute()&coroTimerHubMask]
}

func coroTimerLock(word *uint32) {
	for !latomic.CompareAndSwapUint32(word, 0, 1) {
		coroSchedulerYield()
	}
}

func coroTimerUnlock(word *uint32) {
	latomic.StoreUint32(word, 0)
}

//llgo:nopreempt
func (hub *coroTimerHub) siftUpLocked(i int) {
	h := hub.heap
	item := h[i]
	when := item.when
	for i > 0 {
		parent := (i - 1) >> 1
		if h[parent].when <= when {
			break
		}
		h[i] = h[parent]
		h[i].heapIndex = int32(i)
		i = parent
	}
	h[i] = item
	item.heapIndex = int32(i)
}

//llgo:nopreempt
func (hub *coroTimerHub) siftDownLocked(i int) {
	h := hub.heap
	n := len(h)
	item := h[i]
	when := item.when
	for {
		child := i*2 + 1
		if child >= n {
			break
		}
		if right := child + 1; right < n && h[right].when < h[child].when {
			child = right
		}
		if h[child].when >= when {
			break
		}
		h[i] = h[child]
		h[i].heapIndex = int32(i)
		i = child
	}
	h[i] = item
	item.heapIndex = int32(i)
}

//llgo:nopreempt
func (hub *coroTimerHub) pushInPlaceLocked(state *coroTimeTimerState) bool {
	if hub.heap == nil {
		hub.heap = hub.inlineHeap[:0]
	}
	i := len(hub.heap)
	if i >= cap(hub.heap) {
		return false
	}
	hub.heap = hub.heap[:i+1]
	hub.heap[i] = state
	state.heapIndex = int32(i)
	hub.siftUpLocked(i)
	return true
}

//llgo:nopreempt
func (hub *coroTimerHub) pushLocked(state *coroTimeTimerState) {
	if hub.pushInPlaceLocked(state) {
		return
	}
	i := len(hub.heap)
	hub.heap = append(hub.heap, state)
	state.heapIndex = int32(i)
	hub.siftUpLocked(i)
}

//llgo:nopreempt
func (hub *coroTimerHub) popLocked() *coroTimeTimerState {
	h := hub.heap
	last := len(h) - 1
	top := h[0]
	top.heapIndex = -1
	if last == 0 {
		h[0] = nil
		hub.heap = h[:0]
		return top
	}
	h[0] = h[last]
	h[0].heapIndex = 0
	h[last] = nil
	hub.heap = h[:last]
	hub.siftDownLocked(0)
	return top
}

//llgo:nopreempt
func (hub *coroTimerHub) removeLocked(state *coroTimeTimerState) {
	i := int(state.heapIndex)
	if i < 0 {
		return
	}
	h := hub.heap
	last := len(h) - 1
	state.heapIndex = -1
	if i == last {
		h[last] = nil
		hub.heap = h[:last]
		return
	}
	moved := h[last]
	h[i] = moved
	moved.heapIndex = int32(i)
	h[last] = nil
	hub.heap = h[:last]
	if i > 0 && moved.when < h[(i-1)>>1].when {
		hub.siftUpLocked(i)
	} else {
		hub.siftDownLocked(i)
	}
}

//llgo:nopreempt
func (hub *coroTimerHub) fixLocked(state *coroTimeTimerState) {
	i := int(state.heapIndex)
	if i < 0 {
		return
	}
	if i > 0 && state.when < hub.heap[(i-1)>>1].when {
		hub.siftUpLocked(i)
	} else {
		hub.siftDownLocked(i)
	}
}

// sweepDeadLocked removes channel timers invalidated by coroTimerGCObserveV1
// and collects up to len(pruneKeys) weak keys to prune outside hub.lock.
//
//llgo:nopreempt
func (hub *coroTimerHub) sweepDeadLocked(pruneKeys []uintptr) (count int, more bool) {
	for i := 0; i < len(hub.heap); {
		state := hub.heap[i]
		if coroTimerControlPhase(latomic.LoadUint32(&state.control)) == coroTimerActive {
			i++
			continue
		}
		hub.removeLocked(state)
		if state.channel && state.weak != nil && latomic.LoadUintptr(&state.weak.Live) == 0 {
			if count < len(pruneKeys) {
				pruneKeys[count] = state.weak.Key
				count++
			} else {
				more = true
			}
		}
	}
	return count, more
}

// coroTimerWaitOne is the only current-frame retention span used by a
// standard Timer hub manager. The compiler owns the complete V2
// park/resume/source transaction; source-style runtime code observes only its
// terminal status.
func coroTimerWaitOne(hub *coroTimerHub, expected uint32, deadline int64) uint32 {
	if hub == nil || latomic.LoadUint32(&hub.ownerRoute) != 0 {
		return 0
	}
	outcome := llgoCoroControlledTimerWaitV2(
		unsafe.Pointer(hub),
		&hub.control,
		&hub.ownerRoute,
		expected,
		deadline,
	)
	latomic.StoreUint32(&hub.ownerRoute, 0)
	KeepAlive(hub)
	return outcome
}

func coroTimerRequestGenerationChange(hub *coroTimerHub) {
	if route := latomic.LoadUint32(&hub.ownerRoute); route != 0 &&
		llgoCoroTimerRequestControlledV2(route) == 0 {
		throw("time: controlled timer owner request failed")
	}
}

func coroTimerNextTickerDeadline(when, period, now int64) int64 {
	if period <= 0 {
		return 0
	}
	next := when + period
	if now > when {
		next = when + period*(1+(now-when)/period)
	}
	if next < 0 {
		return int64(^uint64(0) >> 1)
	}
	return next
}

// coroTimerTakeDueChannel is the shared immediate-deadline transition. New
// owns unpublished state; Reset holds state.sendLock and hub.lock. A due timer
// publishes a real channel value before its API returns, not a readiness bit
// which could win select without transferring a value.
func coroTimerTakeDueChannel(state *coroTimeTimerState, now int64) (generation uint32, delta int64, ready bool) {
	if !state.channel || state.when > now {
		return 0, 0, false
	}
	control := latomic.LoadUint32(&state.control)
	if coroTimerControlPhase(control) != coroTimerActive {
		return 0, 0, false
	}
	generation, delta = coroTimerControlGeneration(control), now-state.when
	if state.period > 0 {
		state.when = coroTimerNextTickerDeadline(state.when, state.period, now)
	} else {
		latomic.StoreUint32(&state.control, coroTimerNextControl(control, coroTimerInactive))
	}
	return generation, delta, true
}

// coroTimerManager is the single managed stackless G driving one coroTimerHub
// shard. The executor's timer source only resumes this G; callbacks never run
// in the target wait driver. Channel timer sends are serialized with
// Stop/Reset via state.sendLock so Stop/Reset can suppress or drain a stale
// buffered value before returning. AfterFunc callbacks run in their own
// managed G launched only when the timer actually expires, so stopped
// context.WithTimeout / time.AfterFunc timers spawn zero goroutines and
// overlapping Reset callbacks match Go semantics.
func coroTimerManager(hub *coroTimerHub) {
	var pruneKeys [16]uintptr
	for {
		coroTimerLock(&hub.lock)
		expected := latomic.LoadUint32(&hub.control)
		if expected == 0 {
			expected = coroTimerControl(1, coroTimerActive)
			latomic.StoreUint32(&hub.control, expected)
		}
		if latomic.SwapUint32(&hub.gcDirty, 0) != 0 {
			n, more := hub.sweepDeadLocked(pruneKeys[:])
			if more {
				latomic.StoreUint32(&hub.gcDirty, 1)
			}
			if n > 0 {
				coroTimerUnlock(&hub.lock)
				for i := 0; i < n; i++ {
					weakState.PruneWeak(pruneKeys[i])
				}
				continue
			}
		}
		if len(hub.heap) == 0 {
			hub.parkedDeadline = 0
			hub.manager = 0
			coroTimerUnlock(&hub.lock)
			return
		}
		now := runtimeNano()
		if hub.heap[0].when <= now {
			state := hub.popLocked()
			stateExpected := latomic.LoadUint32(&state.control)
			if coroTimerControlPhase(stateExpected) != coroTimerActive {
				var pruneKey uintptr
				if state.channel && state.weak != nil && latomic.LoadUintptr(&state.weak.Live) == 0 {
					pruneKey = state.weak.Key
				}
				coroTimerUnlock(&hub.lock)
				if pruneKey != 0 {
					weakState.PruneWeak(pruneKey)
				}
				continue
			}
			if state.afterFunc {
				if state.period > 0 {
					coroTimerUnlock(&hub.lock)
					throw("time: periodic AfterFunc timer")
					return
				}
				latomic.StoreUint32(&state.control, coroTimerNextControl(stateExpected, coroTimerInactive))
				cb := state.cb
				if cb == nil {
					cb = state.arg.(func())
				}
				coroTimerUnlock(&hub.lock)
				go coroRunTimerCallback(cb)
				continue
			}
			// Release hub.lock before taking state.sendLock to strictly preserve
			// lock order (state.sendLock -> hub.lock).
			coroTimerUnlock(&hub.lock)
			if state.channel {
				coroTimerLock(&state.sendLock)
			}
			coroTimerLock(&hub.lock)
			if latomic.LoadUint32(&state.control) != stateExpected ||
				coroTimerControlPhase(stateExpected) != coroTimerActive {
				var pruneKey uintptr
				if state.channel && state.weak != nil && latomic.LoadUintptr(&state.weak.Live) == 0 {
					pruneKey = state.weak.Key
				}
				coroTimerUnlock(&hub.lock)
				if state.channel {
					coroTimerUnlock(&state.sendLock)
				}
				if pruneKey != 0 {
					weakState.PruneWeak(pruneKey)
				}
				continue
			}
			when := state.when
			period := state.period
			arg := state.arg
			delta := now - when
			if delta < 0 {
				delta = 0
			}
			if period > 0 {
				state.when = coroTimerNextTickerDeadline(when, period, now)
				hub.pushLocked(state)
			} else {
				latomic.StoreUint32(&state.control, coroTimerNextControl(stateExpected, coroTimerInactive))
			}
			coroTimerUnlock(&hub.lock)
			if state.channel {
				state.maybeBuffered = 1
			}
			coroTimerFire(state, arg, coroTimerControlGeneration(stateExpected), delta)
			if state.channel {
				coroTimerUnlock(&state.sendLock)
			}
			continue
		}
		deadline := hub.heap[0].when
		hub.parkedDeadline = deadline
		coroTimerUnlock(&hub.lock)

		outcome := coroTimerWaitOne(hub, expected, deadline)
		coroTimerLock(&hub.lock)
		hub.parkedDeadline = 0
		coroTimerUnlock(&hub.lock)

		if outcome != coroTimerOutcomeCompleted && outcome != coroTimerOutcomeCanceled {
			throw("time: invalid controlled timer outcome")
			return
		}
	}
}

// The timer hub never carries a strong channel or public Timer across a park.
// Only this delivery span upgrades the weak channel. arg retains its static
// interface type, but its data word stays nil in parked state.
func coroTimerFire(state *coroTimeTimerState, arg any, generation uint32, delta int64) {
	if state.channel {
		channel := llgoMakeStrongFromWeak(unsafe.Pointer(state.weak))
		if channel == nil {
			return
		}
		(*abi.EmptyInterface)(unsafe.Pointer(&arg)).Data = channel
	}
	state.f(arg, uintptr(generation), delta)
}

func coroTimerDrainChannel(channel unsafe.Pointer) bool {
	if channel == nil {
		return false
	}
	_, ready := corort.ChanTryRecv((*corort.Chan)(channel), nil, 0)
	return ready
}

func coroRunTimerCallback(callback func()) {
	callback()
}

//llgo:managedlink
//go:linkname time_now time.now
func time_now() (sec int64, nsec int32, mono int64) {
	sec, nsec = coroRealtime()
	mono = runtimeNano()
	return
}

//llgo:managedlink
//go:linkname time_runtimeNow time.runtimeNow
func time_runtimeNow() (sec int64, nsec int32, mono int64) {
	if now, bubbled := corort.SynctestNow(); bubbled {
		return now / 1e9, int32(now % 1e9), 0
	}
	return time_now()
}

//llgo:managedlink
//go:linkname time_runtimeNano time.runtimeNano
func time_runtimeNano() int64 {
	if now, bubbled := corort.SynctestNow(); bubbled {
		return now
	}
	return runtimeNano()
}

//llgo:managedlink
//go:linkname time_runtimeIsBubbled time.runtimeIsBubbled
func time_runtimeIsBubbled() bool {
	return corort.SynctestIsInBubble()
}

// coroTimerNewAfterFuncFast handles the common uncontended time.AfterFunc /
// context.WithTimeout insertion without allocating a coroutine frame, boxing
// the callback into any, spawning a goroutine, or waking the hub when a hub
// manager is already parked on an earlier or equal deadline.
//
//llgo:nopreempt
func coroTimerNewAfterFuncFast(when int64, cb func()) (unsafe.Pointer, bool) {
	if corort.SynctestIsInBubble() {
		return nil, false
	}
	hub := coroTimerHubForCurrentP()
	if !latomic.CompareAndSwapUint32(&hub.lock, 0, 1) {
		return nil, false
	}
	if hub.manager == 0 || hub.parkedDeadline == 0 || when < hub.parkedDeadline {
		latomic.StoreUint32(&hub.lock, 0)
		return nil, false
	}
	pair := new(coroAfterFuncTimer)
	t := &pair.timer
	state := &pair.state
	t.init = true
	t.state = state
	state.heapIndex = -1
	state.when = when
	state.cb = cb
	state.afterFunc = true
	state.hub = hub
	state.control = coroTimerControl(1, coroTimerActive)
	hub.pushLocked(state)
	latomic.StoreUint32(&hub.lock, 0)
	return unsafe.Pointer(t), true
}

func coroAfterFuncSlow(when int64, cb func()) unsafe.Pointer {
	pair := new(coroAfterFuncTimer)
	t := &pair.timer
	state := &pair.state
	t.init = true
	t.state = state
	state.heapIndex = -1
	state.when = when
	state.cb = cb
	state.afterFunc = true
	if corort.SynctestIsInBubble() {
		coroNewSynctestTimeTimer(t, when, 0)
		return unsafe.Pointer(t)
	}
	hub := coroTimerHubForCurrentP()
	state.hub = hub
	state.control = coroTimerControl(1, coroTimerActive)
	coroTimerLock(&hub.lock)
	hub.pushLocked(state)
	startManager := false
	wake := false
	if hub.manager == 0 {
		hub.manager = 1
		startManager = true
	} else if hub.parkedDeadline == 0 || when < hub.parkedDeadline {
		coroTimerHubBumpControl(hub)
		wake = true
	}
	coroTimerUnlock(&hub.lock)
	if wake {
		coroTimerRequestGenerationChange(hub)
	}
	if startManager {
		go coroTimerManager(hub)
	}
	return unsafe.Pointer(t)
}

//llgo:managedlink
//go:linkname llgoCoroAfterFuncV1 runtime.llgoCoroAfterFuncV1
func llgoCoroAfterFuncV1(when int64, f func()) unsafe.Pointer {
	if t, ok := coroTimerNewAfterFuncFast(when, f); ok {
		return t
	}
	return coroAfterFuncSlow(when, f)
}

func coroTimerNewSlow(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) unsafe.Pointer {
	var t *timeTimer
	var state *coroTimeTimerState
	if cp == nil {
		pair := new(coroAfterFuncTimer)
		t = &pair.timer
		state = &pair.state
		t.init = true
		t.state = state
	} else {
		state = new(coroTimeTimerState)
		t = &timeTimer{c: cp, init: true, state: state}
	}
	state.heapIndex = -1
	state.when = when
	state.period = period
	state.f = f
	state.arg = arg
	state.channel = cp != nil
	_, state.afterFunc = arg.(func())
	if cp != nil && !corort.MarkTimerChannel((*corort.Chan)(cp)) {
		throw("time: invalid synchronous timer channel")
	}
	if cp != nil {
		state.weak = (*weakHandle)(llgoRegisterWeakPointer(cp))
		(*abi.EmptyInterface)(unsafe.Pointer(&state.arg)).Data = nil
	}
	if corort.SynctestIsInBubble() {
		coroNewSynctestTimeTimer(t, when, period)
		return unsafe.Pointer(t)
	}
	if cp != nil {
		corort.EnableGCObserver()
	}
	hub := coroTimerHubForCurrentP()
	state.hub = hub
	state.control = coroTimerControl(1, coroTimerActive)
	if cp != nil {
		if generation, delta, ready := coroTimerTakeDueChannel(state, runtimeNano()); ready {
			state.maybeBuffered = 1
			f(arg, uintptr(generation), delta)
		}
	}
	if coroTimerControlPhase(latomic.LoadUint32(&state.control)) == coroTimerActive {
		if state.channel {
			coroTimerTrack(state)
		}
		coroTimerLock(&hub.lock)
		hub.pushLocked(state)
		startManager := false
		wake := false
		if hub.manager == 0 {
			hub.manager = 1
			startManager = true
		} else if hub.parkedDeadline == 0 || when < hub.parkedDeadline {
			coroTimerHubBumpControl(hub)
			wake = true
		}
		coroTimerUnlock(&hub.lock)
		if wake {
			coroTimerRequestGenerationChange(hub)
		}
		if startManager {
			go coroTimerManager(hub)
		}
	}
	return unsafe.Pointer(t)
}

//llgo:managedlink
//go:linkname llgoCoroTimerNewV1 runtime.llgoCoroTimerNewV1
func llgoCoroTimerNewV1(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) unsafe.Pointer {
	return coroTimerNewSlow(when, period, f, arg, cp)
}

// coroTimerStopFast performs an uncontended timer Stop completely synchronously
// without creating a coroutine frame when the shard stays non-empty and no
// channel value needs draining.
//
//llgo:nopreempt
func coroTimerStopFast(t *timeTimer) (pending bool, handled bool) {
	if t == nil || t.state == nil {
		return false, true
	}
	state := t.state
	if state.synctest {
		return false, false
	}
	hub := state.hub
	if state.channel {
		if !latomic.CompareAndSwapUint32(&state.sendLock, 0, 1) {
			return false, false
		}
		if state.maybeBuffered != 0 {
			latomic.StoreUint32(&state.sendLock, 0)
			return false, false
		}
	}
	if !latomic.CompareAndSwapUint32(&hub.lock, 0, 1) {
		if state.channel {
			latomic.StoreUint32(&state.sendLock, 0)
		}
		return false, false
	}
	if state.heapIndex >= 0 && len(hub.heap) == 1 && hub.manager != 0 {
		// Transitioning shard to empty requires waking the hub so it retires.
		latomic.StoreUint32(&hub.lock, 0)
		if state.channel {
			latomic.StoreUint32(&state.sendLock, 0)
		}
		return false, false
	}
	old := latomic.LoadUint32(&state.control)
	pending = coroTimerControlPhase(old) == coroTimerActive
	if pending {
		latomic.StoreUint32(&state.control, coroTimerNextControl(old, coroTimerInactive))
	}
	if state.heapIndex >= 0 {
		hub.removeLocked(state)
	}
	latomic.StoreUint32(&hub.lock, 0)
	if state.channel {
		latomic.StoreUint32(&state.sendLock, 0)
	}
	return pending, true
}

func coroTimerStopSlow(t *timeTimer) bool {
	if t == nil {
		return false
	}
	if fake := coroSynctestTimeTimerOf(t); fake != nil {
		return fake.stop(t.c)
	}
	state := t.state
	hub := state.hub
	if state.channel {
		coroTimerLock(&state.sendLock)
	}
	coroTimerLock(&hub.lock)
	old := latomic.LoadUint32(&state.control)
	pending := coroTimerControlPhase(old) == coroTimerActive
	if pending {
		latomic.StoreUint32(&state.control, coroTimerNextControl(old, coroTimerInactive))
	}
	wake := false
	if state.heapIndex >= 0 {
		hub.removeLocked(state)
		if len(hub.heap) == 0 && hub.manager != 0 {
			coroTimerHubBumpControl(hub)
			wake = true
		}
	}
	coroTimerUnlock(&hub.lock)
	if wake {
		coroTimerRequestGenerationChange(hub)
	}
	if state.channel {
		if coroTimerDrainChannel(t.c) {
			pending = true
		}
		state.maybeBuffered = 0
		coroTimerUnlock(&state.sendLock)
	}
	return pending
}

//llgo:managedlink
//go:linkname llgoCoroTimerStopV1 runtime.llgoCoroTimerStopV1
func llgoCoroTimerStopV1(timer unsafe.Pointer) bool {
	t := (*timeTimer)(timer)
	if pending, ok := coroTimerStopFast(t); ok {
		return pending
	}
	return coroTimerStopSlow(t)
}

// coroTimerResetFast performs an uncontended future-deadline timer Reset
// completely synchronously without allocating a coroutine frame, waking the
// hub, draining an empty channel, or acquiring coroTimerGC.lock when the
// channel timer is already tracked.
//
//llgo:nopreempt
func coroTimerResetFast(t *timeTimer, when, period int64) (pending bool, handled bool) {
	if t == nil || t.state == nil {
		return false, true
	}
	state := t.state
	if state.synctest {
		return false, false
	}
	hub := state.hub
	if state.channel {
		if !latomic.CompareAndSwapUint32(&state.sendLock, 0, 1) {
			return false, false
		}
		if state.maybeBuffered != 0 {
			latomic.StoreUint32(&state.sendLock, 0)
			return false, false
		}
	}
	if !latomic.CompareAndSwapUint32(&hub.lock, 0, 1) {
		if state.channel {
			latomic.StoreUint32(&state.sendLock, 0)
		}
		return false, false
	}
	if hub.manager == 0 || hub.parkedDeadline == 0 || when < hub.parkedDeadline {
		latomic.StoreUint32(&hub.lock, 0)
		if state.channel {
			latomic.StoreUint32(&state.sendLock, 0)
		}
		return false, false
	}
	if state.channel && when <= runtimeNano() {
		latomic.StoreUint32(&hub.lock, 0)
		latomic.StoreUint32(&state.sendLock, 0)
		return false, false
	}
	old := latomic.LoadUint32(&state.control)
	pending = coroTimerControlPhase(old) == coroTimerActive
	nextControl := coroTimerNextControl(old, coroTimerActive)
	if state.channel && !pending {
		// Dekker barrier against coroTimerGCObserveV1: publish Active first via
		// atomic swap, then confirm the GC observer is neither running nor has
		// pruned state from coroTimerGC.head.
		latomic.SwapUint32(&state.control, nextControl)
		if latomic.LoadUint32(&coroTimerGC.lock) != 0 || latomic.LoadUint32(&state.gcListed) == 0 {
			latomic.StoreUint32(&state.control, old)
			latomic.StoreUint32(&hub.lock, 0)
			latomic.StoreUint32(&state.sendLock, 0)
			return false, false
		}
	} else {
		latomic.StoreUint32(&state.control, nextControl)
	}
	state.when = when
	state.period = period
	if state.heapIndex >= 0 {
		hub.fixLocked(state)
	} else {
		hub.pushLocked(state)
	}
	latomic.StoreUint32(&hub.lock, 0)
	if state.channel {
		latomic.StoreUint32(&state.sendLock, 0)
	}
	return pending, true
}

func coroTimerResetSlow(t *timeTimer, when, period int64) bool {
	if t == nil {
		return false
	}
	if fake := coroSynctestTimeTimerOf(t); fake != nil {
		return fake.reset(when, period, t.c)
	}
	state := t.state
	hub := state.hub
	if state.channel {
		coroTimerLock(&state.sendLock)
	}
	coroTimerLock(&hub.lock)
	old := latomic.LoadUint32(&state.control)
	pending := coroTimerControlPhase(old) == coroTimerActive
	state.when = when
	state.period = period
	latomic.StoreUint32(&state.control, coroTimerNextControl(old, coroTimerActive))
	var generation uint32
	var delta int64
	var immediate bool
	if state.channel {
		generation, delta, immediate = coroTimerTakeDueChannel(state, runtimeNano())
	}
	active := coroTimerControlPhase(latomic.LoadUint32(&state.control)) == coroTimerActive
	startManager := false
	wake := false
	if active {
		if state.heapIndex >= 0 {
			hub.fixLocked(state)
		} else {
			hub.pushLocked(state)
		}
		if hub.manager == 0 {
			hub.manager = 1
			startManager = true
		} else if hub.parkedDeadline == 0 || when < hub.parkedDeadline {
			coroTimerHubBumpControl(hub)
			wake = true
		}
	} else if state.heapIndex >= 0 {
		hub.removeLocked(state)
		if len(hub.heap) == 0 && hub.manager != 0 {
			coroTimerHubBumpControl(hub)
			wake = true
		}
	}
	coroTimerUnlock(&hub.lock)
	if active && state.channel && !pending {
		coroTimerTrack(state)
	}
	if wake {
		coroTimerRequestGenerationChange(hub)
	}
	if state.channel {
		if coroTimerDrainChannel(t.c) {
			pending = true
		}
		state.maybeBuffered = 0
		if immediate {
			state.maybeBuffered = 1
			coroTimerFire(state, state.arg, generation, delta)
		}
		coroTimerUnlock(&state.sendLock)
	}
	if startManager {
		go coroTimerManager(hub)
	}
	return pending
}

//llgo:managedlink
//go:linkname llgoCoroTimerResetV1 runtime.llgoCoroTimerResetV1
func llgoCoroTimerResetV1(timer unsafe.Pointer, when, period int64) bool {
	t := (*timeTimer)(timer)
	if pending, ok := coroTimerResetFast(t, when, period); ok {
		return pending
	}
	return coroTimerResetSlow(t, when, period)
}
