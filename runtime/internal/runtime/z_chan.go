/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/runtime/math"
)

// -----------------------------------------------------------------------------

type Chan struct {
	// mutex is a platform-selected channel-state gate. The ordinary runtime
	// keeps the pthread implementation; llgo_coro replaces it with the
	// single-executor-owner gate from z_chan_lock_coro.go so a managed LLVM
	// coroutine can never block its physical thread while mutating hchan state.
	mutex channelMutex

	qcount   int
	dataqsiz int
	buf      unsafe.Pointer
	elemsize int
	closed   bool
	// timerSync marks the private one-element storage used by the Go 1.23+
	// synchronous timer-channel contract. Ordinary sends/receives still use
	// that storage, while the observable len/cap builtins must both report 0.
	// It is set once before time.NewTimer publishes the channel.
	timerSync bool
	recvx     int
	sendx     int

	sendq chanWaitq
	recvq chanWaitq
}

type chanWaitq struct {
	first *chanWaiter
	last  *chanWaiter
}

type chanWaiter struct {
	prev *chanWaiter
	next *chanWaiter
	all  *chanWaiter

	ch   *Chan
	elem unsafe.Pointer
	size int
	send bool

	queued bool
	status waitStatus

	mutex channelWaitMutex
	cond  channelWaitCond

	sel       *selectState
	caseIndex int
	// coro is non-nil only for a compiler-spilled stackless waiter. The compact
	// operation record is embedded either in a direct park or in one case of a
	// multi-channel select. Such a waiter never owns pthread mutex/cond state;
	// z_chan_coro.go commits it through the exact ChannelOperationSource
	// transaction before any typed payload or completion status is published.
	coro *coroChanOperationV1
}

type selectState struct {
	mutex channelWaitMutex
	cond  channelWaitCond

	status waitStatus
	chosen int
}

type waitStatus uint8

const (
	waitPending waitStatus = iota
	waitClaimed
	waitRecvOK
	waitRecvClosed
	waitSendOK
	waitSendClosed
)

func (s waitStatus) done() bool {
	return s >= waitRecvOK
}

func (s waitStatus) recvOK() bool {
	return s == waitRecvOK || s == waitSendOK
}

func (s waitStatus) panicOnWake() bool {
	return s == waitSendClosed
}

func (q *chanWaitq) enqueue(w *chanWaiter) {
	w.prev = q.last
	w.next = nil
	w.queued = true
	if q.last == nil {
		q.first = w
	} else {
		q.last.next = w
	}
	q.last = w
}

func (q *chanWaitq) enqueueFront(w *chanWaiter) {
	w.prev = nil
	w.next = q.first
	w.queued = true
	if q.first == nil {
		q.last = w
	} else {
		q.first.prev = w
	}
	q.first = w
}

func (q *chanWaitq) dequeue() *chanWaiter {
	w := q.first
	if w != nil {
		q.remove(w)
	}
	return w
}

func (q *chanWaitq) remove(w *chanWaiter) {
	if !w.queued {
		return
	}
	if w.prev == nil {
		q.first = w.next
	} else {
		w.prev.next = w.next
	}
	if w.next == nil {
		q.last = w.prev
	} else {
		w.next.prev = w.prev
	}
	w.prev = nil
	w.next = nil
	w.queued = false
}

func NewChan(eltSize, cap int) *Chan {
	if cap < 0 {
		panicMakeChanSize()
	}
	mem, overflow := math.MulUintptr(uintptr(eltSize), uintptr(cap))
	if overflow || mem > maxAlloc {
		panicMakeChanSize()
	}
	ret := new(Chan)
	ret.elemsize = eltSize
	ret.dataqsiz = cap
	if cap > 0 {
		ret.buf = AllocU(mem)
	}
	ret.mutex.Init(nil)
	return ret
}

func panicMakeChanSize() {
	panic(errorString("makechan: size out of range"))
}

func ChanLen(p *Chan) (n int) {
	if p == nil {
		return 0
	}
	p.mutex.Lock()
	if !p.timerSync {
		n = p.qcount
	}
	p.mutex.Unlock()
	return
}

func ChanCap(p *Chan) int {
	if p == nil {
		return 0
	}
	if p.timerSync {
		return 0
	}
	return p.dataqsiz
}

// MarkTimerChannel enables the Go 1.23+ synchronous timer-channel view over
// one private buffered slot. Marking is idempotent, but only a pristine
// one-element channel may acquire this immutable property.
func MarkTimerChannel(p *Chan) bool {
	if p == nil {
		return false
	}
	p.mutex.Lock()
	ok := p.timerSync || p.dataqsiz == 1 && p.qcount == 0 && !p.closed &&
		p.sendq.first == nil && p.sendq.last == nil && p.recvq.first == nil && p.recvq.last == nil
	if ok {
		p.timerSync = true
	}
	p.mutex.Unlock()
	return ok
}

func panicSendOnClosedChan() {
	panic("send on closed channel")
}

func zeroChanRecv(v unsafe.Pointer, eltSize int) {
	if v != nil && eltSize > 0 {
		c.Memset(v, 0, uintptr(eltSize))
	}
}

func copyChanElem(dst, src unsafe.Pointer, eltSize int) {
	if dst != nil && src != nil && eltSize > 0 {
		c.Memcpy(dst, src, uintptr(eltSize))
	}
}

func chanBuf(p *Chan, i int) unsafe.Pointer {
	return c.Advance(p.buf, i*p.elemsize)
}

func newChanWaiter(ch *Chan, elem unsafe.Pointer, eltSize int, send bool) *chanWaiter {
	w := new(chanWaiter)
	w.ch = ch
	w.elem = elem
	w.size = eltSize
	w.send = send
	w.mutex.Init(nil)
	w.cond.Init(nil)
	w.mutex.Lock()
	return w
}

func newSelectState() *selectState {
	state := (*selectState)(c.Malloc(unsafe.Sizeof(selectState{})))
	if state == nil {
		panic("out of memory")
	}
	c.Memset(unsafe.Pointer(state), 0, unsafe.Sizeof(selectState{}))
	state.chosen = -1
	state.mutex.Init(nil)
	state.cond.Init(nil)
	return state
}

func freeSelectState(state *selectState) {
	c.Free(unsafe.Pointer(state))
}

func newSelectWaiter(ch *Chan, elem unsafe.Pointer, eltSize int, send bool, state *selectState, caseIndex int) *chanWaiter {
	w := (*chanWaiter)(c.Malloc(unsafe.Sizeof(chanWaiter{})))
	if w == nil {
		panic("out of memory")
	}
	c.Memset(unsafe.Pointer(w), 0, unsafe.Sizeof(chanWaiter{}))
	w.ch = ch
	w.elem = elem
	w.size = eltSize
	w.send = send
	w.sel = state
	w.caseIndex = caseIndex
	return w
}

func freeSelectWaiters(w *chanWaiter) {
	for w != nil {
		next := w.all
		c.Free(unsafe.Pointer(w))
		w = next
	}
}

func (w *chanWaiter) wait() {
	for !w.status.done() {
		w.cond.Wait(&w.mutex)
	}
	w.mutex.Unlock()
	w.cond.Destroy()
	w.mutex.Destroy()
}

func (w *chanWaiter) finish(status waitStatus) {
	if w.coro != nil {
		coroRuntimeAbort("pthread completion used for coroutine channel waiter")
		return
	}
	if w.sel != nil {
		w.sel.mutex.Lock()
		w.sel.status = status
		w.sel.mutex.Unlock()
		w.sel.cond.Signal()
		return
	}
	w.mutex.Lock()
	w.status = status
	w.mutex.Unlock()
	w.cond.Signal()
}

func claimWaiter(w *chanWaiter) bool {
	if w.coro != nil {
		return false
	}
	if w.sel != nil {
		w.sel.mutex.Lock()
		if w.sel.status != waitPending {
			w.sel.mutex.Unlock()
			return false
		}
		w.sel.status = waitClaimed
		w.sel.chosen = w.caseIndex
		w.sel.mutex.Unlock()
		return true
	}
	return true
}

func completeRecvWaiter(w *chanWaiter, src unsafe.Pointer, eltSize int, status waitStatus) coroChanMatchResult {
	if w.coro != nil {
		return commitCoroRecvWaiterLocked(w, src, eltSize, status)
	}
	if !claimWaiter(w) {
		return coroChanMatchDiscarded
	}
	if status.recvOK() {
		copyChanElem(w.elem, src, eltSize)
	} else {
		zeroChanRecv(w.elem, eltSize)
	}
	w.finish(status)
	return coroChanMatchCommitted
}

func completeSendWaiter(w *chanWaiter, status waitStatus) coroChanMatchResult {
	if w.coro != nil {
		return commitCoroSendWaiterLocked(w, nil, w.size, status)
	}
	if !claimWaiter(w) {
		return coroChanMatchDiscarded
	}
	w.finish(status)
	return coroChanMatchCommitted
}

func recvFromSendWaiter(dst unsafe.Pointer, w *chanWaiter, eltSize int) coroChanMatchResult {
	if w.coro != nil {
		return commitCoroSendWaiterLocked(w, dst, eltSize, waitSendOK)
	}
	if !claimWaiter(w) {
		return coroChanMatchDiscarded
	}
	copyChanElem(dst, w.elem, eltSize)
	w.finish(waitSendOK)
	return coroChanMatchCommitted
}

func dequeueRecvAndComplete(p *Chan, src unsafe.Pointer, eltSize int, status waitStatus) bool {
	for {
		w := p.recvq.dequeue()
		if w == nil {
			return false
		}
		switch result := completeRecvWaiter(w, src, eltSize, status); result {
		case coroChanMatchCommitted:
			return true
		case coroChanMatchDiscarded:
			continue
		case coroChanMatchRetry:
			p.recvq.enqueueFront(w)
			return false
		default:
			coroRuntimeAbort("invalid coroutine receive waiter completion")
			return false
		}
	}
}

func dequeueSendAndRecv(p *Chan, dst unsafe.Pointer, eltSize int) bool {
	for {
		w := p.sendq.dequeue()
		if w == nil {
			return false
		}
		switch result := recvFromSendWaiter(dst, w, eltSize); result {
		case coroChanMatchCommitted:
			return true
		case coroChanMatchDiscarded:
			continue
		case coroChanMatchRetry:
			p.sendq.enqueueFront(w)
			return false
		default:
			coroRuntimeAbort("invalid coroutine send waiter completion")
			return false
		}
	}
}

func chanTrySendLocked(p *Chan, v unsafe.Pointer, eltSize int) (tryOK bool, closed bool) {
	elemSize := p.elemsize
	if p.closed {
		return false, true
	}
	if dequeueRecvAndComplete(p, v, elemSize, waitRecvOK) {
		return true, false
	}
	if p.qcount < p.dataqsiz {
		copyChanElem(chanBuf(p, p.sendx), v, elemSize)
		p.sendx++
		if p.sendx == p.dataqsiz {
			p.sendx = 0
		}
		p.qcount++
		return true, false
	}
	return false, false
}

func ChanTrySend(p *Chan, v unsafe.Pointer, eltSize int) bool {
	if p == nil {
		return false
	}
	p.mutex.Lock()
	ok, closed := chanTrySendLocked(p, v, eltSize)
	p.mutex.Unlock()
	if closed {
		panicSendOnClosedChan()
	}
	return ok
}

// CoroChanTrySend is the nonblocking, non-panicking first attempt used by
// compiler-owned stackless channel lowering. A closed channel deliberately
// returns false: the exact park transaction rechecks it and returns a typed
// send-closed status without unwinding across an LLVM coroutine suspension.
func CoroChanTrySend(p *Chan, v unsafe.Pointer, eltSize int) bool {
	if p == nil {
		return false
	}
	p.mutex.Lock()
	ok, closed := chanTrySendLocked(p, v, eltSize)
	p.mutex.Unlock()
	return ok && !closed
}

func ChanSend(p *Chan, v unsafe.Pointer, eltSize int) bool {
	if p == nil {
		blockForever()
		return false
	}
	p.mutex.Lock()
	ok, closed := chanTrySendLocked(p, v, eltSize)
	if closed {
		p.mutex.Unlock()
		panicSendOnClosedChan()
	}
	if ok {
		p.mutex.Unlock()
		return true
	}
	w := newChanWaiter(p, v, eltSize, true)
	p.sendq.enqueue(w)
	p.mutex.Unlock()

	w.wait()
	if w.status.panicOnWake() {
		panicSendOnClosedChan()
	}
	return true
}

func chanTryRecvLocked(p *Chan, v unsafe.Pointer, eltSize int) (recvOK bool, tryOK bool) {
	elemSize := p.elemsize
	if p.dataqsiz == 0 {
		if dequeueSendAndRecv(p, v, elemSize) {
			return true, true
		}
	} else if p.qcount > 0 {
		copyChanElem(v, chanBuf(p, p.recvx), elemSize)
		zeroChanRecv(chanBuf(p, p.recvx), elemSize)
		p.recvx++
		if p.recvx == p.dataqsiz {
			p.recvx = 0
		}
		p.qcount--
		dequeueSendToBuffer(p)
		return true, true
	}
	if p.closed {
		zeroChanRecv(v, elemSize)
		return false, true
	}
	return false, false
}

func ChanTryRecv(p *Chan, v unsafe.Pointer, eltSize int) (recvOK bool, tryOK bool) {
	if p == nil {
		return false, false
	}
	p.mutex.Lock()
	recvOK, tryOK = chanTryRecvLocked(p, v, eltSize)
	p.mutex.Unlock()
	return
}

// CoroChanTryRecv is the nonblocking first attempt used by compiler-owned
// stackless channel lowering. Unlike ChanRecv it never retains the caller's
// activation; a false tryOK is completed by the exact park transaction.
func CoroChanTryRecv(p *Chan, v unsafe.Pointer, eltSize int) (recvOK bool, tryOK bool) {
	return ChanTryRecv(p, v, eltSize)
}

func ChanRecv(p *Chan, v unsafe.Pointer, eltSize int) (recvOK bool) {
	if p == nil {
		blockForever()
		return false
	}
	p.mutex.Lock()
	if recvOK, tryOK := chanTryRecvLocked(p, v, eltSize); tryOK {
		p.mutex.Unlock()
		return recvOK
	}
	w := newChanWaiter(p, v, eltSize, false)
	p.recvq.enqueue(w)
	p.mutex.Unlock()

	w.wait()
	return w.status.recvOK()
}

const (
	coroChanCloseOK uint32 = iota
	coroChanCloseNil
	coroChanCloseClosed
)

// CoroChanTryClose performs the complete owner-local close transaction without
// raising a Go panic. The compiler maps the two ordinary language errors to
// its explicit-status terminal path, so neither can unwind through a live LLVM
// coroutine frame. Closing wakes receivers, senders, and select candidates via
// the same channel operation source used by send/receive cancellation.
func CoroChanTryClose(p *Chan) uint32 {
	if p == nil {
		return coroChanCloseNil
	}
	p.mutex.Lock()
	if p.closed {
		p.mutex.Unlock()
		return coroChanCloseClosed
	}
	p.closed = true
	// Claim contention can temporarily leave buffered data behind a queued
	// receiver. Preserve Go's close ordering: publish those values before the
	// remaining receivers observe the closed zero value.
	if !reconcileBufferedChanLocked(p, false) {
		p.mutex.Unlock()
		coroRuntimeAbort("invalid coroutine buffered channel close reconciliation")
		return coroChanCloseClosed
	}
	if !drainClosedChanWaitersLocked(p) {
		p.mutex.Unlock()
		coroRuntimeAbort("invalid coroutine channel close completion")
		return coroChanCloseClosed
	}
	p.mutex.Unlock()
	return coroChanCloseOK
}

func ChanClose(p *Chan) {
	switch CoroChanTryClose(p) {
	case coroChanCloseOK:
		return
	case coroChanCloseNil:
		panic("close of nil channel")
	case coroChanCloseClosed:
		panic("close of closed channel")
	default:
		coroRuntimeAbort("invalid coroutine channel close result")
	}
}

// drainClosedChanWaitersLocked publishes every currently claimable waiter.
// Claim contention is not corruption: the competing select/cancel owner will
// eventually resume and remove that exact node. Its resume tail calls this
// helper again, so ordinary waiters behind it cannot remain stranded on an
// already-closed channel.
func drainClosedChanWaitersLocked(p *Chan) bool {
	for {
		w := p.recvq.dequeue()
		if w == nil {
			break
		}
		switch result := completeRecvWaiter(w, nil, p.elemsize, waitRecvClosed); result {
		case coroChanMatchCommitted, coroChanMatchDiscarded:
			continue
		case coroChanMatchRetry:
			p.recvq.enqueueFront(w)
			return true
		default:
			return false
		}
	}
	for {
		w := p.sendq.dequeue()
		if w == nil {
			break
		}
		switch result := completeSendWaiter(w, waitSendClosed); result {
		case coroChanMatchCommitted, coroChanMatchDiscarded:
			continue
		case coroChanMatchRetry:
			p.sendq.enqueueFront(w)
			return true
		default:
			return false
		}
	}
	return true
}

func blockForever() {
	var mutex channelWaitMutex
	var cond channelWaitCond
	mutex.Init(nil)
	cond.Init(nil)
	mutex.Lock()
	for {
		cond.Wait(&mutex)
	}
}

// -----------------------------------------------------------------------------

// ChanOp represents a channel operation.
type ChanOp struct {
	C *Chan

	Val  unsafe.Pointer
	Size int32

	Send bool
}

const selectInlineChanCount = 8

type selectChanList struct {
	len    int
	inline [selectInlineChanCount]*Chan
	extra  []*Chan
}

func (l *selectChanList) get(i int) *Chan {
	if l.extra != nil {
		return l.extra[i]
	}
	return l.inline[i]
}

func (l *selectChanList) set(i int, ch *Chan) {
	if l.extra != nil {
		l.extra[i] = ch
		return
	}
	l.inline[i] = ch
}

func (l *selectChanList) insert(pos int, ch *Chan) {
	if l.extra != nil {
		l.extra = append(l.extra, nil)
		copy(l.extra[pos+1:], l.extra[pos:])
		l.extra[pos] = ch
		l.len++
		return
	}
	if l.len == selectInlineChanCount {
		extra := make([]*Chan, selectInlineChanCount+1, selectInlineChanCount*2)
		copy(extra, l.inline[:pos])
		extra[pos] = ch
		copy(extra[pos+1:], l.inline[pos:])
		l.extra = extra
		l.len++
		return
	}
	l.len++
	for i := l.len - 1; i > pos; i-- {
		l.set(i, l.get(i-1))
	}
	l.set(pos, ch)
}

func (l *selectChanList) add(ch *Chan) {
	addr := uintptr(unsafe.Pointer(ch))
	pos := 0
	for pos < l.len {
		cur := uintptr(unsafe.Pointer(l.get(pos)))
		if cur == addr {
			return
		}
		if cur > addr {
			break
		}
		pos++
	}
	l.insert(pos, ch)
}

// TrySelect executes a non-blocking select operation.
func TrySelect(ops ...ChanOp) (isel int, recvOK, tryOK bool) {
	n := len(ops)
	if n == 0 {
		return
	}
	start := selectStart(n)
	for i := 0; i < n; i++ {
		isel = (start + i) % n
		op := ops[isel]
		if op.C == nil {
			continue
		}
		op.C.mutex.Lock()
		if op.Send {
			var closed bool
			tryOK, closed = chanTrySendLocked(op.C, op.Val, int(op.Size))
			recvOK = true
			op.C.mutex.Unlock()
			if closed {
				panicSendOnClosedChan()
			}
		} else {
			recvOK, tryOK = chanTryRecvLocked(op.C, op.Val, int(op.Size))
			op.C.mutex.Unlock()
		}
		if tryOK {
			return
		}
	}
	return
}

// Select executes a blocking select operation.
func Select(ops ...ChanOp) (isel int, recvOK bool) {
	if isel, recvOK, ok := TrySelect(ops...); ok {
		return isel, recvOK
	}

	var chans selectChanList
	for _, op := range ops {
		ch := op.C
		if ch == nil {
			continue
		}
		chans.add(ch)
	}
	if chans.len == 0 {
		blockForever()
	}
	lockSelectChannels(&chans)

	start := selectStart(len(ops))
	for n := 0; n < len(ops); n++ {
		i := (start + n) % len(ops)
		op := ops[i]
		if op.C == nil {
			continue
		}
		ch := op.C
		var ready bool
		if op.Send {
			var closed bool
			ready, closed = chanTrySendLocked(ch, op.Val, int(op.Size))
			if closed {
				unlockSelectChannels(&chans)
				panicSendOnClosedChan()
			}
			if ready {
				unlockSelectChannels(&chans)
				return i, true
			}
		} else {
			recvOK, ready = chanTryRecvLocked(ch, op.Val, int(op.Size))
			if ready {
				unlockSelectChannels(&chans)
				return i, recvOK
			}
		}
	}

	state := newSelectState()

	var waiters *chanWaiter
	var lastWaiter *chanWaiter
	for n := 0; n < len(ops); n++ {
		i := (start + n) % len(ops)
		op := ops[i]
		if op.C == nil {
			continue
		}
		w := newSelectWaiter(op.C, op.Val, int(op.Size), op.Send, state, i)
		if op.Send {
			op.C.sendq.enqueue(w)
		} else {
			op.C.recvq.enqueue(w)
		}
		if lastWaiter == nil {
			waiters = w
		} else {
			lastWaiter.all = w
		}
		lastWaiter = w
	}
	unlockSelectChannels(&chans)

	state.mutex.Lock()
	for !state.status.done() {
		state.cond.Wait(&state.mutex)
	}
	isel = state.chosen
	status := state.status
	recvOK = status.recvOK()
	state.mutex.Unlock()

	for w := waiters; w != nil; w = w.all {
		cleanupSelectWaiter(w)
	}
	state.cond.Destroy()
	state.mutex.Destroy()
	freeSelectState(state)
	freeSelectWaiters(waiters)
	if status.panicOnWake() {
		panicSendOnClosedChan()
	}
	return
}

func lockSelectChannels(chans *selectChanList) {
	for i := 0; i < chans.len; i++ {
		ch := chans.get(i)
		ch.mutex.Lock()
	}
}

func unlockSelectChannels(chans *selectChanList) {
	for i := chans.len - 1; i >= 0; i-- {
		chans.get(i).mutex.Unlock()
	}
}

func cleanupSelectWaiter(w *chanWaiter) {
	w.ch.mutex.Lock()
	if w.send {
		w.ch.sendq.remove(w)
	} else {
		w.ch.recvq.remove(w)
	}
	w.ch.mutex.Unlock()
}

func selectStart(n int) int {
	if n <= 1 {
		return 0
	}
	return int(fastrand() % uint32(n))
}

// -----------------------------------------------------------------------------
