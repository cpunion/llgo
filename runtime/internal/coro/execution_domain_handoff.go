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

// ExecutionDomainHandoffHandle identifies one exact physical-owner handoff.
// OwnerEpoch binds the handoff to the already-admitted scheduler-owner episode;
// Generation prevents a delayed claimant or returner from touching a later
// blocking call in the same domain.
//
// The handle is pointer-free so a native pthread, RTOS task, host realm, or
// bare-metal core may carry it without retaining scheduler storage.
type ExecutionDomainHandoffHandle struct {
	Generation uint32
	OwnerEpoch uint32
}

// Valid reports whether the handle can identify one live handoff generation.
func (handle ExecutionDomainHandoffHandle) Valid() bool {
	return handle.Generation != 0 &&
		handle.Generation <= executionDomainHandoffGenerationMask &&
		handle.OwnerEpoch != 0
}

type executionDomainHandoffPhase uint32

const (
	executionDomainHandoffIdle executionDomainHandoffPhase = iota
	// Publishing reserves the next generation before OwnerEpoch is stored.
	// A claimant never accepts this private owner-only transition.
	executionDomainHandoffPublishing
	executionDomainHandoffReleased
	executionDomainHandoffClaimed
	executionDomainHandoffReturnRequested
	executionDomainHandoffReturned
	// Completing exclusively owns clearing OwnerEpoch before Idle is
	// republished, closing reuse against a copied Complete call.
	executionDomainHandoffCompleting
	executionDomainHandoffRetired
)

const (
	executionDomainHandoffPhaseBits      = 3
	executionDomainHandoffPhaseMask      = uint32(1<<executionDomainHandoffPhaseBits) - 1
	executionDomainHandoffGenerationMask = uint32(1<<(32-executionDomainHandoffPhaseBits)) - 1
)

func executionDomainHandoffPack(
	phase executionDomainHandoffPhase,
	generation uint32,
) uint32 {
	return generation<<executionDomainHandoffPhaseBits | uint32(phase)
}

func executionDomainHandoffStatePhase(state uint32) executionDomainHandoffPhase {
	return executionDomainHandoffPhase(state & executionDomainHandoffPhaseMask)
}

func executionDomainHandoffStateGeneration(state uint32) uint32 {
	return state >> executionDomainHandoffPhaseBits
}

type executionDomainHandoffNoCopy struct{}

func (*executionDomainHandoffNoCopy) Lock()   {}
func (*executionDomainHandoffNoCopy) Unlock() {}

// ExecutionDomainHandoff is the target-neutral ownership baton used when one
// physical owner must keep executing a thread-affine foreign call while a
// compensation owner services its released P/source domain.
//
// The zero value is reusable Idle generation zero. Begin first reserves a new
// generation in Publishing, stores the immutable owner epoch, and only then
// release-publishes Released. Claim and RequestReturn race in one atomic state
// word: either the claimant wins and must later FinishReturn at a stable
// scheduler boundary, or the original owner withdraws the unclaimed release
// directly. Complete clears OwnerEpoch behind the private Completing phase
// before publishing Idle, so reuse cannot observe an old epoch.
//
// This object contains no pointer and must remain at a stable address after
// first use. The original owner alone calls Begin, RequestReturn, Complete, and
// Retire. At most one compensation owner may successfully Claim,
// RequestClaimedReturn, and FinishReturn. Waiting and doorbells are target
// policy, not part of this core.
type ExecutionDomainHandoff struct {
	noCopy     executionDomainHandoffNoCopy
	state      uint32
	ownerEpoch uint32
}

func executionDomainHandoffStateMatches(
	handoff *ExecutionDomainHandoff,
	state uint32,
	handle ExecutionDomainHandoffHandle,
	phase executionDomainHandoffPhase,
) bool {
	if handoff == nil || !handle.Valid() ||
		executionDomainHandoffStatePhase(state) != phase ||
		executionDomainHandoffStateGeneration(state) != handle.Generation {
		return false
	}
	epoch := preemptLoad(&handoff.ownerEpoch)
	return epoch == handle.OwnerEpoch && preemptLoad(&handoff.state) == state
}

func (handoff *ExecutionDomainHandoff) transition(
	handle ExecutionDomainHandoffHandle,
	from, to executionDomainHandoffPhase,
) bool {
	if handoff == nil || !handle.Valid() {
		return false
	}
	state := executionDomainHandoffPack(from, handle.Generation)
	return executionDomainHandoffStateMatches(handoff, state, handle, from) &&
		preemptCompareAndSwap(
			&handoff.state,
			state,
			executionDomainHandoffPack(to, handle.Generation),
		)
}

func (handoff *ExecutionDomainHandoff) observes(
	handle ExecutionDomainHandoffHandle,
	phase executionDomainHandoffPhase,
) bool {
	if handoff == nil {
		return false
	}
	state := preemptLoad(&handoff.state)
	return executionDomainHandoffStateMatches(handoff, state, handle, phase)
}

// Begin publishes one released execution domain for ownerEpoch. Generation
// exhaustion permanently retires the gate rather than permitting ABA reuse.
func (handoff *ExecutionDomainHandoff) Begin(
	ownerEpoch uint32,
) (ExecutionDomainHandoffHandle, bool) {
	if handoff == nil || ownerEpoch == 0 || preemptLoad(&handoff.ownerEpoch) != 0 {
		return ExecutionDomainHandoffHandle{}, false
	}
	state := preemptLoad(&handoff.state)
	if executionDomainHandoffStatePhase(state) != executionDomainHandoffIdle {
		return ExecutionDomainHandoffHandle{}, false
	}
	generation := executionDomainHandoffStateGeneration(state)
	if generation == executionDomainHandoffGenerationMask {
		retired := executionDomainHandoffPack(executionDomainHandoffRetired, generation)
		_ = preemptCompareAndSwap(&handoff.state, state, retired)
		return ExecutionDomainHandoffHandle{}, false
	}
	generation++
	publishing := executionDomainHandoffPack(executionDomainHandoffPublishing, generation)
	if !preemptCompareAndSwap(&handoff.state, state, publishing) {
		return ExecutionDomainHandoffHandle{}, false
	}
	preemptStore(&handoff.ownerEpoch, ownerEpoch)
	preemptStore(
		&handoff.state,
		executionDomainHandoffPack(executionDomainHandoffReleased, generation),
	)
	return ExecutionDomainHandoffHandle{
		Generation: generation,
		OwnerEpoch: ownerEpoch,
	}, true
}

// Released returns the exact currently published unclaimed handoff. A target
// thread created with only a stable execution-domain slot uses this observation
// to obtain the handle before Claim.
func (handoff *ExecutionDomainHandoff) Released() (ExecutionDomainHandoffHandle, bool) {
	if handoff == nil {
		return ExecutionDomainHandoffHandle{}, false
	}
	state := preemptLoad(&handoff.state)
	if executionDomainHandoffStatePhase(state) != executionDomainHandoffReleased {
		return ExecutionDomainHandoffHandle{}, false
	}
	handle := ExecutionDomainHandoffHandle{
		Generation: executionDomainHandoffStateGeneration(state),
		OwnerEpoch: preemptLoad(&handoff.ownerEpoch),
	}
	if !handle.Valid() || preemptLoad(&handoff.state) != state {
		return ExecutionDomainHandoffHandle{}, false
	}
	return handle, true
}

// Claim transfers the released domain to one compensation owner.
func (handoff *ExecutionDomainHandoff) Claim(
	handle ExecutionDomainHandoffHandle,
) bool {
	return handoff.transition(
		handle,
		executionDomainHandoffReleased,
		executionDomainHandoffClaimed,
	)
}

// ExecutionDomainHandoffReturnResult records whether RequestReturn withdrew an
// unclaimed release or asked an active compensation owner to return at its next
// stable scheduler boundary.
type ExecutionDomainHandoffReturnResult uint8

const (
	ExecutionDomainHandoffReturnInvalid ExecutionDomainHandoffReturnResult = iota
	ExecutionDomainHandoffReturnUnclaimed
	ExecutionDomainHandoffReturnClaimed
)

// RequestReturn is the original owner's exactly-once return request.
func (handoff *ExecutionDomainHandoff) RequestReturn(
	handle ExecutionDomainHandoffHandle,
) ExecutionDomainHandoffReturnResult {
	if handoff == nil || !handle.Valid() {
		return ExecutionDomainHandoffReturnInvalid
	}
	for {
		state := preemptLoad(&handoff.state)
		if executionDomainHandoffStateGeneration(state) != handle.Generation ||
			preemptLoad(&handoff.ownerEpoch) != handle.OwnerEpoch {
			return ExecutionDomainHandoffReturnInvalid
		}
		if preemptLoad(&handoff.state) != state {
			continue
		}
		switch executionDomainHandoffStatePhase(state) {
		case executionDomainHandoffReleased:
			if preemptCompareAndSwap(
				&handoff.state,
				state,
				executionDomainHandoffPack(executionDomainHandoffReturned, handle.Generation),
			) {
				return ExecutionDomainHandoffReturnUnclaimed
			}
		case executionDomainHandoffClaimed:
			if preemptCompareAndSwap(
				&handoff.state,
				state,
				executionDomainHandoffPack(
					executionDomainHandoffReturnRequested,
					handle.Generation,
				),
			) {
				return ExecutionDomainHandoffReturnClaimed
			}
		default:
			return ExecutionDomainHandoffReturnInvalid
		}
	}
}

// RequestClaimedReturn lets the exact compensation owner autonomously request
// return after it has satisfied a target-neutral service condition, such as a
// parked locked G becoming runnable. Unlike RequestReturn, it cannot withdraw
// an unclaimed publication.
func (handoff *ExecutionDomainHandoff) RequestClaimedReturn(
	handle ExecutionDomainHandoffHandle,
) bool {
	return handoff.transition(
		handle,
		executionDomainHandoffClaimed,
		executionDomainHandoffReturnRequested,
	)
}

// ReturnRequested reports the exact claimed generation's sticky return fact.
func (handoff *ExecutionDomainHandoff) ReturnRequested(
	handle ExecutionDomainHandoffHandle,
) bool {
	return handoff.observes(handle, executionDomainHandoffReturnRequested)
}

// FinishReturn is called by the compensation owner only after it has stopped
// touching every scheduler-owned P/driver/source field.
func (handoff *ExecutionDomainHandoff) FinishReturn(
	handle ExecutionDomainHandoffHandle,
) bool {
	return handoff.transition(
		handle,
		executionDomainHandoffReturnRequested,
		executionDomainHandoffReturned,
	)
}

// Returned reports that no compensation owner can still touch the domain.
func (handoff *ExecutionDomainHandoff) Returned(
	handle ExecutionDomainHandoffHandle,
) bool {
	return handoff.observes(handle, executionDomainHandoffReturned)
}

// Complete consumes the returned generation and republishes reusable Idle.
func (handoff *ExecutionDomainHandoff) Complete(
	handle ExecutionDomainHandoffHandle,
) bool {
	if !handoff.transition(
		handle,
		executionDomainHandoffReturned,
		executionDomainHandoffCompleting,
	) {
		return false
	}
	preemptStore(&handoff.ownerEpoch, 0)
	preemptStore(
		&handoff.state,
		executionDomainHandoffPack(executionDomainHandoffIdle, handle.Generation),
	)
	return true
}

// Idle reports whether no handoff generation owns the domain.
func (handoff *ExecutionDomainHandoff) Idle() bool {
	if handoff == nil {
		return false
	}
	state := preemptLoad(&handoff.state)
	return executionDomainHandoffStatePhase(state) == executionDomainHandoffIdle &&
		preemptLoad(&handoff.ownerEpoch) == 0 &&
		preemptLoad(&handoff.state) == state
}

// Retire permanently closes an idle gate during domain shutdown.
func (handoff *ExecutionDomainHandoff) Retire() bool {
	if handoff == nil || preemptLoad(&handoff.ownerEpoch) != 0 {
		return false
	}
	state := preemptLoad(&handoff.state)
	if executionDomainHandoffStatePhase(state) != executionDomainHandoffIdle {
		return false
	}
	return preemptCompareAndSwap(
		&handoff.state,
		state,
		executionDomainHandoffPack(
			executionDomainHandoffRetired,
			executionDomainHandoffStateGeneration(state),
		),
	)
}

// Retired reports the permanent terminal state.
func (handoff *ExecutionDomainHandoff) Retired() bool {
	if handoff == nil {
		return false
	}
	state := preemptLoad(&handoff.state)
	return executionDomainHandoffStatePhase(state) == executionDomainHandoffRetired &&
		preemptLoad(&handoff.ownerEpoch) == 0 &&
		preemptLoad(&handoff.state) == state
}
