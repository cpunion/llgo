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

// DriveAdmission serializes one target re-entry domain without retaining a Go
// pointer in the target ABI. The target carries only the published uint32 epoch;
// this object remains at a stable scheduler-owned address.
//
// The owner bit protects every non-atomic scheduler/program field. The upper
// gate bits form a monotonic phase that binds every callback CAS to the epoch
// generation it observed. A matching
// callback that arrives while the owner is still inside target Begin does not
// recurse into the scheduler: it publishes Pending and returns. Before releasing
// ownership, the current driver claims Pending and services the still-published
// epoch. Before reusing the owner for a later epoch it clears the old epoch and
// advances phase, invalidating callbacks delayed before their gate CAS.
//
// Epoch is an atomic admission token, not scheduler continuation state. Clearing
// it makes stale or duplicate host re-entry rejectable without reading or
// poisoning non-atomic lifecycle state.
type DriveAdmission struct {
	gate       uint32
	epoch      uint32
	executor   uint32
	generation uint32
	mode       uint32
}

const (
	driveAdmissionOwned uint32 = 1 << iota
	driveAdmissionPending
	driveAdmissionMask           = driveAdmissionOwned | driveAdmissionPending
	driveAdmissionPhaseIncrement = driveAdmissionMask + 1
	driveAdmissionPhaseMask      = ^driveAdmissionMask
)

type DriveAdmissionResult uint8

const (
	DriveAdmissionInvalid DriveAdmissionResult = iota
	DriveAdmissionAcquired
	DriveAdmissionDeferred
	DriveAdmissionStale
)

// Acquire grants initial begin/run ownership. Continuation callbacks use Enter.
// Only the acquired owner may call PublishEpoch, ClearEpoch, or RevokeEpoch.
func (admission *DriveAdmission) Acquire() bool {
	if admission == nil || preemptLoad(&admission.epoch) != 0 {
		return false
	}
	gate := preemptLoad(&admission.gate)
	return gate&driveAdmissionMask == 0 &&
		preemptCompareAndSwap(&admission.gate, gate, gate|driveAdmissionOwned)
}

// PublishExecutor publishes the immutable POD callback identity for this
// single-start admission domain. Generation is stored first and executor is
// the release/commit word; a reader that observes executor can safely validate
// both words before attempting Enter. Production keeps this identity for the
// lifetime of the static single-start program.
func (admission *DriveAdmission) PublishExecutor(executor, generation uint32) bool {
	if admission == nil || executor == 0 || generation == 0 ||
		preemptLoad(&admission.gate)&driveAdmissionMask != driveAdmissionOwned ||
		preemptLoad(&admission.epoch) != 0 ||
		preemptLoad(&admission.executor) != 0 ||
		!preemptCompareAndSwap(&admission.generation, 0, generation) {
		return false
	}
	if !preemptCompareAndSwap(&admission.executor, 0, executor) {
		_ = preemptCompareAndSwap(&admission.generation, generation, 0)
		return false
	}
	return true
}

// PublishMode commits the immutable callback ABI mode after any executor tuple
// has been initialized and before target start or the first epoch publication.
// Mode is the acquire/commit word checked by every mode-aware callback before
// it may acquire ownership or publish Pending. Repeating the exact mode while
// the owner is active is idempotent; a different mode is rejected.
func (admission *DriveAdmission) PublishMode(mode uint32) bool {
	if admission == nil || mode == 0 ||
		preemptLoad(&admission.gate)&driveAdmissionMask != driveAdmissionOwned ||
		preemptLoad(&admission.epoch) != 0 {
		return false
	}
	current := preemptLoad(&admission.mode)
	return current == mode || (current == 0 && preemptCompareAndSwap(&admission.mode, 0, mode))
}

// PublishEpoch exposes one POD callback token while the scheduler owner is
// active. A prior epoch must be cleared before another is published.
func (admission *DriveAdmission) PublishEpoch(epoch uint32) bool {
	if admission == nil || epoch == 0 {
		return false
	}
	gate := preemptLoad(&admission.gate)
	if gate&driveAdmissionMask != driveAdmissionOwned {
		return false
	}
	if !preemptCompareAndSwap(&admission.epoch, 0, epoch) {
		return false
	}
	// Catch an old callback whose Pending publication is already visible after
	// this CAS. This post-check is only a defensive rejection: an old Enter may
	// still race after the check. Correct E1 -> E2 reuse therefore clears E1 and
	// advances the full gate phase while retaining ownership before publishing
	// E2. A delayed E1 CAS then carries the obsolete phase and cannot alias E2.
	if preemptLoad(&admission.gate) != gate {
		_ = preemptCompareAndSwap(&admission.epoch, epoch, 0)
		return false
	}
	return true
}

// ClearEpoch revokes the exact callback token. A callback that already queued
// Pending is harmless: Finish observes epoch zero and discards that stale hint.
func (admission *DriveAdmission) ClearEpoch(epoch uint32) bool {
	if admission == nil || epoch == 0 || preemptLoad(&admission.gate)&driveAdmissionOwned == 0 {
		return false
	}
	return preemptCompareAndSwap(&admission.epoch, epoch, 0)
}

// AdvancePhase invalidates every callback that observed the just-cleared
// epoch while retaining scheduler ownership. The low two gate bits are state;
// the high 30 bits are a monotonic, non-wrapping phase. A callback may win the
// sole Owned -> Owned|Pending race immediately before this method. In that
// case the second CAS atomically drops the now-stale Pending hint and advances
// the phase, so this operation remains bounded and never waits for that caller.
// Exhaustion fails closed; a future ABI can extend the epoch through reserved
// result words without requiring a non-native 64-bit atomic on wasm32.
func (admission *DriveAdmission) AdvancePhase() bool {
	if admission == nil || preemptLoad(&admission.epoch) != 0 {
		return false
	}
	for attempt := 0; attempt != 2; attempt++ {
		gate := preemptLoad(&admission.gate)
		if gate&driveAdmissionOwned == 0 || gate&driveAdmissionPhaseMask == driveAdmissionPhaseMask {
			return false
		}
		next := ((gate & driveAdmissionPhaseMask) + driveAdmissionPhaseIncrement) | driveAdmissionOwned
		if preemptCompareAndSwap(&admission.gate, gate, next) {
			return true
		}
	}
	return false
}

// RevokeEpoch prevents any further callback admission after a fail-stop path.
// It is owner-only and deliberately accepts an already-clear epoch.
func (admission *DriveAdmission) RevokeEpoch() bool {
	if admission == nil || preemptLoad(&admission.gate)&driveAdmissionOwned == 0 {
		return false
	}
	for {
		epoch := preemptLoad(&admission.epoch)
		if epoch == 0 || preemptCompareAndSwap(&admission.epoch, epoch, 0) {
			return true
		}
	}
}

// releaseStaleEnterOwner releases the owner bit acquired by an idle Enter
// after that callback discovers its epoch was revoked. A second callback may
// have observed the same-phase owner before revocation and publish Pending
// after the stale callback's ownership CAS. At most one such transition can
// race this cleanup, so two exact same-phase CAS attempts cover both
// Owned -> idle and Owned|Pending -> idle without waiting.
//
// Pending is discarded only after the callback epoch domain is terminally
// empty. A different nonzero epoch is a later live publication, not proof that
// this callback owns the gate: clearing that owner would orphan the later
// epoch. A phase change, an unexpected gate state, or any nonzero epoch is
// therefore an invariant failure.
func (admission *DriveAdmission) releaseStaleEnterOwner(epoch, owned uint32) DriveAdmissionResult {
	if admission == nil || epoch == 0 || owned&driveAdmissionMask != driveAdmissionOwned {
		return DriveAdmissionInvalid
	}
	idle := owned & driveAdmissionPhaseMask
	for attempt := 0; attempt != 2; attempt++ {
		gate := preemptLoad(&admission.gate)
		if gate&driveAdmissionPhaseMask != idle {
			return DriveAdmissionInvalid
		}
		switch gate & driveAdmissionMask {
		case driveAdmissionOwned, driveAdmissionOwned | driveAdmissionPending:
		default:
			return DriveAdmissionInvalid
		}
		if preemptLoad(&admission.epoch) != 0 {
			return DriveAdmissionInvalid
		}
		if preemptCompareAndSwap(&admission.gate, gate, idle) {
			return DriveAdmissionStale
		}
	}
	return DriveAdmissionInvalid
}

// Enter either transfers idle ownership to the exact epoch callback, queues a
// coalesced callback for the current owner, or rejects a stale POD token without
// touching scheduler-owned state.
func (admission *DriveAdmission) Enter(epoch uint32) DriveAdmissionResult {
	if admission == nil || epoch == 0 {
		return DriveAdmissionStale
	}
	for {
		// The full phase snapshot is bracketed by exact epoch reads. A callback
		// delayed after either read can only CAS that observed phase; an E1 -> E2
		// AdvancePhase makes the CAS fail before it can become owner or Pending.
		if preemptLoad(&admission.epoch) != epoch {
			return DriveAdmissionStale
		}
		gate := preemptLoad(&admission.gate)
		flags := gate & driveAdmissionMask
		if flags == driveAdmissionPending {
			return DriveAdmissionInvalid
		}
		if preemptLoad(&admission.epoch) != epoch {
			return DriveAdmissionStale
		}
		if flags == 0 {
			owned := gate | driveAdmissionOwned
			if !preemptCompareAndSwap(&admission.gate, gate, owned) {
				continue
			}
			// Another callback may have consumed a terminal epoch and released the
			// same phase after the pre-CAS check. Recheck after becoming owner. A
			// callback delayed behind that former owner may still publish Pending
			// against this same-phase owner, so cleanup handles both owner shapes.
			if preemptLoad(&admission.epoch) != epoch {
				return admission.releaseStaleEnterOwner(epoch, owned)
			}
			return DriveAdmissionAcquired
		}
		if flags == driveAdmissionOwned|driveAdmissionPending {
			return DriveAdmissionDeferred
		}
		if flags != driveAdmissionOwned {
			return DriveAdmissionInvalid
		}
		if preemptCompareAndSwap(&admission.gate, gate, gate|driveAdmissionPending) {
			return DriveAdmissionDeferred
		}
	}
}

// EnterMode validates the immutable ABI mode before Enter can mutate gate.
func (admission *DriveAdmission) EnterMode(mode, epoch uint32) DriveAdmissionResult {
	if admission == nil || mode == 0 || preemptLoad(&admission.mode) != mode {
		return DriveAdmissionStale
	}
	return admission.Enter(epoch)
}

// EnterExecutor rejects a wrong immutable executor tuple before it can acquire
// ownership or set the untagged Pending bit. Executor is the acquire/commit
// word: generation is read only after the exact committed executor is visible.
// The identity must remain immutable until all target callbacks have strongly
// joined; callers must not preflight against scheduler-owned non-atomic state.
func (admission *DriveAdmission) EnterExecutor(executor, generation, epoch uint32) DriveAdmissionResult {
	if admission == nil || executor == 0 || generation == 0 ||
		preemptLoad(&admission.executor) != executor ||
		preemptLoad(&admission.generation) != generation {
		return DriveAdmissionStale
	}
	return admission.Enter(epoch)
}

// EnterExecutorMode validates the mode commit word first, then the immutable
// tuple it publishes, before Enter can mutate gate. This ordering prevents a
// cross-ABI or wrong-executor callback from injecting an untagged Pending bit.
func (admission *DriveAdmission) EnterExecutorMode(executor, generation, mode, epoch uint32) DriveAdmissionResult {
	if admission == nil || mode == 0 || preemptLoad(&admission.mode) != mode ||
		executor == 0 || generation == 0 ||
		preemptLoad(&admission.executor) != executor ||
		preemptLoad(&admission.generation) != generation {
		return DriveAdmissionStale
	}
	return admission.Enter(epoch)
}

// CanRelease reports that no scheduler owner or callback epoch remains.
// It deliberately ignores the immutable executor identity retained by a
// single-start program and therefore does not assert that the object is all
// zero or safe to recycle for a different executor.
func (admission *DriveAdmission) CanRelease() bool {
	return admission != nil && preemptLoad(&admission.gate)&driveAdmissionMask == 0 &&
		preemptLoad(&admission.epoch) == 0
}

// ResetExecutorAfterStrongJoin clears an exact immutable callback identity.
// The caller must have strongly joined every target source that could know the
// tuple. This method can check only the local admission-zero precondition; it
// cannot prove the external strong join. Production's static program is
// single-start and intentionally never calls this method.
func (admission *DriveAdmission) ResetExecutorAfterStrongJoin(executor, generation uint32) bool {
	if admission == nil || executor == 0 || generation == 0 ||
		!admission.CanRelease() ||
		preemptLoad(&admission.executor) != executor ||
		preemptLoad(&admission.generation) != generation ||
		!preemptCompareAndSwap(&admission.executor, executor, 0) {
		return false
	}
	return preemptCompareAndSwap(&admission.generation, generation, 0)
}

// ResetModeAfterStrongJoin clears the immutable mode commit word. It has the
// same external strong-join precondition as ResetExecutorAfterStrongJoin and
// is unused by the production single-start program.
func (admission *DriveAdmission) ResetModeAfterStrongJoin(mode uint32) bool {
	return admission != nil && mode != 0 && admission.CanRelease() &&
		preemptCompareAndSwap(&admission.mode, mode, 0)
}

// Finish either claims one coalesced callback while retaining ownership, or
// atomically releases ownership. pending=false means ownership was released.
// An epoch of zero with pending=true is a stale hint queued just before revoke.
func (admission *DriveAdmission) Finish() (epoch uint32, pending bool, ok bool) {
	if admission == nil {
		return 0, false, false
	}
	for {
		gate := preemptLoad(&admission.gate)
		switch gate & driveAdmissionMask {
		case driveAdmissionOwned:
			if preemptCompareAndSwap(&admission.gate, gate, gate&driveAdmissionPhaseMask) {
				return 0, false, true
			}
		case driveAdmissionOwned | driveAdmissionPending:
			if preemptCompareAndSwap(&admission.gate, gate, gate&^driveAdmissionPending) {
				return preemptLoad(&admission.epoch), true, true
			}
		default:
			return 0, false, false
		}
	}
}

// CanRecycle is a strict all-zero assertion for tests and static teardown. An
// admission with a published executor identity cannot be recycled until the
// target has strongly joined and ResetExecutorAfterStrongJoin has cleared it.
// Neither CanRelease nor CanRecycle is a concurrent callback probe.
func (admission *DriveAdmission) CanRecycle() bool {
	return admission != nil && admission.CanRelease() &&
		preemptLoad(&admission.executor) == 0 && preemptLoad(&admission.generation) == 0 &&
		preemptLoad(&admission.mode) == 0 && preemptLoad(&admission.gate)&driveAdmissionPhaseMask == 0
}
