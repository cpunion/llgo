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

import "unsafe"

// NonblockingCapability identifies the bounded attempt protected by a
// NonblockingLease. Capabilities are orthogonal to the backend that eventually
// waits for readiness or completion.
type NonblockingCapability uint32

const (
	NonblockingReadAttempt NonblockingCapability = 1 << iota
	NonblockingWriteAttempt
	NonblockingAcceptAttempt
	NonblockingConnectAttempt
)

const nonblockingCapabilityMask = NonblockingReadAttempt | NonblockingWriteAttempt |
	NonblockingAcceptAttempt | NonblockingConnectAttempt

func validNonblockingCapabilities(capabilities NonblockingCapability) bool {
	return capabilities != 0 && capabilities&^nonblockingCapabilityMask == 0
}

type nonblockingLeaseLifecycle uint64

const (
	nonblockingLeaseUnused nonblockingLeaseLifecycle = iota
	nonblockingLeaseActive
	nonblockingLeaseTransition
	nonblockingLeaseRetired
)

// State is one atomic uint64 so a release compares generation and admission in
// the same linearization operation. Splitting those fields permits a paused
// stale releaser to observe an old generation and later CAS an equal token that
// has been reused by a new generation.
//
//	63                         32 31       30 29 28                 0
//	+---------------------------+-----------+--+--------------------+
//	|       generation          | lifecycle |H |       token        |
//	+---------------------------+-----------+--+--------------------+
//
// Transition is also the admission seal: Acquire accepts Active only, while an
// exact old holder may still Release from either Active or Transition.
const (
	nonblockingLeaseTokenBits       = 29
	nonblockingLeaseTokenMask       = uint64(1<<nonblockingLeaseTokenBits) - 1
	nonblockingLeaseHeld            = uint64(1 << nonblockingLeaseTokenBits)
	nonblockingLeaseLifecycleShift  = nonblockingLeaseTokenBits + 1
	nonblockingLeaseLifecycleMask   = uint64(3 << nonblockingLeaseLifecycleShift)
	nonblockingLeaseGenerationShift = 32
)

func nonblockingLeasePackState(
	lifecycle nonblockingLeaseLifecycle,
	generation uint32,
	token uint32,
	held bool,
) uint64 {
	state := uint64(generation)<<nonblockingLeaseGenerationShift |
		uint64(lifecycle)<<nonblockingLeaseLifecycleShift |
		uint64(token)&nonblockingLeaseTokenMask
	if held {
		state |= nonblockingLeaseHeld
	}
	return state
}

func nonblockingLeaseStateLifecycle(state uint64) nonblockingLeaseLifecycle {
	return nonblockingLeaseLifecycle((state & nonblockingLeaseLifecycleMask) >> nonblockingLeaseLifecycleShift)
}

func nonblockingLeaseStateGeneration(state uint64) uint32 {
	return uint32(state >> nonblockingLeaseGenerationShift)
}

func nonblockingLeaseStateToken(state uint64) uint32 {
	return uint32(state & nonblockingLeaseTokenMask)
}

func nonblockingLeaseStateHeld(state uint64) bool {
	return state&nonblockingLeaseHeld != 0
}

func nonblockingLeaseGateAligned(gate *NonblockingLeaseGate) bool {
	return gate != nil && uintptr(unsafe.Pointer(&gate.state))&7 == 0
}

func nonblockingLeaseGateUsable(gate *NonblockingLeaseGate) bool {
	// Keep the target capability test first. An uncertified target must fail
	// before reaching any i64 atomic, even when the address happens to be aligned.
	return nonblockingLeaseAtomic64Bounded && nonblockingLeaseGateAligned(gate)
}

// nonblockingLeaseNoCopy follows the convention recognized by go vet's
// copylocks analyzer. Lock and Unlock are markers only; they are never called.
type nonblockingLeaseNoCopy struct{}

func (*nonblockingLeaseNoCopy) Lock()   {}
func (*nonblockingLeaseNoCopy) Unlock() {}

// NonblockingLeaseGate owns one resource generation and one exclusive bounded
// attempt. Exclusive admission matches Go's per-direction poll.FD lock and has
// an important safety property: a copied/duplicate lease cannot release some
// other holder's admission. A gate must not be copied after first use; its
// address is part of every lease identity and its storage must remain stable
// through retirement.
//
// State is first to make naturally allocated gates easy to align, but every
// entry still checks the actual address: Go permits only four-byte uint64
// alignment on some 32-bit targets and an embedded gate may otherwise be
// unsuitable for atomic64. The resource and capabilities fields are immutable
// within one Active generation. BeginChange atomically withdraws Active.
// FinishChange cannot mutate them until the old admitted attempt has released,
// and its state CAS publishes the replacement generation before it can be
// acquired. No pointer from this object crosses a coroutine suspension or
// platform callback.
type NonblockingLeaseGate struct {
	state        uint64
	noCopy       nonblockingLeaseNoCopy
	resource     uintptr
	capabilities uint32
}

// NonblockingLease is an immutable, stack-local proof for one exact gate
// generation. Its fields are private, so another package cannot forge a proof.
// A value may be copied, but the gate admits at most one holder and compares
// the complete generation/token identity atomically: exactly one copy can
// release, and every duplicate or stale release fails without effect. Callers
// must not suspend, publish, or box a live lease.
type NonblockingLease struct {
	gate         *NonblockingLeaseGate
	resource     uintptr
	generation   uint32
	capabilities NonblockingCapability
	admission    uint32
}

// Init publishes the first generation of an unused gate. It is owner-only.
func (gate *NonblockingLeaseGate) Init(resource uintptr, capabilities NonblockingCapability) (uint32, bool) {
	if !nonblockingLeaseGateUsable(gate) || !validNonblockingCapabilities(capabilities) ||
		preemptLoad64(&gate.state) != 0 {
		return 0, false
	}
	preemptStoreWord(&gate.resource, resource)
	preemptStore(&gate.capabilities, uint32(capabilities))
	preemptStore64(
		&gate.state,
		nonblockingLeasePackState(nonblockingLeaseActive, 1, 0, false),
	)
	return 1, true
}

// acquireNonblockingLeaseAdmission issues a unique nonzero attempt token and
// installs it as the sole holder. Token exhaustion advances generation and
// issues token one in the same CAS. Generation overflow permanently retires
// the gate. Contention fails immediately so this inline path stays bounded.
func acquireNonblockingLeaseAdmission(gate *NonblockingLeaseGate, state uint64) (generation, token uint32, ok bool) {
	if !nonblockingLeaseGateUsable(gate) {
		return 0, 0, false
	}
	if nonblockingLeaseStateLifecycle(state) != nonblockingLeaseActive ||
		nonblockingLeaseStateHeld(state) {
		return 0, 0, false
	}
	generation = nonblockingLeaseStateGeneration(state)
	token = nonblockingLeaseStateToken(state)
	if generation == 0 {
		retired := nonblockingLeasePackState(nonblockingLeaseRetired, 0, token, false)
		_ = preemptCompareAndSwap64(&gate.state, state, retired)
		return 0, 0, false
	}
	if uint64(token) == nonblockingLeaseTokenMask {
		if generation == ^uint32(0) {
			retired := nonblockingLeasePackState(nonblockingLeaseRetired, generation, token, false)
			_ = preemptCompareAndSwap64(&gate.state, state, retired)
			return 0, 0, false
		}
		generation++
		token = 1
	} else {
		token++
	}
	next := nonblockingLeasePackState(nonblockingLeaseActive, generation, token, true)
	if !preemptCompareAndSwap64(&gate.state, state, next) {
		return 0, 0, false
	}
	return generation, token, true
}

// Acquire obtains the exact active generation if resource and every requested
// capability match. It returns an immutable value proof, never waits and never
// heap-allocates. Keeping the proof in SSA values avoids the hidden heap
// allocation caused by passing &lease through a plain wrapper. The complete-state CAS
// either precedes an owner transition, whose quiescence then joins this lease,
// or loses to it and fails without acquiring anything.
func (gate *NonblockingLeaseGate) Acquire(
	resource uintptr,
	required NonblockingCapability,
) (NonblockingLease, bool) {
	if !nonblockingLeaseGateUsable(gate) || !validNonblockingCapabilities(required) {
		return NonblockingLease{}, false
	}
	// Resource and capabilities are sampled between one complete state load and
	// its admission CAS. The CAS proves that no owner transition changed their
	// generation while the snapshot was being checked.
	state := preemptLoad64(&gate.state)
	if nonblockingLeaseStateLifecycle(state) != nonblockingLeaseActive ||
		nonblockingLeaseStateHeld(state) {
		return NonblockingLease{}, false
	}
	resourceSnapshot := preemptLoadWord(&gate.resource)
	capabilities := NonblockingCapability(preemptLoad(&gate.capabilities))
	if resourceSnapshot != resource || capabilities&required != required {
		return NonblockingLease{}, false
	}
	generation, admission, acquired := acquireNonblockingLeaseAdmission(gate, state)
	if !acquired {
		return NonblockingLease{}, false
	}
	return NonblockingLease{
		gate:         gate,
		resource:     resource,
		generation:   generation,
		capabilities: required,
		admission:    admission,
	}, true
}

// Valid reports whether lease still names its exact active attempt. It is a
// diagnostic/defensive check, not permission to carry the lease across a
// suspension: BeginChange cannot finish while the lease remains held.
func (lease NonblockingLease) Valid(resource uintptr, required NonblockingCapability) bool {
	gate := lease.gate
	if !nonblockingLeaseGateUsable(gate) || !validNonblockingCapabilities(required) ||
		lease.resource != resource || lease.capabilities&required != required {
		return false
	}
	admission := lease.admission
	if admission == 0 {
		return false
	}
	expected := nonblockingLeasePackState(
		nonblockingLeaseActive,
		lease.generation,
		admission,
		true,
	)
	if preemptLoad64(&gate.state) != expected {
		return false
	}
	return preemptLoadWord(&gate.resource) == resource &&
		NonblockingCapability(preemptLoad(&gate.capabilities))&required == required
}

// releaseNonblockingLeaseAdmission clears Held only when generation and token
// match in the same uint64 CAS. Active and Transition both accept the exact old
// holder; every other lifecycle and every stale identity fail without effect.
// A fixed second CAS covers the sole valid conflict: BeginChange may change the
// exact Active state to Transition between the first load and CAS. There is no
// retry loop, so the lease span remains statically bounded and no-preempt.
func releaseNonblockingLeaseAdmission(gate *NonblockingLeaseGate, generation, admission uint32) bool {
	if !nonblockingLeaseGateUsable(gate) || generation == 0 || admission == 0 ||
		uint64(admission) > nonblockingLeaseTokenMask {
		return false
	}
	state := preemptLoad64(&gate.state)
	return releaseNonblockingLeaseAdmissionFromState(gate, generation, admission, state)
}

func releaseNonblockingLeaseAdmissionFromState(
	gate *NonblockingLeaseGate,
	generation,
	admission uint32,
	state uint64,
) bool {
	if !nonblockingLeaseGateUsable(gate) || generation == 0 || admission == 0 ||
		uint64(admission) > nonblockingLeaseTokenMask {
		return false
	}
	lifecycle := nonblockingLeaseStateLifecycle(state)
	if lifecycle != nonblockingLeaseActive && lifecycle != nonblockingLeaseTransition {
		return false
	}
	if nonblockingLeaseStateGeneration(state) != generation ||
		nonblockingLeaseStateToken(state) != admission ||
		!nonblockingLeaseStateHeld(state) {
		return false
	}
	if preemptCompareAndSwap64(&gate.state, state, state&^nonblockingLeaseHeld) {
		return true
	}
	if lifecycle != nonblockingLeaseActive {
		return false
	}
	state = preemptLoad64(&gate.state)
	if nonblockingLeaseStateLifecycle(state) != nonblockingLeaseTransition ||
		nonblockingLeaseStateGeneration(state) != generation ||
		nonblockingLeaseStateToken(state) != admission ||
		!nonblockingLeaseStateHeld(state) {
		return false
	}
	return preemptCompareAndSwap64(&gate.state, state, state&^nonblockingLeaseHeld)
}

// Release attempts to consume one immutable lease proof. The gate's complete
// state CAS, rather than mutation of the caller's copy, is the linearization
// point: duplicate and stale values can never affect a current generation even
// when that generation has reused the same token.
func (lease NonblockingLease) Release() bool {
	gate := lease.gate
	if gate == nil {
		return false
	}
	admission := lease.admission
	if admission == 0 {
		return false
	}
	return releaseNonblockingLeaseAdmission(gate, lease.generation, admission)
}

// BeginChange withdraws one exact active generation and seals new admission.
// The owner may then poll ChangeQuiesced; it must not spin while owning the sole
// executor. A syscall such as SetBlocking performs its physical mutation after
// ChangeQuiesced and before FinishChange publishes the replacement generation.
func (gate *NonblockingLeaseGate) BeginChange(expectedGeneration uint32) bool {
	if !nonblockingLeaseGateUsable(gate) || expectedGeneration == 0 {
		return false
	}
	state := preemptLoad64(&gate.state)
	if nonblockingLeaseStateLifecycle(state) != nonblockingLeaseActive ||
		nonblockingLeaseStateGeneration(state) != expectedGeneration {
		return false
	}
	next := nonblockingLeasePackState(
		nonblockingLeaseTransition,
		expectedGeneration,
		nonblockingLeaseStateToken(state),
		nonblockingLeaseStateHeld(state),
	)
	return preemptCompareAndSwap64(&gate.state, state, next)
}

// ChangeQuiesced reports that the exact withdrawn generation has no admitted
// attempt. Transition itself seals the gate, so the owner may now change
// OS/HAL/host state without a new inline attempt racing the mutation.
func (gate *NonblockingLeaseGate) ChangeQuiesced(expectedGeneration uint32) bool {
	if !nonblockingLeaseGateUsable(gate) || expectedGeneration == 0 {
		return false
	}
	state := preemptLoad64(&gate.state)
	return nonblockingLeaseStateLifecycle(state) == nonblockingLeaseTransition &&
		nonblockingLeaseStateGeneration(state) == expectedGeneration &&
		!nonblockingLeaseStateHeld(state)
}

// FinishChange publishes a fresh active generation after the old attempt is
// quiescent. Generation overflow permanently retires the gate and fails
// closed; resource identity can therefore never wrap onto a stale lease.
func (gate *NonblockingLeaseGate) FinishChange(
	expectedGeneration uint32,
	resource uintptr,
	capabilities NonblockingCapability,
) (uint32, bool) {
	if !nonblockingLeaseGateUsable(gate) || expectedGeneration == 0 ||
		!validNonblockingCapabilities(capabilities) {
		return 0, false
	}
	state := preemptLoad64(&gate.state)
	if nonblockingLeaseStateLifecycle(state) != nonblockingLeaseTransition ||
		nonblockingLeaseStateGeneration(state) != expectedGeneration ||
		nonblockingLeaseStateHeld(state) {
		return 0, false
	}
	// Held with token zero is reserved for the publishing owner. Ordinary
	// admissions always have a nonzero token, so neither a stale Release nor any
	// other protocol entry can clear or acquire this claim. Claiming before the
	// field stores also means two mistaken concurrent owners cannot publish a
	// mixed resource/capability generation.
	claim := nonblockingLeasePackState(
		nonblockingLeaseTransition,
		expectedGeneration,
		0,
		true,
	)
	if !preemptCompareAndSwap64(&gate.state, state, claim) {
		return 0, false
	}

	nextGeneration := expectedGeneration + 1
	if nextGeneration == 0 {
		retired := nonblockingLeasePackState(
			nonblockingLeaseRetired,
			expectedGeneration,
			0,
			false,
		)
		if !preemptCompareAndSwap64(&gate.state, claim, retired) {
			// Every public entry treats the publishing claim as sealed. A strong
			// CAS failure therefore leaves the gate unavailable rather than
			// overwriting state owned by an unknown actor.
			return 0, false
		}
		preemptStore(&gate.capabilities, 0)
		preemptStoreWord(&gate.resource, 0)
		return 0, false
	}
	preemptStoreWord(&gate.resource, resource)
	preemptStore(&gate.capabilities, uint32(capabilities))
	next := nonblockingLeasePackState(nonblockingLeaseActive, nextGeneration, 0, false)
	if !preemptCompareAndSwap64(&gate.state, claim, next) {
		// Never repair this with a blind store: it could erase a newer holder if
		// the owner invariant were broken. Valid protocol actors cannot mutate the
		// claim, so a failure remains unavailable and requires fail-stop handling.
		return 0, false
	}
	return nextGeneration, true
}

// FinishRetire permanently closes a quiescent transition. Retired storage is
// a tombstone and cannot be initialized or reused, so stale generations remain
// invalid even when a platform recycles the same integer resource.
func (gate *NonblockingLeaseGate) FinishRetire(expectedGeneration uint32) bool {
	if !nonblockingLeaseGateUsable(gate) || expectedGeneration == 0 {
		return false
	}
	state := preemptLoad64(&gate.state)
	if nonblockingLeaseStateLifecycle(state) != nonblockingLeaseTransition ||
		nonblockingLeaseStateGeneration(state) != expectedGeneration ||
		nonblockingLeaseStateHeld(state) {
		return false
	}
	retired := nonblockingLeasePackState(
		nonblockingLeaseRetired,
		expectedGeneration,
		nonblockingLeaseStateToken(state),
		false,
	)
	if !preemptCompareAndSwap64(&gate.state, state, retired) {
		return false
	}
	preemptStore(&gate.capabilities, 0)
	preemptStoreWord(&gate.resource, 0)
	return true
}

// Generation returns the current nonzero identity while Active or in a
// transition. A retired or malformed gate has no reusable generation.
func (gate *NonblockingLeaseGate) Generation() (uint32, bool) {
	if !nonblockingLeaseGateUsable(gate) {
		return 0, false
	}
	state := preemptLoad64(&gate.state)
	switch nonblockingLeaseStateLifecycle(state) {
	case nonblockingLeaseActive, nonblockingLeaseTransition:
		generation := nonblockingLeaseStateGeneration(state)
		return generation, generation != 0
	default:
		return 0, false
	}
}
