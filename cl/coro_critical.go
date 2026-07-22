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

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

const coroCriticalDepthLimit = ^uint32(0) >> 2

// coroCriticalProof is the authoritative function-local C0 region proof used
// by both preflight and physical emission. Every map is keyed by the exact
// frozen SSA object; no source name or regenerated block identity participates
// in lowering.
type coroCriticalProof struct {
	roles       map[*ssa.Call]coroCriticalCallRole
	entryDepth  map[*ssa.BasicBlock]uint32
	beforeDepth map[ssa.Instruction]uint32
	afterDepth  map[ssa.Instruction]uint32
}

// proveCoroCriticalRegions proves structured preemption masking without
// introducing a second executable IR. C0 is intentionally strict: a masked
// region is bounded, helper-free, path-balanced, and contains no ordinary call
// or stack cut. Wider scheduler-owned transactions must be represented by an
// operation source, not smuggled through this mask.
func proveCoroCriticalRegions(
	universe *EmissionUniverse,
	plan *coro.SSAPlan,
	audit *coroPhysicalPureSSAAudit,
) (*coroCriticalProof, error) {
	if audit == nil || audit.fn == nil || len(audit.fn.Blocks) == 0 {
		return nil, nil
	}
	fn := audit.fn
	proof := &coroCriticalProof{
		roles:       make(map[*ssa.Call]coroCriticalCallRole),
		entryDepth:  make(map[*ssa.BasicBlock]uint32, len(fn.Blocks)),
		beforeDepth: make(map[ssa.Instruction]uint32),
		afterDepth:  make(map[ssa.Instruction]uint32),
	}
	if universe == nil {
		return nil, nil
	}

	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			role, critical, err := universe.coroCriticalCallSite(call)
			if err != nil {
				return nil, coroCriticalInstructionError(fn, instruction, err.Error())
			}
			if !critical {
				continue
			}
			if plan == nil || !plan.ElidesCall(call) {
				return nil, coroCriticalInstructionError(fn, instruction, "critical marker is not frozen as an elided compiler intrinsic")
			}
			if _, retained := plan.CallPlan(call); retained {
				return nil, coroCriticalInstructionError(fn, instruction, "critical marker retained an ordinary managed CallPlan")
			}
			proof.roles[call] = role
		}
	}
	if len(proof.roles) == 0 {
		return nil, nil
	}
	if plan == nil || audit.ctx == nil {
		return nil, fmt.Errorf("function %q critical regions require a frozen plan and lowering context", fn.String())
	}

	reachable := coroCriticalReachableBlocks(fn)
	for call := range proof.roles {
		if !reachable[call.Block()] {
			return nil, coroCriticalInstructionError(fn, call, "critical marker is unreachable")
		}
	}

	outDepth := make(map[*ssa.BasicBlock]uint32, len(fn.Blocks))
	entry := fn.Blocks[0]
	proof.entryDepth[entry] = 0
	queued := map[*ssa.BasicBlock]bool{entry: true}
	queue := []*ssa.BasicBlock{entry}
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		depth := proof.entryDepth[block]
		for _, instruction := range block.Instrs {
			proof.beforeDepth[instruction] = depth
			if call, ok := instruction.(*ssa.Call); ok {
				switch proof.roles[call] {
				case coroCriticalCallEnter:
					if depth == coroCriticalDepthLimit {
						return nil, coroCriticalInstructionError(fn, instruction, "critical nesting overflows the packed runtime depth")
					}
					depth++
				case coroCriticalCallExit:
					if depth == 0 {
						return nil, coroCriticalInstructionError(fn, instruction, "critical exit underflows depth zero")
					}
					depth--
				}
			}
			proof.afterDepth[instruction] = depth
			if depth != 0 {
				switch instruction.(type) {
				case *ssa.Return, *ssa.Panic:
					return nil, coroCriticalInstructionError(fn, instruction, "function exit or panic is forbidden while preemption is masked")
				}
			}
		}
		outDepth[block] = depth
		if len(block.Succs) == 0 && depth != 0 {
			return nil, fmt.Errorf("function %q block %d terminates with unbalanced critical depth %d", fn.String(), block.Index, depth)
		}
		for _, successor := range block.Succs {
			if successor == nil || !reachable[successor] {
				return nil, fmt.Errorf("function %q block %d has an invalid critical CFG successor", fn.String(), block.Index)
			}
			previous, seen := proof.entryDepth[successor]
			if seen && previous != depth {
				return nil, fmt.Errorf(
					"function %q critical depth join mismatch at block %d: %d versus %d",
					fn.String(), successor.Index, previous, depth,
				)
			}
			if !seen {
				proof.entryDepth[successor] = depth
			}
			if !queued[successor] {
				queued[successor] = true
				queue = append(queue, successor)
			}
		}
	}

	// LLVM emission may retain structurally unreachable source blocks. They are
	// outside every critical region, but still receive total depth maps so
	// codegen never guesses a missing proof entry.
	for _, block := range fn.Blocks {
		if reachable[block] {
			continue
		}
		proof.entryDepth[block] = 0
		outDepth[block] = 0
		for _, instruction := range block.Instrs {
			proof.beforeDepth[instruction] = 0
			proof.afterDepth[instruction] = 0
		}
	}

	if err := validateCoroCriticalMaskedDAG(fn, proof, outDepth, reachable); err != nil {
		return nil, err
	}
	for _, block := range fn.Blocks {
		if !reachable[block] {
			continue
		}
		for _, instruction := range block.Instrs {
			before, after := proof.beforeDepth[instruction], proof.afterDepth[instruction]
			if before == 0 && after == 0 {
				continue
			}
			if err := validateCoroCriticalInstruction(universe, plan, audit, proof, instruction); err != nil {
				return nil, coroCriticalInstructionError(fn, instruction, err.Error())
			}
		}
	}
	return proof, nil
}

func coroCriticalReachableBlocks(fn *ssa.Function) map[*ssa.BasicBlock]bool {
	reachable := make(map[*ssa.BasicBlock]bool)
	if fn == nil || len(fn.Blocks) == 0 {
		return reachable
	}
	queue := []*ssa.BasicBlock{fn.Blocks[0]}
	reachable[fn.Blocks[0]] = true
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		for _, successor := range block.Succs {
			if successor != nil && !reachable[successor] {
				reachable[successor] = true
				queue = append(queue, successor)
			}
		}
	}
	return reachable
}

// validateCoroCriticalMaskedDAG rejects cycles whose backedge remains masked
// and computes the longest dynamic masked instruction path over the resulting
// DAG. A surrounding loop is legal only when every iteration returns to depth
// zero before its backedge.
func validateCoroCriticalMaskedDAG(
	fn *ssa.Function,
	proof *coroCriticalProof,
	outDepth map[*ssa.BasicBlock]uint32,
	reachable map[*ssa.BasicBlock]bool,
) error {
	indegree := make(map[*ssa.BasicBlock]int, len(fn.Blocks))
	reachableCount := 0
	for _, block := range fn.Blocks {
		if !reachable[block] {
			continue
		}
		reachableCount++
		if outDepth[block] == 0 {
			continue
		}
		for _, successor := range block.Succs {
			indegree[successor]++
		}
	}
	queue := make([]*ssa.BasicBlock, 0, reachableCount)
	for _, block := range fn.Blocks {
		if reachable[block] && indegree[block] == 0 {
			queue = append(queue, block)
		}
	}
	carry := make(map[*ssa.BasicBlock]int, reachableCount)
	processed := 0
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		processed++
		length := 0
		if proof.entryDepth[block] != 0 {
			length = carry[block]
		}
		for _, instruction := range block.Instrs {
			before, after := proof.beforeDepth[instruction], proof.afterDepth[instruction]
			active := before != 0 || after != 0
			if !active {
				length = 0
				continue
			}
			if before == 0 {
				length = 0
			}
			if _, debug := instruction.(*ssa.DebugRef); !debug {
				length++
				if length > coroPreemptInstructionBudget {
					return coroCriticalInstructionError(fn, instruction, fmt.Sprintf(
						"critical path exceeds the %d-instruction preemption budget", coroPreemptInstructionBudget,
					))
				}
			}
			if after == 0 {
				length = 0
			}
		}
		if outDepth[block] != 0 {
			for _, successor := range block.Succs {
				if length > carry[successor] {
					carry[successor] = length
				}
			}
		}
		for _, successor := range block.Succs {
			if outDepth[block] == 0 {
				continue
			}
			indegree[successor]--
			if indegree[successor] == 0 {
				queue = append(queue, successor)
			}
		}
	}
	if processed != reachableCount {
		return fmt.Errorf("function %q has a cyclic CFG path while preemption is masked", fn.String())
	}
	return nil
}

func validateCoroCriticalInstruction(
	universe *EmissionUniverse,
	plan *coro.SSAPlan,
	audit *coroPhysicalPureSSAAudit,
	proof *coroCriticalProof,
	instruction ssa.Instruction,
) error {
	if _, debug := instruction.(*ssa.DebugRef); debug {
		return nil
	}
	if call, ok := instruction.(*ssa.Call); ok {
		if role := proof.roles[call]; role == coroCriticalCallEnter || role == coroCriticalCallExit {
			return nil
		}
		frozen, found, err := universe.coroProgramIR.callSitePlan(call)
		if err != nil || !found {
			if err == nil {
				err = fmt.Errorf("call is absent from the frozen ProgramIR")
			}
			return err
		}
		if frozen.failure != "" {
			return fmt.Errorf("invalid frozen intrinsic: %s", frozen.failure)
		}
		if !frozen.plan.Intrinsic || !isCoroAtomicIntrinsic(frozen.opcode) ||
			!frozen.plan.ElidesCall() || !plan.ElidesCall(call) {
			return fmt.Errorf("ordinary or non-atomic call is forbidden while preemption is masked")
		}
		if frozen.plan.IntrinsicSemantics != CoroIntrinsicCallInlineNoSuspend {
			return fmt.Errorf("critical atomic intrinsic lacks exact inline no-suspend semantics")
		}
		return nil
	}

	switch current := instruction.(type) {
	case *ssa.Phi, *ssa.FieldAddr, *ssa.IndexAddr, *ssa.Field, *ssa.Extract,
		*ssa.ChangeType, *ssa.Convert, *ssa.BinOp, *ssa.Store:
		handled, reason := audit.validate(instruction)
		if !handled {
			return fmt.Errorf("scalar/address instruction has no physical lowering proof")
		}
		if reason != "" {
			return fmt.Errorf("scalar/address instruction is not critical-safe: %s", reason)
		}
	case *ssa.UnOp:
		if current.Op != token.MUL && current.Op != token.SUB && current.Op != token.XOR && current.Op != token.NOT {
			return fmt.Errorf("unsupported unary operation while preemption is masked")
		}
		handled, reason := audit.validate(instruction)
		if !handled || reason != "" {
			if reason == "" {
				reason = "no physical lowering proof"
			}
			return fmt.Errorf("unary instruction is not critical-safe: %s", reason)
		}
	case *ssa.If:
		if !coroLeafScalar(current.Cond.Type()) {
			return fmt.Errorf("non-scalar branch condition while preemption is masked")
		}
	case *ssa.Jump:
	case *ssa.Return:
		return fmt.Errorf("return is forbidden while preemption is masked")
	case *ssa.Panic:
		return fmt.Errorf("panic is forbidden while preemption is masked")
	default:
		return fmt.Errorf("%T is outside the bounded critical-region allowlist", instruction)
	}
	if reason := audit.requireNoRuntimeHelpers(instruction); reason != "" {
		return fmt.Errorf("instruction has hidden runtime lowering: %s", reason)
	}
	return nil
}

func coroCriticalInstructionError(fn *ssa.Function, instruction ssa.Instruction, reason string) error {
	block, ordinal := -1, -1
	if instruction != nil && instruction.Block() != nil {
		block = instruction.Block().Index
		for index, candidate := range instruction.Block().Instrs {
			if candidate == instruction {
				ordinal = index
				break
			}
		}
	}
	name := "<nil>"
	if fn != nil {
		name = fn.String()
	}
	return fmt.Errorf("function %q critical instruction block=%d index=%d: %s", name, block, ordinal, reason)
}
