//go:build go1.23 && llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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
	"unsafe"

	ct "github.com/goplus/llgo/runtime/internal/clite/time"
	latomic "github.com/goplus/llgo/runtime/internal/lib/sync/atomic"
	corort "github.com/goplus/llgo/runtime/internal/runtime"
)

const (
	coroTimerControlPhaseBits = 2
	coroTimerControlPhaseMask = 1<<coroTimerControlPhaseBits - 1
	coroTimerControlMaxGen    = ^uint32(0) >> coroTimerControlPhaseBits

	coroTimerInactive uint32 = 1
	coroTimerActive   uint32 = 2

	coroTimerOutcomeCompleted uint32 = 1
	coroTimerOutcomeCanceled  uint32 = 2
)

// timeTimer matches the public prefix of time.Timer and time.Ticker. Everything
// after init is private runtime state. One managed stackless G owns the active
// timer loop; no target callback invokes f and no timer creates an OS thread.
type timeTimer struct {
	c    unsafe.Pointer
	init bool

	state coroTimeTimerState
}

type coroTimeTimerState struct {
	lock       uint32
	sendLock   uint32
	control    uint32
	manager    uint32
	ownerRoute uint32

	when      int64
	period    int64
	f         func(any, uintptr, int64)
	arg       any
	channel   unsafe.Pointer
	afterFunc bool
}

//go:linkname llgoCoroControlledTimerWaitV2 llgo.coroControlledTimerWait
func llgoCoroControlledTimerWaitV2(
	controller unsafe.Pointer,
	control, ownerRoute *uint32,
	expected uint32,
	deadline int64,
) uint32

//llgo:coro noblock
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

func coroTimerLock(word *uint32) {
	for !latomic.CompareAndSwapUint32(word, 0, 1) {
		coroSchedulerYield()
	}
}

func coroTimerUnlock(word *uint32) {
	latomic.StoreUint32(word, 0)
}

// coroTimerWaitOne is the only current-frame retention span used by a
// standard Timer manager. The compiler owns the complete V2 park/resume/source
// transaction; source-style runtime code observes only its terminal status.
func coroTimerWaitOne(t *timeTimer, expected uint32, deadline int64) uint32 {
	if t == nil || latomic.LoadUint32(&t.state.ownerRoute) != 0 {
		return 0
	}
	outcome := llgoCoroControlledTimerWaitV2(
		unsafe.Pointer(t),
		&t.state.control,
		&t.state.ownerRoute,
		expected,
		deadline,
	)
	latomic.StoreUint32(&t.state.ownerRoute, 0)
	KeepAlive(t)
	return outcome
}

func coroTimerRequestGenerationChange(state *coroTimeTimerState) {
	if route := latomic.LoadUint32(&state.ownerRoute); route != 0 &&
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

// coroTimerManager is an ordinary managed G. The executor's timer source only
// resumes this G; callbacks never run in the target wait driver. Channel timer
// sends are serialized with Stop/Reset so the latter can suppress or drain a
// stale buffered value before returning. The coroutine time.AfterFunc patch
// leaves the legacy f slot nil and retains the user callback in arg. This
// manager itself is the callback G. Before invoking a one-shot callback it
// publishes manager=0 under state.lock, so Reset after expiry starts an
// independent manager and may overlap the old callback as required by Go.
func coroTimerManager(t *timeTimer) {
	for {
		state := &t.state
		coroTimerLock(&state.lock)
		expected := latomic.LoadUint32(&state.control)
		if coroTimerControlPhase(expected) != coroTimerActive {
			state.manager = 0
			coroTimerUnlock(&state.lock)
			return
		}
		deadline := state.when
		coroTimerUnlock(&state.lock)

		outcome := coroTimerWaitOne(t, expected, deadline)
		now := runtimeNano()

		if state.channel != nil {
			coroTimerLock(&state.sendLock)
		}
		coroTimerLock(&state.lock)
		if latomic.LoadUint32(&state.control) != expected ||
			coroTimerControlPhase(expected) != coroTimerActive {
			coroTimerUnlock(&state.lock)
			if state.channel != nil {
				coroTimerUnlock(&state.sendLock)
			}
			continue
		}
		if outcome != coroTimerOutcomeCompleted {
			coroTimerUnlock(&state.lock)
			if state.channel != nil {
				coroTimerUnlock(&state.sendLock)
			}
			if outcome == coroTimerOutcomeCanceled {
				// A matching cancellation must also have changed control before
				// publishing it. Reaching this case means an owner invariant was
				// violated rather than an ordinary Stop/Reset race.
				throw("time: controlled timer canceled without generation change")
			}
			throw("time: invalid controlled timer outcome")
			return
		}

		when := state.when
		period := state.period
		f, arg := state.f, state.arg
		delta := now - when
		if delta < 0 {
			delta = 0
		}
		if period > 0 {
			state.when = coroTimerNextTickerDeadline(when, period, now)
		} else {
			latomic.StoreUint32(&state.control, coroTimerNextControl(expected, coroTimerInactive))
		}

		if state.afterFunc {
			if period > 0 {
				coroTimerUnlock(&state.lock)
				throw("time: periodic AfterFunc timer")
				return
			}
			// This manager is already the dedicated callback G. Retire its
			// ownership before the call so Stop observes that the callback has
			// started and Reset may launch the next generation independently.
			state.manager = 0
			coroTimerUnlock(&state.lock)
			coroRunTimerCallback(arg.(func()))
			return
		} else {
			coroTimerUnlock(&state.lock)
			f(arg, uintptr(coroTimerControlGeneration(expected)), delta)
			if state.channel != nil {
				coroTimerUnlock(&state.sendLock)
			}
		}
	}
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
	var tv ct.Timespec
	ct.ClockGettime(ct.CLOCK_REALTIME, &tv)
	sec = int64(tv.Sec)
	nsec = int32(tv.Nsec)
	mono = runtimeNano()
	return
}

//llgo:managedlink
//go:linkname time_runtimeNow time.runtimeNow
func time_runtimeNow() (sec int64, nsec int32, mono int64) {
	return time_now()
}

//llgo:managedlink
//go:linkname time_runtimeNano time.runtimeNano
func time_runtimeNano() int64 {
	return runtimeNano()
}

//llgo:managedlink
//go:linkname time_runtimeIsBubbled time.runtimeIsBubbled
func time_runtimeIsBubbled() bool {
	return false
}

//llgo:managedlink
//go:linkname llgoCoroTimerNewV1 runtime.llgoCoroTimerNewV1
func llgoCoroTimerNewV1(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) unsafe.Pointer {
	t := &timeTimer{c: cp, init: true}
	state := &t.state
	state.when = when
	state.period = period
	state.f = f
	state.arg = arg
	state.channel = cp
	_, state.afterFunc = arg.(func())
	if cp != nil && !corort.MarkTimerChannel((*corort.Chan)(cp)) {
		throw("time: invalid synchronous timer channel")
	}
	state.control = coroTimerControl(1, coroTimerActive)
	state.manager = 1
	go coroTimerManager(t)
	return unsafe.Pointer(t)
}

//llgo:managedlink
//go:linkname llgoCoroTimerStopV1 runtime.llgoCoroTimerStopV1
func llgoCoroTimerStopV1(timer unsafe.Pointer) bool {
	t := (*timeTimer)(timer)
	if t == nil {
		return false
	}
	state := &t.state
	if state.channel != nil {
		coroTimerLock(&state.sendLock)
	}
	coroTimerLock(&state.lock)
	old := latomic.LoadUint32(&state.control)
	pending := coroTimerControlPhase(old) == coroTimerActive
	if pending {
		latomic.StoreUint32(&state.control, coroTimerNextControl(old, coroTimerInactive))
	}
	coroTimerUnlock(&state.lock)
	if pending {
		// A zero route is the legal pre-publication side of the prepare
		// handshake; the post-attach control recheck closes that race.
		coroTimerRequestGenerationChange(state)
	}
	if state.channel != nil {
		if coroTimerDrainChannel(state.channel) {
			pending = true
		}
		coroTimerUnlock(&state.sendLock)
	}
	return pending
}

//llgo:managedlink
//go:linkname llgoCoroTimerResetV1 runtime.llgoCoroTimerResetV1
func llgoCoroTimerResetV1(timer unsafe.Pointer, when, period int64) bool {
	t := (*timeTimer)(timer)
	if t == nil {
		return false
	}
	state := &t.state
	if state.channel != nil {
		coroTimerLock(&state.sendLock)
	}
	coroTimerLock(&state.lock)
	old := latomic.LoadUint32(&state.control)
	pending := coroTimerControlPhase(old) == coroTimerActive
	state.when = when
	state.period = period
	latomic.StoreUint32(&state.control, coroTimerNextControl(old, coroTimerActive))
	startManager := state.manager == 0
	if startManager {
		state.manager = 1
	}
	coroTimerUnlock(&state.lock)
	if pending {
		coroTimerRequestGenerationChange(state)
	}
	if state.channel != nil {
		if coroTimerDrainChannel(state.channel) {
			pending = true
		}
		coroTimerUnlock(&state.sendLock)
	}
	if startManager {
		go coroTimerManager(t)
	}
	return pending
}
