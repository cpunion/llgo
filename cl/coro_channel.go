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
	coroChanSendParkHookV1        = "__llgo_coro_chan_send_park_v1"
	coroChanRecvParkHookV1        = "__llgo_coro_chan_recv_park_v1"
	coroChanResumeHookV1          = "__llgo_coro_chan_resume_v1"
	coroChanSendClosedPanicHookV1 = "__llgo_coro_chan_send_closed_panic_v1"
)

const (
	coroChanResumeSendOK uint64 = iota + 1
	coroChanResumeRecvOK
	coroChanResumeRecvClosed
	coroChanResumeSendClosed
	coroChanResumeTaskAbort
	coroChanResumeShutdown
)

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

func coroChanSendClosedPanicSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func (p *context) requireCoroChannelBody(b llssa.Builder) *coroBodyContext {
	if p.currentCoro == nil || p.compilation == nil || !p.compilation.EnableCoroChannel || b.Func != p.fn {
		panic("coroutine channel lowering requires an active planned physical coroutine body")
	}
	if p.currentCoro.abi.version < coroPhysicalABIVersionV1 || p.currentCoro.completion == nil ||
		p.currentCoro.finalSuspend == nil || p.currentCoro.unsupportedRunDecision == nil {
		panic("coroutine channel lowering requires the complete PhysicalABIV1 scheduler ABI")
	}
	return p.currentCoro
}

func (p *context) newCoroChannelStorage(b llssa.Builder, elemType llssa.Type) (elem, state llssa.Expr) {
	elem = b.Alloc(elemType, false)
	state = b.Alloc(p.prog.RuntimeType("CoroChanParkV1"), false)
	return
}

func (p *context) compileCoroChanSend(b llssa.Builder, channel, value llssa.Expr) {
	body := p.requireCoroChannelBody(b)
	elem, state := p.newCoroChannelStorage(b, value.Type)
	b.Store(elem, value)
	ready := b.CoroChanTrySend(channel, elem)
	closed := b.Func.MakeBlock()
	join := body.coro.SuspendCurrentBlockIfWithResumeDispatch(
		b.UnOp(token.NOT, ready),
		func(suspend llssa.Builder) {
			stateID := body.nextState
			body.nextState++
			body.instructions = 0
			body.publishState(suspend, coroSuspendPark, coroLifecycleSuspended, stateID)
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
		func(resume llssa.Builder, normal llssa.BasicBlock) {
			statusHook := p.pkg.NewFunc(coroChanResumeHookV1, coroChanResumeSignature(), llssa.InC)
			status := resume.Call(statusHook.Expr, body.task, resume.Convert(resume.Prog.VoidPtr(), state))
			dispatch := resume.Switch(status, body.unsupportedRunDecision)
			dispatch.Case(resume.Prog.IntVal(coroChanResumeSendOK, resume.Prog.Uint32()), normal)
			dispatch.Case(resume.Prog.IntVal(coroChanResumeSendClosed, resume.Prog.Uint32()), closed)
			dispatch.Case(resume.Prog.IntVal(coroChanResumeTaskAbort, resume.Prog.Uint32()), body.cancelRunDecision)
			dispatch.Case(resume.Prog.IntVal(coroChanResumeShutdown, resume.Prog.Uint32()), body.cancelRunDecision)
			dispatch.End(resume)
		},
	)
	b.SetBlock(closed)
	body.publishState(b, coroSuspendPanic, coroLifecycleFinalSuspended, body.terminalStateID())
	panicHook := p.pkg.NewFunc(coroChanSendClosedPanicHookV1, coroChanSendClosedPanicSignature(), llssa.InC)
	b.Call(
		panicHook.Expr,
		body.task,
		body.coro.Handle(),
		b.Convert(b.Prog.VoidPtr(), body.header),
	)
	b.Jump(body.finalSuspend)
	b.SetBlock(join)
	body.activate(b)
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
	recvOKSlot := b.Alloc(p.prog.Bool(), false)
	b.Store(recvOKSlot, recvOK)
	recvSuccess := b.Func.MakeBlock()
	recvClosed := b.Func.MakeBlock()
	join := body.coro.SuspendCurrentBlockIfWithResumeDispatch(
		b.UnOp(token.NOT, tryOK),
		func(suspend llssa.Builder) {
			stateID := body.nextState
			body.nextState++
			body.instructions = 0
			body.publishState(suspend, coroSuspendPark, coroLifecycleSuspended, stateID)
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
		func(resume llssa.Builder, normal llssa.BasicBlock) {
			statusHook := p.pkg.NewFunc(coroChanResumeHookV1, coroChanResumeSignature(), llssa.InC)
			status := resume.Call(statusHook.Expr, body.task, resume.Convert(resume.Prog.VoidPtr(), state))
			dispatch := resume.Switch(status, body.unsupportedRunDecision)
			dispatch.Case(resume.Prog.IntVal(coroChanResumeRecvOK, resume.Prog.Uint32()), recvSuccess)
			dispatch.Case(resume.Prog.IntVal(coroChanResumeRecvClosed, resume.Prog.Uint32()), recvClosed)
			dispatch.Case(resume.Prog.IntVal(coroChanResumeTaskAbort, resume.Prog.Uint32()), body.cancelRunDecision)
			dispatch.Case(resume.Prog.IntVal(coroChanResumeShutdown, resume.Prog.Uint32()), body.cancelRunDecision)
			dispatch.End(resume)
		},
	)
	b.SetBlock(recvSuccess)
	b.Store(recvOKSlot, b.Prog.BoolVal(true))
	b.Jump(join)
	b.SetBlock(recvClosed)
	b.Store(recvOKSlot, b.Prog.BoolVal(false))
	b.Jump(join)
	b.SetBlock(join)
	body.activate(b)
	value := b.Load(elem)
	if !instruction.CommaOk {
		return value
	}
	return b.Aggregate(p.type_(instruction.Type(), llssa.InGo), value, b.Load(recvOKSlot))
}
