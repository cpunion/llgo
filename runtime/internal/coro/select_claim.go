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

// SelectClaim is frame-local arbitration shared by every channel case in one
// logical select. It deliberately carries no pointer or exact identity: the
// source-owned OperationID/ParkTicket pair remains the ABA guard. Acquiring is
// still pre-effect and reversible; Committing is the shared, non-copyable-by-
// value effect permission; Claimed is terminal publication. The extra shared
// state prevents two copies of a stack-local pair transaction from both
// starting the same physical transfer.
type SelectClaim struct {
	state uint32
}

const (
	selectClaimOpen uint32 = iota
	selectClaimAcquiring
	selectClaimCommitting
	selectClaimClaimed
)

var (
	_ [4 - unsafe.Sizeof(SelectClaim{})]byte
	_ [unsafe.Sizeof(SelectClaim{}) - 4]byte
	_ [4 - unsafe.Alignof(SelectClaim{})]byte
	_ [unsafe.Alignof(SelectClaim{}) - 4]byte
)

func selectClaimLoad(claim *SelectClaim) uint32 {
	if claim == nil {
		return selectClaimOpen
	}
	return preemptLoad(&claim.state)
}

func selectClaimOwnerAcquire(claim *SelectClaim) uint32 {
	if claim == nil {
		return selectClaimOpen
	}
	for {
		state := selectClaimLoad(claim)
		if state != selectClaimOpen {
			return state
		}
		if preemptCompareAndSwap(&claim.state, selectClaimOpen, selectClaimAcquiring) {
			return selectClaimOpen
		}
	}
}

func selectClaimOwnerReleasePending(claim *SelectClaim) bool {
	return claim == nil || preemptCompareAndSwap(&claim.state, selectClaimAcquiring, selectClaimOpen)
}

func selectClaimOwnerReleaseTerminal(claim *SelectClaim) bool {
	if claim == nil {
		return true
	}
	return preemptCompareAndSwap(&claim.state, selectClaimAcquiring, selectClaimClaimed)
}

func beginExternalSelectClaimEffect(claim *SelectClaim) bool {
	return claim != nil && preemptCompareAndSwap(&claim.state, selectClaimAcquiring, selectClaimCommitting)
}

func publishExternalSelectClaim(claim *SelectClaim) bool {
	return claim != nil && preemptCompareAndSwap(&claim.state, selectClaimCommitting, selectClaimClaimed)
}

// tryAcquireExternalSelectClaims is the constant-work claim half of a future
// select-to-select channel rendezvous. The hchan lock supplies pair stability;
// address order gives both directions the same CAS order. Both exact endpoint
// admissions must already be held, so every frame access below is covered even
// if an owner concurrently seals and tries to detach. No physical effect may
// occur until acquired is true. A failed second claim restores the first to
// Open before either admission is released; ok=false reports a corrupt
// rollback rather than hiding it as ordinary contention. Pairing a select with
// a single-case slot is added with hchan C1.
func tryAcquireExternalSelectClaims(a, b *SelectClaim) (acquired, ok bool) {
	if a == nil || b == nil || a == b {
		return false, false
	}
	first, second := a, b
	if uintptr(unsafe.Pointer(first)) > uintptr(unsafe.Pointer(second)) {
		first, second = second, first
	}
	if !preemptCompareAndSwap(&first.state, selectClaimOpen, selectClaimAcquiring) {
		return false, true
	}
	if preemptCompareAndSwap(&second.state, selectClaimOpen, selectClaimAcquiring) {
		return true, true
	}
	if !preemptCompareAndSwap(&first.state, selectClaimAcquiring, selectClaimOpen) {
		return false, false
	}
	return false, true
}

func beginExternalSelectClaimsEffect(a, b *SelectClaim) bool {
	if a == nil || b == nil || a == b || selectClaimLoad(a) != selectClaimAcquiring ||
		selectClaimLoad(b) != selectClaimAcquiring {
		return false
	}
	first, second := a, b
	if uintptr(unsafe.Pointer(first)) > uintptr(unsafe.Pointer(second)) {
		first, second = second, first
	}
	if !preemptCompareAndSwap(&first.state, selectClaimAcquiring, selectClaimCommitting) ||
		!preemptCompareAndSwap(&second.state, selectClaimAcquiring, selectClaimCommitting) {
		// The second failure is an invariant break, not contention: both claims
		// were already acquired under admissions. Retain any Committing state and
		// both lifetime leases fail-closed; physical effect has not started.
		return false
	}
	return true
}

func publishExternalSelectClaims(a, b *SelectClaim) bool {
	if a == nil || b == nil || a == b || selectClaimLoad(a) != selectClaimCommitting ||
		selectClaimLoad(b) != selectClaimCommitting {
		return false
	}
	// The physical result and both source mailboxes must be release-published
	// before this helper. Both endpoint admissions remain held through these
	// final frame stores and are released only after this helper returns.
	preemptStore(&a.state, selectClaimClaimed)
	preemptStore(&b.state, selectClaimClaimed)
	return true
}

func rollbackExternalSelectClaims(a, b *SelectClaim) bool {
	if a == nil || b == nil || a == b || selectClaimLoad(a) != selectClaimAcquiring ||
		selectClaimLoad(b) != selectClaimAcquiring {
		return false
	}
	first, second := a, b
	if uintptr(unsafe.Pointer(first)) > uintptr(unsafe.Pointer(second)) {
		first, second = second, first
	}
	// Reverse acquisition order. Admissions remain held until both stores have
	// succeeded, so even a fail-closed partial rollback cannot become a UAF.
	if !preemptCompareAndSwap(&second.state, selectClaimAcquiring, selectClaimOpen) ||
		!preemptCompareAndSwap(&first.state, selectClaimAcquiring, selectClaimOpen) {
		return false
	}
	return true
}
