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
// The owner bit protects every non-atomic scheduler/program field. A matching
// callback that arrives while the owner is still inside target Begin does not
// recurse into the scheduler: it publishes Pending and returns. Before releasing
// ownership, the current driver claims Pending and services the still-published
// epoch. This closes both the Begin-before-return and owner-release races.
//
// Epoch is an atomic admission token, not scheduler continuation state. Clearing
// it makes stale or duplicate host re-entry rejectable without reading or
// poisoning non-atomic lifecycle state.
type DriveAdmission struct {
	gate  uint32
	epoch uint32
}

const (
	driveAdmissionOwned uint32 = 1 << iota
	driveAdmissionPending
	driveAdmissionMask = driveAdmissionOwned | driveAdmissionPending
)

type DriveAdmissionResult uint8

const (
	DriveAdmissionInvalid DriveAdmissionResult = iota
	DriveAdmissionAcquired
	DriveAdmissionDeferred
	DriveAdmissionStale
)

// Acquire grants initial begin/run ownership. Continuation callbacks use Enter.
func (admission *DriveAdmission) Acquire() bool {
	if admission == nil || preemptLoad(&admission.epoch) != 0 ||
		!preemptCompareAndSwap(&admission.gate, 0, driveAdmissionOwned) {
		return false
	}
	if preemptLoad(&admission.epoch) != 0 {
		_ = preemptCompareAndSwap(&admission.gate, driveAdmissionOwned, 0)
		return false
	}
	return true
}

// PublishEpoch exposes one POD callback token while the scheduler owner is
// active. A prior epoch must be cleared before another is published.
func (admission *DriveAdmission) PublishEpoch(epoch uint32) bool {
	if admission == nil || epoch == 0 || preemptLoad(&admission.gate)&driveAdmissionOwned == 0 {
		return false
	}
	return preemptCompareAndSwap(&admission.epoch, 0, epoch)
}

// ClearEpoch revokes the exact callback token. A callback that already queued
// Pending is harmless: Finish observes epoch zero and discards that stale hint.
func (admission *DriveAdmission) ClearEpoch(epoch uint32) bool {
	if admission == nil || epoch == 0 || preemptLoad(&admission.gate)&driveAdmissionOwned == 0 {
		return false
	}
	return preemptCompareAndSwap(&admission.epoch, epoch, 0)
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

// Enter either transfers idle ownership to the exact epoch callback, queues a
// coalesced callback for the current owner, or rejects a stale POD token without
// touching scheduler-owned state.
func (admission *DriveAdmission) Enter(epoch uint32) DriveAdmissionResult {
	if admission == nil || epoch == 0 || preemptLoad(&admission.epoch) != epoch {
		return DriveAdmissionStale
	}
	for {
		gate := preemptLoad(&admission.gate)
		if gate&^driveAdmissionMask != 0 || gate == driveAdmissionPending {
			return DriveAdmissionInvalid
		}
		if gate == 0 {
			if !preemptCompareAndSwap(&admission.gate, 0, driveAdmissionOwned) {
				continue
			}
			// Epoch can be revoked by the old owner immediately before it releases
			// the gate. Recheck only after this callback owns the scheduler.
			if preemptLoad(&admission.epoch) != epoch {
				if !preemptCompareAndSwap(&admission.gate, driveAdmissionOwned, 0) {
					return DriveAdmissionInvalid
				}
				return DriveAdmissionStale
			}
			return DriveAdmissionAcquired
		}
		if preemptLoad(&admission.epoch) != epoch {
			return DriveAdmissionStale
		}
		if gate&driveAdmissionPending != 0 {
			return DriveAdmissionDeferred
		}
		if preemptCompareAndSwap(&admission.gate, driveAdmissionOwned, driveAdmissionOwned|driveAdmissionPending) {
			return DriveAdmissionDeferred
		}
	}
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
		switch gate {
		case driveAdmissionOwned:
			if preemptCompareAndSwap(&admission.gate, driveAdmissionOwned, 0) {
				return 0, false, true
			}
		case driveAdmissionOwned | driveAdmissionPending:
			if preemptCompareAndSwap(&admission.gate, gate, driveAdmissionOwned) {
				return preemptLoad(&admission.epoch), true, true
			}
		default:
			return 0, false, false
		}
	}
}

// CanRelease is a strict zero-state assertion for tests and static teardown.
// It is scheduler-owner-only after the target has strong-joined callback ingress
// (or before ingress starts); it is not a concurrent callback probe.
func (admission *DriveAdmission) CanRelease() bool {
	return admission != nil && preemptLoad(&admission.gate) == 0 && preemptLoad(&admission.epoch) == 0
}
