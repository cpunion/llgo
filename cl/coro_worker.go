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
	body := p.coroBody()
	if body == nil || p.compilation == nil || !p.compilation.EnableCoroWorker || b.Func != p.fn {
		panic("coroutine worker lowering requires an active planned physical coroutine body")
	}
	if body.abi.version < coroPhysicalABIVersionV1 || body.completion == nil ||
		body.finalSuspend == nil || body.unsupportedRunDecision == nil {
		panic("coroutine worker lowering requires the complete PhysicalABIV1 scheduler ABI")
	}
	return body
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

type coroWorkerWordResultV1 struct {
	r1    llssa.Expr
	r2    llssa.Expr
	errno llssa.Expr
}

// compileCoroWorkerWordCall is the one physical ForeignWait transaction used
// by both llgo.syscall and exact ordinary C-call thunks. function always names
// a uniform uintptr (...uintptr) thunk whose arity is len(args); typed foreign
// declarations are never called through this ABI directly.
func (p *context) compileCoroWorkerWordCall(
	b llssa.Builder,
	function llssa.Expr,
	args []llssa.Expr,
	keepaliveSlots []llssa.Expr,
) coroWorkerWordResultV1 {
	body := p.requireCoroWorkerBody(b)
	if function.IsNil() || len(args) > coroWorkerMaxArgsV1 {
		panic("coroutine worker word call received an invalid function or argument count")
	}
	word := p.prog.Uintptr()
	if !types.Identical(function.RawType(), word.RawType()) {
		panic("coroutine worker word call function is not uintptr-shaped")
	}
	for index, argument := range args {
		if argument.IsNil() || !types.Identical(argument.RawType(), word.RawType()) {
			panic(fmt.Sprintf("coroutine worker word call argument %d is not uintptr-shaped", index))
		}
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
		function,
		p.prog.IntVal(uint64(len(args)), p.prog.Uint32()),
	)
	for index := 0; index < coroWorkerMaxArgsV1; index++ {
		if index < len(args) {
			physicalArgs = append(physicalArgs, args[index])
		} else {
			physicalArgs = append(physicalArgs, zero)
		}
	}

	body.emitCoroParkOperation(p, b, coroParkOperation{
		shouldSuspend: b.Prog.BoolVal(true),
		park: func(suspend llssa.Builder) {
			park := p.pkg.NewFunc(coroWorkerParkHookV1, coroWorkerParkSignature(), llssa.InC)
			suspend.Call(park.Expr, physicalArgs...)
		},
		resume: func(resume llssa.Builder) llssa.Expr {
			resumeHook := p.pkg.NewFunc(coroWorkerResumeHookV1, coroWorkerResumeSignature(), llssa.InC)
			return resume.Call(
				resumeHook.Expr,
				body.task,
				resume.Convert(resume.Prog.VoidPtr(), state),
				r1,
				r2,
				errno,
			)
		},
		normal:   []uint64{coroWorkerResumeSuccessV1},
		abort:    coroWorkerResumeTaskAbortV1,
		shutdown: coroWorkerResumeShutdownV1,
	})
	// The worker queue deliberately contains only copied uintptr words. Keep
	// every independently proved typed owner live until the physical completion
	// acknowledgement has selected this normal resume path; llvm.fake.use emits
	// no machine code but forces CoroSplit to retain the values in the frame.
	p.emitCoroKeepaliveSlots(b, keepaliveSlots)
	return coroWorkerWordResultV1{r1: b.Load(r1), r2: b.Load(r2), errno: b.Load(errno)}
}

// compileCoroCallKeepaliveSlots spills the exact typed owners which the
// frame-retention proof binds to one suspending call into ramp-entry slots.
// Compiler-owned resume/cancellation dispatch can enter a continuation through
// an edge on which the source SSA value does not dominate. Reloading the slot
// in that continuation preserves both valid LLVM SSA and the typed owner until
// the physical completion/retirement boundary.
func (p *context) compileCoroCallKeepaliveSlots(b llssa.Builder, call *ssa.Call) []llssa.Expr {
	body := p.coroBody()
	if body == nil || body.frameRetention == nil || call == nil {
		return nil
	}
	sources := body.frameRetention.exactCallKeepaliveSources(call)
	slots := make([]llssa.Expr, len(sources))
	for index, source := range sources {
		value := p.compileValue(b, source)
		if coroFrameRetentionIntegerLike(source.Type()) {
			// uintptr transports retain exact pointer provenance only under the
			// selected non-moving conservative/no-GC profile. Re-type the copied
			// word as a pointer in the compiler-owned keepalive slot so the frame
			// carries an address-shaped root rather than an optimizer-only integer.
			value = b.Convert(p.prog.VoidPtr(), value)
		}
		slots[index] = p.coroFrameAlloc(value.Type)
		b.Store(slots[index], value)
	}
	return slots
}

func (p *context) emitCoroKeepaliveSlots(b llssa.Builder, slots []llssa.Expr) {
	values := make([]llssa.Expr, len(slots))
	for index, slot := range slots {
		if slot.IsNil() {
			panic("coroutine keepalive contains a nil frame slot")
		}
		values[index] = b.Load(slot)
	}
	b.KeepAlive(values...)
}

func (p *context) coroWorkerOrdinaryCall(common *ssa.CallCommon) *ssa.Call {
	if p == nil || p.goFn == nil || common == nil {
		return nil
	}
	for _, block := range p.goFn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && &call.Call == common {
				return call
			}
		}
	}
	return nil
}

// compileCoroWorkerSyscall lowers one source-style synchronous llgo.syscall
// family operation into the common ForeignWait recipe. All conventions share
// one park/resume CFG; only the final errno predicate differs. Argument
// evaluation happens before publication, and the fixed pool receives only
// copied uintptr words.
func (p *context) compileCoroWorkerSyscall(
	b llssa.Builder,
	call *ssa.CallCommon,
	args []ssa.Value,
	results *types.Tuple,
	convention syscallFailureConvention,
) llssa.Expr {
	p.validateCoroWorkerSyscallCodegen(args, results)
	direct := p.coroWorkerOrdinaryCall(call)
	if err := validateCoroWorkerSyscallCall(p.compilation.CoroPlan, p.compilation.EmissionUniverse, direct); err != nil {
		panic(fmt.Errorf("coroutine worker syscall lowering: %w", err))
	}
	compiled := make([]llssa.Expr, len(args))
	for index, argument := range args {
		compiled[index] = p.compileValue(b, argument)
	}
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, direct)
	result := p.compileCoroWorkerWordCall(b, compiled[0], compiled[1:], keepaliveSlots)
	errnoValue := p.filterSyscallErrno(b, result.r1, result.errno, convention)
	return b.Aggregate(p.type_(results, llssa.InGo), result.r1, result.r2, errnoValue)
}
