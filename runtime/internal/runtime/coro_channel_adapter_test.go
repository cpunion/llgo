//go:build coro_channel_adapter_test

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
	"testing"
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

// The channel adapter is tested as a named production-source island. These
// definitions supply only unrelated runtime services which z_chan.go must
// type-check but this test never calls.
const maxAlloc = ^uintptr(0) >> 1

type errorString string

type eface struct {
	_type unsafe.Pointer
	data  unsafe.Pointer
}

func AllocU(uintptr) unsafe.Pointer { panic("unexpected channel allocation") }
func fastrand() uint32              { return 1 }
func coroRuntimeAbort(message string) {
	panic(message)
}

//go:linkname coroChannelTestMemcpy C.memcpy
func coroChannelTestMemcpy(dst, src unsafe.Pointer, size uintptr) unsafe.Pointer {
	copy(unsafe.Slice((*byte)(dst), size), unsafe.Slice((*byte)(src), size))
	return dst
}

//go:linkname coroChannelTestMemset C.memset
func coroChannelTestMemset(dst unsafe.Pointer, value int32, size uintptr) unsafe.Pointer {
	bytes := unsafe.Slice((*byte)(dst), size)
	for index := range bytes {
		bytes[index] = byte(value)
	}
	return dst
}

// The fixture is single-threaded. No-op pthread shims preserve the production
// channel source unchanged while avoiding a host C dependency in this named
// Go source-island test.
//
//go:linkname coroChannelTestMutexInit C.pthread_mutex_init
func coroChannelTestMutexInit(unsafe.Pointer, unsafe.Pointer) int32 { return 0 }

//go:linkname coroChannelTestMutexLock C.pthread_mutex_lock
func coroChannelTestMutexLock(unsafe.Pointer) int32 { return 0 }

//go:linkname coroChannelTestMutexUnlock C.pthread_mutex_unlock
func coroChannelTestMutexUnlock(unsafe.Pointer) int32 { return 0 }

//go:linkname coroChannelTestCondSignal C.pthread_cond_signal
func coroChannelTestCondSignal(unsafe.Pointer) int32 { return 0 }

var (
	coroProgramChannelSourceV1State  coro.ChannelOperationSource
	coroProgramExecutorRegistryState coro.ExecutorRegistry
	coroProgramExecutorHandleV1State coro.ExecutorHandle
	coroProgramExecutorBoundV1State  bool
)

func coroTargetRequestExecutorV1(handle coro.ExecutorHandle) bool {
	if !coroProgramExecutorBoundV1State || handle != coroProgramExecutorHandleV1State {
		return false
	}
	result := coroProgramExecutorRegistryState.Request(handle)
	return result == coro.ExecutorRequestPublished || result == coro.ExecutorRequestCoalesced ||
		result == coro.ExecutorRequestIdleWake
}

type coroChannelAdapterFrame struct {
	g          *coro.G
	handle     unsafe.Pointer
	header     *coro.HeaderV1
	storage    unsafe.Pointer
	descriptor unsafe.Pointer
	total      uintptr
	size       uintptr
	align      uintptr
	memory     []uintptr
}

func newCoroChannelAdapterFrame(t *testing.T) *coroChannelAdapterFrame {
	t.Helper()
	g := new(coro.G)
	if !coro.InitG(g) {
		t.Fatal("initialize channel adapter G")
	}
	const (
		size  = uintptr(64)
		align = uintptr(16)
	)
	total, ok := coro.FrameAllocationSize(size, align)
	if !ok {
		t.Fatal("compute channel adapter frame size")
	}
	wordSize := unsafe.Sizeof(uintptr(0))
	memory := make([]uintptr, (total+wordSize-1)/wordSize)
	descriptor := unsafe.Pointer(&coro.FrameDescriptorV1{Version: 1, ResultAlign: 1})
	storage, ok := coro.RegisterFrame(g, unsafe.Pointer(&memory[0]), total, size, align, descriptor)
	if !ok {
		t.Fatal("register channel adapter frame")
	}
	handle := unsafe.Pointer(new(byte))
	header := &coro.HeaderV1{
		G:             unsafe.Pointer(g),
		Descriptor:    descriptor,
		SuspendReason: uint16(coro.SuspendNone),
		Lifecycle:     uint16(coro.FrameInitialSuspended),
	}
	if !coro.PublishFrame(g, handle, header, storage) || !coro.AdoptRoot(g, handle) {
		t.Fatal("publish channel adapter root frame")
	}
	return &coroChannelAdapterFrame{
		g: g, handle: handle, header: header, storage: storage,
		descriptor: descriptor, total: total, size: size, align: align, memory: memory,
	}
}

func beginCoroChannelAdapterFrame(t *testing.T, p *coro.P, frame *coroChannelAdapterFrame) coro.Action {
	t.Helper()
	if !coro.Enqueue(p, frame.g) {
		t.Fatal("enqueue channel adapter G")
	}
	return dequeueCoroChannelAdapterFrame(t, p, frame)
}

func dequeueCoroChannelAdapterFrame(t *testing.T, p *coro.P, frame *coroChannelAdapterFrame) coro.Action {
	t.Helper()
	if next, ok := coro.NextRunnable(p); !ok || next != frame.g {
		t.Fatalf("dequeue channel adapter G = (%p, %t), want %p", next, ok, frame.g)
	}
	return activateCoroChannelAdapterFrame(t, p, frame)
}

func activateCoroChannelAdapterFrame(t *testing.T, p *coro.P, frame *coroChannelAdapterFrame) coro.Action {
	t.Helper()
	action, ok := coro.BeginRunG(p, frame.g)
	if !ok || action.Kind != coro.ActionCheckResume {
		t.Fatalf("begin channel adapter G = (%+v, %t)", action, ok)
	}
	action, ok = coro.Checked(p, frame.g, action, false)
	if !ok || action.Kind != coro.ActionResume {
		t.Fatalf("activate channel adapter G = (%+v, %t)", action, ok)
	}
	outcome, caseID, lease, task, ok := coro.TakeRunDecision(frame.g, coro.ParkTicket{})
	if !ok || outcome != coro.ParkOutcomePending || caseID != 0 || lease.Valid() || task != coro.TaskCancelNone {
		t.Fatalf("take initial channel adapter decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, ok)
	}
	frame.header.SuspendReason = uint16(coro.SuspendNone)
	frame.header.Lifecycle = uint16(coro.FrameActive)
	return action
}

func parkCoroChannelAdapterFrame(
	t *testing.T,
	p *coro.P,
	frame *coroChannelAdapterFrame,
	action coro.Action,
	ch *Chan,
	elem unsafe.Pointer,
	state *CoroChanParkV1,
	send bool,
) {
	t.Helper()
	frame.header.SuspendReason = uint16(coro.SuspendPark)
	frame.header.Lifecycle = uint16(coro.FrameSuspended)
	prepareCoroChanParkV1(
		unsafe.Pointer(frame.g), frame.handle, unsafe.Pointer(frame.header), unsafe.Pointer(ch), elem,
		unsafe.Pointer(state), unsafe.Sizeof(uint32(0)), send,
	)
	parked, ok := coro.Resumed(p, frame.g, action)
	if !ok || parked.Kind != coro.ActionPark {
		t.Fatalf("commit channel adapter park = (%+v, %t)", parked, ok)
	}
}

func resumeCoroChannelAdapterFrame(
	t *testing.T,
	p *coro.P,
	frame *coroChannelAdapterFrame,
	state *CoroChanParkV1,
) (coro.Action, uint32) {
	t.Helper()
	action, ok := coro.BeginRunG(p, frame.g)
	if !ok || action.Kind != coro.ActionCheckResume {
		t.Fatalf("begin completed channel adapter G = (%+v, %t)", action, ok)
	}
	action, ok = coro.Checked(p, frame.g, action, false)
	if !ok || action.Kind != coro.ActionResume {
		t.Fatalf("activate completed channel adapter G = (%+v, %t)", action, ok)
	}
	status := __llgo_coro_chan_resume_v1(unsafe.Pointer(frame.g), unsafe.Pointer(state))
	frame.header.SuspendReason = uint16(coro.SuspendNone)
	frame.header.Lifecycle = uint16(coro.FrameActive)
	return action, status
}

func yieldCoroChannelAdapterFrame(t *testing.T, p *coro.P, frame *coroChannelAdapterFrame, action coro.Action) {
	t.Helper()
	frame.header.SuspendReason = uint16(coro.SuspendYield)
	frame.header.Lifecycle = uint16(coro.FrameSuspended)
	if !coro.PrepareYield(frame.g, frame.handle, frame.header) {
		t.Fatal("prepare channel adapter yield")
	}
	yielded, ok := coro.Resumed(p, frame.g, action)
	if !ok || yielded.Kind != coro.ActionYield {
		t.Fatalf("yield channel adapter G = (%+v, %t)", yielded, ok)
	}
}

func pollCoroChannelAdapterExecutor(t *testing.T, driver *coro.ExecutorDriver) {
	t.Helper()
	for step := 0; ; step++ {
		progress, ok := coro.PollExecutorSlice(driver, 1)
		if !ok {
			t.Fatalf("poll channel adapter executor at step %d", step)
		}
		if progress.Complete {
			return
		}
		if step == 10000 {
			t.Fatal("channel adapter executor did not complete")
		}
	}
}

func TestCoroChannelAdapterPairCommitAndResume(t *testing.T) {
	p := new(coro.P)
	driver := new(coro.ExecutorDriver)
	waits := new(coro.WaitRegistrationTable)
	handle, ok := coroProgramExecutorRegistryState.Register()
	if !ok || !coro.BindExecutorSourceCatalog(
		driver,
		p,
		&coroProgramExecutorRegistryState,
		handle,
		coro.ExecutorSourceCatalog{Waits: waits, Channel: &coroProgramChannelSourceV1State},
	) {
		t.Fatal("bind channel adapter executor")
	}
	coroProgramExecutorHandleV1State = handle
	coroProgramExecutorBoundV1State = true

	receiver := newCoroChannelAdapterFrame(t)
	sender := newCoroChannelAdapterFrame(t)
	var recvValue, sendValue uint32
	sendValue = 0x1234abcd
	var recvState, sendState CoroChanParkV1

	closed := new(Chan)
	closed.elemsize = int(unsafe.Sizeof(uint32(0)))
	closed.mutex.Init(nil)
	closedAction := beginCoroChannelAdapterFrame(t, p, receiver)
	parkCoroChannelAdapterFrame(t, p, receiver, closedAction, closed, unsafe.Pointer(&sendValue), &recvState, true)
	closedSenderAction := beginCoroChannelAdapterFrame(t, p, sender)
	parkCoroChannelAdapterFrame(t, p, sender, closedSenderAction, closed, unsafe.Pointer(&sendValue), &sendState, true)
	ChanClose(closed)
	pollCoroChannelAdapterExecutor(t, driver)
	closedReady := map[*coro.G]bool{}
	for len(closedReady) != 2 {
		g, runnable := coro.NextRunnable(p)
		if !runnable || g == nil || closedReady[g] {
			t.Fatalf("dequeue closed-channel sender = (%p, %t), ready=%v", g, runnable, closedReady)
		}
		closedReady[g] = true
		var frame *coroChannelAdapterFrame
		var state *CoroChanParkV1
		switch g {
		case receiver.g:
			frame, state = receiver, &recvState
		case sender.g:
			frame, state = sender, &sendState
		default:
			t.Fatalf("unexpected closed-channel sender G %p", g)
		}
		action, status := resumeCoroChannelAdapterFrame(t, p, frame, state)
		if status != coroChanResumeSendClosed {
			t.Fatalf("closed-channel send resume status = %d, want %d", status, coroChanResumeSendClosed)
		}
		yieldCoroChannelAdapterFrame(t, p, frame, action)
	}

	ch := new(Chan)
	ch.elemsize = int(unsafe.Sizeof(uint32(0)))
	ch.mutex.Init(nil)
	first, runnable := coro.NextRunnable(p)
	if !runnable || first == nil {
		t.Fatalf("dequeue pair receiver = (%p, %t)", first, runnable)
	}
	pairReceiver, pairReceiverState := receiver, &recvState
	pairSender, pairSenderState := sender, &sendState
	if first == sender.g {
		pairReceiver, pairReceiverState = sender, &sendState
		pairSender, pairSenderState = receiver, &recvState
	} else if first != receiver.g {
		t.Fatalf("unexpected pair receiver G %p", first)
	}
	recvAction := activateCoroChannelAdapterFrame(t, p, pairReceiver)
	parkCoroChannelAdapterFrame(t, p, pairReceiver, recvAction, ch, unsafe.Pointer(&recvValue), pairReceiverState, false)
	sendAction := dequeueCoroChannelAdapterFrame(t, p, pairSender)
	parkCoroChannelAdapterFrame(t, p, pairSender, sendAction, ch, unsafe.Pointer(&sendValue), pairSenderState, true)
	pollCoroChannelAdapterExecutor(t, driver)

	ready := map[*coro.G]bool{}
	for len(ready) != 2 {
		g, runnable := coro.NextRunnable(p)
		if !runnable || g == nil || ready[g] {
			t.Fatalf("dequeue paired channel G = (%p, %t), ready=%v", g, runnable, ready)
		}
		ready[g] = true
		switch g {
		case pairReceiver.g:
			action, status := resumeCoroChannelAdapterFrame(t, p, pairReceiver, pairReceiverState)
			if status != coroChanResumeRecvOK || recvValue != sendValue {
				t.Fatalf("receive resume = status:%d value:%#x, want status:%d value:%#x", status, recvValue, coroChanResumeRecvOK, sendValue)
			}
			yieldCoroChannelAdapterFrame(t, p, pairReceiver, action)
		case pairSender.g:
			action, status := resumeCoroChannelAdapterFrame(t, p, pairSender, pairSenderState)
			if status != coroChanResumeSendOK {
				t.Fatalf("send resume status = %d, want %d", status, coroChanResumeSendOK)
			}
			yieldCoroChannelAdapterFrame(t, p, pairSender, action)
		default:
			t.Fatalf("unexpected paired channel G %p", g)
		}
	}
	if ch.sendq.first != nil || ch.recvq.first != nil || coroProgramChannelSourceV1State.Pending() {
		t.Fatalf("paired channel retained queue/source state: send=%p recv=%p pending=%t",
			ch.sendq.first, ch.recvq.first, coroProgramChannelSourceV1State.Pending())
	}

	// Claim contention can temporarily leave receivers queued while a sender
	// uses an available buffer slot. Closing must deliver that buffered value
	// before publishing the closed zero value to the next receiver.
	bufferValue := uint32(0xdecafbad)
	buffered := &Chan{
		qcount:   1,
		dataqsiz: 1,
		buf:      unsafe.Pointer(&bufferValue),
		elemsize: int(unsafe.Sizeof(bufferValue)),
	}
	buffered.mutex.Init(nil)
	var firstValue, secondValue uint32 = 0, ^uint32(0)
	firstSelect := &selectState{chosen: -1}
	firstSelect.mutex.Init(nil)
	secondSelect := &selectState{chosen: -1}
	secondSelect.mutex.Init(nil)
	sendSelect := &selectState{chosen: -1}
	sendSelect.mutex.Init(nil)
	sendValueAfterClose := uint32(0x11223344)
	firstWaiter := &chanWaiter{
		ch: buffered, elem: unsafe.Pointer(&firstValue), size: buffered.elemsize,
		sel: firstSelect, caseIndex: 3,
	}
	secondWaiter := &chanWaiter{
		ch: buffered, elem: unsafe.Pointer(&secondValue), size: buffered.elemsize,
		sel: secondSelect, caseIndex: 5,
	}
	sendWaiter := &chanWaiter{
		ch: buffered, elem: unsafe.Pointer(&sendValueAfterClose), size: buffered.elemsize, send: true,
		sel: sendSelect, caseIndex: 7,
	}
	buffered.recvq.enqueue(firstWaiter)
	buffered.recvq.enqueue(secondWaiter)
	buffered.sendq.enqueue(sendWaiter)
	ChanClose(buffered)
	if firstValue != 0xdecafbad || firstSelect.status != waitRecvOK || firstSelect.chosen != 3 {
		t.Fatalf("buffered receiver before close = value:%#x status:%d chosen:%d", firstValue, firstSelect.status, firstSelect.chosen)
	}
	if secondValue != 0 || secondSelect.status != waitRecvClosed || secondSelect.chosen != 5 {
		t.Fatalf("receiver after drained close = value:%#x status:%d chosen:%d", secondValue, secondSelect.status, secondSelect.chosen)
	}
	if sendSelect.status != waitSendClosed || sendSelect.chosen != 7 || sendValueAfterClose != 0x11223344 {
		t.Fatalf("buffered sender after close = value:%#x status:%d chosen:%d", sendValueAfterClose, sendSelect.status, sendSelect.chosen)
	}
	if buffered.qcount != 0 || buffered.recvq.first != nil || buffered.recvq.last != nil ||
		buffered.sendq.first != nil || buffered.sendq.last != nil {
		t.Fatalf("closed buffered channel retained data/waiters: count=%d recv=(%p,%p) send=(%p,%p)",
			buffered.qcount, buffered.recvq.first, buffered.recvq.last, buffered.sendq.first, buffered.sendq.last)
	}
}
