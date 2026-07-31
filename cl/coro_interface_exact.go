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
	"go/types"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// coroExactInterfaceMakeInvoke recognizes the deliberately narrow producer
// shape whose managed representation can disappear completely: one concrete
// MakeInterface value consumed only as the receiver of one ordinary invoke.
// Plain/native variants retain normal interface construction.
func coroExactInterfaceMakeInvoke(box *ssa.MakeInterface) (*ssa.Call, bool) {
	if box == nil || box.X == nil || box.X.Type() == nil {
		return nil, false
	}
	if _, nestedInterface := types.Unalias(box.X.Type()).Underlying().(*types.Interface); nestedInterface {
		return nil, false
	}
	referrers, _ := nonDebugReferrers(box)
	if len(referrers) != 1 {
		return nil, false
	}
	call, ok := referrers[0].(*ssa.Call)
	if !ok || call == nil || call.Common() == nil ||
		call.Common().Value != box || !call.Common().IsInvoke() ||
		call.Common().StaticCallee() != nil || call.Common().Method == nil {
		return nil, false
	}
	return call, true
}

func coroPlannedExactInterfaceMakeElision(
	plan *coro.SSAPlan,
	box *ssa.MakeInterface,
) (bool, error) {
	call, structural := coroExactInterfaceMakeInvoke(box)
	if !structural || plan == nil {
		return false, nil
	}
	receiver, _, targetPlan, exact, err := plan.ResolveExactInterfaceCall(call)
	if err != nil {
		return false, err
	}
	if !exact || receiver != box.X {
		return false, nil
	}
	return coroExactInterfaceTargetDirectPlain(targetPlan) ||
		coroExactInterfaceTargetDirectAwait(targetPlan), nil
}

// compileCoroExactInterfaceCall emits an occurrence-local devirtualized
// interface invoke. Whole-program analysis proved that the interface payload
// is the exact concrete SSA value recorded in the frozen physical plan and
// that its sole target has a direct, no-unwind plain primary.
//
// This does not change the shared itab representation. Other occurrences,
// including a typed-nil *T value selecting a generated wrapper, retain their
// independently planned interface dispatch and panic behavior.
func (p *context) compileCoroExactInterfaceCall(
	b llssa.Builder,
	call *ssa.Call,
	instructionPlan coroPhysicalInstructionPlan,
) llssa.Expr {
	if !p.hasCoroPhysicalBody() || call == nil ||
		instructionPlan.control != coroPhysicalControlExactInterfaceCall ||
		instructionPlan.controlTarget == nil ||
		instructionPlan.controlTargetID == "" ||
		instructionPlan.controlReceiver == nil {
		panic("exact interface invocation escaped its frozen physical control recipe")
	}
	receiver, target, targetPlan, exact, err :=
		p.compilation.CoroPlan.ResolveExactInterfaceCall(call)
	if err != nil {
		panic(err)
	}
	if !exact || receiver != instructionPlan.controlReceiver ||
		target != instructionPlan.controlTarget ||
		targetPlan.ID != instructionPlan.controlTargetID ||
		!coroExactInterfaceTargetDirectPlain(targetPlan) {
		panic("exact interface invocation disagrees with its frozen physical control recipe")
	}
	function, _, kind := p.compileManagedFunction(target)
	if function == nil || kind != goFunc {
		panic(fmt.Errorf(
			"exact interface target %q did not resolve to one plain Go entry",
			targetPlan.ID,
		))
	}

	p.emitPCLineLabel(b, call.Pos())
	arguments := make([]llssa.Expr, 0, len(call.Common().Args)+1)
	// Preserve receiver-before-arguments evaluation even though SSA usually
	// makes all operands available before the invoke instruction.
	arguments = append(arguments, p.compileValue(b, receiver))
	arguments = append(arguments, p.compileValues(b, call.Common().Args, fnNormal)...)
	return b.Call(function.Expr, arguments...)
}

func (p *context) compileCoroExactInterfaceAwait(
	b llssa.Builder,
	call *ssa.Call,
	instructionPlan coroPhysicalInstructionPlan,
) llssa.Expr {
	if !p.hasCoroPhysicalBody() || call == nil ||
		instructionPlan.control != coroPhysicalControlExactInterfaceAwait ||
		instructionPlan.controlTarget == nil ||
		instructionPlan.controlTargetID == "" ||
		instructionPlan.controlReceiver == nil {
		panic("exact interface await escaped its frozen physical control recipe")
	}
	receiver, target, targetPlan, exact, err :=
		p.compilation.CoroPlan.ResolveExactInterfaceCall(call)
	if err != nil {
		panic(err)
	}
	if !exact || receiver != instructionPlan.controlReceiver ||
		target != instructionPlan.controlTarget ||
		targetPlan.ID != instructionPlan.controlTargetID ||
		!coroExactInterfaceTargetDirectAwait(targetPlan) {
		panic("exact interface await disagrees with its frozen physical control recipe")
	}

	p.emitPCLineLabel(b, call.Pos())
	arguments := make([]llssa.Expr, 0, len(call.Common().Args)+1)
	arguments = append(arguments, p.compileValue(b, receiver))
	arguments = append(arguments, p.compileValues(b, call.Common().Args, fnNormal)...)
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
	result := p.compileCoroTargetAwaitWithContextAndRecoveryResult(
		b, target, llssa.Nil, arguments, nil, keepaliveSlots,
	)
	p.recordCoroValueAddress(call, result.address)
	return result.value
}
