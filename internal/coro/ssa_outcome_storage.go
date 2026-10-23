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

package coro

import (
	"github.com/xgo-dev/llgo/internal/abi"
	"golang.org/x/tools/go/ssa"
)

const maxSSAOutcomeLocalStorageBytes = abi.MaxStackVarSize

// ssaOutcomeLocalStorageBudgets limits the projected local storage retained by
// defined synchronous outcome call chains. Calls retain their owner's locals,
// so the bound is local storage plus the maximum callee bound, rather than the
// maximum individual variable size. Sequential callees do not retain each
// other's frames. All branch/descriptor candidates contribute to the maximum.
//
// This consumes the frozen ProgramIR projection, never rescans source SSA, and
// does not certify backend spills or foreign/imported stack use. Cycles and
// arithmetic overflow fail closed. Normal coroutine fallback remains available.
func ssaOutcomeLocalStorageBudgets(
	byID map[FunctionID]*ssa.Function,
	facts map[*ssa.Function]SSAFunctionBodyFacts,
	calls map[ssa.CallInstruction]SSACallPlan,
	lowered map[*ssa.Function][]SSALoweredCall,
) map[FunctionID]uint64 {
	ids := make(map[*ssa.Function]FunctionID, len(byID))
	for id, function := range byID {
		ids[function] = id
	}
	const exceeded = maxSSAOutcomeLocalStorageBytes + 1
	result := make(map[FunctionID]uint64, len(byID))
	visiting := make(map[FunctionID]bool)
	var visit func(FunctionID) uint64
	visit = func(id FunctionID) uint64 {
		if cost, found := result[id]; found {
			return cost
		}
		function := byID[id]
		body, defined := facts[function]
		if !defined || !(body.OutcomePlainLeaf || body.OutcomePlainDAG || body.StaticOutcomeLocal) {
			return 0
		}
		if visiting[id] {
			return exceeded
		}
		if body.OutcomeLocalStorageBytes > maxSSAOutcomeLocalStorageBytes {
			result[id] = exceeded
			return exceeded
		}
		visiting[id] = true
		var child uint64
		include := func(target FunctionID) {
			if cost := visit(target); cost > child {
				child = cost
			}
		}
		if body.AtomicPath != nil {
			for _, block := range body.AtomicPath.Blocks {
				for _, occurrence := range block.Calls {
					call, found := calls[occurrence.Instruction]
					if !found || call.Kind != CallDirect || call.Transport != ManagedTransport || call.RawPlain {
						continue
					}
					for _, target := range call.Targets {
						include(target)
					}
				}
			}
		}
		for _, call := range lowered[function] {
			if call.ExplicitStatusElided || call.RawPlain {
				continue
			}
			if target, found := call.targetID(ids); found {
				include(target)
			}
		}
		delete(visiting, id)
		cost := exceeded
		if child <= maxSSAOutcomeLocalStorageBytes-body.OutcomeLocalStorageBytes {
			cost = body.OutcomeLocalStorageBytes + child
		}
		result[id] = cost
		return cost
	}
	for id := range byID {
		visit(id)
	}
	return result
}
