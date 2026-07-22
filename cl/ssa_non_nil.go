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
	"go/constant"
	"go/token"
	"go/types"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// ssaDominatingNonNilProof is deliberately a control-flow proof rather than a
// source-name or helper-name exception. The selected successor is the exact
// edge on which Comparison proves Pointer non-nil, and that successor must
// dominate the instruction whose physical nil check may be removed.
type ssaDominatingNonNilProof struct {
	Pointer    ssa.Value
	Comparison *ssa.BinOp
	Branch     *ssa.If
	Successor  *ssa.BasicBlock
}

func proveSSADominatingNonNil(pointer ssa.Value, use ssa.Instruction) (ssaDominatingNonNilProof, bool) {
	var zero ssaDominatingNonNilProof
	if pointer == nil || use == nil || use.Parent() == nil || use.Block() == nil {
		return zero, false
	}
	canonical := ssaCanonicalPointerValue(pointer)
	if canonical == nil {
		return zero, false
	}
	for _, block := range use.Parent().Blocks {
		if block == nil || len(block.Instrs) == 0 || len(block.Succs) != 2 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok || branch.Parent() != use.Parent() {
			continue
		}
		comparison, ok := branch.Cond.(*ssa.BinOp)
		if !ok || (comparison.Op != token.EQL && comparison.Op != token.NEQ) {
			continue
		}
		compared, ok := ssaNilComparedPointer(comparison)
		if !ok || ssaCanonicalPointerValue(compared) != canonical {
			continue
		}

		// In x/tools SSA Succs[0] is the true edge and Succs[1] is the
		// false edge. p != nil proves non-nil on true; p == nil proves it
		// on false.
		successor := block.Succs[0]
		if comparison.Op == token.EQL {
			successor = block.Succs[1]
		}
		if successor == nil || !successor.Dominates(use.Block()) {
			continue
		}
		return ssaDominatingNonNilProof{
			Pointer:    canonical,
			Comparison: comparison,
			Branch:     branch,
			Successor:  successor,
		}, true
	}
	return zero, false
}

func ssaValueProvenNonNilAt(pointer ssa.Value, use ssa.Instruction) bool {
	_, ok := proveSSADominatingNonNil(pointer, use)
	return ok
}

// ssaAddressValueProvenNonNilAt proves that a pointer-producing SSA address
// constructor cannot yield nil on the path reaching use. Bounds and nil are
// deliberately independent: an IndexAddr participates only when the shared
// fixed-array proof removes its bounds fault and its pointer-to-array base is
// independently known non-nil. This is the single FieldAddr predicate shared
// by helper inventory, ABI preflight, and final emission.
func ssaAddressValueProvenNonNilAt(address ssa.Value, use ssa.Instruction) bool {
	return ssaAddressValueProvenNonNilAtRecursive(address, use, make(map[ssa.Value]bool))
}

func ssaAddressValueProvenNonNilAtRecursive(
	address ssa.Value,
	use ssa.Instruction,
	visiting map[ssa.Value]bool,
) bool {
	if address == nil || use == nil || use.Parent() == nil || use.Block() == nil {
		return false
	}
	if ssaValueProvenNonNilAt(address, use) {
		return true
	}
	address = ssaCanonicalPointerValue(address)
	if address == nil || visiting[address] {
		return false
	}
	visiting[address] = true
	defer delete(visiting, address)

	switch value := address.(type) {
	case *ssa.Global, *ssa.Alloc:
		return true
	case *ssa.FieldAddr:
		return value.Parent() == use.Parent() &&
			ssaAddressValueProvenNonNilAtRecursive(value.X, value, visiting)
	case *ssa.IndexAddr:
		if value.Parent() != use.Parent() || value.X == nil || value.Index == nil {
			return false
		}
		pointer, ok := types.Unalias(value.X.Type()).Underlying().(*types.Pointer)
		if !ok {
			return false
		}
		array, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array)
		return ok && coro.ProveSSAExactSafeFixedArrayIndex(
			value.Parent(), value.Index, array.Len(), value,
		) && ssaAddressValueProvenNonNilAtRecursive(value.X, value, visiting)
	default:
		return false
	}
}

// ssaFunctionValueProvenNonNilAt proves the language-level guard for an exact
// function SSA value without treating Go closures as pointer-shaped storage.
// Raw C function values are physically one word while managed Go functions are
// descriptor-shaped, so they must not participate in ssaCanonicalPointerValue
// or pointer-preserving conversion logic. The exact SSA identity plus CFG
// dominance is sufficient for the indirect-call guard used by raw C transport.
func ssaFunctionValueProvenNonNilAt(function ssa.Value, use ssa.Instruction) bool {
	if function == nil || use == nil || use.Parent() == nil || use.Block() == nil || !ssaFunctionLike(function.Type()) {
		return false
	}
	for _, block := range use.Parent().Blocks {
		if block == nil || len(block.Instrs) == 0 || len(block.Succs) != 2 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok || branch.Parent() != use.Parent() {
			continue
		}
		comparison, ok := branch.Cond.(*ssa.BinOp)
		if !ok || (comparison.Op != token.EQL && comparison.Op != token.NEQ) {
			continue
		}
		compared, ok := ssaNilComparedFunction(comparison)
		if !ok || compared != function {
			continue
		}
		successor := block.Succs[0]
		if comparison.Op == token.EQL {
			successor = block.Succs[1]
		}
		if successor != nil && successor.Dominates(use.Block()) {
			return true
		}
	}
	return false
}

func ssaNilComparedFunction(comparison *ssa.BinOp) (ssa.Value, bool) {
	if comparison == nil {
		return nil, false
	}
	if ssaNilConst(comparison.X) && ssaFunctionLike(comparison.Y.Type()) {
		return comparison.Y, true
	}
	if ssaNilConst(comparison.Y) && ssaFunctionLike(comparison.X.Type()) {
		return comparison.X, true
	}
	return nil, false
}

func ssaFunctionLike(typ types.Type) bool {
	if typ == nil {
		return false
	}
	_, ok := types.Unalias(typ).Underlying().(*types.Signature)
	return ok
}

func ssaNilComparedPointer(comparison *ssa.BinOp) (ssa.Value, bool) {
	if comparison == nil {
		return nil, false
	}
	if ssaNilConst(comparison.X) && ssaPointerLike(comparison.Y.Type()) {
		return comparison.Y, true
	}
	if ssaNilConst(comparison.Y) && ssaPointerLike(comparison.X.Type()) {
		return comparison.X, true
	}
	return nil, false
}

func ssaNilConst(value ssa.Value) bool {
	constant, ok := value.(*ssa.Const)
	// x/tools Const.IsNil intentionally omits the basic unsafe.Pointer
	// type even though an unsafe.Pointer comparison against nil is valid Go.
	// A nil SSA constant is represented uniformly by Value == nil; the other
	// comparison operand is independently required to be pointer-like above.
	return ok && constant.Value == nil
}

// ssaCanonicalPointerValue strips only conversions that preserve the pointer
// bits. Integer conversions are intentionally excluded: uintptr round trips
// carry different language and lifetime semantics and cannot certify non-nil.
func ssaCanonicalPointerValue(value ssa.Value) ssa.Value {
	for value != nil {
		switch converted := value.(type) {
		case *ssa.Convert:
			if converted.X == nil || !ssaPointerLike(converted.Type()) || !ssaPointerLike(converted.X.Type()) {
				return value
			}
			value = converted.X
		case *ssa.ChangeType:
			if converted.X == nil || !ssaPointerLike(converted.Type()) || !ssaPointerLike(converted.X.Type()) {
				return value
			}
			value = converted.X
		default:
			return value
		}
	}
	return nil
}

func ssaPointerLike(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch underlying := types.Unalias(typ).Underlying().(type) {
	case *types.Pointer:
		return true
	case *types.Basic:
		return underlying.Kind() == types.UnsafePointer
	default:
		return false
	}
}

// ssaIntegerValueProvenNonZeroAt accepts only an exact SSA-value comparison
// against an integer constant whose zero-excluding successor dominates use.
// It may look through an integer conversion only when that conversion cannot
// map a non-zero source bit pattern to zero on any supported target. SSA values
// are immutable, so no additional data-flow invalidation is necessary.
func ssaIntegerValueProvenNonZeroAt(value ssa.Value, use ssa.Instruction) bool {
	return ssaIntegerValueProvenNonZeroAtRecursive(value, use, make(map[ssa.Value]bool))
}

func ssaIntegerValueProvenNonZeroAtRecursive(value ssa.Value, use ssa.Instruction, visiting map[ssa.Value]bool) bool {
	if constantIntegerKnownNonZero(value) {
		return true
	}
	if value == nil || use == nil || use.Parent() == nil || use.Block() == nil || !ssaIntegerValue(value) {
		return false
	}
	if visiting[value] {
		return false
	}
	visiting[value] = true
	defer delete(visiting, value)

	switch converted := value.(type) {
	case *ssa.Convert:
		if converted.Parent() == use.Parent() &&
			ssaIntegerConversionPreservesNonZero(converted.X.Type(), converted.Type()) &&
			ssaIntegerValueProvenNonZeroAtRecursive(converted.X, use, visiting) {
			return true
		}
	case *ssa.ChangeType:
		if converted.Parent() == use.Parent() &&
			ssaIntegerConversionPreservesNonZero(converted.X.Type(), converted.Type()) &&
			ssaIntegerValueProvenNonZeroAtRecursive(converted.X, use, visiting) {
			return true
		}
	}
	if ssaIntegerValueProvenPositiveByLoopBoundAt(value, use) {
		return true
	}

	for _, block := range use.Parent().Blocks {
		if block == nil || len(block.Instrs) == 0 || len(block.Succs) != 2 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok || branch.Parent() != use.Parent() {
			continue
		}
		comparison, ok := branch.Cond.(*ssa.BinOp)
		if !ok {
			continue
		}
		successor := ssaIntegerComparisonNonZeroSuccessor(comparison, value, block)
		if successor != nil && successor.Dominates(use.Block()) {
			return true
		}
	}
	return false
}

// ssaIntegerValueProvenPositiveByLoopBoundAt recognizes the canonical loop
// fact `0 <= index < limit`. The true loop edge must dominate use, and the
// induction proof must independently establish that index starts non-negative
// and cannot wrap before the next header test. Therefore limit is strictly
// positive at use. Repeated len(x) builtins are equivalent immutable values
// even when x/tools emits a distinct SSA Call for each source occurrence.
func ssaIntegerValueProvenPositiveByLoopBoundAt(value ssa.Value, use ssa.Instruction) bool {
	if value == nil || use == nil || use.Parent() == nil || use.Block() == nil {
		return false
	}
	for _, block := range use.Parent().Blocks {
		if block == nil || len(block.Instrs) == 0 || len(block.Succs) != 2 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok || branch.Parent() != use.Parent() {
			continue
		}
		comparison, ok := branch.Cond.(*ssa.BinOp)
		if !ok {
			continue
		}
		var index ssa.Value
		switch {
		case comparison.Op == token.LSS && ssaEquivalentIntegerLimitValue(comparison.Y, value):
			index = comparison.X
		case comparison.Op == token.GTR && ssaEquivalentIntegerLimitValue(comparison.X, value):
			index = comparison.Y
		default:
			continue
		}
		trueSuccessor := block.Succs[0]
		if trueSuccessor == nil || !trueSuccessor.Dominates(use.Block()) {
			continue
		}
		if _, nonNegative := coroFrameRetentionNonNegativeRangeIndex(index, block, trueSuccessor, use, 0); nonNegative {
			return true
		}
	}
	return false
}

func ssaEquivalentIntegerLimitValue(left, right ssa.Value) bool {
	if left == right {
		return true
	}
	leftLen, leftOperand := ssaExactBuiltinLenValue(left)
	rightLen, rightOperand := ssaExactBuiltinLenValue(right)
	return leftLen && rightLen && leftOperand == rightOperand
}

func ssaExactBuiltinLenValue(value ssa.Value) (bool, ssa.Value) {
	call, ok := value.(*ssa.Call)
	if !ok || call.Common() == nil || len(call.Common().Args) != 1 {
		return false, nil
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "len" {
		return false, nil
	}
	return true, call.Common().Args[0]
}

// ssaIntegerComparisonNonZeroSuccessor evaluates the exact comparison with
// value replaced by zero. Zero can follow only that result edge, so the other
// successor proves value != 0. This covers equality as well as range guards
// such as `base < 2` without inventing a source-level invariant.
func ssaIntegerComparisonNonZeroSuccessor(comparison *ssa.BinOp, value ssa.Value, block *ssa.BasicBlock) *ssa.BasicBlock {
	if comparison == nil || value == nil || block == nil || len(block.Succs) != 2 {
		return nil
	}
	switch comparison.Op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
	default:
		return nil
	}
	var other *ssa.Const
	valueOnLeft := false
	if comparison.X == value {
		other, _ = comparison.Y.(*ssa.Const)
		valueOnLeft = true
	} else if comparison.Y == value {
		other, _ = comparison.X.(*ssa.Const)
	} else {
		return nil
	}
	if other == nil || other.Value == nil || !ssaIntegerValue(other) {
		return nil
	}
	zero := constant.MakeInt64(0)
	zeroTakesTrue := constant.Compare(other.Value, comparison.Op, zero)
	if valueOnLeft {
		zeroTakesTrue = constant.Compare(zero, comparison.Op, other.Value)
	}
	if zeroTakesTrue {
		return block.Succs[1]
	}
	return block.Succs[0]
}

func ssaIntegerConversionPreservesNonZero(source, target types.Type) bool {
	sourceBasic, sourceOK := types.Unalias(source).Underlying().(*types.Basic)
	targetBasic, targetOK := types.Unalias(target).Underlying().(*types.Basic)
	if !sourceOK || !targetOK || sourceBasic.Info()&types.IsInteger == 0 || targetBasic.Info()&types.IsInteger == 0 ||
		sourceBasic.Info()&types.IsUntyped != 0 || targetBasic.Info()&types.IsUntyped != 0 {
		return false
	}
	if ssaMachineIntegerKind(sourceBasic.Kind()) && ssaMachineIntegerKind(targetBasic.Kind()) {
		return true
	}
	sourceMax := ssaIntegerKindMaxBits(sourceBasic.Kind())
	targetMin := ssaIntegerKindMinBits(targetBasic.Kind())
	return sourceMax != 0 && targetMin >= sourceMax
}

func ssaMachineIntegerKind(kind types.BasicKind) bool {
	return kind == types.Int || kind == types.Uint || kind == types.Uintptr
}

func ssaIntegerKindMinBits(kind types.BasicKind) int {
	if ssaMachineIntegerKind(kind) {
		return 32
	}
	return ssaIntegerKindMaxBits(kind)
}

func ssaIntegerKindMaxBits(kind types.BasicKind) int {
	switch kind {
	case types.Int8, types.Uint8:
		return 8
	case types.Int16, types.Uint16:
		return 16
	case types.Int32, types.Uint32:
		return 32
	case types.Int64, types.Uint64, types.Int, types.Uint, types.Uintptr:
		return 64
	default:
		return 0
	}
}

func ssaIntegerValue(value ssa.Value) bool {
	if value == nil || value.Type() == nil {
		return false
	}
	basic, ok := types.Unalias(value.Type()).Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsInteger != 0
}
