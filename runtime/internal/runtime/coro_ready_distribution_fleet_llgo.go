//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && llgo_coro_native_fleet && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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
	if coroNativeFleetPhysicalOwnerV1State.stop.Quiesced() {
		// Fleet shutdown is a one-way ownership barrier. No continuation may be
		// transferred after the program coordinator has requested peer drain.
		return true
	}
	sourceIndex := uint32(coroNativeFleetDomainCapacityV1)
	for index := uint32(0); index < coroNativeFleetDomainCapacityV1; index++ {
		domain := &state.domains[index]
		if domain.lifecycle == coroNativeFleetDomainActiveV1 &&
			domain.pOwnerV1() == source && domain.driverOwnerV1() == driver {
			sourceIndex = index
			break
		}
	}
	if sourceIndex >= coroNativeFleetDomainCapacityV1 {
		return false
	}
	if sourceIndex != 0 {
		// The adopted program P still runs through the legacy program-owned
		// compatibility loop and does not yet acquire a fleet owner epoch or drain
		// route 1's transfer mailbox. Keep peer-created children local until that
		// loop is migrated; publishing them back would strand their sole ownership
		// root during program close.
		return true
	}
	targetIndex := coroNativeFleetPeerIndexV1
	target := &state.domains[targetIndex]
	if target.lifecycle != coroNativeFleetDomainActiveV1 || !target.handle.Valid() {
		return false
	}
	_, request, published := state.fleet.PublishInitialReadyHeadAndRequest(target.handle, source)
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
