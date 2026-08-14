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

const (
	// coroNativeFleetDomainCapacityV1 is the allocation-free lifetime bound of
	// the native target. Production starts the complete topology; deterministic
	// host tests may configure a smaller prefix. Route identities remain
	// monotonic tombstones and are never reused.
	coroNativeFleetDomainCapacityV1 = coro.ExecutorFleetCapacity

	// These are the hosted profile's logical capacity limits in 64-slot pages,
	// not eager reservations. Every P starts with the target-neutral inline page
	// and grows one stable page at a time before an irreversible park transaction.
	coroNativeSourcePageCountV1 = 16
	coroNativeTimerPageCountV1  = 64
)

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
// island. P, driver, every logical source, callback admission, and doorbell
// stay route-local. The fixed physical coroworker pool is process-shared: its
// POD jobs carry OperationID routes and never become part of this domain.
type coroNativeFleetDomainV1 struct {
	p       coro.P
	driver  coro.ExecutorDriver
	timers  coro.TimerRegistrationTable
	poll    coro.PollOperationSource
	manual  coro.ManualOperationSource
	worker  coro.WorkerOperationSource
	channel coro.ChannelOperationSource
	control coro.TaskControlSource

	ingress   coro.TargetIngress
	doorbell  corodoorbell.Pipe
	admission coro.DriveAdmission

	handle         coro.ExecutorFleetHandle
	nextOwnerEpoch uint32
	ownerEpoch     uint32
	borrowedWait   uint32
	adopted        bool
	owners         coroNativeFleetDomainOwnersV1
	lifecycle      coroNativeFleetDomainLifecycleV1
}

// coroNativeFleetDomainOwnersV1 is populated only for a domain which adopts
// already-bound program storage. Keeping the references in one suffix avoids
// aliasing or copying the hundreds of existing program-global users. Owned
// fleet domains keep this structure zero and use their inline fields.
type coroNativeFleetDomainOwnersV1 struct {
	p       *coro.P
	driver  *coro.ExecutorDriver
	sources coro.ExecutorSourceCatalog
}

func (domain *coroNativeFleetDomainV1) pOwnerV1() *coro.P {
	if domain == nil {
		return nil
	}
	if domain.adopted {
		return domain.owners.p
	}
	return &domain.p
}

func (domain *coroNativeFleetDomainV1) driverOwnerV1() *coro.ExecutorDriver {
	if domain == nil {
		return nil
	}
	if domain.adopted {
		return domain.owners.driver
	}
	return &domain.driver
}

func (domain *coroNativeFleetDomainV1) timerOwnerV1() *coro.TimerRegistrationTable {
	if domain == nil {
		return nil
	}
	if domain.adopted {
		return domain.owners.sources.Timers
	}
	return &domain.timers
}

func (domain *coroNativeFleetDomainV1) pollOwnerV1() *coro.PollOperationSource {
	if domain == nil {
		return nil
	}
	if domain.adopted {
		return domain.owners.sources.Poll
	}
	return &domain.poll
}

func (domain *coroNativeFleetDomainV1) workerOwnerV1() *coro.WorkerOperationSource {
	if domain == nil {
		return nil
	}
	if domain.adopted {
		return domain.owners.sources.Worker
	}
	if _, bound := domain.worker.Route(); !bound {
		return nil
	}
	return &domain.worker
}

func (domain *coroNativeFleetDomainV1) channelOwnerV1() *coro.ChannelOperationSource {
	if domain == nil {
		return nil
	}
	if domain.adopted {
		return domain.owners.sources.Channel
	}
	return &domain.channel
}

func validCoroNativeFleetAdoptedOwnersV1(owners coroNativeFleetDomainOwnersV1) bool {
	sources := owners.sources
	return owners.p != nil && owners.driver != nil && sources.Timers != nil &&
		sources.Poll != nil && sources.Manual != nil && sources.Channel != nil &&
		sources.Control != nil
}

func coroNativeFleetAdoptedOwnersRetiredV1(domain *coroNativeFleetDomainV1) bool {
	if domain == nil || !domain.adopted || !validCoroNativeFleetAdoptedOwnersV1(domain.owners) {
		return false
	}
	owners := domain.owners
	sources := owners.sources
	workerRetired := sources.Worker == nil || sources.Worker.CanRelease()
	return *owners.driver == (coro.ExecutorDriver{}) && sources.Timers.CanRelease() &&
		sources.Poll.CanRelease() && sources.Manual.CanRelease() && workerRetired && sources.Channel.CanRelease() &&
		sources.Control.CanRelease()
}

func coroNativeFleetReleaseAdoptedOwnersV1(domain *coroNativeFleetDomainV1) bool {
	if domain == nil || !domain.adopted {
		return domain != nil
	}
	if !coroNativeFleetAdoptedOwnersRetiredV1(domain) {
		return false
	}
	domain.owners = coroNativeFleetDomainOwnersV1{}
	return true
}

// coroNativeFleetStateV1 must remain target-global for the process lifetime.
// Retired route and ingress tombstones reject delayed two-word callbacks; the
// object is never reset or reused after shutdown.
type coroNativeFleetStateV1 struct {
	fleet       coro.ExecutorFleet
	execution   coro.ExecutionQuota
	domains     [coroNativeFleetDomainCapacityV1]coroNativeFleetDomainV1
	domainCount uint32
	lifecycle   coroNativeFleetLifecycleV1
}

var coroNativeFleetV1State coroNativeFleetStateV1

func coroNativeFleetDomainCandidateV1(domain *coroNativeFleetDomainV1) bool {
	return domain != nil && domain.lifecycle == coroNativeFleetDomainUnusedV1 &&
		domain.handle == (coro.ExecutorFleetHandle{}) && domain.ownerEpoch == 0 &&
		domain.nextOwnerEpoch == 0 && coroNativeAtomicLoadV1(&domain.borrowedWait) == 0 && !domain.adopted &&
		domain.owners == (coroNativeFleetDomainOwnersV1{}) &&
		domain.driver == (coro.ExecutorDriver{}) &&
		domain.timers.CanRelease() && domain.poll.CanRelease() &&
		domain.manual.CanRelease() &&
		domain.worker.CanRelease() && domain.channel.CanRelease() && domain.control.CanRelease() &&
		domain.ingress.CanReleaseResources() && domain.admission.CanRecycle()
}

func coroNativeFleetActivateBoundDomainV1(
	state *coroNativeFleetStateV1,
	domain *coroNativeFleetDomainV1,
	handle coro.ExecutorFleetHandle,
) bool {
	if state == nil || domain == nil || !handle.Valid() {
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
		coro.ConfirmExecutorFleetClose(&state.fleet, handle) &&
		coroNativeFleetReleaseAdoptedOwnersV1(domain)
}

// coroNativeFleetAbortActiveDomainV1 is startup rollback, not ordinary
// reusable shutdown. The fleet lifecycle stays Failed and every consumed route
// remains a permanent tombstone. Although handles are not published until the
// complete configured fleet start succeeds, the exported POD shim can be called
// with guessed words, so rollback still performs the full target-ingress join.
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
		!coro.ConfirmExecutorFleetClose(&state.fleet, domain.handle) ||
		!coroNativeFleetReleaseAdoptedOwnersV1(domain) {
		return false
	}
	domain.lifecycle = coroNativeFleetDomainFailedV1
	return true
}

func coroNativeFleetValidateOwnedDomainSourcesV1(domain *coroNativeFleetDomainV1) bool {
	if domain == nil || domain.adopted {
		return false
	}
	return coro.TimerRegistrationConfiguredCapacity(&domain.timers) == coro.TimerRegistrationPageCapacity &&
		coro.PollOperationConfiguredCapacity(&domain.poll) == coro.PollOperationPageCapacity &&
		coro.ManualOperationConfiguredCapacity(&domain.manual) == coro.ManualOperationPageCapacity &&
		coro.WorkerOperationConfiguredCapacity(&domain.worker) == coro.WorkerOperationPageCapacity &&
		coro.ChannelOperationConfiguredCapacity(&domain.channel) == coro.ChannelOperationPageCapacity
}

func coroNativeFleetBindDomainV1(state *coroNativeFleetStateV1, index uint32, workerEnabled bool) bool {
	if state == nil || index >= coroNativeFleetDomainCapacityV1 {
		return false
	}
	domain := &state.domains[index]
	if !coroNativeFleetDomainCandidateV1(domain) ||
		!coroNativeFleetValidateOwnedDomainSourcesV1(domain) ||
		!domain.doorbell.Open() {
		return false
	}
	var worker *coro.WorkerOperationSource
	if workerEnabled {
		worker = &domain.worker
	}
	handle, ok := coro.BindExecutorFleet(
		&state.fleet,
		&domain.driver,
		&domain.p,
		coro.ExecutorSourceCatalog{
			Timers:  &domain.timers,
			Poll:    &domain.poll,
			Manual:  &domain.manual,
			Worker:  worker,
			Channel: &domain.channel,
			Control: &domain.control,
		},
	)
	if !ok {
		_ = domain.doorbell.Close()
		return false
	}
	return coroNativeFleetActivateBoundDomainV1(state, domain, handle)
}

func coroNativeFleetAdoptDomainV1(
	state *coroNativeFleetStateV1,
	index uint32,
	owners coroNativeFleetDomainOwnersV1,
) bool {
	if state == nil || index >= coroNativeFleetDomainCapacityV1 ||
		!validCoroNativeFleetAdoptedOwnersV1(owners) {
		return false
	}
	domain := &state.domains[index]
	if !coroNativeFleetDomainCandidateV1(domain) || !domain.doorbell.Open() {
		return false
	}
	handle, ok := coro.AdoptExecutorFleet(&state.fleet, owners.driver, owners.p)
	if !ok {
		_ = domain.doorbell.Close()
		return false
	}
	domain.adopted = true
	domain.owners = owners
	return coroNativeFleetActivateBoundDomainV1(state, domain, handle)
}

func coroNativeFleetStartDomainsV1(
	state *coroNativeFleetStateV1,
	program *coroNativeFleetDomainOwnersV1,
	count uint32,
) bool {
	if state == nil || state.lifecycle != coroNativeFleetUnusedV1 || state.domainCount != 0 ||
		count == 0 || count > coroNativeFleetDomainCapacityV1 || !state.fleet.AllRetired() {
		return false
	}
	// The configured prefix is immutable even on startup failure. Consumed route
	// identities and guessed callback words must keep observing one permanent
	// fail-stop policy rather than a recyclable zero state.
	state.domainCount = count
	workerEnabled := program == nil || program.sources.Worker != nil
	for index := uint32(0); index < count; index++ {
		started := false
		if index == 0 && program != nil {
			started = coroNativeFleetAdoptDomainV1(state, index, *program)
		} else {
			started = coroNativeFleetBindDomainV1(state, index, workerEnabled)
		}
		if !started {
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

// coroNativeFleetStartStateV1 consumes an exact-zero fleet. Monotonic route
// allocation freezes domain[i] as Route i+1 for the requested deterministic
// topology. No other adapter may pre-bind this private fleet.
func coroNativeFleetStartStateV1(state *coroNativeFleetStateV1, count uint32) bool {
	return coroNativeFleetStartDomainsV1(state, nil, count)
}

// coroNativeFleetStartV1 retains a deterministic two-domain host-test entry.
// Production starts the complete bounded topology through
// coroNativeFleetStartProgramV1.
func coroNativeFleetStartV1() bool {
	return coroNativeFleetStartStateV1(&coroNativeFleetV1State, 2)
}

func coroNativeFleetDomainForHandleV1(
	state *coroNativeFleetStateV1,
	handle coro.ExecutorFleetHandle,
	want coroNativeFleetDomainLifecycleV1,
) (*coroNativeFleetDomainV1, bool) {
	if state == nil || state.domainCount == 0 || state.domainCount > coroNativeFleetDomainCapacityV1 ||
		!handle.Valid() || handle.Route == 0 || handle.Route > state.domainCount {
		return nil, false
	}
	domain := &state.domains[handle.Route-1]
	return domain, domain.handle == handle && domain.lifecycle == want
}

func coroNativeFleetHandleV1(index uint32) (coro.ExecutorFleetHandle, bool) {
	state := &coroNativeFleetV1State
	if state.lifecycle != coroNativeFleetActiveV1 || index >= state.domainCount {
		return coro.ExecutorFleetHandle{}, false
	}
	domain := &state.domains[index]
	return domain.handle, domain.lifecycle == coroNativeFleetDomainActiveV1 && domain.handle.Valid()
}

// coroNativeFleetWorkerTransportReadyV1 is the process-shared worker-pool
// startup preflight. The pool remains one physical backend; every logical job
// carries the exact route of one already-bound WorkerOperationSource.
func coroNativeFleetWorkerTransportReadyV1() bool {
	state := &coroNativeFleetV1State
	if state.lifecycle != coroNativeFleetActiveV1 {
		return false
	}
	for index := uint32(0); index < state.domainCount; index++ {
		domain := &state.domains[index]
		worker := domain.workerOwnerV1()
		if worker == nil {
			return false
		}
		route, routeOK := worker.Route()
		if domain.lifecycle != coroNativeFleetDomainActiveV1 || !domain.handle.Valid() ||
			domain.handle.Route != index+1 || !routeOK || uint32(route) != domain.handle.Route {
			return false
		}
	}
	return true
}

// coroNativeFleetWorkerSubmissionOwnerV1 validates the transient owner-side
// queue reservation capability after CurrentExecutorWorkerDriver has proved
// the active managed resume. It reads only route-lifetime-immutable fields, so
// different M owners may validate their domains concurrently. The durable
// worker job keeps only its OperationID and scalar call words.
func coroNativeFleetWorkerSubmissionOwnerV1(handle coro.ExecutorHandle, route coro.RouteID) bool {
	state := &coroNativeFleetV1State
	if state.lifecycle != coroNativeFleetActiveV1 || !route.Valid() ||
		uint32(route) > state.domainCount {
		return false
	}
	domain := &state.domains[uint32(route)-1]
	worker := domain.workerOwnerV1()
	if worker == nil {
		return false
	}
	workerRoute, routeOK := worker.Route()
	return domain.lifecycle == coroNativeFleetDomainActiveV1 &&
		domain.handle.Executor == handle && domain.handle.Route == uint32(route) &&
		routeOK && workerRoute == route
}

func coroNativeFleetInvalidIngressV1() coro.OperationRouteIngressResult {
	return coro.OperationRouteIngressResult{
		Route:    coro.OperationRoutePostInvalid,
		Executor: coro.ExecutorRequestInvalid,
	}
}

// A normal retained wait asks for a doorbell only after the executor gate has
// published IdleArmed. A replacement M deliberately keeps the same driver
// active while it borrows the route, so borrowedWait is the orthogonal
// physical-wait publication which makes every accepted request ring during
// that interval. The durable source/request remains authoritative.
func coroNativeFleetRequestNeedsRingV1(
	domain *coroNativeFleetDomainV1,
	result coro.ExecutorRequestResult,
) bool {
	return coro.ExecutorRequestNeedsDoorbell(result) ||
		domain != nil && coroNativeAtomicLoadV1(&domain.borrowedWait) != 0
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
	pollResult coro.PollOperationResult,
) coro.OperationRouteIngressResult {
	state := &coroNativeFleetV1State
	if !id.Valid() || id.Route() == 0 || uint32(id.Route()) > state.domainCount {
		return coroNativeFleetInvalidIngressV1()
	}
	domain := &state.domains[uint32(id.Route())-1]
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
		if payload != (coro.ScalarResultPayloadV1{}) || control != coro.TaskCancelNone ||
			pollResult != coro.PollOperationResultInvalid {
			_, _ = domain.ingress.Leave()
			return coroNativeFleetInvalidIngressV1()
		}
		result = coroNativeFleetV1State.fleet.PostManualAndRequest(id)
	case coro.OperationSourceWorker:
		if !payload.Valid() || control != coro.TaskCancelNone || pollResult != coro.PollOperationResultInvalid {
			_, _ = domain.ingress.Leave()
			return coroNativeFleetInvalidIngressV1()
		}
		result = coroNativeFleetV1State.fleet.PostWorkerAndRequest(id, payload)
	case coro.OperationSourcePoll:
		if payload != (coro.ScalarResultPayloadV1{}) || control != coro.TaskCancelNone ||
			(pollResult != coro.PollOperationReady && pollResult != coro.PollOperationClosing) {
			_, _ = domain.ingress.Leave()
			return coroNativeFleetInvalidIngressV1()
		}
		result = coroNativeFleetV1State.fleet.PostPollAndRequest(id, pollResult)
	case coro.OperationSourceControl:
		if payload != (coro.ScalarResultPayloadV1{}) ||
			(control != coro.TaskCancelAbort && control != coro.TaskCancelShutdown) ||
			pollResult != coro.PollOperationResultInvalid {
			_, _ = domain.ingress.Leave()
			return coroNativeFleetInvalidIngressV1()
		}
		result = coroNativeFleetV1State.fleet.PostTaskControlAndRequest(id, control)
	default:
		// Timer has no producer callback: each owner discovers expiry from its
		// monotonic clock. Unknown source kinds never fall back to a legacy global.
		_, _ = domain.ingress.Leave()
		return coroNativeFleetInvalidIngressV1()
	}

	ringOK := true
	if coroNativeFleetRequestNeedsRingV1(domain, result.Executor) {
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
	return coroNativeFleetPostV1(
		id, coro.ScalarResultPayloadV1{}, coro.TaskCancelNone, coro.PollOperationResultInvalid,
	)
}

func coroNativeFleetPostWorkerV1(id coro.OperationID, payload coro.ScalarResultPayloadV1) coro.OperationRouteIngressResult {
	return coroNativeFleetPostV1(id, payload, coro.TaskCancelNone, coro.PollOperationResultInvalid)
}

func coroNativeFleetPostPollV1(
	id coro.OperationID,
	result coro.PollOperationResult,
) coro.OperationRouteIngressResult {
	return coroNativeFleetPostV1(id, coro.ScalarResultPayloadV1{}, coro.TaskCancelNone, result)
}

func coroNativeFleetPostTaskControlV1(id coro.OperationID, kind coro.TaskCancelKind) coro.OperationRouteIngressResult {
	return coroNativeFleetPostV1(
		id, coro.ScalarResultPayloadV1{}, kind, coro.PollOperationResultInvalid,
	)
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
// two-word OperationID. The scalar payload is POD and contains no pointer owner
// identity; route is the complete logical destination identity.
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

// __llgo_coro_native_fleet_post_poll_v1 is the route-aware native reactor
// ingress. result accepts only PollOperationReady or PollOperationClosing.
//
//export __llgo_coro_native_fleet_post_poll_v1
func __llgo_coro_native_fleet_post_poll_v1(sourceSlot, generation, result uint32) uint32 {
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	return packCoroNativeFleetIngressV1(
		coroNativeFleetPostPollV1(id, coro.PollOperationResult(result)),
	)
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
	moved, more, status := coroNativeFleetTryDrainOwnerEpochV1(handle, epoch, budget)
	return moved, more, status == coro.RunnableTransferDrainComplete
}

func coroNativeFleetTryDrainOwnerEpochV1(
	handle coro.ExecutorFleetHandle,
	epoch, budget uint32,
) (moved uint32, more bool, status coro.RunnableTransferDrainStatus) {
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || epoch == 0 || domain.ownerEpoch != epoch ||
		budget == 0 || budget > coro.RunnableTransferMailboxCapacity {
		return 0, false, coro.RunnableTransferDrainInvalid
	}
	p := domain.pOwnerV1()
	if p == nil {
		return 0, false, coro.RunnableTransferDrainInvalid
	}
	return coroNativeFleetV1State.fleet.TryDrainPNeutralRunnables(handle, p, budget)
}

func coroNativeFleetCancelOwnerRunnableDemandV1(
	handle coro.ExecutorFleetHandle,
	epoch uint32,
) (inflight, ok bool) {
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || epoch == 0 || domain.ownerEpoch != epoch {
		return false, false
	}
	p := domain.pOwnerV1()
	if p == nil {
		return false, false
	}
	return coroNativeFleetV1State.fleet.CancelPNeutralRunnableRequest(handle, p)
}

func coroNativeFleetRequestOwnerRunnableV1(
	handle coro.ExecutorFleetHandle,
	epoch uint32,
) bool {
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || epoch == 0 || domain.ownerEpoch != epoch {
		return false
	}
	p := domain.pOwnerV1()
	return p != nil && coroNativeFleetV1State.fleet.RequestPNeutralRunnable(handle, p)
}

// coroNativeFleetPollOwnerEpochV1 reuses the existing unified source
// transaction. There is no fleet-specific polling loop or scheduler copy.
func coroNativeFleetPollOwnerEpochV1(
	handle coro.ExecutorFleetHandle,
	epoch uint32,
	now int64,
) (drained, promoted int, ok bool) {
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || epoch == 0 || domain.ownerEpoch != epoch || now < 0 {
		return 0, 0, false
	}
	driver := domain.driverOwnerV1()
	if driver == nil {
		return 0, 0, false
	}
	timers, promoted, pollOK := coro.PollExecutorAt(driver, now)
	return timers, promoted, pollOK
}

// coroNativeFleetRunOwnerEpochV1 executes the same physical reducer as the
// process program, but with ordinary-domain policy and an explicit owner clock
// sample. It neither settles a terminal executor close nor finishes the owner
// epoch: the program-level fleet coordinator must handle both boundaries after
// observing the returned stop.
func coroNativeFleetRunOwnerEpochV1(
	handle coro.ExecutorFleetHandle,
	epoch uint32,
	now int64,
	budget uint32,
) coroRunResultV1 {
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || epoch == 0 || domain.ownerEpoch != epoch || now < 0 || budget == 0 {
		return coroRunResultV1{}
	}
	p, driver := domain.pOwnerV1(), domain.driverOwnerV1()
	if p == nil || driver == nil {
		return coroRunResultV1{}
	}
	return coroRunSliceAtV1(p, driver, now, budget)
}

// coroNativeFleetCommitOwnerDestroyV1 settles an ordinary G's final-root
// receipt without interpreting an empty P as process termination. Unhandled
// panic remains explicit in the returned action for the program coordinator;
// normal completion releases only the G and leaves the domain active.
func coroNativeFleetCommitOwnerDestroyV1(
	handle coro.ExecutorFleetHandle,
	epoch uint32,
	g *coro.G,
	receipt coro.Action,
) (coro.Action, bool) {
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || epoch == 0 || domain.ownerEpoch != epoch || g == nil {
		return coro.Action{}, false
	}
	driver := domain.driverOwnerV1()
	if driver == nil {
		return coro.Action{}, false
	}
	result, ok := coro.CommitExecutorRunDomainDestroy(driver, g, receipt)
	if !ok {
		return coro.Action{}, false
	}
	if result.Kind == coro.ActionComplete {
		retireOwner := coro.ActionRetiresPhysicalOwner(result)
		if !coroReleaseCompletedTask(g) ||
			retireOwner && !coroTargetRetirePhysicalOwnerV1(
				domain.pOwnerV1(),
				driver,
			) {
			return coro.Action{}, false
		}
	}
	return result, result.Kind == coro.ActionComplete || result.Kind == coro.ActionPanicComplete
}

// coroNativeFleetEnterOwnerCompatibilityV1 clears only bounded-runner fairness
// bookkeeping after the coordinator has proved this domain stable and is about
// to cross an unbounded close/idle compatibility boundary.
func coroNativeFleetEnterOwnerCompatibilityV1(handle coro.ExecutorFleetHandle, epoch uint32) bool {
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	driver := domain.driverOwnerV1()
	return valid && driver != nil && epoch != 0 && domain.ownerEpoch == epoch &&
		coro.EnterExecutorRunCompatibility(driver)
}

// coroNativeFleetOwnerWaitPlanV1 is the pointer-free result of one exact
// domain idle transaction. Armed means the driver committed its executor gate
// and the owner epoch has already been released; the physical M may now block
// until its doorbell, poll set, deadline, or coordinator stop wakes it.
type coroNativeFleetOwnerWaitPlanV1 struct {
	Epoch       uint32
	Deadline    int64
	HasDeadline bool
	Armed       bool
}

// coroNativeFleetPrepareOwnerWaitAtV1 crosses from the bounded reducer to the
// common timer-aware idle transaction. Unlike command-main sleep, an ordinary
// empty P may enter standby because a routed runnable transfer is itself a
// future wake source. A racing fact leaves the epoch owned and returns
// Armed=false; a committed sleep releases the epoch before returning.
func coroNativeFleetPrepareOwnerWaitAtV1(
	handle coro.ExecutorFleetHandle,
	epoch uint32,
	now, freshNow int64,
) (coroNativeFleetOwnerWaitPlanV1, bool) {
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	driver := domain.driverOwnerV1()
	if !valid || driver == nil || epoch == 0 || domain.ownerEpoch != epoch || now < 0 || freshNow < now {
		return coroNativeFleetOwnerWaitPlanV1{}, false
	}
	entered, compatibilityOK := coro.EnterExecutorRunStandbyCompatibility(driver)
	if !compatibilityOK {
		return coroNativeFleetOwnerWaitPlanV1{}, false
	}
	if !entered {
		return coroNativeFleetOwnerWaitPlanV1{}, true
	}
	prepared, ok := coro.PrepareExecutorStandbyAt(driver, now)
	if !ok {
		return coroNativeFleetOwnerWaitPlanV1{}, false
	}
	if !prepared {
		return coroNativeFleetOwnerWaitPlanV1{}, true
	}
	// Prepare may consume an advisory request which was published after the
	// owner's pre-idle mailbox scan. Import again while the executor gate is
	// IdleArmed and before CommitSleep: an already-published transfer becomes
	// ready work, while any later publisher observes IdleArmed and must ring the
	// route doorbell. This is the mailbox equivalent of the source-set B scan.
	if _, _, status := coroNativeFleetTryDrainOwnerEpochV1(
		handle,
		epoch,
		coro.RunnableTransferMailboxCapacity,
	); status != coro.RunnableTransferDrainComplete && status != coro.RunnableTransferDrainContended {
		return coroNativeFleetOwnerWaitPlanV1{}, false
	}
	sleep, deadline, hasDeadline, committed := coro.CommitExecutorSleepAt(driver, freshNow)
	if !committed {
		return coroNativeFleetOwnerWaitPlanV1{}, false
	}
	if !sleep {
		return coroNativeFleetOwnerWaitPlanV1{}, true
	}
	plan := coroNativeFleetOwnerWaitPlanV1{
		Epoch:       epoch,
		Deadline:    deadline,
		HasDeadline: hasDeadline,
		Armed:       true,
	}
	if !coroNativeFleetFinishOwnerEpochV1(handle, epoch) {
		return coroNativeFleetOwnerWaitPlanV1{}, false
	}
	return plan, true
}

// coroNativeFleetWakeOwnerAtV1 reacquires the exact domain after its physical
// wait returned and services every durable source at a fresh monotonic sample.
// A spurious platform wake is legal and simply promotes zero tasks.
func coroNativeFleetWakeOwnerAtV1(
	handle coro.ExecutorFleetHandle,
	now int64,
) (epoch uint32, ok bool) {
	if now < 0 {
		return 0, false
	}
	epoch, acquired := coroNativeFleetBeginOwnerEpochV1(handle)
	if !acquired {
		return 0, false
	}
	domain, valid := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || domain.ownerEpoch != epoch {
		return 0, false
	}
	driver := domain.driverOwnerV1()
	if driver == nil {
		return 0, false
	}
	if !coro.WakeExecutorAt(driver, now) {
		return 0, false
	}
	return epoch, true
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
	if !ok || domain.ownerEpoch != 0 ||
		!coro.BeginExecutorFleetClose(&coroNativeFleetV1State.fleet, handle) {
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
	if domain.adopted {
		if !coroNativeFleetReleaseAdoptedOwnersV1(domain) {
			domain.lifecycle = coroNativeFleetDomainFailedV1
			return false
		}
	}
	domain.lifecycle = coroNativeFleetDomainRetiredV1
	return true
}

// coroNativeFleetBeginExternalDriverCloseV1 records that the adopted program
// driver already sealed its authoritative request gate through command or
// terminal close. Route ingress and target backend are nevertheless retired by
// the fleet first; only the driver transition itself is delegated.
func coroNativeFleetBeginExternalDriverCloseV1(handle coro.ExecutorFleetHandle) bool {
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainBackendRetiredV1,
	)
	return ok && domain.adopted &&
		coro.BeginExecutorFleetExternalDriverClose(&coroNativeFleetV1State.fleet, handle)
}

// coroNativeFleetConfirmExternalDriverCloseV1 runs only after the authoritative
// program Confirm call zeroed its driver and unbound every source. It clears
// the fleet mailbox/pointer suffix, then releases the retained owner references.
func coroNativeFleetConfirmExternalDriverCloseV1(handle coro.ExecutorFleetHandle) bool {
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainBackendRetiredV1,
	)
	if !ok || !domain.adopted ||
		!coro.ConfirmExecutorFleetExternalClose(&coroNativeFleetV1State.fleet, handle) ||
		!coroNativeFleetReleaseAdoptedOwnersV1(domain) {
		return false
	}
	domain.lifecycle = coroNativeFleetDomainRetiredV1
	return true
}

func coroNativeFleetAllRetiredV1() bool {
	state := &coroNativeFleetV1State
	if state.lifecycle != coroNativeFleetClosingV1 || state.domainCount == 0 ||
		state.domainCount > coroNativeFleetDomainCapacityV1 || !state.fleet.AllRetired() {
		return false
	}
	for index := range state.domains {
		domain := &state.domains[index]
		if uint32(index) >= state.domainCount {
			if !coroNativeFleetDomainCandidateV1(domain) {
				return false
			}
			continue
		}
		if domain.lifecycle != coroNativeFleetDomainRetiredV1 || domain.ownerEpoch != 0 ||
			domain.owners != (coroNativeFleetDomainOwnersV1{}) ||
			!domain.ingress.Retired() || !domain.doorbell.Closed() ||
			!domain.admission.CanRelease() || domain.driver != (coro.ExecutorDriver{}) ||
			!domain.timers.CanRelease() || !domain.poll.CanRelease() ||
			!domain.manual.CanRelease() ||
			!domain.worker.CanRelease() || !domain.channel.CanRelease() || !domain.control.CanRelease() {
			return false
		}
	}
	state.lifecycle = coroNativeFleetRetiredV1
	return true
}
