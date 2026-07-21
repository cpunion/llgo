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

type coroNativeFleetTargetLifecycleV1 uint8

const (
	coroNativeFleetTargetUnusedV1 coroNativeFleetTargetLifecycleV1 = iota
	coroNativeFleetTargetActiveV1
	coroNativeFleetTargetClosingV1
	coroNativeFleetTargetRetiredV1
	coroNativeFleetTargetFailedV1
)

type coroNativeFleetTargetStateV1 struct {
	program   coro.ExecutorFleetHandle
	waitEpoch uint32
	runEpoch  uint32
	lifecycle coroNativeFleetTargetLifecycleV1
}

var coroNativeFleetTargetV1State coroNativeFleetTargetStateV1

func coroNativeFleetActiveDomainForRouteV1(route coro.RouteID) (*coroNativeFleetDomainV1, bool) {
	if coroNativeFleetTargetV1State.lifecycle != coroNativeFleetTargetActiveV1 ||
		!route.Valid() || uint32(route) > coroNativeFleetDomainCapacityV1 {
		return nil, false
	}
	domain := &coroNativeFleetV1State.domains[uint32(route)-1]
	if domain.lifecycle != coroNativeFleetDomainActiveV1 ||
		domain.handle.Route != uint32(route) || !domain.handle.Valid() {
		return nil, false
	}
	return domain, true

}

func coroNativeFleetActiveProgramDomainV1(handle coro.ExecutorHandle) (*coroNativeFleetDomainV1, bool) {
	state := &coroNativeFleetTargetV1State
	if state.lifecycle != coroNativeFleetTargetActiveV1 || state.program.Executor != handle ||
		!state.program.Valid() {
		return nil, false
	}
	domain, ok := coroNativeFleetActiveDomainForRouteV1(coro.RouteID(state.program.Route))
	return domain, ok && domain.handle == state.program
}

func coroTargetBeginExecutorRunV2(handle coro.ExecutorHandle, epoch uint32) coroTargetRunRequestResultV2 {
	state := &coroNativeFleetTargetV1State
	if state.lifecycle != coroNativeFleetTargetActiveV1 || state.program.Executor != handle ||
		epoch == 0 || state.runEpoch != 0 || state.waitEpoch != 0 {
		return coroTargetRunRequestInvalidV2
	}
	state.runEpoch = epoch
	return coroTargetRunRequestInlineV2
}

func coroTargetConsumeExecutorRunV2(handle coro.ExecutorHandle, epoch uint32) bool {
	state := &coroNativeFleetTargetV1State
	if state.lifecycle != coroNativeFleetTargetActiveV1 || state.program.Executor != handle ||
		epoch == 0 || state.runEpoch != epoch {
		return false
	}
	state.runEpoch = 0
	return true
}

// CoroNativePollServerDescriptorV1 recognizes both fixed route doorbells. It
// exposes descriptor ownership only; neither fd is a scheduler/P identity.
func CoroNativePollServerDescriptorV1(fd uintptr) bool {
	for index := uint32(0); index < coroNativeFleetDomainCapacityV1; index++ {
		if coroNativeFleetV1State.domains[index].doorbell.OwnsDescriptor(fd) {
			return true
		}
	}
	return false
}

func coroTargetExecutorStartV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeFleetTargetV1State
	if state.lifecycle != coroNativeFleetTargetUnusedV1 || state.program != (coro.ExecutorFleetHandle{}) ||
		handle != coroProgramExecutorHandleV1State || handle.Slot == 0 || handle.Generation == 0 ||
		!coroNativeWorkerPoolCanReleaseV1() || !coroNativeFleetStartProgramV1() {
		return false
	}
	program, programOK := coroNativeFleetHandleV1(0)
	if !programOK || program.Executor != handle || program.Route != 1 {
		state.lifecycle = coroNativeFleetTargetFailedV1
		coroRuntimeAbort("native coroutine fleet program route mismatch")
		return false
	}
	state.program = program
	state.lifecycle = coroNativeFleetTargetActiveV1
	if !coroNativeWorkerPoolStartFleetV1() {
		state.lifecycle = coroNativeFleetTargetFailedV1
		coroRuntimeAbort("native coroutine fleet worker start failed")
		return false
	}
	if !coroNativeFleetPhysicalOwnerStartV1() {
		_ = coroNativeWorkerPoolStopFleetV1()
		state.lifecycle = coroNativeFleetTargetFailedV1
		coroRuntimeAbort("native coroutine fleet peer owner start failed")
		return false
	}
	return true
}

func coroTargetBeginExecutorWaitV1(
	handle coro.ExecutorHandle,
	epoch uint32,
	deadline int64,
	hasDeadline bool,
) coroTargetDispatchResultV1 {
	state := &coroNativeFleetTargetV1State
	domain, ok := coroNativeFleetActiveProgramDomainV1(handle)
	if !ok || epoch == 0 || state.waitEpoch != 0 || state.runEpoch != 0 {
		return coroTargetDispatchInvalidV1
	}
	state.waitEpoch = epoch
	if !coroTargetWaitExecutorV1(&domain.doorbell, deadline, hasDeadline) {
		return coroTargetDispatchInvalidV1
	}
	state.waitEpoch = 0
	return coroTargetDispatchCompleteV1
}

func coroTargetPollExecutorWakeV1(coro.ExecutorHandle, uint32) coroTargetDispatchResultV1 {
	return coroTargetDispatchInvalidV1
}

func coroTargetRequestExecutorV1(handle coro.ExecutorHandle) bool {
	// This compatibility tail is used only by program-global sources which do
	// not yet carry an OperationID. ExecutorHandle alone cannot identify a
	// fleet route because independent registries may issue equal generations.
	// Route-aware sources must use coroNativeFleetPostV1 instead.
	domain, ok := coroNativeFleetActiveProgramDomainV1(handle)
	if !ok || !domain.ingress.Enter() {
		return false
	}
	if domain.lifecycle != coroNativeFleetDomainActiveV1 || domain.handle != coroNativeFleetTargetV1State.program {
		_, _ = domain.ingress.Leave()
		return false
	}
	result := coroProgramExecutorRegistryV1State.Request(handle)
	accepted := result == coro.ExecutorRequestPublished || result == coro.ExecutorRequestCoalesced ||
		result == coro.ExecutorRequestIdleWake
	ringOK := !coro.ExecutorRequestNeedsDoorbell(result) || domain.doorbell.Ring()
	_, leaveOK := domain.ingress.Leave()
	return accepted && ringOK && leaveOK
}

func coroTargetPostTaskControlV1(
	id coro.OperationID,
	kind coro.TaskCancelKind,
	executor coro.ExecutorHandle,
) coro.TaskControlExecutorPostResult {
	invalid := coro.TaskControlExecutorPostResult{
		Control:  coro.TaskControlPostInvalid,
		Executor: coro.ExecutorRequestInvalid,
	}
	domain, ok := coroNativeFleetActiveDomainForRouteV1(id.Route())
	if !ok || domain.handle.Executor != executor {
		return invalid
	}
	posted := coroNativeFleetPostTaskControlV1(id, kind)
	result := invalid
	result.Executor = posted.Executor
	switch posted.Route {
	case coro.OperationRoutePosted:
		result.Control = coro.TaskControlPosted
	case coro.OperationRoutePostCoalesced:
		result.Control = coro.TaskControlCoalesced
	case coro.OperationRoutePostSourceClosed, coro.OperationRoutePostClosed:
		result.Control = coro.TaskControlPostClosed
	case coro.OperationRoutePostSourceStale, coro.OperationRoutePostStale:
		result.Control = coro.TaskControlPostStale
	default:
		return invalid
	}
	return result
}

func coroTargetBeginExecutorCloseV1(handle coro.ExecutorHandle, epoch uint32) coroTargetDispatchResultV1 {
	state := &coroNativeFleetTargetV1State
	if state.lifecycle != coroNativeFleetTargetActiveV1 || state.program.Executor != handle ||
		epoch == 0 || state.waitEpoch != 0 || state.runEpoch != 0 ||
		!coroNativeFleetPhysicalOwnerStopV1() || !coroNativeWorkerPoolStopFleetV1() {
		return coroTargetDispatchInvalidV1
	}
	state.lifecycle = coroNativeFleetTargetClosingV1
	program, programOK := coroNativeFleetHandleV1(0)
	peer, peerOK := coroNativeFleetHandleV1(1)
	if !programOK || !peerOK || program != state.program ||
		!coroNativeFleetBeginRouteCloseV1(program) || !coroNativeFleetBeginRouteCloseV1(peer) ||
		!coroNativeFleetConfirmRouteCloseV1(program) || !coroNativeFleetConfirmRouteCloseV1(peer) ||
		!coroNativeFleetRetireBackendV1(program) || !coroNativeFleetRetireBackendV1(peer) ||
		!coroNativeFleetBeginExternalDriverCloseV1(program) || !coroNativeFleetRetireDriverV1(peer) {
		state.lifecycle = coroNativeFleetTargetFailedV1
		return coroTargetDispatchInvalidV1
	}
	return coroTargetDispatchCompleteV1
}

func coroTargetPollExecutorCloseV1(coro.ExecutorHandle, uint32) coroTargetDispatchResultV1 {
	return coroTargetDispatchInvalidV1
}

// coroTargetExecutorRetiredV1 is called after the authoritative program
// Confirm path has zeroed its adopted driver and sources. It completes the
// fleet pointer-suffix retirement before the program handle itself is cleared.
func coroTargetExecutorRetiredV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeFleetTargetV1State
	if state.lifecycle != coroNativeFleetTargetClosingV1 || state.program.Executor != handle ||
		!coroNativeFleetConfirmExternalDriverCloseV1(state.program) ||
		!coroNativeFleetAllRetiredV1() {
		state.lifecycle = coroNativeFleetTargetFailedV1
		return false
	}
	state.lifecycle = coroNativeFleetTargetRetiredV1
	return true
}

func coroNativePostWaitV1(
	waitSlot, waitGeneration, executorSlot, executorGeneration uint32,
) coro.WaitExecutorPostResult {
	closed := coro.WaitExecutorPostResult{
		Wait:     coro.WaitRegistrationPostClosed,
		Executor: coro.ExecutorRequestClosed,
	}
	executor := coro.ExecutorHandle{Slot: executorSlot, Generation: executorGeneration}
	domain, ok := coroNativeFleetActiveProgramDomainV1(executor)
	if !ok || !domain.ingress.Enter() {
		return closed
	}
	wait := coro.WaitRegistrationHandle{Slot: waitSlot, Generation: waitGeneration}
	result := coro.PostWaitAndRequest(
		&coroProgramWaitTableV1State,
		wait,
		&coroProgramExecutorRegistryV1State,
		executor,
	)
	ringOK := !coro.ExecutorRequestNeedsDoorbell(result.Executor) || domain.doorbell.Ring()
	_, leaveOK := domain.ingress.Leave()
	if !ringOK || !leaveOK {
		return coro.WaitExecutorPostResult{
			Wait:     coro.WaitRegistrationPostInvalid,
			Executor: coro.ExecutorRequestInvalid,
		}
	}
	return result
}

//export __llgo_coro_native_post_wait_v1
func __llgo_coro_native_post_wait_v1(
	waitSlot, waitGeneration, executorSlot, executorGeneration uint32,
) uint32 {
	result := coroNativePostWaitV1(waitSlot, waitGeneration, executorSlot, executorGeneration)
	return uint32(result.Wait) | uint32(result.Executor)<<8
}
