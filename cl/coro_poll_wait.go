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
	"go/token"
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroPollParkHookV2   = "__llgo_coro_poll_park_v2"
	coroPollResumeHookV2 = "__llgo_coro_poll_resume_v2"
)

const (
	coroPollResumeReadyV2 uint64 = iota + 1
	coroPollResumeClosingV2
	coroPollResumeTimeoutV2
	coroPollResumeOperationCanceledV2
	coroPollResumeTaskAbortV2
	coroPollResumeShutdownV2
)

func coroPollParkSignatureV2() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
		types.NewParam(token.NoPos, nil, "context", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "fd", types.Typ[types.Int32]),
		types.NewParam(token.NoPos, nil, "interest", types.Typ[types.Uint32]),
		types.NewParam(token.NoPos, nil, "deadline", types.Typ[types.Int64]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroPollResumeSignatureV2() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func (p *context) requireCoroPollWaitBody(b llssa.Builder) *coroBodyContext {
	if p.currentCoro == nil || p.compilation == nil ||
		p.compilation.CoroFrameRetentionABI != CoroFrameRetentionParkABIV2 || b.Func != p.fn {
		panic("coroutine poll wait lowering requires an active planned ParkABIV2 physical coroutine body")
	}
	if p.currentCoro.abi.version < coroPhysicalABIVersionV1 || p.currentCoro.completion == nil ||
		p.currentCoro.finalSuspend == nil || p.currentCoro.unsupportedRunDecision == nil ||
		p.currentCoro.cancelRunDecision == nil {
		panic("coroutine poll wait lowering requires the complete PhysicalABIV1 scheduler ABI")
	}
	return p.currentCoro
}

// compileCoroPollWait lowers one synchronous source-style descriptor wait into
// a compiler-owned PollParkV2 transaction. Only the copied scalar descriptor
// identity crosses the stack cut. The opaque source, WaitSet, lease, and
// cancellation state stay in fixed typed storage that LLVM spills into the
// stackless coroutine frame.
func (p *context) compileCoroPollWait(b llssa.Builder, args []ssa.Value) llssa.Expr {
	body := p.requireCoroPollWaitBody(b)
	if len(args) != 4 {
		panic("llgo.coroPollWait requires exactly (uintptr, int32, uint32, int64) arguments")
	}
	context := p.compileValue(b, args[0])
	fd := p.compileValue(b, args[1])
	interest := p.compileValue(b, args[2])
	deadline := p.compileValue(b, args[3])
	state := b.Alloc(p.prog.RuntimeType("CoroPollParkV2"), false)
	result := b.Alloc(p.prog.Uint32(), false)

	join := body.coro.SuspendCurrentBlockIfWithResumeDispatch(
		b.Prog.BoolVal(true),
		func(suspend llssa.Builder) {
			stateID := body.nextState
			body.nextState++
			body.instructions = 0
			body.publishState(suspend, coroSuspendPark, coroLifecycleSuspended, stateID)
			park := p.pkg.NewFunc(coroPollParkHookV2, coroPollParkSignatureV2(), llssa.InC)
			suspend.Call(
				park.Expr,
				body.task,
				body.coro.Handle(),
				suspend.Convert(suspend.Prog.VoidPtr(), body.header),
				suspend.Convert(suspend.Prog.VoidPtr(), state),
				context,
				fd,
				interest,
				deadline,
			)
		},
		func(resume llssa.Builder, normal llssa.BasicBlock) {
			resumeHook := p.pkg.NewFunc(coroPollResumeHookV2, coroPollResumeSignatureV2(), llssa.InC)
			status := resume.Call(
				resumeHook.Expr,
				body.task,
				resume.Convert(resume.Prog.VoidPtr(), state),
			)
			resume.Store(result, status)
			abort, shutdown := body.cancellationRunDecisionTargets(resume)
			dispatch := resume.Switch(status, body.unsupportedRunDecision)
			dispatch.Case(resume.Prog.IntVal(coroPollResumeReadyV2, resume.Prog.Uint32()), normal)
			dispatch.Case(resume.Prog.IntVal(coroPollResumeClosingV2, resume.Prog.Uint32()), normal)
			dispatch.Case(resume.Prog.IntVal(coroPollResumeTimeoutV2, resume.Prog.Uint32()), normal)
			dispatch.Case(resume.Prog.IntVal(coroPollResumeTaskAbortV2, resume.Prog.Uint32()), abort)
			dispatch.Case(resume.Prog.IntVal(coroPollResumeShutdownV2, resume.Prog.Uint32()), shutdown)
			dispatch.End(resume)
		},
	)
	b.SetBlock(join)
	body.activate(b)
	return b.Load(result)
}
