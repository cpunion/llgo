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

// The production coroutine runtime makes the obsolete pthread-backed channel
// waiter fail closed. This source-island test still type-checks the shared
// legacy allocation helpers, so provide their method surface locally without
// importing pthread or weakening the production abort path. None of the
// adapter tests performs a blocking channel wait through these objects.
type channelWaitMutex struct{}
type channelWaitCond struct{}

func (*channelWaitMutex) Init(*struct{}) int32 { return 0 }
func (*channelWaitMutex) Lock()                {}
func (*channelWaitMutex) Unlock()              {}
func (*channelWaitMutex) Destroy()             {}

func (*channelWaitCond) Init(*struct{}) int32         { return 0 }
func (*channelWaitCond) Wait(*channelWaitMutex) int32 { return 0 }
func (*channelWaitCond) Signal() int32                { return 0 }
func (*channelWaitCond) Destroy()                     {}

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

func coroTargetRequestChannelOperationV1(id coro.OperationID) bool {
	return id.Valid() && id.Source() == coro.OperationSourceChannel && id.Route() == coro.RouteID(1) &&
		coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State)
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
	handle, ok := coroProgramExecutorRegistryState.Register()
	if !ok || !coro.BindExecutorSourceCatalog(
		driver,
		p,
		&coroProgramExecutorRegistryState,
		handle,
		coro.ExecutorSourceCatalog{Channel: &coroProgramChannelSourceV1State},
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

	// One physical select shares a single claim across both queue nodes. A
	// second physical coroutine sender commits the second case; both tasks must
	// resume while the selector removes and recycles its losing first case.
	selectG, runnable := coro.NextRunnable(p)
	if !runnable || selectG == nil {
		t.Fatalf("dequeue channel selector = (%p, %t)", selectG, runnable)
	}
	var selectFrame *coroChannelAdapterFrame
	switch selectG {
	case receiver.g:
		selectFrame = receiver
	case sender.g:
		selectFrame = sender
	default:
		t.Fatalf("unexpected channel selector G %p", selectG)
	}
	selectAction := activateCoroChannelAdapterFrame(t, p, selectFrame)
	selectChannels := [2]*Chan{new(Chan), new(Chan)}
	for _, selectedChannel := range selectChannels {
		selectedChannel.elemsize = int(unsafe.Sizeof(uint32(0)))
		selectedChannel.mutex.Init(nil)
	}
	var firstSelectedValue, secondSelectedValue uint32
	selectOps := []ChanOp{
		{C: selectChannels[0], Val: unsafe.Pointer(&firstSelectedValue), Size: int32(unsafe.Sizeof(firstSelectedValue))},
		{C: selectChannels[1], Val: unsafe.Pointer(&secondSelectedValue), Size: int32(unsafe.Sizeof(secondSelectedValue))},
	}
	var selectCases [2]CoroChanSelectCaseV1
	var coroSelectState CoroChanSelectV1
	selectFrame.header.SuspendReason = uint16(coro.SuspendPark)
	selectFrame.header.Lifecycle = uint16(coro.FrameSuspended)
	prepareCoroChanSelectV1(
		unsafe.Pointer(selectFrame.g),
		selectFrame.handle,
		unsafe.Pointer(selectFrame.header),
		unsafe.Pointer(&selectCases[0]),
		unsafe.Pointer(&coroSelectState),
		selectOps,
	)
	if parked, ok := coro.Resumed(p, selectFrame.g, selectAction); !ok || parked.Kind != coro.ActionPark {
		t.Fatalf("commit channel select park = (%+v, %t)", parked, ok)
	}
	if selectChannels[0].recvq.first != &selectCases[0].waiter ||
		selectChannels[1].recvq.first != &selectCases[1].waiter {
		t.Fatalf("channel select waiters not published: first=%p second=%p",
			selectChannels[0].recvq.first, selectChannels[1].recvq.first)
	}
	deferredG, deferredOK := coro.NextRunnable(p)
	if !deferredOK || deferredG == nil || deferredG == selectFrame.g {
		t.Fatalf("dequeue unrelated ready G before select completion = (%p, %t)", deferredG, deferredOK)
	}
	var directSender *coroChannelAdapterFrame
	switch deferredG {
	case receiver.g:
		directSender = receiver
	case sender.g:
		directSender = sender
	default:
		t.Fatalf("unexpected direct select sender G %p", deferredG)
	}
	selectedValue := uint32(0xa5b6c7d8)
	var directSendState CoroChanParkV1
	directSendAction := activateCoroChannelAdapterFrame(t, p, directSender)
	parkCoroChannelAdapterFrame(
		t, p, directSender, directSendAction, selectChannels[1], unsafe.Pointer(&selectedValue), &directSendState, true,
	)
	pollCoroChannelAdapterExecutor(t, driver)
	completed := map[*coro.G]bool{}
	for len(completed) != 2 {
		next, nextOK := coro.NextRunnable(p)
		if !nextOK || next == nil || completed[next] {
			t.Fatalf("dequeue select pair G = (%p, %t), completed=%v", next, nextOK, completed)
		}
		completed[next] = true
		switch next {
		case selectFrame.g:
			selectAction, ok = coro.BeginRunG(p, selectFrame.g)
			if !ok || selectAction.Kind != coro.ActionCheckResume {
				t.Fatalf("begin completed channel selector = (%+v, %t)", selectAction, ok)
			}
			selectAction, ok = coro.Checked(p, selectFrame.g, selectAction, false)
			if !ok || selectAction.Kind != coro.ActionResume {
				t.Fatalf("activate completed channel selector = (%+v, %t)", selectAction, ok)
			}
			selectedIndex, selectedOK, selectStatus := CoroChanSelectResume(
				unsafe.Pointer(selectFrame.g),
				unsafe.Pointer(&selectCases[0]),
				unsafe.Pointer(&coroSelectState),
				selectOps...,
			)
			if selectedIndex != 1 || !selectedOK || selectStatus != coroChanResumeRecvOK ||
				firstSelectedValue != 0 || secondSelectedValue != selectedValue {
				t.Fatalf("channel select resume = index:%d ok:%t status:%d values:(%#x,%#x)",
					selectedIndex, selectedOK, selectStatus, firstSelectedValue, secondSelectedValue)
			}
			selectFrame.header.SuspendReason = uint16(coro.SuspendNone)
			selectFrame.header.Lifecycle = uint16(coro.FrameActive)
			yieldCoroChannelAdapterFrame(t, p, selectFrame, selectAction)
		case directSender.g:
			directSendAction, directStatus := resumeCoroChannelAdapterFrame(t, p, directSender, &directSendState)
			if directStatus != coroChanResumeSendOK {
				t.Fatalf("direct select sender resume status = %d, want %d", directStatus, coroChanResumeSendOK)
			}
			yieldCoroChannelAdapterFrame(t, p, directSender, directSendAction)
		default:
			t.Fatalf("unexpected completed select pair G %p", next)
		}
	}
	if selectChannels[0].recvq.first != nil || selectChannels[1].recvq.first != nil ||
		coroProgramChannelSourceV1State.Pending() {
		t.Fatalf("channel select retained queue/source state: first=%p second=%p pending=%t",
			selectChannels[0].recvq.first, selectChannels[1].recvq.first,
			coroProgramChannelSourceV1State.Pending())
	}

	// Reuse the selector immediately for a direct receive after its two source
	// slots have been recycled. This is the native generated-code sequence when
	// a selected case is followed by another blocking channel operation.
	follow := new(Chan)
	follow.elemsize = int(unsafe.Sizeof(uint32(0)))
	follow.mutex.Init(nil)
	next, nextOK := coro.NextRunnable(p)
	if !nextOK || next == nil {
		t.Fatalf("dequeue post-select sender = (%p, %t)", next, nextOK)
	}
	if next != directSender.g {
		if next != selectFrame.g || !coro.Enqueue(p, next) {
			t.Fatalf("rotate post-select ready queue from %p", next)
		}
		next, nextOK = coro.NextRunnable(p)
	}
	if !nextOK || next != directSender.g {
		t.Fatalf("dequeue post-select direct sender = (%p, %t), want %p", next, nextOK, directSender.g)
	}
	followValue := uint32(0x10293847)
	var followSendState, followRecvState CoroChanParkV1
	followSendAction := activateCoroChannelAdapterFrame(t, p, directSender)
	parkCoroChannelAdapterFrame(
		t, p, directSender, followSendAction, follow, unsafe.Pointer(&followValue), &followSendState, true,
	)
	followRecvAction := dequeueCoroChannelAdapterFrame(t, p, selectFrame)
	var followGot uint32
	parkCoroChannelAdapterFrame(
		t, p, selectFrame, followRecvAction, follow, unsafe.Pointer(&followGot), &followRecvState, false,
	)
	pollCoroChannelAdapterExecutor(t, driver)
	followCompleted := map[*coro.G]bool{}
	for len(followCompleted) != 2 {
		next, nextOK = coro.NextRunnable(p)
		if !nextOK || next == nil || followCompleted[next] {
			t.Fatalf("dequeue post-select pair G = (%p, %t), completed=%v", next, nextOK, followCompleted)
		}
		followCompleted[next] = true
		switch next {
		case directSender.g:
			followSendAction, status := resumeCoroChannelAdapterFrame(t, p, directSender, &followSendState)
			if status != coroChanResumeSendOK {
				t.Fatalf("post-select send status = %d, want %d", status, coroChanResumeSendOK)
			}
			yieldCoroChannelAdapterFrame(t, p, directSender, followSendAction)
		case selectFrame.g:
			followRecvAction, status := resumeCoroChannelAdapterFrame(t, p, selectFrame, &followRecvState)
			if status != coroChanResumeRecvOK || followGot != followValue {
				t.Fatalf("post-select receive = status:%d value:%#x, want status:%d value:%#x",
					status, followGot, coroChanResumeRecvOK, followValue)
			}
			yieldCoroChannelAdapterFrame(t, p, selectFrame, followRecvAction)
		default:
			t.Fatalf("unexpected post-select pair G %p", next)
		}
	}
	if follow.sendq.first != nil || follow.recvq.first != nil || coroProgramChannelSourceV1State.Pending() {
		t.Fatalf("post-select pair retained queue/source state: send=%p recv=%p pending=%t",
			follow.sendq.first, follow.recvq.first, coroProgramChannelSourceV1State.Pending())
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

	// Task cancellation is a logical competitor of every physical case. It
	// must win once, detach both hchan nodes, and return through the compiler's
	// typed cancellation edge without exposing a selected value.
	canceledG, runnable := coro.NextRunnable(p)
	if !runnable || canceledG == nil {
		t.Fatalf("dequeue channel selector for cancellation = (%p, %t)", canceledG, runnable)
	}
	var canceledFrame *coroChannelAdapterFrame
	switch canceledG {
	case receiver.g:
		canceledFrame = receiver
	case sender.g:
		canceledFrame = sender
	default:
		t.Fatalf("unexpected canceled selector G %p", canceledG)
	}
	canceledAction := activateCoroChannelAdapterFrame(t, p, canceledFrame)
	canceledChannels := [2]*Chan{new(Chan), new(Chan)}
	var canceledValues [2]uint32
	canceledOps := make([]ChanOp, len(canceledChannels))
	for index, canceledChannel := range canceledChannels {
		canceledChannel.elemsize = int(unsafe.Sizeof(uint32(0)))
		canceledChannel.mutex.Init(nil)
		canceledOps[index] = ChanOp{
			C: canceledChannel, Val: unsafe.Pointer(&canceledValues[index]), Size: int32(unsafe.Sizeof(uint32(0))),
		}
	}
	var canceledCases [2]CoroChanSelectCaseV1
	var canceledState CoroChanSelectV1
	canceledFrame.header.SuspendReason = uint16(coro.SuspendPark)
	canceledFrame.header.Lifecycle = uint16(coro.FrameSuspended)
	prepareCoroChanSelectV1(
		unsafe.Pointer(canceledFrame.g),
		canceledFrame.handle,
		unsafe.Pointer(canceledFrame.header),
		unsafe.Pointer(&canceledCases[0]),
		unsafe.Pointer(&canceledState),
		canceledOps,
	)
	if parked, ok := coro.Resumed(p, canceledFrame.g, canceledAction); !ok || parked.Kind != coro.ActionPark {
		t.Fatalf("commit canceled channel select park = (%+v, %t)", parked, ok)
	}
	deferredCanceledG, deferredCanceledOK := coro.NextRunnable(p)
	if !deferredCanceledOK || deferredCanceledG == nil || deferredCanceledG == canceledFrame.g {
		t.Fatalf("dequeue unrelated G before select cancellation = (%p, %t)", deferredCanceledG, deferredCanceledOK)
	}
	if !coro.RequestTaskCancellation(p, canceledFrame.g, coro.TaskCancelAbort) {
		t.Fatal("request channel select task cancellation")
	}
	pollCoroChannelAdapterExecutor(t, driver)
	if next, ok := coro.NextRunnable(p); !ok || next != canceledFrame.g {
		t.Fatalf("dequeue canceled channel selector = (%p, %t), want %p", next, ok, canceledFrame.g)
	}
	canceledAction, ok = coro.BeginRunG(p, canceledFrame.g)
	if !ok || canceledAction.Kind != coro.ActionCheckResume {
		t.Fatalf("begin canceled channel selector = (%+v, %t)", canceledAction, ok)
	}
	canceledAction, ok = coro.Checked(p, canceledFrame.g, canceledAction, false)
	if !ok || canceledAction.Kind != coro.ActionResume {
		t.Fatalf("activate canceled channel selector = (%+v, %t)", canceledAction, ok)
	}
	canceledIndex, canceledOK, canceledStatus := CoroChanSelectResume(
		unsafe.Pointer(canceledFrame.g),
		unsafe.Pointer(&canceledCases[0]),
		unsafe.Pointer(&canceledState),
		canceledOps...,
	)
	if canceledIndex != -1 || canceledOK || canceledStatus != coroChanResumeTaskAbort {
		t.Fatalf("canceled channel select resume = index:%d ok:%t status:%d",
			canceledIndex, canceledOK, canceledStatus)
	}
	if canceledChannels[0].recvq.first != nil || canceledChannels[1].recvq.first != nil ||
		coroProgramChannelSourceV1State.Pending() {
		t.Fatalf("canceled channel select retained queue/source state: first=%p second=%p pending=%t",
			canceledChannels[0].recvq.first, canceledChannels[1].recvq.first,
			coroProgramChannelSourceV1State.Pending())
	}
}
