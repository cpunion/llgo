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

	"golang.org/x/tools/go/ssa"
)

// coroPhysicalPreemptPlan is the immutable, function-local safepoint emission
// plan. Block-entry sites form a feedback vertex set: removing incoming edges
// to those blocks makes the CFG acyclic, so every executable cycle crosses at
// least one poll. Instruction sites then bound every remaining acyclic path.
//
// Functions with structured critical regions retain the established
// depth-aware emitter until this graph plan models critical entry/exit as
// explicit reset edges.
type coroPhysicalPreemptPlan struct {
	blockEntries       map[*ssa.BasicBlock]struct{}
	beforeInstructions map[ssa.Instruction]struct{}
}

func (p *coroPhysicalPreemptPlan) pollsAtBlock(block *ssa.BasicBlock) bool {
	if p == nil || block == nil {
		return false
	}
	_, polls := p.blockEntries[block]
	return polls
}

func (p *coroPhysicalPreemptPlan) pollsBefore(instruction ssa.Instruction) bool {
	if p == nil || instruction == nil {
		return false
	}
	_, polls := p.beforeInstructions[instruction]
	return polls
}

func planCoroPhysicalPreemption(
	audit *coroPhysicalPureSSAAudit,
	critical *coroCriticalProof,
	needsPreempt bool,
) (*coroPhysicalPreemptPlan, error) {
	if !needsPreempt || critical != nil {
		return nil, nil
	}
	if audit == nil {
		return nil, fmt.Errorf("preemptible physical function has no frozen SSA audit")
	}
	fn := audit.fn
	if fn == nil || len(fn.Blocks) == 0 {
		return nil, fmt.Errorf("preemptible physical function has no SSA CFG")
	}
	result := &coroPhysicalPreemptPlan{
		blockEntries:       make(map[*ssa.BasicBlock]struct{}),
		beforeInstructions: make(map[ssa.Instruction]struct{}),
	}

	// Every CFG root is also a scheduler-chain boundary. The ordinary entry is
	// always one root; x/tools may retain separately-entered exceptional or
	// unreachable blocks with no represented predecessor.
	for _, block := range fn.Blocks {
		if block != nil && (block.Index == 0 || len(block.Preds) == 0) {
			result.blockEntries[block] = struct{}{}
		}
	}

	// Every directed cycle contains a DFS back edge. Polling the target of each
	// back edge therefore produces a compact feedback vertex set, including
	// irreducible SCCs and self loops without assuming Go source reducibility.
	const (
		coroPreemptBlockVisiting uint8 = iota + 1
		coroPreemptBlockComplete
	)
	state := make(map[*ssa.BasicBlock]uint8, len(fn.Blocks))
	var visit func(*ssa.BasicBlock)
	visit = func(block *ssa.BasicBlock) {
		if block == nil {
			return
		}
		state[block] = coroPreemptBlockVisiting
		for _, successor := range block.Succs {
			switch state[successor] {
			case 0:
				visit(successor)
			case coroPreemptBlockVisiting:
				result.blockEntries[successor] = struct{}{}
			}
		}
		state[block] = coroPreemptBlockComplete
	}
	for _, block := range fn.Blocks {
		if block != nil && state[block] == 0 {
			visit(block)
		}
	}

	// Treat every selected block entry as a reset by removing its incoming
	// edges. The resulting graph must be a DAG. Propagate the maximum source
	// instruction distance across merges and insert a poll before the first
	// instruction that would exceed the established straight-line budget.
	indegree := make(map[*ssa.BasicBlock]int, len(fn.Blocks))
	for _, block := range fn.Blocks {
		if block == nil {
			return nil, fmt.Errorf("preemptible physical function has a nil SSA block")
		}
		indegree[block] = 0
	}
	for _, block := range fn.Blocks {
		for _, successor := range block.Succs {
			if successor == nil {
				return nil, fmt.Errorf("preemptible physical function has a nil CFG successor")
			}
			if result.pollsAtBlock(successor) {
				continue
			}
			indegree[successor]++
		}
	}
	queue := make([]*ssa.BasicBlock, 0, len(fn.Blocks))
	for _, block := range fn.Blocks {
		if indegree[block] == 0 {
			queue = append(queue, block)
		}
	}
	distance := make(map[*ssa.BasicBlock]int, len(fn.Blocks))
	processed := 0
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		processed++
		current := distance[block]
		if result.pollsAtBlock(block) {
			current = 0
		}
		for _, instruction := range block.Instrs {
			if instruction == nil {
				continue
			}
			if _, phi := instruction.(*ssa.Phi); phi {
				continue
			}
			if _, debug := instruction.(*ssa.DebugRef); debug {
				continue
			}
			if audit.ctx != nil {
				if _, unevaluated := audit.ctx.unevaluatedSSA[instruction]; unevaluated {
					continue
				}
			}
			if current >= coroPreemptInstructionBudget {
				result.beforeInstructions[instruction] = struct{}{}
				current = 0
			}
			current++
		}
		for _, successor := range block.Succs {
			if result.pollsAtBlock(successor) {
				continue
			}
			if current > distance[successor] {
				distance[successor] = current
			}
			indegree[successor]--
			if indegree[successor] == 0 {
				queue = append(queue, successor)
			}
		}
	}
	if processed != len(fn.Blocks) {
		return nil, fmt.Errorf(
			"preemption feedback set left %d of %d SSA blocks cyclic",
			len(fn.Blocks)-processed, len(fn.Blocks),
		)
	}
	return result, nil
}
