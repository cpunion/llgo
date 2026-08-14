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

package coro

// DeferredExecutorHandoffPhase is the physical-start state of one prepared
// execution-domain replacement. The logical ownership generation remains in
// ExecutionDomainHandoff; this gate only decides whether a durable executor
// request has made physical dispatch necessary.
type DeferredExecutorHandoffPhase uint8

const (
	DeferredExecutorHandoffIdle DeferredExecutorHandoffPhase = iota
	DeferredExecutorHandoffArmed
	DeferredExecutorHandoffStarting
	DeferredExecutorHandoffQueued
	DeferredExecutorHandoffStarted
)

const (
	deferredExecutorHandoffPhaseBits = 3
	deferredExecutorHandoffPhaseMask = uint32(1<<deferredExecutorHandoffPhaseBits) - 1
	deferredExecutorHandoffSlotMask  = uint32(1<<(32-deferredExecutorHandoffPhaseBits)) - 1
)

type deferredExecutorHandoffNoCopy struct{}

func (*deferredExecutorHandoffNoCopy) Lock()   {}
func (*deferredExecutorHandoffNoCopy) Unlock() {}

// DeferredExecutorHandoff is a pointer-free, stable-address dispatch gate for
// a replacement which has already obtained an exact ExecutionDomainHandoff
// and directory slot but has not yet consumed a physical thread. Arm happens
// only after the blocked owner releases its managed-execution permit. A
// durable target request and the returning owner then race one CAS:
//
//   - BeginStart wins and must publish Queued or Started;
//   - Withdraw wins and proves that no physical owner was dispatched.
//
// Starting is a short publication interval, not a scheduler wait state. The
// returning owner may yield until the request publisher records the outcome.
// Queued distinguishes an asynchronously dispatched cached thread, which can
// still be canceled before C-to-Go dispatch, from a synchronously acknowledged
// start. Complete is called only after the ordinary generation-bound return and
// strong-recycle protocol has finished.
//
// Slot is deliberately a routing hint rather than a generation capability. A
// delayed accepted request may start a later armed use of the same slot; that
// is a safe coalesced compensation request. ExecutionDomainHandoff remains the
// authority which prevents a stale physical owner from claiming a later call.
type DeferredExecutorHandoff struct {
	noCopy deferredExecutorHandoffNoCopy
	state  uint32
}

func deferredExecutorHandoffPack(slot uint32, phase DeferredExecutorHandoffPhase) uint32 {
	return slot<<deferredExecutorHandoffPhaseBits | uint32(phase)
}

func deferredExecutorHandoffUnpack(state uint32) (uint32, DeferredExecutorHandoffPhase) {
	return state >> deferredExecutorHandoffPhaseBits,
		DeferredExecutorHandoffPhase(state & deferredExecutorHandoffPhaseMask)
}

func deferredExecutorHandoffValid(slot uint32, phase DeferredExecutorHandoffPhase) bool {
	if phase == DeferredExecutorHandoffIdle {
		return slot == 0
	}
	return slot != 0 && slot <= deferredExecutorHandoffSlotMask &&
		phase <= DeferredExecutorHandoffStarted
}

// Arm publishes one prepared replacement after its managed-execution permit
// has been released. The zero value is reusable Idle.
func (handoff *DeferredExecutorHandoff) Arm(slot uint32) bool {
	return handoff != nil && deferredExecutorHandoffValid(slot, DeferredExecutorHandoffArmed) &&
		preemptCompareAndSwap(
			&handoff.state,
			0,
			deferredExecutorHandoffPack(slot, DeferredExecutorHandoffArmed),
		)
}

// BeginStart lets one accepted durable executor request become the unique
// physical-start publisher. A false result means there is no armed replacement
// to start; Idle and an already-starting/started request are both benign.
func (handoff *DeferredExecutorHandoff) BeginStart() (slot uint32, started bool) {
	if handoff == nil {
		return 0, false
	}
	state := preemptLoad(&handoff.state)
	slot, phase := deferredExecutorHandoffUnpack(state)
	if !deferredExecutorHandoffValid(slot, phase) || phase != DeferredExecutorHandoffArmed {
		return 0, false
	}
	return slot, preemptCompareAndSwap(
		&handoff.state,
		state,
		deferredExecutorHandoffPack(slot, DeferredExecutorHandoffStarting),
	)
}

// PublishStart completes the unique Starting interval. queued records whether
// the cached-thread dispatch can still be withdrawn through its C token.
func (handoff *DeferredExecutorHandoff) PublishStart(slot uint32, queued bool) bool {
	if handoff == nil || !deferredExecutorHandoffValid(slot, DeferredExecutorHandoffStarting) {
		return false
	}
	phase := DeferredExecutorHandoffStarted
	if queued {
		phase = DeferredExecutorHandoffQueued
	}
	return preemptCompareAndSwap(
		&handoff.state,
		deferredExecutorHandoffPack(slot, DeferredExecutorHandoffStarting),
		deferredExecutorHandoffPack(slot, phase),
	)
}

// RetryStart returns a failed physical-start publication to Armed. The durable
// request caller must report failure; a later request may retry, while a
// concurrently returning owner may withdraw the restored arm.
func (handoff *DeferredExecutorHandoff) RetryStart(slot uint32) bool {
	return handoff != nil && deferredExecutorHandoffValid(slot, DeferredExecutorHandoffStarting) &&
		preemptCompareAndSwap(
			&handoff.state,
			deferredExecutorHandoffPack(slot, DeferredExecutorHandoffStarting),
			deferredExecutorHandoffPack(slot, DeferredExecutorHandoffArmed),
		)
}

// Withdraw wins only before a durable request has begun physical dispatch.
func (handoff *DeferredExecutorHandoff) Withdraw(slot uint32) bool {
	return handoff != nil && deferredExecutorHandoffValid(slot, DeferredExecutorHandoffArmed) &&
		preemptCompareAndSwap(
			&handoff.state,
			deferredExecutorHandoffPack(slot, DeferredExecutorHandoffArmed),
			0,
		)
}

// Observe returns one atomic state snapshot. ok rejects an impossible packed
// value rather than allowing target code to treat corruption as Idle.
func (handoff *DeferredExecutorHandoff) Observe() (
	slot uint32,
	phase DeferredExecutorHandoffPhase,
	ok bool,
) {
	if handoff == nil {
		return 0, DeferredExecutorHandoffIdle, false
	}
	state := preemptLoad(&handoff.state)
	slot, phase = deferredExecutorHandoffUnpack(state)
	return slot, phase, deferredExecutorHandoffValid(slot, phase)
}

// Complete clears a dispatched replacement only after its exact logical
// return and physical recycle have completed.
func (handoff *DeferredExecutorHandoff) Complete(slot uint32) bool {
	if handoff == nil || slot == 0 || slot > deferredExecutorHandoffSlotMask {
		return false
	}
	for _, phase := range [...]DeferredExecutorHandoffPhase{
		DeferredExecutorHandoffQueued,
		DeferredExecutorHandoffStarted,
	} {
		if preemptCompareAndSwap(
			&handoff.state,
			deferredExecutorHandoffPack(slot, phase),
			0,
		) {
			return true
		}
	}
	return false
}

// Idle reports whether no prepared physical dispatch remains.
func (handoff *DeferredExecutorHandoff) Idle() bool {
	return handoff != nil && preemptLoad(&handoff.state) == 0
}
