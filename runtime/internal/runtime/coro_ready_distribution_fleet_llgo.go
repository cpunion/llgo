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

func coroTargetReadyDistributionFailV1(message string) bool {
	coroRuntimeAbort(message)
	return false
}

func coroTargetReadyDistributionDomainV1(
	p *coro.P,
	driver *coro.ExecutorDriver,
) (*coroNativeFleetDomainV1, bool) {
	if p == nil || driver == nil {
		return nil, false
	}
	state := &coroNativeFleetV1State
	for index := uint32(0); index < state.domainCount; index++ {
		domain := &state.domains[index]
		if domain.lifecycle == coroNativeFleetDomainActiveV1 &&
			domain.pOwnerV1() == p && domain.driverOwnerV1() == driver {
			return domain, true
		}
	}
	return nil, false
}

// coroTargetAfterStableRunActionV1 is owner-to-owner work distribution, not a
// producer callback. The active domain prefix and every P/driver identity are
// frozen before any peer M starts, and the program coordinator joins all peer
// Ms before route close; the fleet's route producer lease therefore needs no
// additional target-ingress lease around this short publish/request/ring tail.
func coroTargetAfterStableRunActionV1(source *coro.P, driver *coro.ExecutorDriver) bool {
	state := &coroNativeFleetV1State
	if source == nil || driver == nil {
		return coroTargetReadyDistributionFailV1("native ready distribution lacks source owner")
	}
	if coroNativeFleetPhysicalOwnerV1State.stop.Quiesced() {
		// Fleet shutdown is a one-way ownership barrier. A peer action which
		// committed concurrently with program-main return is still valid, but
		// it must not publish another transfer after the stop boundary.
		return true
	}
	if state.lifecycle != coroNativeFleetActiveV1 {
		return coroTargetReadyDistributionFailV1("native ready distribution fleet is not active")
	}
	sourceDomain, ok := coroTargetReadyDistributionDomainV1(source, driver)
	if !ok {
		return coroTargetReadyDistributionFailV1("native ready distribution source route mismatch")
	}
	distribution, distributed := state.fleet.DistributePNeutralRunnable(
		sourceDomain.handle,
		source,
	)
	if !distributed {
		return coroTargetReadyDistributionFailV1("native ready distribution core rejected owner")
	}
	if !distribution.Valid() {
		return true
	}
	target, valid := coroNativeFleetDomainForHandleV1(
		state,
		distribution.Target,
		coroNativeFleetDomainActiveV1,
	)
	if !valid {
		return coroTargetReadyDistributionFailV1("native ready distribution target route mismatch")
	}
	if coroNativeFleetRequestNeedsRingV1(target, distribution.Request) && !target.doorbell.Ring() {
		return coroTargetReadyDistributionFailV1("native ready distribution doorbell failed")
	}
	return true
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

func coroTargetRequestProgramRunnableV1(p *coro.P, driver *coro.ExecutorDriver) bool {
	domain, ok := coroTargetReadyDistributionDomainV1(p, driver)
	return ok && domain.adopted &&
		coroNativeFleetV1State.fleet.RequestPNeutralRunnable(domain.handle, p)
}

func coroTargetBeforeProgramRunSliceV1(p *coro.P, driver *coro.ExecutorDriver) bool {
	domain, valid := coroTargetReadyDistributionDomainV1(p, driver)
	if !valid || !domain.adopted {
		return false
	}
	if _, ok := coroNativeFleetV1State.fleet.CancelPNeutralRunnableRequest(
		domain.handle,
		p,
	); !ok {
		return false
	}
	_, ok := coroTargetDrainProgramTransfersV1(p, driver)
	return ok
}
