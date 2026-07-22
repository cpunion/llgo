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

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func coroInterfaceDispatchNeedsAwait(dispatch *coroInterfaceDispatchPlan) bool {
	if dispatch == nil {
		return false
	}
	for _, candidate := range dispatch.candidates {
		if candidate.plan.Emission == coro.EmitCoroutine {
			return true
		}
	}
	return false
}

// compileCoroInterfaceDispatchAwait lowers a closed interface invoke into
// one receiver-aware dispatch chain. The ordinary itab method word is used
// only as the exact target discriminator: an async itab slot currently names
// a $coro root whose physical signature cannot be called as a legacy method.
// Each selected target is therefore invoked through its planned primary entry;
// coroutine candidates use the same structured child-await transaction as a
// static synchronous-style Go call, while plain candidates remain direct.
//
// This is the closed-world bridge to the canonical {descriptor,env} ABI. Once
// itab emission stores that descriptor directly, the candidate chain reduces
// to one validated descriptor entry load without changing scheduler semantics.
func (p *context) compileCoroInterfaceDispatchAwait(
	b llssa.Builder, call *ssa.Call, instructionPlan coroPhysicalInstructionPlan,
) llssa.Expr {
	if !p.hasCoroPhysicalBody() || call == nil || call.Common() == nil || !call.Common().IsInvoke() ||
		instructionPlan.control != coroPhysicalControlClosedInterfaceAwait {
		panic("coroutine interface dispatch escaped its frozen physical control recipe")
	}
	dispatch := instructionPlan.controlInterface
	if dispatch == nil || !coroInterfaceDispatchNeedsAwait(dispatch) {
		panic("coroutine interface dispatch has an incomplete frozen physical control recipe")
	}
	p.recordCallerLocationForCall(b, &call.Call)
	p.emitPCLineLabel(b, call.Pos())
	// Preserve source evaluation order and the existing nil-interface check.
	intf := p.compileValue(b, dispatch.receiver)
	methodValue := b.Imethod(intf, dispatch.method)
	methodWord := b.Convert(p.prog.VoidPtr(), b.Field(methodValue, 0))
	env := b.Field(methodValue, 1)
	args := p.compileValues(b, call.Call.Args, fnNormal)
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)

	resultCount := dispatch.sourceCallSignature.Results().Len()
	var resultSlot llssa.Expr
	if resultCount != 0 {
		resultSlot = p.coroFrameAlloca(p.type_(call.Type(), llssa.InGo))
	}
	join := p.fn.MakeBlock()
	next := p.fn.MakeBlock()
	b.Jump(next)
	for _, candidate := range dispatch.candidates {
		b.SetBlockEx(next, llssa.AtEnd, false)
		selected := p.fn.MakeBlock()
		next = p.fn.MakeBlock()
		methodEntry, _, methodKind := p.compileFunction(candidate.methodEntry)
		if methodKind != goFunc || methodEntry == nil {
			panic(fmt.Sprintf("coroutine interface dispatch: target %q has no exact itab method entry", candidate.id))
		}
		entry, _, kind := p.compileFunction(candidate.function)
		if kind != goFunc || entry == nil {
			panic(fmt.Sprintf("coroutine interface dispatch: target %q has no Go primary entry", candidate.id))
		}
		entryWord := b.Convert(p.prog.VoidPtr(), methodEntry.Expr)
		b.If(b.BinOp(token.EQL, methodWord, entryWord), selected, next)

		b.SetBlockEx(selected, llssa.AtEnd, false)
		receiverType := p.type_(candidate.receiver, llssa.InGo)
		var dynamicReceiver llssa.Expr
		if _, pointer := types.Unalias(candidate.receiver).Underlying().(*types.Pointer); pointer {
			dynamicReceiver = b.Convert(receiverType, env)
		} else {
			receiverAddress := b.Convert(p.prog.Pointer(receiverType), env)
			p.compileCoroImplicitNilAccessGuard(b, receiverAddress)
			dynamicReceiver = b.LoadKnownNonNil(receiverAddress)
		}
		receiver := dynamicReceiver
		if !types.Identical(candidate.receiver, candidate.targetReceiver) {
			pointer, ok := types.Unalias(candidate.receiver).Underlying().(*types.Pointer)
			if !ok || !types.Identical(pointer.Elem(), candidate.targetReceiver) {
				panic(fmt.Sprintf(
					"coroutine interface dispatch: target %q cannot adapt dynamic receiver %s to declared receiver %s",
					candidate.id, candidate.receiver, candidate.targetReceiver,
				))
			}
			p.compileCoroImplicitNilAccessGuard(b, dynamicReceiver)
			receiver = b.LoadKnownNonNil(dynamicReceiver)
		}
		physical := make([]llssa.Expr, 0, len(args)+1)
		physical = append(physical, receiver)
		physical = append(physical, args...)
		var result llssa.Expr
		switch candidate.plan.Emission {
		case coro.EmitCoroutine:
			result = p.compileCoroTargetAwaitWithKeepalive(b, candidate.function, physical, keepaliveSlots)
		case coro.EmitPlain:
			result = b.Call(entry.Expr, physical...)
		default:
			panic(fmt.Sprintf("coroutine interface dispatch: target %q has emission %s", candidate.id, candidate.plan.Emission))
		}
		if resultCount != 0 {
			b.Store(resultSlot, result)
		}
		b.Jump(join)
	}

	// A closed plan and the frozen itab method table must agree exactly. Nil
	// interfaces already took the ordinary panic edge in Imethod; any non-nil
	// unmatched word is corrupted representation state, not an open fallback.
	b.SetBlockEx(next, llssa.AtEnd, false)
	trap := p.pkg.NewFunc(
		"llvm.trap",
		types.NewSignatureType(nil, nil, nil, nil, nil, false),
		llssa.InC,
	)
	b.Call(trap.Expr)
	b.Unreachable()

	b.SetBlockContinuation(join)
	if resultCount == 0 {
		return llssa.Nil
	}
	return b.LoadKnownNonNil(resultSlot)
}
