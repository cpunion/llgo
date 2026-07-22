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
	coroTimerParkHookV2           = "__llgo_coro_timer_park_v2"
	coroControlledTimerParkHookV2 = "__llgo_coro_timer_park_controlled_v2"
	coroTimerResumeHookV2         = "__llgo_coro_timer_resume_v2"
)

const (
	coroTimerResumeSuccessV2 uint64 = iota + 1
	coroTimerResumeOperationCanceledV2
	coroTimerResumeTaskAbortV2
	coroTimerResumeShutdownV2
)

func coroTimerParkSignatureV2() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
		types.NewParam(token.NoPos, nil, "delay", types.Typ[types.Int64]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroTimerResumeSignatureV2() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func coroControlledTimerParkSignatureV2() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
		types.NewParam(token.NoPos, nil, "controller", pointer),
		types.NewParam(token.NoPos, nil, "control", types.NewPointer(types.Typ[types.Uint32])),
		types.NewParam(token.NoPos, nil, "expected", types.Typ[types.Uint32]),
		types.NewParam(token.NoPos, nil, "deadline", types.Typ[types.Int64]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func (p *context) requireCoroTimerSleepBody(b llssa.Builder) *coroBodyContext {
	body := p.coroBody()
	if body == nil || p.compilation == nil ||
		p.compilation.CoroFrameRetentionABI != CoroFrameRetentionParkABIV2 || b.Func != p.fn {
		panic("coroutine timer Sleep lowering requires an active planned ParkABIV2 physical coroutine body")
	}
	if body.abi.version < coroPhysicalABIVersionV1 || body.completion == nil ||
		body.finalSuspend == nil || body.unsupportedRunDecision == nil ||
		body.cancelRunDecision == nil {
		panic("coroutine timer Sleep lowering requires the complete PhysicalABIV1 scheduler ABI")
	}
	return body
}

// compileCoroTimerSleep lowers the synchronous source-style time.Sleep
// intrinsic into one compiler-owned TimerParkV2 transaction. The opaque state
// is a typed local so LLVM's coroutine passes spill its fixed layout into the
// stackless frame; source code never owns a frame pointer or source identity.
func (p *context) compileCoroTimerSleep(b llssa.Builder, args []ssa.Value) {
	body := p.requireCoroTimerSleepBody(b)
	if len(args) != 1 {
		panic("llgo.coroTimerSleep requires exactly one int64 argument")
	}
	delay := p.compileValue(b, args[0])
	state := b.Alloc(p.prog.RuntimeType("CoroTimerParkV2"), false)

	body.emitCoroParkOperation(b, coroParkOperation{
		shouldSuspend: b.Prog.BoolVal(true),
		park: func(suspend llssa.Builder) {
			park := p.pkg.NewFunc(coroTimerParkHookV2, coroTimerParkSignatureV2(), llssa.InC)
			suspend.Call(
				park.Expr,
				body.task,
				body.coro.Handle(),
				suspend.Convert(suspend.Prog.VoidPtr(), body.header),
				suspend.Convert(suspend.Prog.VoidPtr(), state),
				delay,
			)
		},
		resume: func(resume llssa.Builder) llssa.Expr {
			resumeHook := p.pkg.NewFunc(coroTimerResumeHookV2, coroTimerResumeSignatureV2(), llssa.InC)
			return resume.Call(
				resumeHook.Expr,
				body.task,
				resume.Convert(resume.Prog.VoidPtr(), state),
			)
		},
		normal:   []uint64{coroTimerResumeSuccessV2},
		abort:    coroTimerResumeTaskAbortV2,
		shutdown: coroTimerResumeShutdownV2,
	})
}

// compileCoroControlledTimerWait lowers the standard Timer manager's
// synchronous-style wait into the same source-aware TimerParkV2 transaction as
// Sleep, augmented only with the logical Stop/Reset identity. Completed and
// operation-canceled are returned after exact lease cleanup and recycle;
// task abort/shutdown enter compiler cleanup and never return to the manager.
func (p *context) compileCoroControlledTimerWait(b llssa.Builder, args []ssa.Value) llssa.Expr {
	body := p.requireCoroTimerSleepBody(b)
	if len(args) != 4 {
		panic("llgo.coroControlledTimerWait requires exactly (unsafe.Pointer, *uint32, uint32, int64) arguments")
	}
	controller := p.compileValue(b, args[0])
	control := p.compileValue(b, args[1])
	expected := p.compileValue(b, args[2])
	deadline := p.compileValue(b, args[3])
	state := b.Alloc(p.prog.RuntimeType("CoroTimerParkV2"), false)
	result := b.Alloc(p.prog.Uint32(), false)

	body.emitCoroParkOperation(b, coroParkOperation{
		shouldSuspend: b.Prog.BoolVal(true),
		park: func(suspend llssa.Builder) {
			park := p.pkg.NewFunc(coroControlledTimerParkHookV2, coroControlledTimerParkSignatureV2(), llssa.InC)
			suspend.Call(
				park.Expr,
				body.task,
				body.coro.Handle(),
				suspend.Convert(suspend.Prog.VoidPtr(), body.header),
				suspend.Convert(suspend.Prog.VoidPtr(), state),
				controller,
				control,
				expected,
				deadline,
			)
		},
		resume: func(resume llssa.Builder) llssa.Expr {
			resumeHook := p.pkg.NewFunc(coroTimerResumeHookV2, coroTimerResumeSignatureV2(), llssa.InC)
			status := resume.Call(
				resumeHook.Expr,
				body.task,
				resume.Convert(resume.Prog.VoidPtr(), state),
			)
			resume.Store(result, status)
			return status
		},
		normal: []uint64{
			coroTimerResumeSuccessV2,
			coroTimerResumeOperationCanceledV2,
		},
		abort:    coroTimerResumeTaskAbortV2,
		shutdown: coroTimerResumeShutdownV2,
	})
	// The timer table deliberately owns only a scalar controller key. This
	// post-resume use makes the address-shaped owner and its interior control
	// pointer live across llvm.coro.suspend until source retirement completes.
	b.KeepAlive(controller, control)
	return b.Load(result)
}
