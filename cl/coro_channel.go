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

package cl

import (
	"fmt"
	"go/token"
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroChanSendParkHookV1 = "__llgo_coro_chan_send_park_v1"
	coroChanRecvParkHookV1 = "__llgo_coro_chan_recv_park_v1"
	coroChanResumeHookV1   = "__llgo_coro_chan_resume_v1"
)

const (
	coroChanResumeSendOK uint64 = iota + 1
	coroChanResumeRecvOK
	coroChanResumeRecvClosed
	coroChanResumeSendClosed
	coroChanResumeTaskAbort
	coroChanResumeShutdown
)

const (
	coroChanCloseOK uint64 = iota
	coroChanCloseNil
	coroChanCloseClosed
)

func isCoroCloseBuiltinCall(call *ssa.Call) bool {
	if call == nil || call.Common() == nil {
		return false
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	return ok && builtin.Name() == "close"
}

func coroChanParkSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "channel", pointer),
		types.NewParam(token.NoPos, nil, "elem", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
		types.NewParam(token.NoPos, nil, "size", types.Typ[types.Uintptr]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroChanResumeSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func (p *context) requireCoroChannelBody(b llssa.Builder) *coroBodyContext {
	body := p.coroBody()
	if body == nil || b.Func != p.fn {
		panic("coroutine channel lowering requires an active planned physical coroutine body")
	}
	if body.abi.version < coroPhysicalABIVersionV1 || body.completion == nil ||
		body.finalSuspend == nil || body.unsupportedRunDecision == nil {
		panic("coroutine channel lowering requires the complete PhysicalABIV1 scheduler ABI")
	}
	return body
}

func (p *context) newCoroChannelStorage(b llssa.Builder, elemType llssa.Type) (elem, state llssa.Expr) {
	// These addresses may be published to hchan immediately before suspend.
	// Allocate them in the physical ramp entry so no waiter can retain a
	// resume-local M stack address. Reset at the logical operation point because
	// the same static channel instruction may execute repeatedly in a loop.
	elem = p.coroFrameAlloca(elemType)
	stateType := p.prog.RuntimeType("CoroChanParkV1")
	state = p.coroFrameAlloca(stateType)
	b.Store(elem, b.Prog.Zero(elemType))
	b.Store(state, b.Prog.Zero(stateType))
	return
}

func (p *context) compileCoroChanSend(b llssa.Builder, channel, value llssa.Expr) {
	body := p.requireCoroChannelBody(b)
	elem, state := p.newCoroChannelStorage(b, value.Type)
	b.Store(elem, value)
	ready := b.CoroChanTrySend(channel, elem)
	body.emitCoroParkOperation(p, b, coroParkOperation{
		shouldSuspend: b.UnOp(token.NOT, ready),
		park: func(suspend llssa.Builder) {
			park := p.pkg.NewFunc(coroChanSendParkHookV1, coroChanParkSignature(), llssa.InC)
			suspend.Call(
				park.Expr,
				body.task,
				body.coro.Handle(),
				suspend.Convert(suspend.Prog.VoidPtr(), body.header),
				suspend.Convert(suspend.Prog.VoidPtr(), channel),
				suspend.Convert(suspend.Prog.VoidPtr(), elem),
				suspend.Convert(suspend.Prog.VoidPtr(), state),
				p.prog.IntVal(p.prog.SizeOf(value.Type), p.prog.Uintptr()),
			)
		},
		resume: func(resume llssa.Builder) llssa.Expr {
			statusHook := p.pkg.NewFunc(coroChanResumeHookV1, coroChanResumeSignature(), llssa.InC)
			return resume.Call(statusHook.Expr, body.task, resume.Convert(resume.Prog.VoidPtr(), state))
		},
		normal: []uint64{coroChanResumeSendOK},
		faults: []coroParkFaultRoute{
			{status: coroChanResumeSendClosed, kind: coroFaultChannelSendClosedV1},
		},
		abort:    coroChanResumeTaskAbort,
		shutdown: coroChanResumeShutdown,
	})
}

func (p *context) compileCoroChanRecv(b llssa.Builder, instruction *ssa.UnOp, channel llssa.Expr) llssa.Expr {
	if instruction == nil || instruction.Op != token.ARROW {
		panic(fmt.Errorf("coroutine channel receive requires one SSA receive instruction"))
	}
	body := p.requireCoroChannelBody(b)
	elemType := p.prog.Elem(channel.Type)
	elem, state := p.newCoroChannelStorage(b, elemType)
	result := b.CoroChanTryRecv(channel, elem)
	recvOK := b.Extract(result, 0)
	tryOK := b.Extract(result, 1)
	recvOKSlot := p.coroFrameAlloca(p.prog.Bool())
	b.Store(recvOKSlot, recvOK)
	body.emitCoroParkOperation(p, b, coroParkOperation{
		shouldSuspend: b.UnOp(token.NOT, tryOK),
		park: func(suspend llssa.Builder) {
			park := p.pkg.NewFunc(coroChanRecvParkHookV1, coroChanParkSignature(), llssa.InC)
			suspend.Call(
				park.Expr,
				body.task,
				body.coro.Handle(),
				suspend.Convert(suspend.Prog.VoidPtr(), body.header),
				suspend.Convert(suspend.Prog.VoidPtr(), channel),
				suspend.Convert(suspend.Prog.VoidPtr(), elem),
				suspend.Convert(suspend.Prog.VoidPtr(), state),
				p.prog.IntVal(p.prog.SizeOf(elemType), p.prog.Uintptr()),
			)
		},
		resume: func(resume llssa.Builder) llssa.Expr {
			statusHook := p.pkg.NewFunc(coroChanResumeHookV1, coroChanResumeSignature(), llssa.InC)
			status := resume.Call(statusHook.Expr, body.task, resume.Convert(resume.Prog.VoidPtr(), state))
			resume.Store(
				recvOKSlot,
				resume.BinOp(
					token.EQL,
					status,
					resume.Prog.IntVal(coroChanResumeRecvOK, resume.Prog.Uint32()),
				),
			)
			return status
		},
		normal: []uint64{
			coroChanResumeRecvOK,
			coroChanResumeRecvClosed,
		},
		abort:    coroChanResumeTaskAbort,
		shutdown: coroChanResumeShutdown,
	})
	// elem is compiler-owned coroutine-frame storage allocated above. Its
	// address is valid even when the channel element has size zero, so loading
	// it must not synthesize a user nil-dereference helper that was never part
	// of the frozen call graph.
	value := b.LoadKnownNonNil(elem)
	if !instruction.CommaOk {
		return value
	}
	return b.Aggregate(p.type_(instruction.Type(), llssa.InGo), value, b.Load(recvOKSlot))
}

func (p *context) compileCoroChanClose(b llssa.Builder, channel llssa.Expr) {
	p.compileCoroChanCloseWithRecovery(b, channel, nil)
}

// compileCoroChanCloseWithRecovery shares the typed close operation between an
// ordinary source call and a deferred cleanup carrier. A cleanup-time close
// fault replaces the current panic overlay while preserving the drainer's
// normal/RunDefers/cancellation base; a source-time fault enters cleanup from
// the ordinary Recover continuation.
func (p *context) compileCoroChanCloseWithRecovery(
	b llssa.Builder,
	channel llssa.Expr,
	cleanup *coroStaticCleanupState,
) {
	body := p.requireCoroChannelBody(b)
	if cleanup != nil && body.cleanup != cleanup {
		panic("coroutine deferred close does not belong to the active cleanup drainer")
	}
	status := b.CoroChanTryClose(channel)
	nilChannel := b.Func.MakeBlock()
	alreadyClosed := b.Func.MakeBlock()
	normal := b.Func.MakeBlock()
	dispatch := b.Switch(status, body.unsupportedRunDecision)
	dispatch.Case(b.Prog.IntVal(coroChanCloseOK, b.Prog.Uint32()), normal)
	dispatch.Case(b.Prog.IntVal(coroChanCloseNil, b.Prog.Uint32()), nilChannel)
	dispatch.Case(b.Prog.IntVal(coroChanCloseClosed, b.Prog.Uint32()), alreadyClosed)
	dispatch.End(b)

	b.SetBlockEx(nilChannel, llssa.AtEnd, false)
	if cleanup == nil {
		p.compileCoroTerminalFault(b, coroFaultChannelCloseNilV1)
	} else {
		cleanup.replaceFault(p, b, coroFaultChannelCloseNilV1)
	}
	b.SetBlockEx(alreadyClosed, llssa.AtEnd, false)
	if cleanup == nil {
		p.compileCoroTerminalFault(b, coroFaultChannelCloseClosedV1)
	} else {
		cleanup.replaceFault(p, b, coroFaultChannelCloseClosedV1)
	}
	b.SetBlockContinuation(normal)
	body.activate(b)
}

func (p *context) compileCoroChanSelect(b llssa.Builder, states []*llssa.SelectState) llssa.Expr {
	body := p.requireCoroChannelBody(b)
	frame := p.fn.NewBuilder()
	defer frame.Dispose()
	frame.SetBlockEx(p.fn.Block(0), llssa.AtStart, true)
	plan := b.NewCoroSelectInFrame(frame, states)
	attempt := b.CoroChanSelectTry(plan)
	chosenSlot := p.coroFrameAlloca(b.Prog.Int())
	recvOKSlot := p.coroFrameAlloca(b.Prog.Bool())
	b.Store(chosenSlot, b.Extract(attempt, 0))
	b.Store(recvOKSlot, b.Extract(attempt, 1))
	tryOK := b.Extract(attempt, 2)
	body.emitCoroParkOperation(p, b, coroParkOperation{
		shouldSuspend: b.UnOp(token.NOT, tryOK),
		park: func(suspend llssa.Builder) {
			suspend.CoroChanSelectPark(
				plan,
				body.task,
				body.coro.Handle(),
				suspend.Convert(suspend.Prog.VoidPtr(), body.header),
			)
		},
		resume: func(resume llssa.Builder) llssa.Expr {
			result := resume.CoroChanSelectResume(plan, body.task)
			resume.Store(chosenSlot, resume.Extract(result, 0))
			resume.Store(recvOKSlot, resume.Extract(result, 1))
			return resume.Extract(result, 2)
		},
		normal: []uint64{
			coroChanResumeSendOK,
			coroChanResumeRecvOK,
			coroChanResumeRecvClosed,
		},
		faults: []coroParkFaultRoute{
			{status: coroChanResumeSendClosed, kind: coroFaultChannelSendClosedV1},
		},
		abort:    coroChanResumeTaskAbort,
		shutdown: coroChanResumeShutdown,
	})
	return b.CoroChanSelectResult(plan, b.Load(chosenSlot), b.Load(recvOKSlot))
}

func (p *context) compileCoroChanTrySelect(b llssa.Builder, states []*llssa.SelectState) llssa.Expr {
	body := p.requireCoroChannelBody(b)
	plan := b.NewCoroSelect(states)
	attempt := b.CoroChanSelectTry(plan)
	closed := b.Func.MakeBlock()
	normal := b.Func.MakeBlock()
	b.If(b.Extract(attempt, 3), closed, normal)
	b.SetBlockEx(closed, llssa.AtEnd, false)
	p.compileCoroTerminalFault(b, coroFaultChannelSendClosedV1)
	b.SetBlockContinuation(normal)
	body.activate(b)
	return b.CoroChanSelectResult(plan, b.Extract(attempt, 0), b.Extract(attempt, 1))
}
