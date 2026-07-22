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
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// ProveSSAExactSafeFixedArrayIndex proves that one immutable SSA index is in
// [0, bound) at use. It accepts constants, unsigned values on a dominating
// constant upper-bound edge, and the two canonical x/tools range/for induction
// forms whose signed increment cannot wrap. It deliberately does not reason
// about slices, strings, arbitrary inequalities, or source variable names.
//
// This is the single target-neutral proof shared by planning and frontend
// lowering. Pointer-to-array nil safety is an independent fact and is not
// implied by this function.
func ProveSSAExactSafeFixedArrayIndex(
	fn *ssa.Function,
	index ssa.Value,
	bound int64,
	use ssa.Instruction,
) bool {
	if fn == nil || index == nil || bound <= 0 || use == nil || use.Parent() != fn || use.Block() == nil {
		return false
	}
	if exact, ok := index.(*ssa.Const); ok {
		if exact.Value == nil {
			return false
		}
		integer, exactInteger := constant.Int64Val(exact.Value)
		return exactInteger && integer >= 0 && integer < bound
	}
	for _, block := range fn.Blocks {
		if len(block.Instrs) == 0 || len(block.Succs) != 2 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		comparison, ok := branch.Cond.(*ssa.BinOp)
		if !ok || comparison.Op != token.LSS || comparison.X != index ||
			!ssaExactPositiveIntegerAtMost(comparison.Y, bound) ||
			!block.Succs[0].Dominates(use.Block()) {
			continue
		}
		limit, _ := ssaExactPositiveInteger(comparison.Y)
		if ssaExactNonNegativeRangeIndex(index, block, block.Succs[0], use, limit) {
			return true
		}
	}
	return false
}

func analyzeSSAExactSafeFixedArrayIndexes(functions []*ssa.Function) map[ssa.Instruction]int64 {
	result := make(map[ssa.Instruction]int64)
	for _, fn := range functions {
		if fn == nil {
			continue
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				var base, index ssa.Value
				switch operation := instruction.(type) {
				case *ssa.Index:
					base, index = operation.X, operation.Index
				case *ssa.IndexAddr:
					base, index = operation.X, operation.Index
				default:
					continue
				}
				bound, _, fixed := ssaExactFixedArrayBound(base)
				if fixed && ProveSSAExactSafeFixedArrayIndex(fn, index, bound, instruction) {
					result[instruction] = bound
				}
			}
		}
	}
	return result
}

func ssaExactFixedArrayBound(value ssa.Value) (bound int64, pointerBase bool, ok bool) {
	if value == nil || value.Type() == nil {
		return 0, false, false
	}
	typ := types.Unalias(value.Type()).Underlying()
	if pointer, isPointer := typ.(*types.Pointer); isPointer {
		array, isArray := types.Unalias(pointer.Elem()).Underlying().(*types.Array)
		if !isArray {
			return 0, false, false
		}
		return array.Len(), true, true
	}
	array, isArray := typ.(*types.Array)
	if !isArray {
		return 0, false, false
	}
	return array.Len(), false, true
}

func ssaExactPositiveIntegerAtMost(value ssa.Value, maximum int64) bool {
	integer, ok := ssaExactPositiveInteger(value)
	return ok && integer <= maximum
}

func ssaExactPositiveInteger(value ssa.Value) (int64, bool) {
	exact, ok := value.(*ssa.Const)
	if !ok || exact.Value == nil {
		return 0, false
	}
	integer, exactInteger := constant.Int64Val(exact.Value)
	return integer, exactInteger && integer > 0
}

func ssaExactNonNegativeRangeIndex(
	index ssa.Value,
	header, trueSuccessor *ssa.BasicBlock,
	use ssa.Instruction,
	constantUpperBound int64,
) bool {
	if index == nil || header == nil || trueSuccessor == nil || use == nil || use.Block() == nil ||
		constantUpperBound <= 0 || !trueSuccessor.Dominates(use.Block()) {
		return false
	}
	basic, ok := types.Unalias(index.Type()).Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsInteger == 0 {
		return false
	}
	if basic.Info()&types.IsUnsigned != 0 {
		return true
	}

	var phi *ssa.Phi
	var next *ssa.BinOp
	initial := int64(0)
	indexIsNext := false
	if candidate, ok := index.(*ssa.Phi); ok {
		phi = candidate
	} else if add, ok := index.(*ssa.BinOp); ok && add.Op == token.ADD {
		candidate, increment := ssaExactPhiAndConstant(add.X, add.Y)
		if candidate == nil || increment != 1 {
			return false
		}
		phi = candidate
		next = add
		initial = -1
		indexIsNext = true
	} else {
		return false
	}
	if phi.Block() != header || len(header.Preds) < 2 || len(phi.Edges) != len(header.Preds) {
		return false
	}
	if !indexIsNext {
		for _, edge := range phi.Edges {
			add, ok := edge.(*ssa.BinOp)
			if !ok || add.Op != token.ADD {
				continue
			}
			edgePhi, increment := ssaExactPhiAndConstant(add.X, add.Y)
			if edgePhi == phi && increment > 0 {
				next = add
				break
			}
		}
	}
	if next == nil {
		return false
	}
	_, increment := ssaExactPhiAndConstant(next.X, next.Y)
	maximum, ok := ssaExactSignedIntegerMax(basic.Kind())
	if !ok || increment <= 0 || constantUpperBound-1 > maximum ||
		increment > maximum-(constantUpperBound-1) {
		return false
	}

	initialCount, recursiveCount := 0, 0
	var recursivePredecessors []*ssa.BasicBlock
	for edgeIndex, edge := range phi.Edges {
		predecessor := header.Preds[edgeIndex]
		if predecessor == nil {
			return false
		}
		if exact, ok := edge.(*ssa.Const); ok && exact.Value != nil {
			integer, exactInteger := constant.Int64Val(exact.Value)
			if !exactInteger || integer != initial || header.Dominates(predecessor) {
				return false
			}
			initialCount++
			continue
		}
		if edge != next || !header.Dominates(predecessor) || !trueSuccessor.Dominates(predecessor) {
			return false
		}
		if indexIsNext {
			if next.Block() != header {
				return false
			}
		} else if next.Block() != predecessor {
			return false
		}
		recursiveCount++
		recursivePredecessors = append(recursivePredecessors, predecessor)
	}
	if initialCount != 1 || recursiveCount == 0 {
		return false
	}
	for _, predecessor := range recursivePredecessors {
		if ssaExactBlockCanReachWithoutCrossing(header.Succs[1], predecessor, header) {
			return false
		}
	}
	return true
}

func ssaExactBlockCanReachWithoutCrossing(from, target, stop *ssa.BasicBlock) bool {
	if from == nil || target == nil {
		return false
	}
	seen := make(map[*ssa.BasicBlock]bool)
	queue := []*ssa.BasicBlock{from}
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		if block == target {
			return true
		}
		if block == nil || block == stop || seen[block] {
			continue
		}
		seen[block] = true
		queue = append(queue, block.Succs...)
	}
	return false
}

func ssaExactSignedIntegerMax(kind types.BasicKind) (int64, bool) {
	switch kind {
	case types.Int8:
		return 1<<7 - 1, true
	case types.Int16:
		return 1<<15 - 1, true
	case types.Int32:
		return 1<<31 - 1, true
	case types.Int64:
		return 1<<63 - 1, true
	case types.Int:
		return 1<<31 - 1, true
	default:
		return 0, false
	}
}

func ssaExactPhiAndConstant(left, right ssa.Value) (*ssa.Phi, int64) {
	if phi, ok := left.(*ssa.Phi); ok {
		if exact, ok := right.(*ssa.Const); ok && exact.Value != nil {
			if integer, exactInteger := constant.Int64Val(exact.Value); exactInteger {
				return phi, integer
			}
		}
	}
	if phi, ok := right.(*ssa.Phi); ok {
		if exact, ok := left.(*ssa.Const); ok && exact.Value != nil {
			if integer, exactInteger := constant.Int64Val(exact.Value); exactInteger {
				return phi, integer
			}
		}
	}
	return nil, 0
}
