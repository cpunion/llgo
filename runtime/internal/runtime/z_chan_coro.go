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
// the old-P materialization barrier.
type coroChanCleanupCursorV1 struct {
	ch       *Chan
	status   waitStatus
	phase    coroChanCleanupPhaseV1
	selected bool
}

// CoroChanParkV1 is compiler-spilled storage for one direct blocking channel
// operation. It is not a Future/Task object and is never separately allocated:
// LLGo emits one typed alloca which LLVM CoroSplit retains only on the slow
// path. The hchan queue points at waiter while source admission pins this exact
// coroutine frame through commit or cancellation cleanup.
//
// The type is exported solely so the compiler can request its target layout
// from the frozen runtime package. Its fields remain runtime-private and no Go
// aggregate crosses a C or compiler hook ABI.
type CoroChanParkV1 struct {
	wait      coro.WaitSetRecord
	claim     coro.SelectClaim
	packet    coro.ResumePacket
	cleanup   coro.ResumeCleanupPlan
	reconcile coroChanCleanupCursorV1
	ticket    coro.ParkTicket
	operation coroChanOperationV1
	waiter    chanWaiter
	magic     uint32
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
		cursor.selected == cursor.status.done()
}

func validCoroChanParkV1(state *CoroChanParkV1) bool {
	if state == nil || state.magic != coroChanParkMagicV1 ||
		state.waiter.status > waitSendClosed || state.waiter.size < 0 ||
		state.reconcile != (coroChanCleanupCursorV1{}) {
		return false
	}
	if state.operation == (coroChanOperationV1{}) && state.waiter == (chanWaiter{}) {
		return state.ticket != (coro.ParkTicket{})
	}
	if state.waiter.coro != &state.operation || state.operation.waiter != &state.waiter ||
		state.operation.claim != &state.claim {
		return false
	}
	if state.waiter.ch == nil {
		return state.operation.id == (coro.OperationID{}) && state.operation.magic == 0
	}
	return validCoroChanOperationV1(&state.operation, &state.waiter)
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

func requestCoroChannelExecutorV1(operation *coroChanOperationV1) bool {
	return operation != nil && operation.id.Valid() &&
		coroTargetRequestChannelOperationV1(operation.id)
}

func requestCoroChannelPairExecutorsV1(first, second *coroChanOperationV1) bool {
	if first == nil || second == nil || !first.id.Valid() || !second.id.Valid() ||
		!requestCoroChannelExecutorV1(first) {
		return false
	}
	return first.id.Route() == second.id.Route() || requestCoroChannelExecutorV1(second)
}

func commitCoroRecvWaiterLocked(w *chanWaiter, src unsafe.Pointer, eltSize int, status waitStatus) coroChanMatchResult {
	if !validCoroChanOperationV1(w.coro, w) || w.send || w.size != eltSize ||
		!status.done() || status == waitSendClosed {
		return coroChanMatchInvalid
	}
	var transaction coro.ChannelExternalCommit
	for {
		result := coro.BeginChannelExternalCommit(
			&transaction,
			w.coro.source,
			w.coro.id,
			w.coro.claim,
		)
		classified := classifyCoroChanSingleBegin(result)
		if classified == coroChanMatchRetry {
			// Acquiring/Committing is a no-suspend claim critical section. Its
			// holder never waits for hchan, so retrying under the already-held
			// channel gate cannot form a lock cycle or strand a rendezvous.
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
	if status.recvOK() {
		copyChanElem(w.elem, src, eltSize)
	} else {
		zeroChanRecv(w.elem, eltSize)
	}
	w.status = status
	if !transaction.Commit() || !requestCoroChannelExecutorV1(w.coro) {
		return coroChanMatchInvalid
	}
	return coroChanMatchCommitted
}

func commitCoroSendWaiterLocked(w *chanWaiter, dst unsafe.Pointer, eltSize int, status waitStatus) coroChanMatchResult {
	if !validCoroChanOperationV1(w.coro, w) || !w.send || w.size != eltSize ||
		(status != waitSendOK && status != waitSendClosed) {
		return coroChanMatchInvalid
	}
	var transaction coro.ChannelExternalCommit
	for {
		result := coro.BeginChannelExternalCommit(
			&transaction,
			w.coro.source,
			w.coro.id,
			w.coro.claim,
		)
		classified := classifyCoroChanSingleBegin(result)
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
	if status == waitSendOK {
		copyChanElem(dst, w.elem, eltSize)
	}
	w.status = status
	if !transaction.Commit() || !requestCoroChannelExecutorV1(w.coro) {
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
	if !transaction.Commit() || !requestCoroChannelPairExecutorsV1(send.coro, recv.coro) {
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
	if !validCoroChanOperationV1(waiter.coro, waiter) || transaction == nil || !status.done() ||
		!transaction.BeginEffect() {
		return false
	}
	waiter.status = status
	return transaction.Commit() && requestCoroChannelExecutorV1(waiter.coro)
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
		if !transaction.BeginEffect() {
			return false, false
		}
		copyChanElem(peer.elem, waiter.elem, ch.elemsize)
		waiter.status = waitSendOK
		peer.finish(waitRecvOK)
		if !transaction.Commit() || !requestCoroChannelExecutorV1(waiter.coro) {
			return false, false
		}
		return true, true
	}
	if ch.qcount < ch.dataqsiz {
		var transaction coro.ChannelExternalCommit
		if beginCurrentCoroChannelCommit(waiter, &transaction) != coroChanMatchCommitted ||
			!transaction.BeginEffect() {
			return false, false
		}
		copyChanElem(chanBuf(ch, ch.sendx), waiter.elem, ch.elemsize)
		ch.sendx++
		if ch.sendx == ch.dataqsiz {
			ch.sendx = 0
		}
		ch.qcount++
		waiter.status = waitSendOK
		if !transaction.Commit() || !requestCoroChannelExecutorV1(waiter.coro) {
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
			if !transaction.BeginEffect() {
				return false, false
			}
			copyChanElem(waiter.elem, peer.elem, ch.elemsize)
			waiter.status = waitRecvOK
			peer.finish(waitSendOK)
			if !transaction.Commit() || !requestCoroChannelExecutorV1(waiter.coro) {
				return false, false
			}
			return true, true
		}
	} else if ch.qcount > 0 {
		var transaction coro.ChannelExternalCommit
		if beginCurrentCoroChannelCommit(waiter, &transaction) != coroChanMatchCommitted ||
			!transaction.BeginEffect() {
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
		if !transaction.Commit() || !requestCoroChannelExecutorV1(waiter.coro) {
			return false, false
		}
		// Refill is a separate committed sender endpoint under the same hchan
		// lock. Failure leaves the now-available buffer slot visible to a later
		// sender without changing the completed receive.
		dequeueSendToBuffer(ch)
		return true, true
	}
	if ch.closed {
		var transaction coro.ChannelExternalCommit
		if beginCurrentCoroChannelCommit(waiter, &transaction) != coroChanMatchCommitted ||
			!transaction.BeginEffect() {
			return false, false
		}
		zeroChanRecv(waiter.elem, ch.elemsize)
		waiter.status = waitRecvClosed
		if !transaction.Commit() || !requestCoroChannelExecutorV1(waiter.coro) {
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
func dequeueSendToBufferStepLocked(ch *Chan) coroChanQueueStepV1 {
	if ch == nil || ch.closed || ch.qcount >= ch.dataqsiz {
		return coroChanQueueIdleV1
	}
	w := ch.sendq.dequeue()
	if w == nil {
		return coroChanQueueIdleV1
	}
	if w.coro != nil {
		switch result := commitCoroSendWaiterLocked(w, chanBuf(ch, ch.sendx), ch.elemsize, waitSendOK); result {
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

func dequeueSendToBufferLocked(ch *Chan) (progress, ok bool) {
	for {
		switch dequeueSendToBufferStepLocked(ch) {
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

func dequeueSendToBuffer(ch *Chan) {
	if _, ok := dequeueSendToBufferLocked(ch); !ok {
		coroRuntimeAbort("invalid coroutine channel buffer refill")
	}
}

// dequeueBufferToRecvStepLocked examines and removes at most one queued
// receiver. The buffer position advances only after the exact receiver
// transaction has committed its typed copy.
func dequeueBufferToRecvStepLocked(ch *Chan) coroChanQueueStepV1 {
	if ch == nil || ch.qcount == 0 {
		return coroChanQueueIdleV1
	}
	w := ch.recvq.dequeue()
	if w == nil {
		return coroChanQueueIdleV1
	}
	switch result := completeRecvWaiter(w, chanBuf(ch, ch.recvx), ch.elemsize, waitRecvOK); result {
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
func dequeueBufferToRecvLocked(ch *Chan) (progress, ok bool) {
	for {
		switch dequeueBufferToRecvStepLocked(ch) {
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
func reconcileBufferedChanLocked(ch *Chan, refill bool) bool {
	if ch == nil || ch.dataqsiz == 0 {
		return true
	}
	for {
		progress := false
		if ch.qcount > 0 {
			consumed, ok := dequeueBufferToRecvLocked(ch)
			if !ok {
				return false
			}
			progress = consumed
		}
		if refill && !ch.closed && ch.qcount < ch.dataqsiz {
			filled, ok := dequeueSendToBufferLocked(ch)
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
	driver, _, route, current := coro.CurrentExecutorChannelDriver(task)
	p, park, source, ownerOK := coro.CurrentExecutorChannelParkOwner(driver, task)
	if !current || !ownerOK || !coro.CanReserveChannelOperations(p, source, physical) {
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
	*state = CoroChanParkV1{}
	ch := (*Chan)(channel)
	size := int(eltSize)
	state.magic = coroChanParkMagicV1
	state.operation = coroChanOperationV1{claim: &state.claim, waiter: &state.waiter}
	state.waiter = chanWaiter{ch: ch, elem: elem, size: size, send: send, coro: &state.operation}
	if ch == nil {
		ticket, ok := coro.PrepareEmptyChannelPark(
			(*coro.G)(g), handle, (*coro.HeaderV1)(header), &state.wait, fastrand(),
		)
		if !ok {
			coroRuntimeAbort("cannot prepare nil coroutine channel park")
			return
		}
		state.ticket = ticket
		if !coro.BindSingleWaitSetResumePacket(&state.wait, &state.packet, coro.OperationID{}) {
			coroRuntimeAbort("cannot bind nil coroutine channel resume")
		}
		return
	}
	if size != ch.elemsize {
		coroRuntimeAbort("coroutine channel element size mismatch")
		return
	}
	task := (*coro.G)(g)
	driver, _, route, current := coro.CurrentExecutorChannelDriver(task)
	_, _, source, ownerOK := coro.CurrentExecutorChannelParkOwner(driver, task)
	if !current || !ownerOK {
		coroRuntimeAbort("cannot resolve coroutine channel park owner")
		return
	}
	ticket, id, ok := coro.PrepareSingleChannelPark(
		task,
		handle,
		(*coro.HeaderV1)(header),
		source,
		&state.wait,
		&state.claim,
		1,
		fastrand(),
	)
	if !ok || id.Route() != route {
		coroRuntimeAbort("cannot prepare coroutine channel park")
		return
	}
	state.ticket = ticket
	state.operation.id = id
	state.operation.source = source
	state.operation.magic = coroChanOperationMagicV1
	if !coro.BindWaitSetResumeCleanup(
		&state.wait,
		&state.packet,
		&state.cleanup,
		coro.ResumeCleanupBinding{
			Kind:         coro.ResumeCleanupChannelDirect,
			Context:      unsafe.Pointer(state),
			Entries:      unsafe.Pointer(&state.operation.id),
			Claim:        &state.claim,
			Count:        1,
			RuntimeCount: 1,
			Stride:       unsafe.Sizeof(coro.OperationID{}),
		},
	) {
		coroRuntimeAbort("cannot bind coroutine channel cleanup")
		return
	}
	ch.mutex.Lock()
	var ready bool
	if send {
		ready, ok = coroChanTrySendLocked(ch, &state.waiter)
	} else {
		ready, ok = coroChanTryRecvLocked(ch, &state.waiter)
	}
	if !ok {
		ch.mutex.Unlock()
		coroRuntimeAbort("cannot commit coroutine channel park")
		return
	}
	if !ready {
		if send {
			ch.sendq.enqueue(&state.waiter)
		} else {
			ch.recvq.enqueue(&state.waiter)
		}
	}
	ch.mutex.Unlock()
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

//export __llgo_coro_chan_resume_v1
func __llgo_coro_chan_resume_v1(g, storage unsafe.Pointer) uint32 {
	state := (*CoroChanParkV1)(storage)
	if g == nil || !validCoroChanParkV1(state) {
		coroRuntimeAbort("invalid coroutine channel resume ABI")
		return coroChanResumeInvalid
	}
	outcome, caseID, task, result, small, ok := coro.TakeResumePacket(
		(*coro.G)(g),
		state.ticket,
		&state.packet,
		nil,
	)
	if !ok {
		coroRuntimeAbort("invalid coroutine channel resume packet")
		return coroChanResumeInvalid
	}
	if result == coro.ResumeResultNone {
		if outcome != coro.ParkOutcomeCanceled || caseID != 0 || small != coro.ResumeSmallInvalid {
			coroRuntimeAbort("invalid nil-channel run decision")
			return coroChanResumeInvalid
		}
		*state = CoroChanParkV1{}
		switch task {
		case coro.TaskCancelAbort:
			return coroChanResumeTaskAbort
		case coro.TaskCancelShutdown:
			return coroChanResumeShutdown
		default:
			coroRuntimeAbort("nil-channel park resumed without task cancellation")
			return coroChanResumeInvalid
		}
	}
	if result != coro.ResumeResultChannel || outcome != coro.ParkOutcomeCompleted ||
		caseID != 1 || task != coro.TaskCancelNone || small == coro.ResumeSmallInvalid {
		coroRuntimeAbort("invalid materialized coroutine channel decision")
		return coroChanResumeInvalid
	}
	*state = CoroChanParkV1{}
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

func finishCoroChanCleanupCursorV1(
	cursor *coroChanCleanupCursorV1,
) (small uint8, complete bool, ok bool) {
	if !validCoroChanCleanupCursorV1(cursor) ||
		cursor.phase == coroChanCleanupIdleV1 {
		return 0, false, false
	}
	small = coro.ResumeSmallInvalid
	if cursor.selected {
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
		switch dequeueBufferToRecvStepLocked(ch) {
		case coroChanQueueCommittedV1, coroChanQueueDiscardedV1:
		case coroChanQueueIdleV1, coroChanQueueBlockedV1:
			cursor.phase = coroChanCleanupBufferSendV1
		default:
			ch.mutex.Unlock()
			return 0, false, false
		}
	case coroChanCleanupBufferSendV1:
		if ch.dataqsiz != 0 && !ch.closed && ch.qcount < ch.dataqsiz {
			switch dequeueSendToBufferStepLocked(ch) {
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
		switch dequeueClosedRecvStepLocked(ch) {
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
		switch dequeueClosedSendStepLocked(ch) {
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
	selected bool,
) (small uint8, complete bool, ok bool) {
	if operation == nil || waiter == nil || cursor == nil ||
		!validCoroChanCleanupCursorV1(cursor) {
		return 0, false, false
	}
	if cursor.phase != coroChanCleanupIdleV1 {
		idOnly := coroChanOperationV1{id: operation.id}
		if !operation.id.Valid() || *operation != idOnly ||
			*waiter != (chanWaiter{}) || cursor.selected != selected {
			return 0, false, false
		}
		return advanceCoroChanCleanupCursorV1(cursor)
	}
	if *operation == (coroChanOperationV1{}) && *waiter == (chanWaiter{}) {
		if selected {
			return 0, false, false
		}
		return coro.ResumeSmallInvalid, true, true
	}
	if !validCoroChanOperationV1(operation, waiter) {
		return 0, false, false
	}
	status := waiter.status
	if selected != status.done() {
		return 0, false, false
	}
	ch := waiter.ch
	ch.mutex.Lock()
	if waiter.send {
		ch.sendq.remove(waiter)
	} else {
		ch.recvq.remove(waiter)
	}
	ch.mutex.Unlock()
	id := operation.id
	*waiter = chanWaiter{}
	*operation = coroChanOperationV1{id: id}
	*cursor = coroChanCleanupCursorV1{
		ch:       ch,
		status:   status,
		phase:    coroChanCleanupBufferRecvV1,
		selected: selected,
	}
	return coro.ResumeSmallInvalid, false, validCoroChanCleanupCursorV1(cursor)
}

func coroMaterializeChannelResumeCleanupStepV1(step coro.ResumeCleanupStep) bool {
	selected := step.Outcome == coro.ParkOutcomeCompleted && step.WinnerCase == step.Index+1
	var (
		small    uint8
		complete bool
		ok       bool
	)
	switch step.Kind {
	case coro.ResumeCleanupChannelDirect:
		if step.Index != 0 {
			return false
		}
		state := (*CoroChanParkV1)(step.Context)
		if state == nil || state.magic != coroChanParkMagicV1 {
			return false
		}
		small, complete, ok = materializeCoroChanOperationV1(
			&state.operation,
			&state.waiter,
			&state.reconcile,
			selected,
		)
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
			selected,
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
