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
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

const (
	coroChanParkMagicV1      uint32 = 0x43485031 // "CHP1"
	coroChanOperationMagicV1 uint32 = 0x43484f31 // "CHO1"
	coroChanSelectMagicV1    uint32 = 0x43485331 // "CHS1"
)

// coroChanOperationV1 is the common compact endpoint embedded by direct
// channel parks and every case of a multi-channel select. Keeping arbitration
// here lets chanWaiter stay agnostic to the surrounding frame layout without
// introducing an interface or allocating one object per case.
type coroChanOperationV1 struct {
	id     coro.OperationID
	claim  *coro.SelectClaim
	waiter *chanWaiter
	source *coro.ChannelOperationSource
	magic  uint32
	direct bool
}

type coroChanCleanupPhaseV1 uint8

const (
	coroChanCleanupIdleV1 coroChanCleanupPhaseV1 = iota
	coroChanCleanupBufferRecvV1
	coroChanCleanupBufferSendV1
	coroChanCleanupClosedRecvV1
	coroChanCleanupClosedSendV1
)

// coroChanCleanupCursorV1 is frame-local scheduler work, not hchan state. It
// releases the hchan mutex after every bounded reduction and never survives
// the old-P materialization barrier. status records the physical hchan effect;
// deliver separately records whether that effect is the logical Go result. A
// strong task stop may cancel the continuation after a peer already committed,
// in which case status is complete while deliver remains false.
type coroChanCleanupCursorV1 struct {
	ch      *Chan
	status  waitStatus
	phase   coroChanCleanupPhaseV1
	deliver bool
}

// CoroChanParkV1 is compiler-spilled storage for one direct blocking channel
// operation. It is not a Future/Task object and is never separately allocated:
// LLGo emits one typed alloca which LLVM CoroSplit retains only on the slow
// path. The hchan queue points at waiter while source admission pins this exact
// coroutine frame through commit or cancellation cleanup.
//
// The type is exported solely so the compiler can request its target layout
// from the frozen runtime package. Its fields remain runtime-private and no Go
// aggregate crosses a C or compiler hook ABI. Storage is zero-filled with its
// containing coroutine frame before the first prepare; every successful
// resume restores the complete zero value before the compiler can reuse it.
type CoroChanParkV1 struct {
	coro.DirectChannelParkStorageV1
	waiter chanWaiter
	magic  uint32
}

// CoroChanSelectCaseV1 is one compiler-spilled physical channel candidate.
// Its typed value remains in the adjacent ChanOp storage emitted by the
// compiler; this object owns only queue linkage and the exact source endpoint.
type CoroChanSelectCaseV1 struct {
	operation coroChanOperationV1
	waiter    chanWaiter
	order     uint32
}

// CoroChanSelectV1 is shared compiler-spilled state for one blocking select.
// candidates points to the sibling fixed-size alloca in the same LLVM
// coroutine frame. Neither object is a Future and neither is separately
// allocated by the runtime.
type CoroChanSelectV1 struct {
	wait       coro.WaitSetRecord
	claim      coro.SelectClaim
	packet     coro.ResumePacket
	cleanup    coro.ResumeCleanupPlan
	reconcile  coroChanCleanupCursorV1
	ticket     coro.ParkTicket
	candidates unsafe.Pointer
	count      uintptr
	magic      uint32
}

type coroChanMatchResult uint8

const (
	coroChanMatchInvalid coroChanMatchResult = iota
	coroChanMatchCommitted
	coroChanMatchDiscarded
	coroChanMatchRetry
)

const (
	coroChanResumeInvalid uint32 = iota
	coroChanResumeSendOK
	coroChanResumeRecvOK
	coroChanResumeRecvClosed
	coroChanResumeSendClosed
	coroChanResumeTaskAbort
	coroChanResumeShutdown
)

func validCoroChanOperationV1(operation *coroChanOperationV1, waiter *chanWaiter) bool {
	if operation == nil || waiter == nil || operation.magic != coroChanOperationMagicV1 ||
		operation.waiter != waiter || waiter.coro != operation || !operation.id.Valid() ||
		operation.claim == nil || waiter.ch == nil || waiter.status > waitSendClosed || waiter.size < 0 ||
		operation.source == nil {
		return false
	}
	route, ok := operation.source.Route()
	return ok && route == operation.id.Route()
}

func validCoroChanCleanupCursorV1(cursor *coroChanCleanupCursorV1) bool {
	if cursor == nil {
		return false
	}
	if cursor.phase == coroChanCleanupIdleV1 {
		return *cursor == (coroChanCleanupCursorV1{})
	}
	return cursor.ch != nil &&
		cursor.phase >= coroChanCleanupBufferRecvV1 &&
		cursor.phase <= coroChanCleanupClosedSendV1 &&
		cursor.status <= waitSendClosed &&
		(!cursor.deliver || cursor.status.done())
}

func coroChanSelectCaseAt(base unsafe.Pointer, index uintptr) *CoroChanSelectCaseV1 {
	return (*CoroChanSelectCaseV1)(unsafe.Add(base, index*unsafe.Sizeof(CoroChanSelectCaseV1{})))
}

func coroChanSelectOrderLess(candidates unsafe.Pointer, ops []ChanOp, left, right int) bool {
	leftIndex := coroChanSelectCaseAt(candidates, uintptr(left)).order
	rightIndex := coroChanSelectCaseAt(candidates, uintptr(right)).order
	leftAddress := uintptr(unsafe.Pointer(ops[leftIndex].C))
	rightAddress := uintptr(unsafe.Pointer(ops[rightIndex].C))
	return leftAddress < rightAddress || leftAddress == rightAddress && leftIndex < rightIndex
}

func swapCoroChanSelectOrder(candidates unsafe.Pointer, left, right int) {
	a := coroChanSelectCaseAt(candidates, uintptr(left))
	b := coroChanSelectCaseAt(candidates, uintptr(right))
	a.order, b.order = b.order, a.order
}

func siftDownCoroChanSelectOrder(candidates unsafe.Pointer, ops []ChanOp, root, end int) {
	for {
		child := root*2 + 1
		if child >= end {
			return
		}
		if child+1 < end && coroChanSelectOrderLess(candidates, ops, child, child+1) {
			child++
		}
		if !coroChanSelectOrderLess(candidates, ops, root, child) {
			return
		}
		swapCoroChanSelectOrder(candidates, root, child)
		root = child
	}
}

// sortCoroChanSelectOrder builds a channel-address lock permutation in the
// candidate array itself. Heap sort needs no interface dispatch, recursion,
// or auxiliary allocation and remains O(n log n) for large source selects.
func sortCoroChanSelectOrder(candidates unsafe.Pointer, ops []ChanOp) {
	for index := range ops {
		coroChanSelectCaseAt(candidates, uintptr(index)).order = uint32(index)
	}
	for root := len(ops)/2 - 1; root >= 0; root-- {
		siftDownCoroChanSelectOrder(candidates, ops, root, len(ops))
	}
	for end := len(ops) - 1; end > 0; end-- {
		swapCoroChanSelectOrder(candidates, 0, end)
		siftDownCoroChanSelectOrder(candidates, ops, 0, end)
	}
}

func lockCoroChanSelectChannels(candidates unsafe.Pointer, ops []ChanOp) {
	var previous *Chan
	for position := range ops {
		index := coroChanSelectCaseAt(candidates, uintptr(position)).order
		ch := ops[index].C
		if ch != nil && ch != previous {
			ch.mutex.Lock()
			previous = ch
		}
	}
}

func unlockCoroChanSelectChannels(candidates unsafe.Pointer, ops []ChanOp) {
	var previous *Chan
	for position := len(ops) - 1; position >= 0; position-- {
		index := coroChanSelectCaseAt(candidates, uintptr(position)).order
		ch := ops[index].C
		if ch != nil && ch != previous {
			ch.mutex.Unlock()
			previous = ch
		}
	}
}

func validCoroChanSelectV1(state *CoroChanSelectV1, candidates unsafe.Pointer, ops []ChanOp) bool {
	return state != nil && state.magic == coroChanSelectMagicV1 && state.candidates == candidates &&
		state.count == uintptr(len(ops)) && (len(ops) == 0 || candidates != nil) &&
		state.reconcile == (coroChanCleanupCursorV1{})
}

func classifyCoroChanSingleBegin(result coro.ChannelExternalCommitBeginResult) coroChanMatchResult {
	switch result {
	case coro.ChannelExternalCommitBeginPrepared:
		return coroChanMatchCommitted
	case coro.ChannelExternalCommitBeginAdmissionFailed:
		// Apply may already have sealed a canceled/losing endpoint. Its queue
		// node is stale and can be dropped; resume cleanup owns the generation.
		return coroChanMatchDiscarded
	case coro.ChannelExternalCommitBeginClaimResolved:
		// Another case already owns the logical select. Admission still pinned
		// this queue node long enough to classify it as stale.
		return coroChanMatchDiscarded
	case coro.ChannelExternalCommitBeginClaimContended:
		return coroChanMatchRetry
	default:
		return coroChanMatchInvalid
	}
}

func classifyCoroChanPairBegin(result coro.ChannelExternalCommitPairBeginResult) coroChanMatchResult {
	switch result {
	case coro.ChannelExternalCommitPairBeginPrepared:
		return coroChanMatchCommitted
	case coro.ChannelExternalCommitPairBeginFirstAdmissionFailed,
		coro.ChannelExternalCommitPairBeginSecondAdmissionFailed:
		return coroChanMatchDiscarded
	case coro.ChannelExternalCommitPairBeginClaimResolved:
		return coroChanMatchDiscarded
	case coro.ChannelExternalCommitPairBeginClaimContended:
		return coroChanMatchRetry
	default:
		return coroChanMatchInvalid
	}
}

// coroChanCompletionEndpointV1 is the only source identity needed after an
// irreversible hchan commit. Keeping it outside compiler-spilled waiter state
// lets an exact open/unbuffered direct rendezvous retire that state before the
// executor can observe the completion.
type coroChanCompletionEndpointV1 struct {
	source *coro.ChannelOperationSource
	id     coro.OperationID
}

func coroChanCompletionEndpointForV1(
	operation *coroChanOperationV1,
) (coroChanCompletionEndpointV1, bool) {
	if operation == nil || operation.source == nil || !operation.id.Valid() {
		return coroChanCompletionEndpointV1{}, false
	}
	return coroChanCompletionEndpointV1{source: operation.source, id: operation.id}, true
}

// coroChanDirectResultFinalizableV1 recognizes the exact typed cleanup which
// is a no-op: a compiler-owned direct waiter has already been detached from an
// open unbuffered hchan and its payload/status effect is complete. Buffered
// channels, close propagation, select candidates, cancellation, and any node
// still linked in a queue retain the bounded runtime cleanup cursor.
func coroChanDirectResultFinalizableV1(
	operation *coroChanOperationV1,
	status waitStatus,
) bool {
	if operation == nil || !operation.direct || operation.waiter == nil ||
		!validCoroChanOperationV1(operation, operation.waiter) || !status.done() {
		return false
	}
	return coroChanDirectCommitShapeV1(operation, status, true)
}

// coroChanDirectCommitShapeV1 checks only the hchan-owned half of the exact
// direct waiter certificate. Callers which already proved
// validCoroChanOperationV1 under this hchan lock can retain that proof across
// the no-suspend transaction instead of revalidating the same operation,
// waiter, source route, and back-pointers at every phase boundary.
func coroChanDirectCommitShapeV1(
	operation *coroChanOperationV1,
	status waitStatus,
	completed bool,
) bool {
	waiter := operation.waiter
	ch := waiter.ch
	wantStatus := waitPending
	if completed {
		wantStatus = status
	}
	return operation.direct && status.done() && waiter.status == wantStatus &&
		!waiter.queued && waiter.prev == nil && waiter.next == nil &&
		ch != nil && ch.dataqsiz == 0 && ch.qcount == 0 && !ch.closed
}

// coroChanDirectCommitCandidateV1 is the pre-effect counterpart of
// coroChanDirectResultFinalizableV1. The exact waiter has been detached under
// its hchan lock but its typed payload and terminal status have not yet been
// written. Only this open, unbuffered, one-operation shape may prepare the
// owner-local source fast lane.
func coroChanDirectCommitCandidateV1(
	operation *coroChanOperationV1,
	status waitStatus,
) bool {
	if operation == nil || !operation.direct || operation.waiter == nil ||
		!validCoroChanOperationV1(operation, operation.waiter) || !status.done() {
		return false
	}
	return coroChanDirectCommitShapeV1(operation, status, false)
}

func finalizeCoroChanDirectResultV1(operation *coroChanOperationV1, recycled bool) bool {
	if operation == nil || operation.waiter == nil {
		return false
	}
	id, waiter := operation.id, operation.waiter
	if recycled {
		ch := waiter.ch
		if id != (coro.OperationID{}) || !operation.direct ||
			operation.magic != coroChanOperationMagicV1 || operation.claim == nil ||
			operation.source == nil || waiter.coro != operation || !waiter.status.done() ||
			waiter.queued || waiter.prev != nil || waiter.next != nil || ch == nil ||
			ch.dataqsiz != 0 || ch.qcount != 0 || ch.closed {
			return false
		}
		*waiter = chanWaiter{}
		*operation = coroChanOperationV1{}
		return true
	}
	if !id.Valid() {
		return false
	}
	*waiter = chanWaiter{}
	*operation = coroChanOperationV1{id: id}
	return true
}

func publishCoroChannelOwnerLocalV1(
	current *coro.G,
	driver *coro.ExecutorDriver,
	endpoint coroChanCompletionEndpointV1,
) (published, ok bool) {
	if endpoint.source == nil || !endpoint.id.Valid() {
		return false, false
	}
	if current != nil && driver != nil {
		return coro.TryPublishOwnerLocalChannelCompletionCurrent(
			current,
			driver,
			endpoint.source,
			endpoint.id,
		)
	}
	return false, true
}

type coroChanExternalCommitContextV1 struct {
	current          *coro.G
	driver           *coro.ExecutorDriver
	route            coro.RouteID
	ownerLocalDirect bool
	directResult     bool
}

// coroChanExternalContextValueV1 turns an optional optimization capability
// into an explicit value without consulting ambient runtime state. A zero
// value retains the fully routed correctness path; wrappers which actually
// own a current-task capability sample it before entering the shared channel
// transaction.
func coroChanExternalContextValueV1(
	context *coroChanExternalCommitContextV1,
) coroChanExternalCommitContextV1 {
	if context != nil {
		return *context
	}
	return coroChanExternalCommitContextV1{}
}

func currentCoroChannelExternalContextV1() coroChanExternalCommitContextV1 {
	current, driver, route := coroCurrentTaskV1()
	return coroChanExternalCommitContextV1{
		current: current,
		driver:  driver,
		route:   route,
	}
}

// coroChannelExternalContextForTaskV1 consumes the compiler-carried logical
// task directly. Channel lowering already owns this value as its first hidden
// coroutine parameter, so recovering it through getg/TLS on every rendezvous
// would both repeat work and unnecessarily disable owner-local completion on
// targets without a native TLS-backed current-task adapter.
func coroChannelExternalContextForTaskV1(task *coro.G) coroChanExternalCommitContextV1 {
	// Direct one-case completion consumes the compiler-carried task and derives
	// its exact P/driver capability in the same core transaction which commits
	// the peer. Do not decompose that capability here merely to make the core
	// reconstruct and revalidate it a second time. A queued select endpoint asks
	// resolveCoroChannelExternalContextV1 for the richer source capability only
	// on that less common branch.
	return coroChanExternalCommitContextV1{current: task}
}

func resolveCoroChannelExternalContextV1(
	context coroChanExternalCommitContextV1,
) coroChanExternalCommitContextV1 {
	if context.current == nil || context.driver != nil {
		return context
	}
	driver, route, current := coro.CurrentExecutorDriverForCompilerTask(context.current)
	if !current {
		// Compatibility adapters do not retain the bounded runner's private
		// issued marker. They still admit the complete arbitrary-caller proof.
		var handle coro.ExecutorHandle
		driver, handle, route, current = coro.CurrentExecutorDriver(context.current)
		if !current || handle.Slot == 0 || handle.Generation == 0 {
			return coroChanExternalCommitContextV1{}
		}
	}
	context.driver = driver
	context.route = route
	return context
}

func finishDirectCoroChannelCompletionV1(
	waiter *chanWaiter,
	status waitStatus,
	context *coroChanExternalCommitContextV1,
) bool {
	if waiter == nil || waiter.direct == nil || waiter.coro != nil || !status.done() {
		return false
	}
	current, route := (*coro.G)(nil), coro.RouteID(0)
	if context != nil {
		current, route = context.current, context.route
	}
	owner, route, result := coro.FinishDirectChannelCompletionFromCompilerTask(
		current, waiter.direct, uint8(status), route,
	)
	switch result {
	case coro.DirectChannelCompletionFinishInline:
		*waiter = chanWaiter{}
		return true
	case coro.DirectChannelCompletionFinishOwnerPublished:
		return true
	case coro.DirectChannelCompletionFinishNeedsTarget:
		return coroTargetPublishDirectChannelCompletionV1(owner, route, waiter.direct)
	default:
		return false
	}
}

func prepareDirectCoroRecvWaiterLockedV1(
	waiter *chanWaiter,
	src unsafe.Pointer,
	eltSize int,
	status waitStatus,
) coroChanMatchResult {
	if waiter == nil || waiter.direct == nil || waiter.coro != nil || waiter.send ||
		waiter.size != eltSize || !status.done() || status == waitSendClosed {
		return coroChanMatchInvalid
	}
	switch coro.BeginDirectChannelCompletion(waiter.direct) {
	case coro.DirectChannelCompletionBeginCanceled:
		return coroChanMatchDiscarded
	case coro.DirectChannelCompletionBeginAcquired:
	default:
		return coroChanMatchInvalid
	}
	if status.recvOK() {
		copyChanElem(waiter.elem, src, eltSize)
	} else {
		zeroChanRecv(waiter.elem, eltSize)
	}
	waiter.status = status
	return coroChanMatchCommitted
}

func commitDirectCoroRecvWaiterLockedV1(
	waiter *chanWaiter,
	src unsafe.Pointer,
	eltSize int,
	status waitStatus,
	context *coroChanExternalCommitContextV1,
) coroChanMatchResult {
	if result := prepareDirectCoroRecvWaiterLockedV1(
		waiter, src, eltSize, status,
	); result != coroChanMatchCommitted {
		return result
	}
	if !finishDirectCoroChannelCompletionV1(waiter, status, context) {
		return coroChanMatchInvalid
	}
	return coroChanMatchCommitted
}

func prepareDirectCoroSendWaiterLockedV1(
	waiter *chanWaiter,
	dst unsafe.Pointer,
	eltSize int,
	status waitStatus,
) coroChanMatchResult {
	if waiter == nil || waiter.direct == nil || waiter.coro != nil || !waiter.send ||
		waiter.size != eltSize || (status != waitSendOK && status != waitSendClosed) {
		return coroChanMatchInvalid
	}
	switch coro.BeginDirectChannelCompletion(waiter.direct) {
	case coro.DirectChannelCompletionBeginCanceled:
		return coroChanMatchDiscarded
	case coro.DirectChannelCompletionBeginAcquired:
	default:
		return coroChanMatchInvalid
	}
	if status == waitSendOK {
		copyChanElem(dst, waiter.elem, eltSize)
	}
	waiter.status = status
	return coroChanMatchCommitted
}

func commitDirectCoroSendWaiterLockedV1(
	waiter *chanWaiter,
	dst unsafe.Pointer,
	eltSize int,
	status waitStatus,
	context *coroChanExternalCommitContextV1,
) coroChanMatchResult {
	if result := prepareDirectCoroSendWaiterLockedV1(
		waiter, dst, eltSize, status,
	); result != coroChanMatchCommitted {
		return result
	}
	if !finishDirectCoroChannelCompletionV1(waiter, status, context) {
		return coroChanMatchInvalid
	}
	return coroChanMatchCommitted
}

// prepareCoroChannelExternalV1 samples the managed-owner capability before the
// no-return effect boundary. The common path reuses the same sample after the
// typed transfer; the exact direct shape may additionally reserve an empty
// same-P source slot for mailbox-free publication.
func prepareCoroChannelExternalV1(
	transaction *coro.ChannelExternalCommit,
	operation *coroChanOperationV1,
	status waitStatus,
) coroChanExternalCommitContextV1 {
	context := currentCoroChannelExternalContextV1()
	return prepareCoroChannelExternalWithContextV1(transaction, operation, status, context)
}

func prepareCoroChannelExternalWithContextV1(
	transaction *coro.ChannelExternalCommit,
	operation *coroChanOperationV1,
	status waitStatus,
	context coroChanExternalCommitContextV1,
) coroChanExternalCommitContextV1 {
	context.ownerLocalDirect = false
	context.directResult = coroChanDirectCommitCandidateV1(operation, status)
	if context.directResult &&
		context.current != nil && context.driver != nil {
		context.ownerLocalDirect = transaction.PrepareOwnerLocalDirect(
			context.current,
			context.driver,
		)
	}
	return context
}

// beginCoroChannelExternalV1 is the queued-waiter direct ingress. The exact
// current-G capability is sampled once; on the common same-P shape the source
// transaction consumes it while acquiring admission, avoiding a second full
// endpoint audit after the select claim has already excluded its owner.
func beginCoroChannelExternalV1(
	waiter *chanWaiter,
	status waitStatus,
	transaction *coro.ChannelExternalCommit,
) (coroChanMatchResult, coroChanExternalCommitContextV1) {
	context := currentCoroChannelExternalContextV1()
	return beginCoroChannelExternalWithContextV1(waiter, status, transaction, context)
}

func beginCoroChannelExternalWithContextV1(
	waiter *chanWaiter,
	status waitStatus,
	transaction *coro.ChannelExternalCommit,
	context coroChanExternalCommitContextV1,
) (coroChanMatchResult, coroChanExternalCommitContextV1) {
	return beginCoroChannelExternalValidatedWithContextV1(
		waiter, status, transaction, context,
		coroChanDirectCommitCandidateV1(waiter.coro, status),
	)
}

func beginCoroChannelExternalValidatedWithContextV1(
	waiter *chanWaiter,
	status waitStatus,
	transaction *coro.ChannelExternalCommit,
	context coroChanExternalCommitContextV1,
	directResult bool,
) (coroChanMatchResult, coroChanExternalCommitContextV1) {
	context = resolveCoroChannelExternalContextV1(context)
	context.ownerLocalDirect = false
	context.directResult = directResult
	direct := directResult &&
		context.current != nil && context.driver != nil
	for {
		var result coro.ChannelExternalCommitBeginResult
		if direct {
			result, context.ownerLocalDirect = coro.BeginChannelOwnerLocalDirectCommit(
				transaction,
				waiter.coro.source,
				waiter.coro.id,
				waiter.coro.claim,
				context.current,
				context.driver,
			)
		} else {
			result = coro.BeginChannelExternalCommit(
				transaction,
				waiter.coro.source,
				waiter.coro.id,
				waiter.coro.claim,
			)
		}
		classified := classifyCoroChanSingleBegin(result)
		if classified != coroChanMatchRetry {
			return classified, context
		}
	}
}

// commitCoroChannelExternalV1 samples the current managed owner once for the
// complete post-effect tail. Previously CommitAtRoute and owner-local
// publication each repeated the TLS/runtime-context/driver proof, making a
// successful same-P rendezvous pay that full lookup three times. The physical
// transaction still publishes before any scheduler-local mutation, and a nil
// current retains the ordinary routed producer path.
func commitCoroChannelExternalV1(
	transaction *coro.ChannelExternalCommit,
	operation *coroChanOperationV1,
	status waitStatus,
	context coroChanExternalCommitContextV1,
) bool {
	endpoint, endpointOK := coroChanCompletionEndpointForV1(operation)
	if transaction == nil || !status.done() || !endpointOK {
		return false
	}
	directResult := context.directResult
	if context.ownerLocalDirect {
		if !directResult {
			return false
		}
		switch transaction.CommitOwnerLocalDirectWithResult(uint8(status)) {
		case coro.ChannelOwnerLocalCommitted:
			return finalizeCoroChanDirectResultV1(operation, false)
		case coro.ChannelOwnerLocalCompletedInline:
			return finalizeCoroChanDirectResultV1(operation, true)
		case coro.ChannelOwnerLocalCommitFallback:
		case coro.ChannelOwnerLocalCommitInvalid:
			return false
		default:
			return false
		}
	}
	var committed bool
	if directResult {
		committed = transaction.CommitAtRouteWithResult(context.route, uint8(status))
	} else {
		committed = transaction.CommitAtRoute(context.route)
	}
	if !committed {
		return false
	}
	local, localOK := publishCoroChannelOwnerLocalV1(context.current, context.driver, endpoint)
	if !localOK {
		return false
	}
	if local {
		return !directResult || finalizeCoroChanDirectResultV1(operation, false)
	}
	return coroTargetRequestChannelOperationV1(endpoint.id)
}

func commitCoroChannelExternalPairV1(
	transaction *coro.ChannelExternalCommitPair,
	first, second *coroChanOperationV1,
) bool {
	firstEndpoint, firstEndpointOK := coroChanCompletionEndpointForV1(first)
	secondEndpoint, secondEndpointOK := coroChanCompletionEndpointForV1(second)
	if transaction == nil || !firstEndpointOK || !secondEndpointOK {
		return false
	}
	firstDirect := coroChanDirectResultFinalizableV1(first, waitSendOK)
	secondDirect := coroChanDirectResultFinalizableV1(second, waitRecvOK)
	firstSmall, secondSmall := uint8(coro.ResumeSmallInvalid), uint8(coro.ResumeSmallInvalid)
	if firstDirect {
		firstSmall = uint8(waitSendOK)
	}
	if secondDirect {
		secondSmall = uint8(waitRecvOK)
	}
	current, driver, route := coroCurrentTaskV1()
	if !transaction.CommitAtRouteWithResults(route, firstSmall, secondSmall) ||
		firstEndpoint.source == nil || secondEndpoint.source == nil {
		return false
	}
	firstLocal, firstOK := publishCoroChannelOwnerLocalV1(current, driver, firstEndpoint)
	secondLocal, secondOK := publishCoroChannelOwnerLocalV1(current, driver, secondEndpoint)
	if !firstOK || !secondOK ||
		firstLocal && firstDirect && !finalizeCoroChanDirectResultV1(first, false) ||
		secondLocal && secondDirect && !finalizeCoroChanDirectResultV1(second, false) {
		return false
	}
	if !firstLocal && !coroTargetRequestChannelOperationV1(firstEndpoint.id) {
		return false
	}
	if secondLocal || !firstLocal && firstEndpoint.id.Route() == secondEndpoint.id.Route() {
		return true
	}
	return coroTargetRequestChannelOperationV1(secondEndpoint.id)
}

func commitCoroRecvWaiterLocked(w *chanWaiter, src unsafe.Pointer, eltSize int, status waitStatus) coroChanMatchResult {
	return commitCoroRecvWaiterLockedWithContext(
		w, src, eltSize, status, currentCoroChannelExternalContextV1(),
	)
}

func commitCoroRecvWaiterLockedWithContext(
	w *chanWaiter,
	src unsafe.Pointer,
	eltSize int,
	status waitStatus,
	context coroChanExternalCommitContextV1,
) coroChanMatchResult {
	if !validCoroChanOperationV1(w.coro, w) || w.send || w.size != eltSize ||
		!status.done() || status == waitSendClosed {
		return coroChanMatchInvalid
	}
	directResult := coroChanDirectCommitShapeV1(w.coro, status, false)
	var transaction coro.ChannelExternalCommit
	classified, context := beginCoroChannelExternalValidatedWithContextV1(
		w, status, &transaction, context, directResult,
	)
	if classified != coroChanMatchCommitted {
		return classified
	}
	if !transaction.BeginEffect() {
		return coroChanMatchInvalid
	}
	if status.recvOK() {
		copyChanElem(w.elem, src, eltSize)
	} else {
		zeroChanRecv(w.elem, eltSize)
	}
	w.status = status
	if !commitCoroChannelExternalV1(&transaction, w.coro, status, context) {
		return coroChanMatchInvalid
	}
	return coroChanMatchCommitted
}

func commitCoroSendWaiterLocked(w *chanWaiter, dst unsafe.Pointer, eltSize int, status waitStatus) coroChanMatchResult {
	return commitCoroSendWaiterLockedWithContext(
		w, dst, eltSize, status, currentCoroChannelExternalContextV1(),
	)
}

func commitCoroSendWaiterLockedWithContext(
	w *chanWaiter,
	dst unsafe.Pointer,
	eltSize int,
	status waitStatus,
	context coroChanExternalCommitContextV1,
) coroChanMatchResult {
	if !validCoroChanOperationV1(w.coro, w) || !w.send || w.size != eltSize ||
		(status != waitSendOK && status != waitSendClosed) {
		return coroChanMatchInvalid
	}
	directResult := coroChanDirectCommitShapeV1(w.coro, status, false)
	var transaction coro.ChannelExternalCommit
	classified, context := beginCoroChannelExternalValidatedWithContextV1(
		w, status, &transaction, context, directResult,
	)
	if classified != coroChanMatchCommitted {
		return classified
	}
	if !transaction.BeginEffect() {
		return coroChanMatchInvalid
	}
	if status == waitSendOK {
		copyChanElem(dst, w.elem, eltSize)
	}
	w.status = status
	if !commitCoroChannelExternalV1(&transaction, w.coro, status, context) {
		return coroChanMatchInvalid
	}
	return coroChanMatchCommitted
}

func commitCoroPairLocked(send, recv *chanWaiter, eltSize int) coroChanMatchResult {
	if send == nil || recv == nil || send.coro == nil || recv.coro == nil || send.coro == recv.coro ||
		send.coro.claim == recv.coro.claim || !validCoroChanOperationV1(send.coro, send) ||
		!validCoroChanOperationV1(recv.coro, recv) ||
		!send.send || recv.send || send.ch == nil || send.ch != recv.ch ||
		send.size != eltSize || recv.size != eltSize {
		return coroChanMatchInvalid
	}
	var transaction coro.ChannelExternalCommitPair
	for {
		result := coro.BeginChannelExternalCommitPair(
			&transaction,
			send.coro.source,
			send.coro.id,
			send.coro.claim,
			recv.coro.source,
			recv.coro.id,
			recv.coro.claim,
		)
		classified := classifyCoroChanPairBegin(result)
		if classified == coroChanMatchRetry {
			continue
		}
		if classified != coroChanMatchCommitted {
			return classified
		}
		break
	}
	if !transaction.BeginEffect() {
		return coroChanMatchInvalid
	}
	copyChanElem(recv.elem, send.elem, eltSize)
	send.status = waitSendOK
	recv.status = waitRecvOK
	if !commitCoroChannelExternalPairV1(&transaction, send.coro, recv.coro) {
		return coroChanMatchInvalid
	}
	return coroChanMatchCommitted
}

func beginCurrentCoroChannelCommit(waiter *chanWaiter, transaction *coro.ChannelExternalCommit) coroChanMatchResult {
	if !validCoroChanOperationV1(waiter.coro, waiter) || transaction == nil ||
		*transaction != (coro.ChannelExternalCommit{}) {
		return coroChanMatchInvalid
	}
	for {
		classified := classifyCoroChanSingleBegin(coro.BeginChannelExternalCommit(
			transaction,
			waiter.coro.source,
			waiter.coro.id,
			waiter.coro.claim,
		))
		if classified != coroChanMatchRetry {
			return classified
		}
	}
}

func finishCurrentCoroChannelCommit(
	waiter *chanWaiter,
	transaction *coro.ChannelExternalCommit,
	status waitStatus,
) bool {
	if !validCoroChanOperationV1(waiter.coro, waiter) || transaction == nil || !status.done() {
		return false
	}
	context := prepareCoroChannelExternalV1(transaction, waiter.coro, status)
	if !transaction.BeginEffect() {
		return false
	}
	waiter.status = status
	return commitCoroChannelExternalV1(transaction, waiter.coro, status, context)
}

func coroChanTrySendLocked(ch *Chan, waiter *chanWaiter) (ready bool, ok bool) {
	if ch == nil || !validCoroChanOperationV1(waiter.coro, waiter) || waiter.ch != ch || !waiter.send ||
		waiter.size != ch.elemsize {
		return false, false
	}
	if ch.closed {
		var transaction coro.ChannelExternalCommit
		if beginCurrentCoroChannelCommit(waiter, &transaction) != coroChanMatchCommitted ||
			!finishCurrentCoroChannelCommit(waiter, &transaction, waitSendClosed) {
			return false, false
		}
		return true, true
	}
	for {
		peer := ch.recvq.dequeue()
		if peer == nil {
			break
		}
		if peer.coro != nil {
			switch result := commitCoroPairLocked(waiter, peer, ch.elemsize); result {
			case coroChanMatchCommitted:
				return true, true
			case coroChanMatchDiscarded:
				continue
			case coroChanMatchRetry:
				ch.recvq.enqueueFront(peer)
				return false, true
			default:
				return false, false
			}
		}
		var transaction coro.ChannelExternalCommit
		classified := beginCurrentCoroChannelCommit(waiter, &transaction)
		if classified != coroChanMatchCommitted {
			if classified == coroChanMatchRetry {
				ch.recvq.enqueueFront(peer)
				return false, true
			}
			return false, false
		}
		if !claimWaiter(peer) {
			if !transaction.Abort() {
				return false, false
			}
			continue
		}
		context := prepareCoroChannelExternalV1(&transaction, waiter.coro, waitSendOK)
		if !transaction.BeginEffect() {
			return false, false
		}
		copyChanElem(peer.elem, waiter.elem, ch.elemsize)
		waiter.status = waitSendOK
		peer.finish(waitRecvOK)
		if !commitCoroChannelExternalV1(&transaction, waiter.coro, waitSendOK, context) {
			return false, false
		}
		return true, true
	}
	if ch.qcount < ch.dataqsiz {
		var transaction coro.ChannelExternalCommit
		if beginCurrentCoroChannelCommit(waiter, &transaction) != coroChanMatchCommitted {
			return false, false
		}
		context := prepareCoroChannelExternalV1(&transaction, waiter.coro, waitSendOK)
		if !transaction.BeginEffect() {
			return false, false
		}
		copyChanElem(chanBuf(ch, ch.sendx), waiter.elem, ch.elemsize)
		ch.sendx++
		if ch.sendx == ch.dataqsiz {
			ch.sendx = 0
		}
		ch.qcount++
		waiter.status = waitSendOK
		if !commitCoroChannelExternalV1(&transaction, waiter.coro, waitSendOK, context) {
			return false, false
		}
		return true, true
	}
	return false, true
}

func coroChanTryRecvLocked(ch *Chan, waiter *chanWaiter) (ready bool, ok bool) {
	if ch == nil || !validCoroChanOperationV1(waiter.coro, waiter) || waiter.ch != ch || waiter.send ||
		waiter.size != ch.elemsize {
		return false, false
	}
	if ch.dataqsiz == 0 {
		for {
			peer := ch.sendq.dequeue()
			if peer == nil {
				break
			}
			if peer.coro != nil {
				switch result := commitCoroPairLocked(peer, waiter, ch.elemsize); result {
				case coroChanMatchCommitted:
					return true, true
				case coroChanMatchDiscarded:
					continue
				case coroChanMatchRetry:
					ch.sendq.enqueueFront(peer)
					return false, true
				default:
					return false, false
				}
			}
			var transaction coro.ChannelExternalCommit
			classified := beginCurrentCoroChannelCommit(waiter, &transaction)
			if classified != coroChanMatchCommitted {
				if classified == coroChanMatchRetry {
					ch.sendq.enqueueFront(peer)
					return false, true
				}
				return false, false
			}
			if !claimWaiter(peer) {
				if !transaction.Abort() {
					return false, false
				}
				continue
			}
			context := prepareCoroChannelExternalV1(&transaction, waiter.coro, waitRecvOK)
			if !transaction.BeginEffect() {
				return false, false
			}
			copyChanElem(waiter.elem, peer.elem, ch.elemsize)
			waiter.status = waitRecvOK
			peer.finish(waitSendOK)
			if !commitCoroChannelExternalV1(&transaction, waiter.coro, waitRecvOK, context) {
				return false, false
			}
			return true, true
		}
	} else if ch.qcount > 0 {
		var transaction coro.ChannelExternalCommit
		if beginCurrentCoroChannelCommit(waiter, &transaction) != coroChanMatchCommitted {
			return false, false
		}
		context := prepareCoroChannelExternalV1(&transaction, waiter.coro, waitRecvOK)
		if !transaction.BeginEffect() {
			return false, false
		}
		copyChanElem(waiter.elem, chanBuf(ch, ch.recvx), ch.elemsize)
		zeroChanRecv(chanBuf(ch, ch.recvx), ch.elemsize)
		ch.recvx++
		if ch.recvx == ch.dataqsiz {
			ch.recvx = 0
		}
		ch.qcount--
		waiter.status = waitRecvOK
		if !commitCoroChannelExternalV1(&transaction, waiter.coro, waitRecvOK, context) {
			return false, false
		}
		// Refill is a separate committed sender endpoint under the same hchan
		// lock. Failure leaves the now-available buffer slot visible to a later
		// sender without changing the completed receive.
		dequeueSendToBuffer(ch, &context)
		return true, true
	}
	if ch.closed {
		var transaction coro.ChannelExternalCommit
		if beginCurrentCoroChannelCommit(waiter, &transaction) != coroChanMatchCommitted {
			return false, false
		}
		context := prepareCoroChannelExternalV1(&transaction, waiter.coro, waitRecvClosed)
		if !transaction.BeginEffect() {
			return false, false
		}
		zeroChanRecv(waiter.elem, ch.elemsize)
		waiter.status = waitRecvClosed
		if !commitCoroChannelExternalV1(&transaction, waiter.coro, waitRecvClosed, context) {
			return false, false
		}
		return true, true
	}
	return false, true
}

type coroChanQueueStepV1 uint8

const (
	coroChanQueueInvalidV1 coroChanQueueStepV1 = iota
	coroChanQueueIdleV1
	coroChanQueueCommittedV1
	coroChanQueueDiscardedV1
	coroChanQueueBlockedV1
)

// dequeueSendToBufferStepLocked examines and removes at most one queued
// sender. The caller owns ch.mutex.
func dequeueSendToBufferStepLocked(
	ch *Chan,
	context *coroChanExternalCommitContextV1,
) coroChanQueueStepV1 {
	if ch == nil || ch.closed || ch.qcount >= ch.dataqsiz {
		return coroChanQueueIdleV1
	}
	w := ch.sendq.dequeue()
	if w == nil {
		return coroChanQueueIdleV1
	}
	if w.direct != nil {
		result := commitDirectCoroSendWaiterLockedV1(
			w, chanBuf(ch, ch.sendx), ch.elemsize, waitSendOK, context,
		)
		switch result {
		case coroChanMatchCommitted:
			ch.sendx++
			if ch.sendx == ch.dataqsiz {
				ch.sendx = 0
			}
			ch.qcount++
			return coroChanQueueCommittedV1
		case coroChanMatchDiscarded:
			return coroChanQueueDiscardedV1
		default:
			return coroChanQueueInvalidV1
		}
	}
	if w.coro != nil {
		result := commitCoroSendWaiterLockedWithContext(
			w, chanBuf(ch, ch.sendx), ch.elemsize, waitSendOK,
			coroChanExternalContextValueV1(context),
		)
		switch result {
		case coroChanMatchCommitted:
			ch.sendx++
			if ch.sendx == ch.dataqsiz {
				ch.sendx = 0
			}
			ch.qcount++
			return coroChanQueueCommittedV1
		case coroChanMatchDiscarded:
			return coroChanQueueDiscardedV1
		case coroChanMatchRetry:
			ch.sendq.enqueueFront(w)
			return coroChanQueueBlockedV1
		default:
			return coroChanQueueInvalidV1
		}
	}
	if !claimWaiter(w) {
		return coroChanQueueDiscardedV1
	}
	copyChanElem(chanBuf(ch, ch.sendx), w.elem, ch.elemsize)
	ch.sendx++
	if ch.sendx == ch.dataqsiz {
		ch.sendx = 0
	}
	ch.qcount++
	w.finish(waitSendOK)
	return coroChanQueueCommittedV1
}

func dequeueSendToBufferLocked(
	ch *Chan,
	context *coroChanExternalCommitContextV1,
) (progress, ok bool) {
	for {
		switch dequeueSendToBufferStepLocked(ch, context) {
		case coroChanQueueCommittedV1:
			return true, true
		case coroChanQueueDiscardedV1:
			continue
		case coroChanQueueIdleV1, coroChanQueueBlockedV1:
			return false, true
		default:
			return false, false
		}
	}
}

func dequeueSendToBuffer(ch *Chan, context *coroChanExternalCommitContextV1) {
	if _, ok := dequeueSendToBufferLocked(ch, context); !ok {
		coroRuntimeAbort("invalid coroutine channel buffer refill")
	}
}

// dequeueBufferToRecvStepLocked examines and removes at most one queued
// receiver. The buffer position advances only after the exact receiver
// transaction has committed its typed copy.
func dequeueBufferToRecvStepLocked(
	ch *Chan,
	context *coroChanExternalCommitContextV1,
) coroChanQueueStepV1 {
	if ch == nil || ch.qcount == 0 {
		return coroChanQueueIdleV1
	}
	w := ch.recvq.dequeue()
	if w == nil {
		return coroChanQueueIdleV1
	}
	switch result := completeRecvWaiterWithContext(
		w, chanBuf(ch, ch.recvx), ch.elemsize, waitRecvOK, context,
	); result {
	case coroChanMatchCommitted:
		zeroChanRecv(chanBuf(ch, ch.recvx), ch.elemsize)
		ch.recvx++
		if ch.recvx == ch.dataqsiz {
			ch.recvx = 0
		}
		ch.qcount--
		return coroChanQueueCommittedV1
	case coroChanMatchDiscarded:
		return coroChanQueueDiscardedV1
	case coroChanMatchRetry:
		ch.recvq.enqueueFront(w)
		return coroChanQueueBlockedV1
	default:
		return coroChanQueueInvalidV1
	}
}

// dequeueBufferToRecvLocked restores the ordinary buffered-channel invariant
// after claim contention temporarily leaves both queued receivers and buffered
// data.
func dequeueBufferToRecvLocked(
	ch *Chan,
	context *coroChanExternalCommitContextV1,
) (progress, ok bool) {
	for {
		switch dequeueBufferToRecvStepLocked(ch, context) {
		case coroChanQueueCommittedV1:
			return true, true
		case coroChanQueueDiscardedV1:
			continue
		case coroChanQueueIdleV1, coroChanQueueBlockedV1:
			return false, true
		default:
			return false, false
		}
	}
}

// reconcileBufferedChanLocked drains every immediately committable receiver
// from buffered data and, while the channel is open, refills newly available
// slots from queued senders. Claim contention stops this bounded pass; the
// winning/canceled coroutine's resume tail invokes it again after removing the
// contended node.
func reconcileBufferedChanLocked(
	ch *Chan,
	refill bool,
	context *coroChanExternalCommitContextV1,
) bool {
	if ch == nil || ch.dataqsiz == 0 {
		return true
	}
	for {
		progress := false
		if ch.qcount > 0 {
			consumed, ok := dequeueBufferToRecvLocked(ch, context)
			if !ok {
				return false
			}
			progress = consumed
		}
		if refill && !ch.closed && ch.qcount < ch.dataqsiz {
			filled, ok := dequeueSendToBufferLocked(ch, context)
			if !ok {
				return false
			}
			progress = progress || filled
		}
		if !progress {
			return true
		}
	}
}

// CoroChanSelectTry is the nonblocking first pass for compiler-owned blocking
// select. A closed send deliberately falls through to the physical park path:
// that path turns it into an explicit resume status instead of unwinding a Go
// panic through an active LLVM coroutine frame.
func CoroChanSelectTry(ops ...ChanOp) (isel int, recvOK, tryOK, sendClosed bool) {
	isel = -1
	if len(ops) == 0 {
		return
	}
	start := selectStart(len(ops))
	for offset := 0; offset < len(ops); offset++ {
		index := (start + offset) % len(ops)
		op := ops[index]
		if op.C == nil {
			continue
		}
		if op.Size < 0 || int(op.Size) != op.C.elemsize {
			coroRuntimeAbort("invalid coroutine select channel operation")
			return
		}
		op.C.mutex.Lock()
		if op.Send {
			var closed bool
			tryOK, closed = chanTrySendLocked(op.C, op.Val, int(op.Size))
			recvOK = true
			op.C.mutex.Unlock()
			if closed {
				return -1, false, false, true
			}
		} else {
			recvOK, tryOK = chanTryRecvLocked(op.C, op.Val, int(op.Size))
			op.C.mutex.Unlock()
		}
		if tryOK {
			return index, recvOK, true, false
		}
	}
	return -1, false, false, false
}

// ensureCoroChannelOperationCapacityV1 grows only the source catalog needed by
// the current owner P. Each page is retained by the source's monotonic pointer
// directory, so native, WASM, embedded, and bare-metal targets pay for parked
// channel concurrency in 64-operation increments instead of reserving a large
// per-executor table up front.
//
// Growth happens before BeginParkSet and before any hchan lock or queue
// publication. The runtime allocation boundary is synchronous, so no partial
// park transaction or scheduler lock can survive an allocation failure.
func ensureCoroChannelOperationCapacityV1(
	p *coro.P,
	source *coro.ChannelOperationSource,
	needed uint32,
) bool {
	if p == nil || source == nil || needed == 0 ||
		needed > coro.ChannelOperationMaximumCapacity {
		return false
	}
	for !coro.CanReserveChannelOperations(p, source, needed) {
		if !growCoroChannelOperationCapacityV1(p, source) {
			return false
		}
	}
	return true
}

func growCoroChannelOperationCapacityV1(
	p *coro.P,
	source *coro.ChannelOperationSource,
) bool {
	if p == nil || source == nil ||
		coro.ChannelOperationConfiguredCapacity(source) >= coro.ChannelOperationMaximumCapacity {
		return false
	}
	page := new(coro.ChannelOperationPage)
	if page == nil {
		return false
	}
	attached := coro.AttachChannelOperationPage(source, p, page, nil)
	if !attached {
		block := new(coro.OperationPageDirectoryBlock)
		attached = block != nil && coro.AttachChannelOperationPage(source, p, page, block)
	}
	return attached
}

// prepareCoroChannelDirectReservationV1 combines the direct park's capacity
// check with selection of its exact reusable slot. The opaque capability is
// consumed before any suspension, eliminating the second catalog scan which
// the general select-capacity API necessarily performs.
func prepareCoroChannelDirectReservationV1(
	p *coro.P,
	source *coro.ChannelOperationSource,
) (coro.ChannelDirectReservation, bool) {
	if p == nil || source == nil {
		return coro.ChannelDirectReservation{}, false
	}
	for {
		if reservation, ok := source.PreflightDirectReservation(p); ok {
			return reservation, true
		}
		if !growCoroChannelOperationCapacityV1(p, source) {
			return coro.ChannelDirectReservation{}, false
		}
	}
}

func prepareCoroChanSelectV1(
	g, handle, header, candidates, storage unsafe.Pointer,
	ops []ChanOp,
) {
	if g == nil || handle == nil || header == nil || storage == nil ||
		len(ops) > int(coro.MaxSelectOperationCases) || len(ops) != 0 && candidates == nil {
		coroRuntimeAbort("invalid coroutine channel select park ABI")
		return
	}
	physical := uint32(0)
	for index := range ops {
		op := &ops[index]
		candidate := coroChanSelectCaseAt(candidates, uintptr(index))
		*candidate = CoroChanSelectCaseV1{}
		if op.C == nil {
			continue
		}
		if op.Size < 0 || int(op.Size) != op.C.elemsize {
			coroRuntimeAbort("coroutine select channel element size mismatch")
			return
		}
		physical++
	}
	state := (*CoroChanSelectV1)(storage)
	*state = CoroChanSelectV1{
		candidates: candidates,
		count:      uintptr(len(ops)),
		magic:      coroChanSelectMagicV1,
	}
	task := (*coro.G)(g)
	frameHeader := (*coro.HeaderV1)(header)
	if physical == 0 {
		ticket, ok := coro.PrepareEmptyChannelPark(task, handle, frameHeader, &state.wait, fastrand())
		if !ok {
			coroRuntimeAbort("cannot prepare empty coroutine channel select")
			return
		}
		state.ticket = ticket
		if !coro.BindSingleWaitSetResumePacket(&state.wait, &state.packet, coro.OperationID{}) {
			coroRuntimeAbort("cannot bind empty coroutine channel select resume")
		}
		return
	}
	sortCoroChanSelectOrder(candidates, ops)
	_, _, route, p, park, source, current := coro.CurrentExecutorChannelParkContext(task)
	if !current || !ensureCoroChannelOperationCapacityV1(p, source, physical) {
		coroRuntimeAbort("coroutine channel select source capacity exhausted")
		return
	}
	ticket, ok := coro.BeginParkSet(park, physical, fastrand())
	if !ok || !coro.PrepareWaitSetRecord(&state.wait, task, ticket) {
		coroRuntimeAbort("cannot begin coroutine channel select park")
		return
	}
	for index := range ops {
		op := &ops[index]
		if op.C == nil {
			continue
		}
		candidate := coroChanSelectCaseAt(candidates, uintptr(index))
		candidate.operation = coroChanOperationV1{
			claim:  &state.claim,
			waiter: &candidate.waiter,
			source: source,
			magic:  coroChanOperationMagicV1,
		}
		candidate.waiter = chanWaiter{
			ch: op.C, elem: op.Val, size: int(op.Size), send: op.Send, coro: &candidate.operation,
		}
		id, attached := source.ReserveAndAttachWait(
			p,
			park,
			ticket,
			&state.wait,
			uint32(index)+1,
			&state.claim,
		)
		if !attached || id.Route() != route {
			coroRuntimeAbort("cannot attach coroutine channel select case")
			return
		}
		candidate.operation.id = id
	}
	if !coro.SealParkSet(park, ticket) ||
		!coro.PrepareParkSet(task, handle, frameHeader, ticket, &state.wait) {
		coroRuntimeAbort("cannot seal coroutine channel select park")
		return
	}
	for index := range ops {
		candidate := coroChanSelectCaseAt(candidates, uintptr(index))
		if ops[index].C != nil && !source.ExposeExternalCommit(
			p,
			task,
			candidate.operation.id,
			ticket,
			&state.wait,
			&state.claim,
		) {
			coroRuntimeAbort("cannot expose coroutine channel select case")
			return
		}
	}
	state.ticket = ticket
	if !coro.BindWaitSetResumeCleanup(
		&state.wait,
		&state.packet,
		&state.cleanup,
		coro.ResumeCleanupBinding{
			Kind:         coro.ResumeCleanupChannelSelect,
			Context:      unsafe.Pointer(state),
			Entries:      candidates,
			Claim:        &state.claim,
			Count:        uint32(len(ops)),
			RuntimeCount: uint32(len(ops)),
			Stride:       unsafe.Sizeof(CoroChanSelectCaseV1{}),
		},
	) {
		coroRuntimeAbort("cannot bind coroutine channel select cleanup")
		return
	}
	lockCoroChanSelectChannels(candidates, ops)
	start := selectStart(len(ops))
	for offset := 0; offset < len(ops); offset++ {
		index := (start + offset) % len(ops)
		op := &ops[index]
		if op.C == nil {
			continue
		}
		candidate := coroChanSelectCaseAt(candidates, uintptr(index))
		var ready bool
		if op.Send {
			ready, ok = coroChanTrySendLocked(op.C, &candidate.waiter)
		} else {
			ready, ok = coroChanTryRecvLocked(op.C, &candidate.waiter)
		}
		if !ok {
			unlockCoroChanSelectChannels(candidates, ops)
			coroRuntimeAbort("cannot commit coroutine channel select case")
			return
		}
		if ready {
			unlockCoroChanSelectChannels(candidates, ops)
			return
		}
	}
	for offset := 0; offset < len(ops); offset++ {
		index := (start + offset) % len(ops)
		op := &ops[index]
		if op.C == nil {
			continue
		}
		waiter := &coroChanSelectCaseAt(candidates, uintptr(index)).waiter
		if op.Send {
			op.C.sendq.enqueue(waiter)
		} else {
			op.C.recvq.enqueue(waiter)
		}
	}
	unlockCoroChanSelectChannels(candidates, ops)
}

// CoroChanSelectPark installs all slow-path cases immediately before the
// compiler emits llvm.coro.suspend. The variadic ChanOp backing array and both
// state objects are compiler allocas retained by CoroSplit.
func CoroChanSelectPark(g, handle, header, candidates, storage unsafe.Pointer, ops ...ChanOp) {
	prepareCoroChanSelectV1(g, handle, header, candidates, storage, ops)
}

// CoroChanSelectResume consumes only the P-neutral packet produced before
// runnable publication. Every queue node, source generation, result lease, and
// old-P pointer has already been retired by typed materialization.
func CoroChanSelectResume(
	g, candidates, storage unsafe.Pointer,
	ops ...ChanOp,
) (isel int, recvOK bool, status uint32) {
	isel = -1
	state := (*CoroChanSelectV1)(storage)
	if g == nil || !validCoroChanSelectV1(state, candidates, ops) {
		coroRuntimeAbort("invalid coroutine channel select resume ABI")
		return -1, false, coroChanResumeInvalid
	}
	task := (*coro.G)(g)
	outcome, caseID, cancel, result, small, ok := coro.TakeResumePacket(
		task,
		state.ticket,
		&state.packet,
		nil,
	)
	if !ok {
		coroRuntimeAbort("invalid coroutine channel select resume packet")
		return -1, false, coroChanResumeInvalid
	}
	if result == coro.ResumeResultNone {
		if outcome != coro.ParkOutcomeCanceled || caseID != 0 || small != coro.ResumeSmallInvalid ||
			cancel != coro.TaskCancelAbort && cancel != coro.TaskCancelShutdown {
			coroRuntimeAbort("invalid empty coroutine channel select decision")
			return -1, false, coroChanResumeInvalid
		}
		*state = CoroChanSelectV1{}
		if cancel == coro.TaskCancelShutdown {
			return -1, false, coroChanResumeShutdown
		}
		return -1, false, coroChanResumeTaskAbort
	}
	if result != coro.ResumeResultChannel || outcome != coro.ParkOutcomeCompleted ||
		caseID == 0 || int(caseID) > len(ops) || cancel != coro.TaskCancelNone ||
		small == coro.ResumeSmallInvalid {
		coroRuntimeAbort("invalid materialized coroutine channel select decision")
		return -1, false, coroChanResumeInvalid
	}
	isel = int(caseID - 1)
	status = uint32(small)
	recvOK = waitStatus(small).recvOK()
	*state = CoroChanSelectV1{}
	switch waitStatus(status) {
	case waitSendOK:
		return isel, recvOK, coroChanResumeSendOK
	case waitRecvOK:
		return isel, recvOK, coroChanResumeRecvOK
	case waitRecvClosed:
		return isel, recvOK, coroChanResumeRecvClosed
	case waitSendClosed:
		return isel, recvOK, coroChanResumeSendClosed
	default:
		coroRuntimeAbort("invalid coroutine channel select completion status")
		return -1, false, coroChanResumeInvalid
	}
}

func prepareCoroChanParkV1(
	g, handle, header, channel, elem, storage unsafe.Pointer,
	eltSize uintptr,
	send bool,
) {
	if g == nil || handle == nil || header == nil || elem == nil || storage == nil ||
		eltSize > uintptr(^uint(0)>>1) {
		coroRuntimeAbort("invalid coroutine channel park ABI")
		return
	}
	state := (*CoroChanParkV1)(storage)
	// The frame allocator and the prior resume own whole-state clearing. Doing
	// it again here made every actual park clear this large record twice.
	ch := (*Chan)(channel)
	size := int(eltSize)
	state.magic = coroChanParkMagicV1
	if ch != nil && size != ch.elemsize {
		coroRuntimeAbort("coroutine channel element size mismatch")
		return
	}
	task := (*coro.G)(g)
	state.waiter = chanWaiter{
		ch: ch, elem: elem, size: size, send: send, direct: &state.Completion,
	}
	driver, route := coro.PrepareCurrentDirectChannelPark(
		task,
		handle,
		(*coro.HeaderV1)(header),
		&state.DirectChannelParkStorageV1,
	)
	if driver == nil {
		coroRuntimeAbort("cannot prepare compact coroutine channel park")
		return
	}
	// A nil channel has no physical endpoint. Its compact record remains bound
	// until task cancellation publishes it to the same owner inbox.
	if ch == nil {
		return
	}
	// Preparation has already authenticated the compiler-carried task and
	// frozen its exact executor route for this no-suspend runtime call. Reuse
	// that certificate instead of repeating the G/P/driver lookup before the
	// hchan transaction.
	context := coroChanExternalCommitContextV1{
		current: task,
		driver:  driver,
		route:   route,
	}
	ch.mutex.Lock()
	ready := false
	status := waitPending
	if send {
		tryOK, closed := chanTrySendLockedWithContext(ch, elem, size, &context)
		ready = tryOK || closed
		if closed {
			status = waitSendClosed
		} else if tryOK {
			status = waitSendOK
		}
	} else {
		recvOK, tryOK := chanTryRecvLockedWithContext(ch, elem, size, &context)
		ready = tryOK
		if tryOK && recvOK {
			status = waitRecvOK
		} else if tryOK {
			status = waitRecvClosed
		}
	}
	if ready {
		if coro.BeginDirectChannelCompletion(&state.Completion) !=
			coro.DirectChannelCompletionBeginAcquired {
			ch.mutex.Unlock()
			coroRuntimeAbort("cannot claim compact coroutine channel result")
			return
		}
		state.waiter.status = status
		if !finishDirectCoroChannelCompletionV1(&state.waiter, status, &context) {
			ch.mutex.Unlock()
			coroRuntimeAbort("cannot publish compact coroutine channel result")
			return
		}
	} else if send {
		ch.sendq.enqueue(&state.waiter)
	} else {
		ch.recvq.enqueue(&state.waiter)
	}
	ch.mutex.Unlock()
}

func prepareCoroChanParkStateV2(
	task *coro.G,
	handle unsafe.Pointer,
	frameHeader *coro.HeaderV1,
	state *CoroChanParkV1,
	ch *Chan,
	elem unsafe.Pointer,
	size int,
	send bool,
	_ uint32, line uint32,
) (*coro.ExecutorDriver, coro.RouteID) {
	// magic is the compiler-spill lifecycle capability. Fresh coroutine frames
	// are zero-filled, and the direct resume prologue clears magic only after
	// every embedded wait/completion/waiter record has been retired. Reject a
	// second prepare before mutating the frame header; the issued core path may
	// then initialize only live fields instead of re-zeroing the whole spill.
	if state == nil || state.magic != 0 || state.Ticket.Valid() {
		return nil, 0
	}
	frameHeader.SuspendReason = uint16(coro.SuspendPark)
	frameHeader.Lifecycle = uint16(coro.FrameSuspended)
	frameHeader.Line = line
	// magic == 0 certifies that the prior resume retired every embedded record.
	// Initialize only the five words consumed by the hchan queue instead of
	// materializing a mostly-zero chanWaiter aggregate on every handoff.
	state.waiter.ch = ch
	state.waiter.elem = elem
	state.waiter.size = size
	state.waiter.send = send
	state.waiter.direct = &state.Completion
	driver, route := coro.PrepareCurrentDirectChannelPark(
		task,
		handle,
		frameHeader,
		&state.DirectChannelParkStorageV1,
	)
	if driver == nil {
		return nil, 0
	}
	// Publish the compiler-spill lifecycle capability only after the core has
	// accepted the park. A nil result is terminal for every caller and never
	// exposes partially initialized state as live.
	state.magic = coroChanParkMagicV1
	return driver, route
}

// tryOrParkCoroChanV2 is the single-lock compiler transaction for an ordinary
// one-case channel operation. A ready endpoint returns its typed status without
// touching the coroutine header or park storage. Only the not-ready edge
// publishes SuspendPark, builds the compact wait graph, and exposes the hchan
// waiter while the same channel critical section is still held. This removes
// the former Try-unlock-Park-lock sequence without giving the hchan ownership
// of a coroutine handle or scheduler queue.
func tryOrParkCoroChanV2(
	g, handle, header, channel, elem, storage unsafe.Pointer,
	eltSize uintptr,
	stateID, line uint32,
	send bool,
) uint32 {
	if g == nil || handle == nil || header == nil || elem == nil || storage == nil ||
		eltSize > uintptr(^uint(0)>>1) {
		coroRuntimeAbort("invalid coroutine channel try-or-park ABI")
		return coroChanResumeInvalid
	}
	task := (*coro.G)(g)
	frameHeader := (*coro.HeaderV1)(header)
	if frameHeader.G != g || frameHeader.SuspendReason != uint16(coro.SuspendNone) ||
		frameHeader.Lifecycle != uint16(coro.FrameActive) {
		coroRuntimeAbort("invalid active coroutine channel frame")
		return coroChanResumeInvalid
	}
	state := (*CoroChanParkV1)(storage)
	ch := (*Chan)(channel)
	size := int(eltSize)
	if ch != nil && size != ch.elemsize {
		coroRuntimeAbort("coroutine channel element size mismatch")
		return coroChanResumeInvalid
	}

	// A nil channel has no physical critical section. It publishes only the
	// cancellation-owned compact park and always reaches llvm.coro.suspend.
	if ch == nil {
		if driver, _ := prepareCoroChanParkStateV2(
			task, handle, frameHeader, state, ch, elem, size, send, stateID, line,
		); driver == nil {
			coroRuntimeAbort("cannot prepare nil coroutine channel park")
			return coroChanResumeInvalid
		}
		return coroChanResumeInvalid
	}

	ch.mutex.Lock()
	var resolved coroChanExternalCommitContextV1
	context := (*coroChanExternalCommitContextV1)(nil)
	if send && ch.recvq.first != nil || !send && ch.sendq.first != nil {
		resolved = coroChannelExternalContextForTaskV1(task)
		context = &resolved
	}
	if send {
		ready, closed := chanTrySendLockedWithContext(ch, elem, size, context)
		if ready && !closed {
			ch.mutex.Unlock()
			return coroChanResumeSendOK
		}
		if !closed {
			driver, _ := prepareCoroChanParkStateV2(
				task, handle, frameHeader, state, ch, elem, size, send, stateID, line,
			)
			if driver == nil {
				ch.mutex.Unlock()
				coroRuntimeAbort("cannot prepare coroutine channel send park")
				return coroChanResumeInvalid
			}
			ch.sendq.enqueue(&state.waiter)
			ch.mutex.Unlock()
			return coroChanResumeInvalid
		}
		// Preserve the existing explicit-status fault route: a send on a closed
		// channel materializes a typed completion and reports the fault only from
		// the post-suspend resume gate, never by unwinding across llvm.coro.resume.
		driver, route := prepareCoroChanParkStateV2(
			task, handle, frameHeader, state, ch, elem, size, send, stateID, line,
		)
		if driver == nil {
			ch.mutex.Unlock()
			coroRuntimeAbort("cannot prepare closed coroutine channel send")
			return coroChanResumeInvalid
		}
		closedContext := coroChanExternalCommitContextV1{
			current: task, driver: driver, route: route,
		}
		if coro.BeginDirectChannelCompletion(&state.Completion) !=
			coro.DirectChannelCompletionBeginAcquired {
			ch.mutex.Unlock()
			coroRuntimeAbort("cannot claim closed coroutine channel send")
			return coroChanResumeInvalid
		}
		state.waiter.status = waitSendClosed
		if !finishDirectCoroChannelCompletionV1(&state.waiter, waitSendClosed, &closedContext) {
			ch.mutex.Unlock()
			coroRuntimeAbort("cannot publish closed coroutine channel send")
			return coroChanResumeInvalid
		}
		ch.mutex.Unlock()
		return coroChanResumeInvalid
	}

	recvOK, ready := chanTryRecvLockedWithContext(ch, elem, size, context)
	if ready {
		ch.mutex.Unlock()
		if recvOK {
			return coroChanResumeRecvOK
		}
		return coroChanResumeRecvClosed
	}
	if driver, _ := prepareCoroChanParkStateV2(
		task, handle, frameHeader, state, ch, elem, size, send, stateID, line,
	); driver == nil {
		ch.mutex.Unlock()
		coroRuntimeAbort("cannot prepare coroutine channel receive park")
		return coroChanResumeInvalid
	}
	ch.recvq.enqueue(&state.waiter)
	ch.mutex.Unlock()
	return coroChanResumeInvalid
}

//export __llgo_coro_chan_send_try_park_v2
func __llgo_coro_chan_send_try_park_v2(
	g, handle, header, channel, elem, storage unsafe.Pointer,
	eltSize uintptr,
	stateID, line uint32,
) uint32 {
	return tryOrParkCoroChanV2(
		g, handle, header, channel, elem, storage, eltSize, stateID, line, true,
	)
}

//export __llgo_coro_chan_recv_try_park_v2
func __llgo_coro_chan_recv_try_park_v2(
	g, handle, header, channel, elem, storage unsafe.Pointer,
	eltSize uintptr,
	stateID, line uint32,
) uint32 {
	return tryOrParkCoroChanV2(
		g, handle, header, channel, elem, storage, eltSize, stateID, line, false,
	)
}

//export __llgo_coro_chan_send_park_v1
func __llgo_coro_chan_send_park_v1(
	g, handle, header, channel, elem, storage unsafe.Pointer,
	eltSize uintptr,
) {
	prepareCoroChanParkV1(g, handle, header, channel, elem, storage, eltSize, true)
}

//export __llgo_coro_chan_recv_park_v1
func __llgo_coro_chan_recv_park_v1(
	g, handle, header, channel, elem, storage unsafe.Pointer,
	eltSize uintptr,
) {
	prepareCoroChanParkV1(g, handle, header, channel, elem, storage, eltSize, false)
}

// coroChanResumeCompatibilityV1 remains available to the runtime's manually
// driven adapter tests. Current compiler lowering uses the issued V2 ABI below
// and does not retain this function as an external binary entry.
func coroChanResumeCompatibilityV1(g, storage unsafe.Pointer) uint32 {
	state := (*CoroChanParkV1)(storage)
	if g == nil || state == nil || state.magic != coroChanParkMagicV1 ||
		state.Ticket == (coro.ParkTicket{}) {
		coroRuntimeAbort("invalid coroutine channel resume ABI")
		return coroChanResumeInvalid
	}
	outcome, task, small, ok := coro.TakeDirectChannelResume(
		(*coro.G)(g),
		&state.DirectChannelParkStorageV1,
	)
	if !ok {
		coroRuntimeAbort("invalid coroutine channel resume packet")
		return coroChanResumeInvalid
	}
	if outcome == coro.ParkOutcomeCanceled {
		if small != coro.ResumeSmallInvalid {
			coroRuntimeAbort("invalid canceled channel run decision")
			return coroChanResumeInvalid
		}
		// Promotion/materialization and TakeDirectChannelResume have already
		// cleared wait, waiter, and completion ownership. Only these two scalar
		// compiler receipts remain live; clearing the entire spill record here
		// rewrote roughly two hundred already-zero bytes on every handoff.
		state.Ticket = coro.ParkTicket{}
		state.magic = 0
		switch task {
		case coro.TaskCancelAbort:
			return coroChanResumeTaskAbort
		case coro.TaskCancelShutdown:
			return coroChanResumeShutdown
		default:
			coroRuntimeAbort("channel park resumed without task cancellation")
			return coroChanResumeInvalid
		}
	}
	if outcome != coro.ParkOutcomeCompleted || task != coro.TaskCancelNone ||
		small == coro.ResumeSmallInvalid {
		coroRuntimeAbort("invalid materialized coroutine channel decision")
		return coroChanResumeInvalid
	}
	state.Ticket = coro.ParkTicket{}
	state.magic = 0
	switch waitStatus(small) {
	case waitSendOK:
		return coroChanResumeSendOK
	case waitRecvOK:
		return coroChanResumeRecvOK
	case waitRecvClosed:
		return coroChanResumeRecvClosed
	case waitSendClosed:
		return coroChanResumeSendClosed
	default:
		coroRuntimeAbort("invalid coroutine channel completion status")
		return coroChanResumeInvalid
	}
}

// __llgo_coro_chan_resume_v2 consumes only a bounded runner's issued physical
// resume. The compiler already uses the matching V2 try-or-park transaction;
// keeping its resume half exact removes the arbitrary-runner fallback from
// every generated one-case channel continuation.
//
//export __llgo_coro_chan_resume_v2
func __llgo_coro_chan_resume_v2(g, storage unsafe.Pointer) uint32 {
	state := (*CoroChanParkV1)(storage)
	if g == nil || state == nil || state.magic != coroChanParkMagicV1 ||
		state.Ticket == (coro.ParkTicket{}) {
		coroRuntimeAbort("invalid coroutine channel resume ABI")
		return coroChanResumeInvalid
	}
	word := coro.TakeIssuedDirectChannelResumeWordV1(
		(*coro.G)(g),
		&state.DirectChannelParkStorageV1,
	)
	class := word & coro.DirectChannelResumeWordClassMaskV1
	payload := uint8(word & coro.DirectChannelResumeWordPayloadMaskV1)
	if class != coro.DirectChannelResumeWordCompletedV1 &&
		class != coro.DirectChannelResumeWordCanceledV1 {
		coroRuntimeAbort("invalid issued coroutine channel resume packet")
		return coroChanResumeInvalid
	}
	state.Ticket = coro.ParkTicket{}
	state.magic = 0
	if class == coro.DirectChannelResumeWordCanceledV1 {
		switch coro.TaskCancelKind(payload) {
		case coro.TaskCancelAbort:
			return coroChanResumeTaskAbort
		case coro.TaskCancelShutdown:
			return coroChanResumeShutdown
		default:
			coroRuntimeAbort("channel park resumed without task cancellation")
			return coroChanResumeInvalid
		}
	}
	switch waitStatus(payload) {
	case waitSendOK:
		return coroChanResumeSendOK
	case waitRecvOK:
		return coroChanResumeRecvOK
	case waitRecvClosed:
		return coroChanResumeRecvClosed
	case waitSendClosed:
		return coroChanResumeSendClosed
	default:
		coroRuntimeAbort("invalid coroutine channel completion status")
		return coroChanResumeInvalid
	}
}

func finishCoroChanCleanupCursorV1(
	cursor *coroChanCleanupCursorV1,
) (small uint8, complete bool, ok bool) {
	if !validCoroChanCleanupCursorV1(cursor) ||
		cursor.phase == coroChanCleanupIdleV1 {
		return 0, false, false
	}
	small = coro.ResumeSmallInvalid
	if cursor.deliver {
		small = uint8(cursor.status)
	}
	*cursor = coroChanCleanupCursorV1{}
	return small, true, true
}

// advanceCoroChanCleanupCursorV1 performs at most one peer-waiter operation.
// Phase-only transitions are separate reductions so releasing the hchan gate
// never hides a loop behind one ExecutorRunStepMaterialize.
func advanceCoroChanCleanupCursorV1(
	cursor *coroChanCleanupCursorV1,
) (small uint8, complete bool, ok bool) {
	if !validCoroChanCleanupCursorV1(cursor) ||
		cursor.phase == coroChanCleanupIdleV1 {
		return 0, false, false
	}
	ch := cursor.ch
	finish := false
	ch.mutex.Lock()
	switch cursor.phase {
	case coroChanCleanupBufferRecvV1:
		if ch.dataqsiz == 0 || ch.qcount == 0 {
			cursor.phase = coroChanCleanupBufferSendV1
			break
		}
		switch dequeueBufferToRecvStepLocked(ch, nil) {
		case coroChanQueueCommittedV1, coroChanQueueDiscardedV1:
		case coroChanQueueIdleV1, coroChanQueueBlockedV1:
			cursor.phase = coroChanCleanupBufferSendV1
		default:
			ch.mutex.Unlock()
			return 0, false, false
		}
	case coroChanCleanupBufferSendV1:
		if ch.dataqsiz != 0 && !ch.closed && ch.qcount < ch.dataqsiz {
			switch dequeueSendToBufferStepLocked(ch, nil) {
			case coroChanQueueCommittedV1:
				cursor.phase = coroChanCleanupBufferRecvV1
			case coroChanQueueDiscardedV1:
			case coroChanQueueIdleV1, coroChanQueueBlockedV1:
				finish = true
			default:
				ch.mutex.Unlock()
				return 0, false, false
			}
		} else {
			finish = true
		}
		if finish {
			if ch.closed {
				cursor.phase = coroChanCleanupClosedRecvV1
				finish = false
			}
		}
	case coroChanCleanupClosedRecvV1:
		if !ch.closed {
			ch.mutex.Unlock()
			return 0, false, false
		}
		switch dequeueClosedRecvStepLocked(ch, nil) {
		case coroChanQueueCommittedV1, coroChanQueueDiscardedV1:
		case coroChanQueueIdleV1:
			cursor.phase = coroChanCleanupClosedSendV1
		case coroChanQueueBlockedV1:
			finish = true
		default:
			ch.mutex.Unlock()
			return 0, false, false
		}
	case coroChanCleanupClosedSendV1:
		if !ch.closed {
			ch.mutex.Unlock()
			return 0, false, false
		}
		switch dequeueClosedSendStepLocked(ch, nil) {
		case coroChanQueueCommittedV1, coroChanQueueDiscardedV1:
		case coroChanQueueIdleV1, coroChanQueueBlockedV1:
			finish = true
		default:
			ch.mutex.Unlock()
			return 0, false, false
		}
	default:
		ch.mutex.Unlock()
		return 0, false, false
	}
	ch.mutex.Unlock()
	if finish {
		return finishCoroChanCleanupCursorV1(cursor)
	}
	return coro.ResumeSmallInvalid, false, validCoroChanCleanupCursorV1(cursor)
}

func materializeCoroChanOperationV1(
	operation *coroChanOperationV1,
	waiter *chanWaiter,
	cursor *coroChanCleanupCursorV1,
	deliver bool,
	allowCommittedCancel bool,
) (small uint8, complete bool, ok bool) {
	if operation == nil || waiter == nil || cursor == nil ||
		!validCoroChanCleanupCursorV1(cursor) ||
		deliver && allowCommittedCancel {
		return 0, false, false
	}
	if cursor.phase != coroChanCleanupIdleV1 {
		idOnly := coroChanOperationV1{id: operation.id}
		if !operation.id.Valid() || *operation != idOnly ||
			*waiter != (chanWaiter{}) || cursor.deliver != deliver ||
			cursor.status.done() && !deliver && !allowCommittedCancel {
			return 0, false, false
		}
		return advanceCoroChanCleanupCursorV1(cursor)
	}
	if *operation == (coroChanOperationV1{}) && *waiter == (chanWaiter{}) {
		if deliver {
			return 0, false, false
		}
		return coro.ResumeSmallInvalid, true, true
	}
	if !validCoroChanOperationV1(operation, waiter) {
		return 0, false, false
	}
	status := waiter.status
	physicalComplete := status.done()
	if deliver && !physicalComplete || physicalComplete && !deliver && !allowCommittedCancel {
		return 0, false, false
	}
	ch := waiter.ch
	ch.mutex.Lock()
	if waiter.send {
		ch.sendq.remove(waiter)
	} else {
		ch.recvq.remove(waiter)
	}
	// A completed rendezvous on an open unbuffered channel has no buffer or
	// closed-channel reconciliation work. The old bounded cursor would perform
	// two phase-only reductions before reaching the same result. Finish this
	// exact zero-peer shape while the channel snapshot is still protected; all
	// buffered, closed, and potentially cascading cases retain the resumable
	// one-peer-per-reduction cleanup path below.
	directRendezvous := deliver && ch.dataqsiz == 0 && !ch.closed
	ch.mutex.Unlock()
	id := operation.id
	*waiter = chanWaiter{}
	*operation = coroChanOperationV1{id: id}
	if directRendezvous {
		small := uint8(coro.ResumeSmallInvalid)
		if deliver {
			small = uint8(status)
		}
		return small, true, true
	}
	*cursor = coroChanCleanupCursorV1{
		ch:      ch,
		status:  status,
		phase:   coroChanCleanupBufferRecvV1,
		deliver: deliver,
	}
	return coro.ResumeSmallInvalid, false, validCoroChanCleanupCursorV1(cursor)
}

func coroMaterializeChannelResumeCleanupStepV1(step coro.ResumeCleanupStep) bool {
	deliver := step.Outcome == coro.ParkOutcomeCompleted && step.WinnerCase == step.Index+1
	allowCommittedCancel := step.Outcome == coro.ParkOutcomeCanceled
	var (
		small    uint8
		complete bool
		ok       bool
	)
	switch step.Kind {
	case coro.ResumeCleanupChannelSelect:
		state := (*CoroChanSelectV1)(step.Context)
		if state == nil || state.magic != coroChanSelectMagicV1 || step.Index >= uint32(state.count) ||
			state.candidates == nil {
			return false
		}
		candidate := coroChanSelectCaseAt(state.candidates, uintptr(step.Index))
		small, complete, ok = materializeCoroChanOperationV1(
			&candidate.operation,
			&candidate.waiter,
			&state.reconcile,
			deliver,
			allowCommittedCancel,
		)
		if ok {
			candidate.order = 0
		}
	default:
		return false
	}
	if !ok || !complete {
		return ok
	}
	return coro.CommitResumeCleanupStep(step, small)
}

func coroMaterializeDirectChannelCompletionV1(
	completion *coro.DirectChannelCompletion,
) bool {
	context, small, matched, ok := coro.DirectChannelCompletionSnapshot(completion)
	if !ok || context == nil {
		return false
	}
	state := (*CoroChanParkV1)(context)
	if state == nil || state.magic != coroChanParkMagicV1 || state.waiter.direct != completion ||
		state.waiter.coro != nil {
		return false
	}
	waiter, ch := &state.waiter, state.waiter.ch
	if ch == nil {
		if matched || waiter.queued || waiter.status != waitPending {
			return false
		}
		*waiter = chanWaiter{}
		return coro.CommitDirectChannelCompletion(completion, small)
	}
	ch.mutex.Lock()
	if waiter.send {
		ch.sendq.remove(waiter)
	} else {
		ch.recvq.remove(waiter)
	}
	if matched {
		if small == coro.ResumeSmallInvalid || !waitStatus(small).done() || waiter.status != waitStatus(small) {
			ch.mutex.Unlock()
			return false
		}
	} else if waiter.status != waitPending {
		ch.mutex.Unlock()
		return false
	}
	*waiter = chanWaiter{}
	ch.mutex.Unlock()
	return coro.CommitDirectChannelCompletion(completion, small)
}
