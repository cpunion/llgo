//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

import "github.com/goplus/llgo/runtime/internal/coro"

func coroTargetReadySpawnDomainV1(parent *coro.G) (*coroNativeFleetDomainV1, bool) {
	driver, handle, route, ok := coro.CurrentExecutorDriver(parent)
	if !ok || !route.Valid() {
		return nil, false
	}
	domain, valid := coroNativeFleetActiveDomainForRouteV1(route)
	if !valid || domain.driverOwnerV1() != driver || domain.handle.Executor != handle {
		return nil, false
	}
	return domain, true
}

// coroTargetCanRecordReadySpawnV1 performs every target-specific validation
// before CommitSpawn publishes the child to the scheduler. An occupied hint is
// deliberately valid: the new child remains on the owner's ordinary FIFO.
func coroTargetCanRecordReadySpawnV1(parent *coro.G) bool {
	_, ok := coroTargetReadySpawnDomainV1(parent)
	return ok
}

// coroTargetRecordReadySpawnV1 records causal provenance, not permanent task
// affinity. CommitSpawn cannot change the running owner's frozen route/domain
// identity, so losing it here is an invariant violation rather than a
// recoverable post-publication failure. A second spawn in the same physical
// resume remains local; this keeps the hint O(1).
func coroTargetRecordReadySpawnV1(parent, child *coro.G) {
	domain, ok := coroTargetReadySpawnDomainV1(parent)
	if !ok || child == nil {
		coroRuntimeAbort("native coroutine spawn owner changed after commit")
		return
	}
	if domain.readySpawn == nil {
		domain.readySpawn = child
	}
}

// coroTargetAfterStableRunActionV1 is owner-to-owner work distribution, not a
// producer callback. The two domains and their P/driver identities are frozen
// before either physical M starts, and the program coordinator joins both Ms
// before route close; the fleet's route producer lease therefore needs no
// additional target-ingress lease around this short publish/request/ring tail.
func coroTargetAfterStableRunActionV1(source *coro.P, driver *coro.ExecutorDriver) bool {
	state := &coroNativeFleetV1State
	if state.lifecycle != coroNativeFleetActiveV1 || source == nil || driver == nil {
		return false
	}
	sourceIndex := uint32(coroNativeFleetDomainCapacityV1)
	var sourceDomain *coroNativeFleetDomainV1
	for index := uint32(0); index < coroNativeFleetDomainCapacityV1; index++ {
		domain := &state.domains[index]
		if domain.lifecycle == coroNativeFleetDomainActiveV1 &&
			domain.pOwnerV1() == source && domain.driverOwnerV1() == driver {
			sourceIndex = index
			sourceDomain = domain
			break
		}
	}
	if sourceIndex >= coroNativeFleetDomainCapacityV1 || sourceDomain == nil {
		return false
	}
	candidate := sourceDomain.readySpawn
	sourceDomain.readySpawn = nil
	if candidate == nil {
		return true
	}
	if coroNativeFleetPhysicalOwnerV1State.stop.Quiesced() {
		// Fleet shutdown is a one-way ownership barrier. No continuation may be
		// transferred after the program coordinator has requested peer drain.
		return true
	}
	targetIndex := uint32(0)
	if sourceIndex == 0 {
		targetIndex = coroNativeFleetPeerIndexV1
	}
	target := &state.domains[targetIndex]
	if target.lifecycle != coroNativeFleetDomainActiveV1 || !target.handle.Valid() {
		return false
	}
	_, request, published := state.fleet.PublishPNeutralRunnableAndRequest(target.handle, source, candidate)
	if !published {
		// No initial head, a contended bounded mailbox, or a full mailbox simply
		// retains ordinary FIFO execution on the current P.
		return request == coro.ExecutorRequestInvalid || request == coro.ExecutorRequestClosed
	}
	accepted := request == coro.ExecutorRequestPublished ||
		request == coro.ExecutorRequestCoalesced || request == coro.ExecutorRequestIdleWake
	if !accepted {
		return false
	}
	return !coro.ExecutorRequestNeedsDoorbell(request) || target.doorbell.Ring()
}

// coroTargetDrainProgramTransfersV1 imports the adopted route-1 mailbox while
// the program's existing DriveAdmission owns its P. A separate fleet owner
// epoch would duplicate that serialization and conflict with the host ABI.
// Contention or a bounded reducer's pending physical action is ordinary
// retryable work; corrupt identity is the only failure.
func coroTargetDrainProgramTransfersV1(p *coro.P, driver *coro.ExecutorDriver) (more, ok bool) {
	state := &coroNativeFleetV1State
	target := &coroNativeFleetTargetV1State
	if state.lifecycle != coroNativeFleetActiveV1 ||
		target.lifecycle != coroNativeFleetTargetActiveV1 || !target.program.Valid() {
		return false, false
	}
	domain, valid := coroNativeFleetDomainForHandleV1(
		state,
		target.program,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || domain.pOwnerV1() != p || domain.driverOwnerV1() != driver {
		return false, false
	}
	moved, pending, status := state.fleet.TryDrainPNeutralRunnables(
		target.program,
		p,
		coro.RunnableTransferMailboxCapacity,
	)
	switch status {
	case coro.RunnableTransferDrainComplete:
		return moved != 0 || pending, true
	case coro.RunnableTransferDrainOwnerUnstable, coro.RunnableTransferDrainContended:
		return true, true
	default:
		return false, false
	}
}

func coroTargetBeforeProgramRunSliceV1(p *coro.P, driver *coro.ExecutorDriver) bool {
	_, ok := coroTargetDrainProgramTransfersV1(p, driver)
	return ok
}
