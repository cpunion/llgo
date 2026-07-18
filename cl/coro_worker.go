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
	coroWorkerParkHookV1   = "__llgo_coro_worker_park_v1"
	coroWorkerResumeHookV1 = "__llgo_coro_worker_resume_v1"
)

const (
	coroWorkerResumeSuccessV1 uint64 = iota + 1
	coroWorkerResumeTaskAbortV1
	coroWorkerResumeShutdownV1
)

func coroWorkerParkSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
		types.NewParam(token.NoPos, nil, "function", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "argc", types.Typ[types.Uint32]),
	}
	for index := 0; index < coroWorkerMaxArgsV1; index++ {
		params = append(params, types.NewParam(token.NoPos, nil, fmt.Sprintf("a%d", index), types.Typ[types.Uintptr]))
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), nil, false)
}

func coroWorkerResumeSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	wordPointer := types.NewPointer(types.Typ[types.Uintptr])
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
		types.NewParam(token.NoPos, nil, "r1", wordPointer),
		types.NewParam(token.NoPos, nil, "r2", wordPointer),
		types.NewParam(token.NoPos, nil, "errno", wordPointer),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func (p *context) requireCoroWorkerBody(b llssa.Builder) *coroBodyContext {
	if p.currentCoro == nil || p.compilation == nil || !p.compilation.EnableCoroWorker || b.Func != p.fn {
		panic("coroutine worker lowering requires an active planned physical coroutine body")
	}
	if p.currentCoro.abi.version < coroPhysicalABIVersionV1 || p.currentCoro.completion == nil ||
		p.currentCoro.finalSuspend == nil || p.currentCoro.unsupportedRunDecision == nil {
		panic("coroutine worker lowering requires the complete PhysicalABIV1 scheduler ABI")
	}
	return p.currentCoro
}

func (p *context) validateCoroWorkerSyscallCodegen(args []ssa.Value, results *types.Tuple) {
	if len(args) < 1 || len(args)-1 > coroWorkerMaxArgsV1 || results == nil || results.Len() != 3 {
		panic("coroutine worker syscall lowering received a non-V1 argument/result shape")
	}
	uintptrLike := func(typ types.Type) bool {
		basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
		return ok && basic.Kind() == types.Uintptr
	}
	for index, argument := range args {
		if argument == nil || !uintptrLike(argument.Type()) {
			panic(fmt.Sprintf("coroutine worker syscall argument %d is not uintptr-shaped", index))
		}
	}
	for index := 0; index < results.Len(); index++ {
		if !uintptrLike(results.At(index).Type()) {
			panic(fmt.Sprintf("coroutine worker syscall result %d is not uintptr-shaped", index))
		}
	}
}

// compileCoroWorkerSyscall lowers one source-style synchronous llgo.syscall
// into the common ForeignWait operation recipe. Argument evaluation happens
// before publication; the fixed pool receives only copied uintptr words and
// the resume hook restores the ordinary three-result tuple.
func (p *context) compileCoroWorkerSyscall(b llssa.Builder, args []ssa.Value, results *types.Tuple) llssa.Expr {
	body := p.requireCoroWorkerBody(b)
	p.validateCoroWorkerSyscallCodegen(args, results)
	compiled := make([]llssa.Expr, len(args))
	for index, argument := range args {
		compiled[index] = p.compileValue(b, argument)
	}

	state := b.Alloc(p.prog.RuntimeType("CoroWorkerParkV1"), false)
	r1 := b.Alloc(p.prog.Uintptr(), false)
	r2 := b.Alloc(p.prog.Uintptr(), false)
	errno := b.Alloc(p.prog.Uintptr(), false)
	zero := p.prog.Zero(p.prog.Uintptr())
	physicalArgs := make([]llssa.Expr, 0, 6+coroWorkerMaxArgsV1)
	physicalArgs = append(physicalArgs,
		body.task,
		body.coro.Handle(),
		b.Convert(b.Prog.VoidPtr(), body.header),
		b.Convert(b.Prog.VoidPtr(), state),
		compiled[0],
		p.prog.IntVal(uint64(len(compiled)-1), p.prog.Uint32()),
	)
	for index := 0; index < coroWorkerMaxArgsV1; index++ {
		if index+1 < len(compiled) {
			physicalArgs = append(physicalArgs, compiled[index+1])
		} else {
			physicalArgs = append(physicalArgs, zero)
		}
	}

	join := body.coro.SuspendCurrentBlockIfWithResumeDispatch(
		b.Prog.BoolVal(true),
		func(suspend llssa.Builder) {
			stateID := body.nextState
			body.nextState++
			body.instructions = 0
			body.publishState(suspend, coroSuspendPark, coroLifecycleSuspended, stateID)
			park := p.pkg.NewFunc(coroWorkerParkHookV1, coroWorkerParkSignature(), llssa.InC)
			suspend.Call(park.Expr, physicalArgs...)
		},
		func(resume llssa.Builder, normal llssa.BasicBlock) {
			resumeHook := p.pkg.NewFunc(coroWorkerResumeHookV1, coroWorkerResumeSignature(), llssa.InC)
			status := resume.Call(
				resumeHook.Expr,
				body.task,
				resume.Convert(resume.Prog.VoidPtr(), state),
				r1,
				r2,
				errno,
			)
			dispatch := resume.Switch(status, body.unsupportedRunDecision)
			dispatch.Case(resume.Prog.IntVal(coroWorkerResumeSuccessV1, resume.Prog.Uint32()), normal)
			dispatch.Case(resume.Prog.IntVal(coroWorkerResumeTaskAbortV1, resume.Prog.Uint32()), body.cancelRunDecision)
			dispatch.Case(resume.Prog.IntVal(coroWorkerResumeShutdownV1, resume.Prog.Uint32()), body.cancelRunDecision)
			dispatch.End(resume)
		},
	)
	b.SetBlock(join)
	body.activate(b)
	return b.Aggregate(p.type_(results, llssa.InGo), b.Load(r1), b.Load(r2), b.Load(errno))
}
