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

const coroChanParkMagicV1 uint32 = 0x43485031 // "CHP1"

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
	wait   coro.WaitSetRecord
	claim  coro.SelectClaim
	ticket coro.ParkTicket
	id     coro.OperationID
	waiter chanWaiter
	magic  uint32
}

type coroChanMatchResult uint8

const (
	coroChanMatchInvalid coroChanMatchResult = iota
	coroChanMatchCommitted
	coroChanMatchDiscarded
	coroChanMatchRetry
)

var coroChanSendClosedPanicV1 any = "send on closed channel"

const (
	coroChanResumeInvalid uint32 = iota
	coroChanResumeSendOK
	coroChanResumeRecvOK
	coroChanResumeRecvClosed
	coroChanResumeSendClosed
	coroChanResumeTaskAbort
	coroChanResumeShutdown
)

func validCoroChanParkV1(state *CoroChanParkV1) bool {
	return state != nil && state.magic == coroChanParkMagicV1 && state.waiter.coro == state &&
		state.waiter.status <= waitSendClosed && state.waiter.size >= 0
}

func classifyCoroChanSingleBegin(result coro.ChannelExternalCommitBeginResult) coroChanMatchResult {
	switch result {
	case coro.ChannelExternalCommitBeginPrepared:
		return coroChanMatchCommitted
	case coro.ChannelExternalCommitBeginAdmissionFailed:
		// Apply may already have sealed a canceled/losing endpoint. Its queue
		// node is stale and can be dropped; resume cleanup owns the generation.
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
	case coro.ChannelExternalCommitPairBeginClaimContended:
		return coroChanMatchRetry
	default:
		return coroChanMatchInvalid
	}
}

func requestCoroChannelExecutorV1() bool {
	return coroProgramExecutorBoundV1State &&
		coroProgramExecutorHandleV1State != (coro.ExecutorHandle{}) &&
		coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State)
}

func commitCoroRecvWaiterLocked(w *chanWaiter, src unsafe.Pointer, eltSize int, status waitStatus) coroChanMatchResult {
	state := w.coro
	if !validCoroChanParkV1(state) || state.waiter.ch == nil || state.waiter.send ||
		state.waiter.size != eltSize || !status.done() || status == waitSendClosed {
		return coroChanMatchInvalid
	}
	var transaction coro.ChannelExternalCommit
	result := coro.BeginChannelExternalCommit(
		&transaction,
		&coroProgramChannelSourceV1State,
		state.id,
		&state.claim,
	)
	classified := classifyCoroChanSingleBegin(result)
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
	if !transaction.Commit() || !requestCoroChannelExecutorV1() {
		return coroChanMatchInvalid
	}
	return coroChanMatchCommitted
}

func commitCoroSendWaiterLocked(w *chanWaiter, dst unsafe.Pointer, eltSize int, status waitStatus) coroChanMatchResult {
	state := w.coro
	if !validCoroChanParkV1(state) || state.waiter.ch == nil || !state.waiter.send ||
		state.waiter.size != eltSize || (status != waitSendOK && status != waitSendClosed) {
		return coroChanMatchInvalid
	}
	var transaction coro.ChannelExternalCommit
	result := coro.BeginChannelExternalCommit(
		&transaction,
		&coroProgramChannelSourceV1State,
		state.id,
		&state.claim,
	)
	classified := classifyCoroChanSingleBegin(result)
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
	if !transaction.Commit() || !requestCoroChannelExecutorV1() {
		return coroChanMatchInvalid
	}
	return coroChanMatchCommitted
}

func commitCoroPairLocked(send, recv *chanWaiter, eltSize int) coroChanMatchResult {
	if send == nil || recv == nil || send.coro == nil || recv.coro == nil || send.coro == recv.coro ||
		!validCoroChanParkV1(send.coro) || !validCoroChanParkV1(recv.coro) ||
		!send.send || recv.send || send.ch == nil || send.ch != recv.ch ||
		send.size != eltSize || recv.size != eltSize {
		return coroChanMatchInvalid
	}
	var transaction coro.ChannelExternalCommitPair
	result := coro.BeginChannelExternalCommitPair(
		&transaction,
		&coroProgramChannelSourceV1State,
		send.coro.id,
		&send.coro.claim,
		&coroProgramChannelSourceV1State,
		recv.coro.id,
		&recv.coro.claim,
	)
	classified := classifyCoroChanPairBegin(result)
	if classified != coroChanMatchCommitted {
		return classified
	}
	if !transaction.BeginEffect() {
		return coroChanMatchInvalid
	}
	copyChanElem(recv.elem, send.elem, eltSize)
	send.status = waitSendOK
	recv.status = waitRecvOK
	if !transaction.Commit() || !requestCoroChannelExecutorV1() {
		return coroChanMatchInvalid
	}
	return coroChanMatchCommitted
}

func beginCurrentCoroChannelCommit(state *CoroChanParkV1, transaction *coro.ChannelExternalCommit) coroChanMatchResult {
	if !validCoroChanParkV1(state) || transaction == nil || *transaction != (coro.ChannelExternalCommit{}) {
		return coroChanMatchInvalid
	}
	return classifyCoroChanSingleBegin(coro.BeginChannelExternalCommit(
		transaction,
		&coroProgramChannelSourceV1State,
		state.id,
		&state.claim,
	))
}

func finishCurrentCoroChannelCommit(
	state *CoroChanParkV1,
	transaction *coro.ChannelExternalCommit,
	status waitStatus,
) bool {
	if !validCoroChanParkV1(state) || transaction == nil || !status.done() ||
		!transaction.BeginEffect() {
		return false
	}
	state.waiter.status = status
	return transaction.Commit() && requestCoroChannelExecutorV1()
}

func coroChanTrySendLocked(ch *Chan, state *CoroChanParkV1) (ready bool, ok bool) {
	if ch == nil || !validCoroChanParkV1(state) || state.waiter.ch != ch || !state.waiter.send ||
		state.waiter.size != ch.elemsize {
		return false, false
	}
	if ch.closed {
		var transaction coro.ChannelExternalCommit
		if beginCurrentCoroChannelCommit(state, &transaction) != coroChanMatchCommitted ||
			!finishCurrentCoroChannelCommit(state, &transaction, waitSendClosed) {
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
			switch result := commitCoroPairLocked(&state.waiter, peer, ch.elemsize); result {
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
		classified := beginCurrentCoroChannelCommit(state, &transaction)
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
		copyChanElem(peer.elem, state.waiter.elem, ch.elemsize)
		state.waiter.status = waitSendOK
		peer.finish(waitRecvOK)
		if !transaction.Commit() || !requestCoroChannelExecutorV1() {
			return false, false
		}
		return true, true
	}
	if ch.qcount < ch.dataqsiz {
		var transaction coro.ChannelExternalCommit
		if beginCurrentCoroChannelCommit(state, &transaction) != coroChanMatchCommitted ||
			!transaction.BeginEffect() {
			return false, false
		}
		copyChanElem(chanBuf(ch, ch.sendx), state.waiter.elem, ch.elemsize)
		ch.sendx++
		if ch.sendx == ch.dataqsiz {
			ch.sendx = 0
		}
		ch.qcount++
		state.waiter.status = waitSendOK
		if !transaction.Commit() || !requestCoroChannelExecutorV1() {
			return false, false
		}
		return true, true
	}
	return false, true
}

func coroChanTryRecvLocked(ch *Chan, state *CoroChanParkV1) (ready bool, ok bool) {
	if ch == nil || !validCoroChanParkV1(state) || state.waiter.ch != ch || state.waiter.send ||
		state.waiter.size != ch.elemsize {
		return false, false
	}
	if ch.dataqsiz == 0 {
		for {
			peer := ch.sendq.dequeue()
			if peer == nil {
				break
			}
			if peer.coro != nil {
				switch result := commitCoroPairLocked(peer, &state.waiter, ch.elemsize); result {
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
			classified := beginCurrentCoroChannelCommit(state, &transaction)
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
			copyChanElem(state.waiter.elem, peer.elem, ch.elemsize)
			state.waiter.status = waitRecvOK
			peer.finish(waitSendOK)
			if !transaction.Commit() || !requestCoroChannelExecutorV1() {
				return false, false
			}
			return true, true
		}
	} else if ch.qcount > 0 {
		var transaction coro.ChannelExternalCommit
		if beginCurrentCoroChannelCommit(state, &transaction) != coroChanMatchCommitted ||
			!transaction.BeginEffect() {
			return false, false
		}
		copyChanElem(state.waiter.elem, chanBuf(ch, ch.recvx), ch.elemsize)
		zeroChanRecv(chanBuf(ch, ch.recvx), ch.elemsize)
		ch.recvx++
		if ch.recvx == ch.dataqsiz {
			ch.recvx = 0
		}
		ch.qcount--
		state.waiter.status = waitRecvOK
		if !transaction.Commit() || !requestCoroChannelExecutorV1() {
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
		if beginCurrentCoroChannelCommit(state, &transaction) != coroChanMatchCommitted ||
			!transaction.BeginEffect() {
			return false, false
		}
		zeroChanRecv(state.waiter.elem, ch.elemsize)
		state.waiter.status = waitRecvClosed
		if !transaction.Commit() || !requestCoroChannelExecutorV1() {
			return false, false
		}
		return true, true
	}
	return false, true
}

func dequeueSendToBufferLocked(ch *Chan) (progress, ok bool) {
	if ch == nil || ch.closed || ch.qcount >= ch.dataqsiz {
		return false, true
	}
	for {
		w := ch.sendq.dequeue()
		if w == nil {
			return false, true
		}
		if w.coro != nil {
			switch result := commitCoroSendWaiterLocked(w, chanBuf(ch, ch.sendx), ch.elemsize, waitSendOK); result {
			case coroChanMatchCommitted:
				ch.sendx++
				if ch.sendx == ch.dataqsiz {
					ch.sendx = 0
				}
				ch.qcount++
				return true, true
			case coroChanMatchDiscarded:
				continue
			case coroChanMatchRetry:
				ch.sendq.enqueueFront(w)
				return false, true
			default:
				return false, false
			}
		}
		if !claimWaiter(w) {
			continue
		}
		copyChanElem(chanBuf(ch, ch.sendx), w.elem, ch.elemsize)
		ch.sendx++
		if ch.sendx == ch.dataqsiz {
			ch.sendx = 0
		}
		ch.qcount++
		w.finish(waitSendOK)
		return true, true
	}
}

func dequeueSendToBuffer(ch *Chan) {
	if _, ok := dequeueSendToBufferLocked(ch); !ok {
		coroRuntimeAbort("invalid coroutine channel buffer refill")
	}
}

// dequeueBufferToRecvLocked restores the ordinary buffered-channel invariant
// after claim contention temporarily leaves both queued receivers and buffered
// data. The buffer position advances only after the exact receiver transaction
// has committed its typed copy.
func dequeueBufferToRecvLocked(ch *Chan) (progress, ok bool) {
	if ch == nil || ch.qcount == 0 {
		return false, true
	}
	for {
		w := ch.recvq.dequeue()
		if w == nil {
			return false, true
		}
		switch result := completeRecvWaiter(w, chanBuf(ch, ch.recvx), ch.elemsize, waitRecvOK); result {
		case coroChanMatchCommitted:
			zeroChanRecv(chanBuf(ch, ch.recvx), ch.elemsize)
			ch.recvx++
			if ch.recvx == ch.dataqsiz {
				ch.recvx = 0
			}
			ch.qcount--
			return true, true
		case coroChanMatchDiscarded:
			continue
		case coroChanMatchRetry:
			ch.recvq.enqueueFront(w)
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
	state.waiter = chanWaiter{ch: ch, elem: elem, size: size, send: send, coro: state}
	if ch == nil {
		ticket, ok := coro.PrepareEmptyChannelPark(
			(*coro.G)(g), handle, (*coro.HeaderV1)(header), &state.wait, fastrand(),
		)
		if !ok {
			coroRuntimeAbort("cannot prepare nil coroutine channel park")
			return
		}
		state.ticket = ticket
		return
	}
	if size != ch.elemsize {
		coroRuntimeAbort("coroutine channel element size mismatch")
		return
	}
	ticket, id, ok := coro.PrepareSingleChannelPark(
		(*coro.G)(g),
		handle,
		(*coro.HeaderV1)(header),
		&coroProgramChannelSourceV1State,
		&state.wait,
		&state.claim,
		1,
		fastrand(),
	)
	if !ok {
		coroRuntimeAbort("cannot prepare coroutine channel park")
		return
	}
	state.ticket, state.id = ticket, id
	ch.mutex.Lock()
	var ready bool
	if send {
		ready, ok = coroChanTrySendLocked(ch, state)
	} else {
		ready, ok = coroChanTryRecvLocked(ch, state)
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
	outcome, caseID, lease, task, ok := coro.TakeRunDecision((*coro.G)(g), state.ticket)
	if !ok {
		coroRuntimeAbort("invalid coroutine channel run decision")
		return coroChanResumeInvalid
	}
	if state.waiter.ch == nil {
		if outcome != coro.ParkOutcomeCanceled || caseID != 0 || lease.Valid() {
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
	ch := state.waiter.ch
	ch.mutex.Lock()
	if state.waiter.send {
		ch.sendq.remove(&state.waiter)
	} else {
		ch.recvq.remove(&state.waiter)
	}
	if !reconcileBufferedChanLocked(ch, !ch.closed) {
		ch.mutex.Unlock()
		coroRuntimeAbort("cannot reconcile coroutine buffered channel")
		return coroChanResumeInvalid
	}
	if ch.closed {
		if !drainClosedChanWaitersLocked(ch) {
			ch.mutex.Unlock()
			coroRuntimeAbort("cannot finish closed coroutine channel drain")
			return coroChanResumeInvalid
		}
	}
	ch.mutex.Unlock()
	discard := outcome == coro.ParkOutcomeCanceled
	if outcome == coro.ParkOutcomeCompleted {
		if caseID != 1 || task != coro.TaskCancelNone || !lease.Valid() || !state.waiter.status.done() {
			coroRuntimeAbort("invalid completed coroutine channel decision")
			return coroChanResumeInvalid
		}
	} else if outcome != coro.ParkOutcomeCanceled || caseID != 0 ||
		task != coro.TaskCancelAbort && task != coro.TaskCancelShutdown {
		coroRuntimeAbort("invalid canceled coroutine channel decision")
		return coroChanResumeInvalid
	}
	if !coro.FinishSingleChannelPark(
		(*coro.G)(g),
		&coroProgramChannelSourceV1State,
		state.id,
		&state.claim,
		lease,
		discard,
	) {
		coroRuntimeAbort("cannot finish coroutine channel park")
		return coroChanResumeInvalid
	}
	status := state.waiter.status
	*state = CoroChanParkV1{}
	if discard {
		if task == coro.TaskCancelShutdown {
			return coroChanResumeShutdown
		}
		return coroChanResumeTaskAbort
	}
	switch status {
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

// __llgo_coro_chan_send_closed_panic_v1 converts the channel resume status
// into the scheduler's terminal explicit-status transaction. The payload is a
// package-global interface, so both words outlive frame destruction.
//
//export __llgo_coro_chan_send_closed_panic_v1
func __llgo_coro_chan_send_closed_panic_v1(g, handle, header unsafe.Pointer) {
	payload := *(*eface)(unsafe.Pointer(&coroChanSendClosedPanicV1))
	if payload._type == nil || !coro.PreparePanic(
		(*coro.G)(g),
		handle,
		(*coro.HeaderV1)(header),
		unsafe.Pointer(payload._type),
		payload.data,
	) {
		coroRuntimeAbort("invalid coroutine channel send-closed panic handoff")
	}
}
