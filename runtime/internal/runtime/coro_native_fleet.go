//go:build (darwin || linux) && !baremetal && (!llgo || (llgo_coro && llgo_coro_native_pipe))

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

import (
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/corodoorbell"
)

// coroNativeFleetDomainCapacityV1 is the first executable native multi-P
// profile. It is deliberately smaller than coro.ExecutorFleetCapacity: this
// adapter proves independent ownership and routing without making a policy
// claim about the eventual GOMAXPROCS/default-P count.
const coroNativeFleetDomainCapacityV1 = 2

type coroNativeFleetLifecycleV1 uint8

const (
	coroNativeFleetUnusedV1 coroNativeFleetLifecycleV1 = iota
	coroNativeFleetActiveV1
	coroNativeFleetClosingV1
	coroNativeFleetRetiredV1
	coroNativeFleetFailedV1
)

type coroNativeFleetDomainLifecycleV1 uint8

const (
	coroNativeFleetDomainUnusedV1 coroNativeFleetDomainLifecycleV1 = iota
	coroNativeFleetDomainActiveV1
	coroNativeFleetDomainRouteClosingV1
	coroNativeFleetDomainRouteRetiredV1
	coroNativeFleetDomainBackendRetiredV1
	coroNativeFleetDomainRetiredV1
	coroNativeFleetDomainFailedV1
)

// coroNativeFleetDomainV1 is one statically addressed production ownership
// island. P, driver, every source, callback admission, and doorbell are
// independent. The first cut intentionally excludes Timer and Poll: their
// target callbacks do not yet route through OperationRouteRegistry, and
// ExecutorFleet rejects either source before consuming a route.
//
// Worker is completion-side only in this slice. The existing coroworker queue
// is a process singleton which retains one ExecutorHandle; duplicating it here
// would create a second scheduler/backend policy. A later change must make its
// POD job transport route-aware before fleet submission is enabled.
type coroNativeFleetDomainV1 struct {
	p       coro.P
	driver  coro.ExecutorDriver
	waits   coro.WaitRegistrationTable
	manual  coro.ManualOperationSource
	worker  coro.WorkerOperationSource
	control coro.TaskControlSource

	ingress   coro.TargetIngress
	doorbell  corodoorbell.Pipe
	admission coro.DriveAdmission

	handle         coro.ExecutorFleetHandle
	nextOwnerEpoch uint32
	ownerEpoch     uint32
	lifecycle      coroNativeFleetDomainLifecycleV1
}

// coroNativeFleetStateV1 must remain target-global for the process lifetime.
// Retired route and ingress tombstones reject delayed two-word callbacks; the
// object is never reset or reused after shutdown.
type coroNativeFleetStateV1 struct {
	fleet     coro.ExecutorFleet
	domains   [coroNativeFleetDomainCapacityV1]coroNativeFleetDomainV1
	lifecycle coroNativeFleetLifecycleV1
}

var coroNativeFleetV1State coroNativeFleetStateV1

func coroNativeFleetDomainCandidateV1(domain *coroNativeFleetDomainV1) bool {
	return domain != nil && domain.lifecycle == coroNativeFleetDomainUnusedV1 &&
		domain.handle == (coro.ExecutorFleetHandle{}) && domain.ownerEpoch == 0 &&
		domain.nextOwnerEpoch == 0 && domain.driver == (coro.ExecutorDriver{}) &&
		domain.waits.CanRelease() && domain.manual.CanRelease() &&
		domain.worker.CanRelease() && domain.control.CanRelease() &&
		domain.ingress.CanReleaseResources() && domain.admission.CanRecycle()
}

func coroNativeFleetRollbackBoundDomainV1(
	state *coroNativeFleetStateV1,
	domain *coroNativeFleetDomainV1,
	handle coro.ExecutorFleetHandle,
) bool {
	if state == nil || domain == nil || !handle.Valid() {
		return false
	}
	return coro.BeginExecutorFleetClose(&state.fleet, handle) &&
		coro.ConfirmExecutorFleetRouteClose(&state.fleet, handle) &&
		coro.BeginExecutorFleetDriverClose(&state.fleet, handle) &&
		coro.ConfirmExecutorFleetClose(&state.fleet, handle)
}

// coroNativeFleetAbortActiveDomainV1 is startup rollback, not ordinary
// reusable shutdown. The fleet lifecycle stays Failed and every consumed route
// remains a permanent tombstone. Although handles are not published until the
// complete two-domain start succeeds, the exported POD shim can be called with
// guessed words, so rollback still performs the full target-ingress join.
func coroNativeFleetAbortActiveDomainV1(
	state *coroNativeFleetStateV1,
	domain *coroNativeFleetDomainV1,
) bool {
	if state == nil || domain == nil || domain.lifecycle != coroNativeFleetDomainActiveV1 ||
		!domain.handle.Valid() || domain.ownerEpoch != 0 ||
		!coro.BeginExecutorFleetClose(&state.fleet, domain.handle) ||
		!coro.ConfirmExecutorFleetRouteClose(&state.fleet, domain.handle) ||
		!domain.ingress.Seal() {
		return false
	}
	for !domain.ingress.Quiesced() {
		if _, ok := domain.doorbell.WaitBounded(1); !ok {
			return false
		}
	}
	if !domain.doorbell.Close() || !domain.ingress.Retire() ||
		!domain.admission.ResetExecutorAfterStrongJoin(
			domain.handle.Executor.Slot,
			domain.handle.Executor.Generation,
		) || !coro.BeginExecutorFleetDriverClose(&state.fleet, domain.handle) ||
		!coro.ConfirmExecutorFleetClose(&state.fleet, domain.handle) {
		return false
	}
	domain.lifecycle = coroNativeFleetDomainFailedV1
	return true
}

func coroNativeFleetBindDomainV1(state *coroNativeFleetStateV1, index uint32) bool {
	if state == nil || index >= coroNativeFleetDomainCapacityV1 {
		return false
	}
	domain := &state.domains[index]
	if !coroNativeFleetDomainCandidateV1(domain) || !domain.doorbell.Open() {
		return false
	}
	handle, ok := coro.BindExecutorFleet(
		&state.fleet,
		&domain.driver,
		&domain.p,
		coro.ExecutorSourceCatalog{
			Waits:   &domain.waits,
			Manual:  &domain.manual,
			Worker:  &domain.worker,
			Control: &domain.control,
		},
	)
	if !ok {
		_ = domain.doorbell.Close()
		return false
	}
	// Publish the immutable callback-route identity before opening TargetIngress.
	// It remains a tombstone even if a later setup phase fails.
	domain.handle = handle
	if !domain.ingress.Start() {
		closed := domain.doorbell.Close()
		rolledBack := coroNativeFleetRollbackBoundDomainV1(state, domain, handle)
		if !closed || !rolledBack {
			domain.lifecycle = coroNativeFleetDomainFailedV1
		}
		return false
	}
	if !domain.admission.Acquire() ||
		!domain.admission.PublishExecutor(handle.Executor.Slot, handle.Executor.Generation) {
		_ = domain.admission.RevokeEpoch()
		_, _, _ = domain.admission.Finish()
		_ = domain.ingress.Seal()
		joined := domain.ingress.Quiesced()
		closed := joined && domain.doorbell.Close()
		retired := closed && domain.ingress.Retire()
		rolledBack := retired && coroNativeFleetRollbackBoundDomainV1(state, domain, handle)
		if !rolledBack {
			domain.lifecycle = coroNativeFleetDomainFailedV1
		}
		return false
	}
	if epoch, pending, finished := domain.admission.Finish(); !finished || pending || epoch != 0 {
		return false
	}
	domain.lifecycle = coroNativeFleetDomainActiveV1
	return true
}

// coroNativeFleetStartStateV1 consumes an exact-zero fleet. Monotonic route
// allocation therefore freezes domain[0]/domain[1] as Route 1/Route 2. No
// other adapter may pre-bind this private fleet; the production-island test
// asserts the mapping before any OperationID is issued.
func coroNativeFleetStartStateV1(state *coroNativeFleetStateV1) bool {
	if state == nil {
		return false
	}
	if state.lifecycle != coroNativeFleetUnusedV1 || !state.fleet.AllRetired() {
		return false
	}
	for index := uint32(0); index < coroNativeFleetDomainCapacityV1; index++ {
		if !coroNativeFleetBindDomainV1(state, index) {
			for previous := uint32(0); previous < index; previous++ {
				if domain := &state.domains[previous]; domain.lifecycle == coroNativeFleetDomainActiveV1 &&
					!coroNativeFleetAbortActiveDomainV1(state, domain) {
					state.lifecycle = coroNativeFleetFailedV1
					return false
				}
			}
			state.lifecycle = coroNativeFleetFailedV1
			return false
		}
	}
	state.lifecycle = coroNativeFleetActiveV1
	return true
}

// coroNativeFleetStartV1 binds exactly two independent native domains. It is
// intentionally separate from the legacy single-P program entry so the
// migration can be validated without changing process-start semantics. A
// failed or retired static fleet is never restarted.
func coroNativeFleetStartV1() bool {
	return coroNativeFleetStartStateV1(&coroNativeFleetV1State)
}

func coroNativeFleetDomainForHandleV1(
	state *coroNativeFleetStateV1,
	handle coro.ExecutorFleetHandle,
	want coroNativeFleetDomainLifecycleV1,
) (*coroNativeFleetDomainV1, bool) {
	if state == nil || !handle.Valid() || handle.Route == 0 || handle.Route > coroNativeFleetDomainCapacityV1 {
		return nil, false
	}
	domain := &state.domains[handle.Route-1]
	return domain, domain.handle == handle && domain.lifecycle == want
}

func coroNativeFleetHandleV1(index uint32) (coro.ExecutorFleetHandle, bool) {
	state := &coroNativeFleetV1State
	if state.lifecycle != coroNativeFleetActiveV1 || index >= coroNativeFleetDomainCapacityV1 {
		return coro.ExecutorFleetHandle{}, false
	}
	domain := &state.domains[index]
	return domain.handle, domain.lifecycle == coroNativeFleetDomainActiveV1 && domain.handle.Valid()
}

func coroNativeFleetInvalidIngressV1() coro.OperationRouteIngressResult {
	return coro.OperationRouteIngressResult{
		Route:    coro.OperationRoutePostInvalid,
		Executor: coro.ExecutorRequestInvalid,
	}
}

// coroNativeFleetPostV1 is the complete target-global completion ingress. The
// caller supplies only the frozen two-word OperationID (plus a source-specific
// POD result/control value). Route selects the stable ingress as the first
// target-state access; no callback retains or receives an executor, P, G,
// source, driver, wait record, or LLVM coroutine handle.
func coroNativeFleetPostV1(
	id coro.OperationID,
	payload coro.ScalarResultPayloadV1,
	control coro.TaskCancelKind,
) coro.OperationRouteIngressResult {
	if !id.Valid() || id.Route() == 0 || uint32(id.Route()) > coroNativeFleetDomainCapacityV1 {
		return coroNativeFleetInvalidIngressV1()
	}
	domain := &coroNativeFleetV1State.domains[uint32(id.Route())-1]
	// Enter is deliberately the first read of mutable domain storage. A close
	// owner may retire every pointer-bearing suffix immediately after Leave.
	if !domain.ingress.Enter() {
		return coro.OperationRouteIngressResult{
			Route:    coro.OperationRoutePostClosed,
			Executor: coro.ExecutorRequestClosed,
		}
	}
	if domain.handle.Route != uint32(id.Route()) {
		_, _ = domain.ingress.Leave()
		return coroNativeFleetInvalidIngressV1()
	}

	var result coro.OperationRouteIngressResult
	switch id.Source() {
	case coro.OperationSourceManual:
		if payload != (coro.ScalarResultPayloadV1{}) || control != coro.TaskCancelNone {
			_, _ = domain.ingress.Leave()
			return coroNativeFleetInvalidIngressV1()
		}
		result = coroNativeFleetV1State.fleet.PostManualAndRequest(id)
	case coro.OperationSourceWorker:
		if !payload.Valid() || control != coro.TaskCancelNone {
			_, _ = domain.ingress.Leave()
			return coroNativeFleetInvalidIngressV1()
		}
		result = coroNativeFleetV1State.fleet.PostWorkerAndRequest(id, payload)
	case coro.OperationSourceControl:
		if payload != (coro.ScalarResultPayloadV1{}) ||
			control != coro.TaskCancelAbort && control != coro.TaskCancelShutdown {
			_, _ = domain.ingress.Leave()
			return coroNativeFleetInvalidIngressV1()
		}
		result = coroNativeFleetV1State.fleet.PostTaskControlAndRequest(id, control)
	default:
		// Timer and Poll remain deliberately unsupported, including syntactically
		// valid routed IDs. There is no fallback to a legacy global source.
		_, _ = domain.ingress.Leave()
		return coroNativeFleetInvalidIngressV1()
	}

	ringOK := true
	if coro.ExecutorRequestNeedsDoorbell(result.Executor) {
		ringOK = domain.doorbell.Ring()
	}
	_, leaveOK := domain.ingress.Leave()
	// Leave is the absolute final domain access. The durable source fact is not
	// rolled back if a physical doorbell fails; fail-stop callers treat the
	// invalid return as target corruption while the fact remains observable.
	if !ringOK || !leaveOK {
		return coroNativeFleetInvalidIngressV1()
	}
	return result
}

func coroNativeFleetPostManualV1(id coro.OperationID) coro.OperationRouteIngressResult {
	return coroNativeFleetPostV1(id, coro.ScalarResultPayloadV1{}, coro.TaskCancelNone)
}

func coroNativeFleetPostWorkerV1(id coro.OperationID, payload coro.ScalarResultPayloadV1) coro.OperationRouteIngressResult {
	return coroNativeFleetPostV1(id, payload, coro.TaskCancelNone)
}

func coroNativeFleetPostTaskControlV1(id coro.OperationID, kind coro.TaskCancelKind) coro.OperationRouteIngressResult {
	return coroNativeFleetPostV1(id, coro.ScalarResultPayloadV1{}, kind)
}

func packCoroNativeFleetIngressV1(result coro.OperationRouteIngressResult) uint32 {
	return uint32(result.Route) | uint32(result.Executor)<<8
}

// __llgo_coro_native_fleet_post_manual_v1 is the two-word native completion
// ABI for a manual/future platform source. The low result byte is
// OperationRoutePostResult and the next byte is ExecutorRequestResult.
//
//export __llgo_coro_native_fleet_post_manual_v1
func __llgo_coro_native_fleet_post_manual_v1(sourceSlot, generation uint32) uint32 {
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	return packCoroNativeFleetIngressV1(coroNativeFleetPostManualV1(id))
}

// __llgo_coro_native_fleet_post_worker_v1 routes a completed worker job by its
// two-word OperationID. The scalar payload is POD and contains no owner
// identity. Submission remains disabled until coroworker.Queue is fleet-aware.
//
//export __llgo_coro_native_fleet_post_worker_v1
func __llgo_coro_native_fleet_post_worker_v1(
	sourceSlot, generation uint32,
	r1, r2, errno uint64,
) uint32 {
	payload, ok := coro.MakeScalarResultPayloadV1(
		coro.ScalarResultKindWords,
		0,
		3,
		r1,
		r2,
		errno,
	)
	if !ok {
		return packCoroNativeFleetIngressV1(coroNativeFleetInvalidIngressV1())
	}
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	return packCoroNativeFleetIngressV1(coroNativeFleetPostWorkerV1(id, payload))
}

// __llgo_coro_native_fleet_post_control_v1 routes task cancellation without
// the redundant ExecutorHandle carried by the legacy single-P ABI.
//
//export __llgo_coro_native_fleet_post_control_v1
func __llgo_coro_native_fleet_post_control_v1(sourceSlot, generation, kind uint32) uint32 {
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	return packCoroNativeFleetIngressV1(
		coroNativeFleetPostTaskControlV1(id, coro.TaskCancelKind(kind)),
	)
}

// coroNativeFleetBeginOwnerEpochV1 serializes one domain owner without a TLS
// current-P lookup. The epoch is bounded uint32 POD state. Each successful
// epoch consumes this domain's retained doorbell latch before scheduler-owned
// fields are serviced.
func coroNativeFleetBeginOwnerEpochV1(handle coro.ExecutorFleetHandle) (uint32, bool) {
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !ok || domain.ownerEpoch != 0 || domain.nextOwnerEpoch == ^uint32(0) ||
		!domain.admission.Acquire() {
		return 0, false
	}
	epoch := domain.nextOwnerEpoch + 1
	if !domain.admission.PublishEpoch(epoch) {
		_, _, _ = domain.admission.Finish()
		return 0, false
	}
	domain.nextOwnerEpoch = epoch
	domain.ownerEpoch = epoch
	if _, wakeOK := domain.doorbell.ConsumeRetainedWake(); !wakeOK {
		_ = domain.admission.ClearEpoch(epoch)
		_ = domain.admission.AdvancePhase()
		domain.ownerEpoch = 0
		_, _, _ = domain.admission.Finish()
		return 0, false
	}
	return epoch, true
}

// coroNativeFleetDrainOwnerEpochV1 imports no more than budget P-neutral
// initial/yielded continuations into this exact domain. It does not claim work
// stealing for parked, cancelable, pinned, result-owning, or running Gs.
func coroNativeFleetDrainOwnerEpochV1(
	handle coro.ExecutorFleetHandle,
	epoch, budget uint32,
) (moved uint32, more bool, ok bool) {
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || epoch == 0 || domain.ownerEpoch != epoch ||
		budget == 0 || budget > coro.RunnableTransferMailboxCapacity {
		return 0, false, false
	}
	return coroNativeFleetV1State.fleet.DrainPNeutralRunnables(handle, &domain.p, budget)
}

// coroNativeFleetPollOwnerEpochV1 reuses the existing unified source
// transaction. There is no fleet-specific polling loop or scheduler copy.
func coroNativeFleetPollOwnerEpochV1(
	handle coro.ExecutorFleetHandle,
	epoch uint32,
) (drained, promoted int, ok bool) {
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || epoch == 0 || domain.ownerEpoch != epoch {
		return 0, 0, false
	}
	return coro.PollExecutor(&domain.driver)
}

func coroNativeFleetFinishOwnerEpochV1(handle coro.ExecutorFleetHandle, epoch uint32) bool {
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !ok || epoch == 0 || domain.ownerEpoch != epoch ||
		!domain.admission.ClearEpoch(epoch) || !domain.admission.AdvancePhase() {
		return false
	}
	domain.ownerEpoch = 0
	finishedEpoch, pending, finished := domain.admission.Finish()
	return finished && !pending && finishedEpoch == 0
}

func coroNativeFleetPublishPNeutralRunnableV1(
	handle coro.ExecutorFleetHandle,
	source *coro.P,
	g *coro.G,
) (coro.RunnableTransferID, coro.ExecutorRequestResult, bool) {
	return coroNativeFleetV1State.fleet.PublishPNeutralRunnableAndRequest(handle, source, g)
}

// The close API is deliberately staged so ordering is executable and
// testable: route admission seal/strong-join, target callback/backend
// unregister+join and doorbell close, then driver/source/executor retirement.
func coroNativeFleetBeginRouteCloseV1(handle coro.ExecutorFleetHandle) bool {
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !ok || domain.ownerEpoch != 0 || !coro.BeginExecutorFleetClose(&coroNativeFleetV1State.fleet, handle) {
		return false
	}
	domain.lifecycle = coroNativeFleetDomainRouteClosingV1
	if coroNativeFleetV1State.lifecycle == coroNativeFleetActiveV1 {
		coroNativeFleetV1State.lifecycle = coroNativeFleetClosingV1
	}
	return true
}

func coroNativeFleetConfirmRouteCloseV1(handle coro.ExecutorFleetHandle) bool {
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainRouteClosingV1,
	)
	if !ok || !coro.ConfirmExecutorFleetRouteClose(&coroNativeFleetV1State.fleet, handle) {
		return false
	}
	domain.lifecycle = coroNativeFleetDomainRouteRetiredV1
	return true
}

func coroNativeFleetRetireBackendV1(handle coro.ExecutorFleetHandle) bool {
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainRouteRetiredV1,
	)
	if !ok || domain.ownerEpoch != 0 || !domain.admission.CanRelease() || !domain.ingress.Seal() {
		return false
	}
	// Producer shims are bounded leaves. A callback admitted before Seal may
	// observe the already-retired route and owe no doorbell byte, so bounded
	// waits are merely kernel scheduling points while the atomic join converges.
	for !domain.ingress.Quiesced() {
		if _, waitOK := domain.doorbell.WaitBounded(1); !waitOK {
			domain.lifecycle = coroNativeFleetDomainFailedV1
			return false
		}
	}
	if !domain.doorbell.Close() || !domain.ingress.Retire() ||
		!domain.admission.ResetExecutorAfterStrongJoin(
			handle.Executor.Slot,
			handle.Executor.Generation,
		) {
		domain.lifecycle = coroNativeFleetDomainFailedV1
		return false
	}
	domain.lifecycle = coroNativeFleetDomainBackendRetiredV1
	return true
}

func coroNativeFleetRetireDriverV1(handle coro.ExecutorFleetHandle) bool {
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainBackendRetiredV1,
	)
	if !ok || !coro.BeginExecutorFleetDriverClose(&coroNativeFleetV1State.fleet, handle) ||
		!coro.ConfirmExecutorFleetClose(&coroNativeFleetV1State.fleet, handle) {
		return false
	}
	domain.lifecycle = coroNativeFleetDomainRetiredV1
	return true
}

func coroNativeFleetAllRetiredV1() bool {
	state := &coroNativeFleetV1State
	if state.lifecycle != coroNativeFleetClosingV1 || !state.fleet.AllRetired() {
		return false
	}
	for index := range state.domains {
		domain := &state.domains[index]
		if domain.lifecycle != coroNativeFleetDomainRetiredV1 || domain.ownerEpoch != 0 ||
			!domain.ingress.Retired() || !domain.doorbell.Closed() ||
			!domain.admission.CanRelease() || domain.driver != (coro.ExecutorDriver{}) ||
			!domain.waits.CanRelease() || !domain.manual.CanRelease() ||
			!domain.worker.CanRelease() || !domain.control.CanRelease() {
			return false
		}
	}
	state.lifecycle = coroNativeFleetRetiredV1
	return true
}
