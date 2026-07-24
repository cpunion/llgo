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
	"fmt"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// proveSSAExactNoUnwind computes the greatest fixed point of defined bodies
// whose complete SSA recipe cannot initiate a Go unwind. It is deliberately a
// small lowering contract, not a source-name allowlist. A body leaves this set
// as soon as it contains an operation outside the recipe or depends on a
// callee outside the fixed point.
//
// The result only removes scanSSAFunctionBody's blanket local MayUnwind seed.
// Explicit frontend Exec facts and graph-propagated callee facts remain
// authoritative.
func proveSSAExactNoUnwind(
	functions []*ssa.Function,
	trusted map[*ssa.Function]SSAFunctionPolicy,
	closedCalls map[ssa.CallInstruction]SSAClosedDynamicCallCertificate,
	trustedInlineNoUnwind map[ssa.CallInstruction]bool,
	elidedCalls map[ssa.CallInstruction]bool,
	safeFixedArrayIndexes map[ssa.Instruction]int64,
	canonicalizer *ssaFunctionCanonicalizer,
) (map[*ssa.Function]bool, map[ssa.CallInstruction]bool, error) {
	bodySet := make(map[*ssa.Function]bool, len(functions))
	for _, fn := range functions {
		bodySet[fn] = true
	}
	leaves := make(map[*ssa.Function]bool)
	for fn, policy := range trusted {
		if policy.ForeignNoBlockCertificate != "" || policy.ForeignSyncCertificate != "" ||
			policy.AssemblyNoSuspendCertificate != "" ||
			policy.IgnoreBody && policy.OverrideExternal && policy.External == ExternalKnown &&
				policy.Effect == NoSuspend && !policy.Exec.Contains(MayUnwind) {
			// IgnoreBody+ExternalKnown is the frozen statement that this exact
			// declaration is physically implemented outside Go. It need not be
			// nonblocking or IRQ-safe to establish the narrower fact that the
			// declaration itself cannot initiate a Go unwind. The other effect and
			// execution flags still propagate through the ordinary graph.
			leaves[fn] = true
		}
	}

	type candidate struct {
		dependencies    map[*ssa.Function]struct{}
		contextualCalls map[ssa.CallInstruction]map[*ssa.Function]struct{}
	}
	candidates := make(map[*ssa.Function]candidate, len(functions))
	for _, fn := range functions {
		policy := trusted[fn]
		if policy.IgnoreBody || policy.Exec.Contains(MayUnwind) ||
			policy.OverrideExternal && policy.External != Defined {
			continue
		}
		analysis := newSSAExactNoUnwindAnalysis(
			fn, bodySet, leaves, closedCalls, trustedInlineNoUnwind, elidedCalls, safeFixedArrayIndexes, canonicalizer,
		)
		analysis.trustedLocal = policy.TrustedNoUnwind
		ok, err := analysis.scan()
		if err != nil {
			return nil, nil, fmt.Errorf("coro: prove exact no-unwind body %q: %w", fn.Name(), err)
		}
		if ok {
			candidates[fn] = candidate{
				dependencies:    analysis.dependencies,
				contextualCalls: analysis.contextualCalls,
			}
		}
	}

	result := make(map[*ssa.Function]bool, len(candidates)+len(leaves))
	for fn := range leaves {
		result[fn] = true
	}
	for fn := range candidates {
		result[fn] = true
	}
	for changed := true; changed; {
		changed = false
		for fn, candidate := range candidates {
			if !result[fn] {
				continue
			}
			for dependency := range candidate.dependencies {
				if !result[dependency] {
					result[fn] = false
					changed = true
					break
				}
			}
		}
	}
	contextualCalls := make(map[ssa.CallInstruction]bool)
	for _, candidate := range candidates {
		for call, dependencies := range candidate.contextualCalls {
			exact := true
			for dependency := range dependencies {
				if !result[dependency] {
					exact = false
					break
				}
			}
			if exact {
				contextualCalls[call] = true
			}
		}
	}
	// External leaves are useful while solving dependencies, but callers only
	// need to know which defined scanner seeds may be cleared.
	for fn := range leaves {
		delete(result, fn)
	}
	return result, contextualCalls, nil
}

type ssaExactNoUnwindAnalysis struct {
	fn                    *ssa.Function
	bodySet               map[*ssa.Function]bool
	leaves                map[*ssa.Function]bool
	closedCalls           map[ssa.CallInstruction]SSAClosedDynamicCallCertificate
	trustedInlineNoUnwind map[ssa.CallInstruction]bool
	elidedCalls           map[ssa.CallInstruction]bool
	safeFixedArrayIndexes map[ssa.Instruction]int64
	canonicalizer         *ssaFunctionCanonicalizer
	blocks                map[ssa.Instruction]*ssa.BasicBlock
	dependencies          map[*ssa.Function]struct{}
	contextualCalls       map[ssa.CallInstruction]map[*ssa.Function]struct{}
	nonEvalMemo           map[ssa.Value]uint8
	scalarBitcast         *ssa.Alloc
	assumedNonNil         map[ssa.Value]bool
	meaningful            bool
	trustedLocal          bool
}

func newSSAExactNoUnwindAnalysis(
	fn *ssa.Function,
	bodySet map[*ssa.Function]bool,
	leaves map[*ssa.Function]bool,
	closedCalls map[ssa.CallInstruction]SSAClosedDynamicCallCertificate,
	trustedInlineNoUnwind map[ssa.CallInstruction]bool,
	elidedCalls map[ssa.CallInstruction]bool,
	safeFixedArrayIndexes map[ssa.Instruction]int64,
	canonicalizer *ssaFunctionCanonicalizer,
) *ssaExactNoUnwindAnalysis {
	analysis := &ssaExactNoUnwindAnalysis{
		fn:                    fn,
		bodySet:               bodySet,
		leaves:                leaves,
		closedCalls:           closedCalls,
		trustedInlineNoUnwind: trustedInlineNoUnwind,
		elidedCalls:           elidedCalls,
		safeFixedArrayIndexes: safeFixedArrayIndexes,
		canonicalizer:         canonicalizer,
		blocks:                make(map[ssa.Instruction]*ssa.BasicBlock),
		dependencies:          make(map[*ssa.Function]struct{}),
		contextualCalls:       make(map[ssa.CallInstruction]map[*ssa.Function]struct{}),
		nonEvalMemo:           make(map[ssa.Value]uint8),
		assumedNonNil:         make(map[ssa.Value]bool),
	}
	if fn != nil {
		if proof, exact := ProveSSAExactScalarBitcast(fn); exact {
			analysis.scalarBitcast = proof.Allocation
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				analysis.blocks[instruction] = block
			}
		}
	}
	return analysis
}

func (a *ssaExactNoUnwindAnalysis) scan() (bool, error) {
	if a.fn == nil || len(a.fn.Blocks) == 0 {
		return false, nil
	}
	for _, block := range a.fn.Blocks {
		for _, instruction := range block.Instrs {
			if _, debug := instruction.(*ssa.DebugRef); debug {
				continue
			}
			if value, ok := instruction.(ssa.Value); ok && a.onlyUsedAsNonEvaluatedOperand(value) {
				// unsafe.Sizeof/Alignof do not evaluate their operand. The SSA
				// builder may nevertheless retain the operand's value-producing
				// instructions, so exclude an exact use-only backward slice.
				continue
			}
			ok, err := a.instruction(instruction)
			if err != nil || !ok {
				return false, err
			}
		}
	}
	// Reaching this point proves every executable SSA instruction in the body.
	// A body containing only Return/Jump instructions is therefore no-unwind as
	// well: those control instructions cannot initiate a Go panic merely because
	// there was no value-producing operation to mark as meaningful.
	return true, nil
}

func (a *ssaExactNoUnwindAnalysis) instruction(instruction ssa.Instruction) (bool, error) {
	if a.trustedLocal {
		switch instruction := instruction.(type) {
		case *ssa.Panic, *ssa.Defer, *ssa.RunDefers, *ssa.Send, *ssa.Select:
			return false, nil
		case *ssa.UnOp:
			if instruction.Op == token.ARROW {
				return false, nil
			}
		case *ssa.Call:
			return a.call(instruction)
		}
		a.meaningful = true
		return true, nil
	}
	switch instruction := instruction.(type) {
	case *ssa.Return, *ssa.Jump:
		return true, nil
	case *ssa.If, *ssa.Phi, *ssa.Extract, *ssa.Field:
		a.meaningful = true
		return true, nil
	case *ssa.Alloc:
		if instruction.Heap && instruction != a.scalarBitcast {
			return false, nil
		}
		// x/tools spills aggregate parameters and local values through a
		// non-escaping *ssa.Alloc. It also conservatively marks the one exact
		// scalar-bitcast parameter slot Heap even though its complete use graph is
		// proven above. Both lower to physical local storage and cannot initiate a
		// Go unwind.
		a.meaningful = true
		return true, nil
	case *ssa.BinOp:
		if !noUnwindBinOp(instruction) {
			return false, nil
		}
		a.meaningful = true
		return true, nil
	case *ssa.UnOp:
		switch instruction.Op {
		case token.MUL:
			if ssaExactZeroLengthSliceToArrayValueDeref(instruction) {
				a.meaningful = true
				return true, nil
			}
			if !a.pointerProvenNonNil(instruction.X, instruction) {
				return false, nil
			}
		case token.NOT, token.XOR:
		default:
			return false, nil
		}
		a.meaningful = true
		return true, nil
	case *ssa.Convert:
		if !noUnwindConversion(instruction.X.Type(), instruction.Type()) {
			return false, nil
		}
		a.meaningful = true
		return true, nil
	case *ssa.ChangeType:
		if !noUnwindConversion(instruction.X.Type(), instruction.Type()) {
			return false, nil
		}
		a.meaningful = true
		return true, nil
	case *ssa.FieldAddr:
		if !a.pointerProvenNonNil(instruction.X, instruction) {
			return false, nil
		}
		a.meaningful = true
		return true, nil
	case *ssa.Index:
		if !a.fixedArrayIndexProven(instruction.X, instruction.Index, instruction, instruction) {
			return false, nil
		}
		a.meaningful = true
		return true, nil
	case *ssa.IndexAddr:
		if !a.fixedArrayIndexProven(instruction.X, instruction.Index, instruction, instruction) {
			return false, nil
		}
		a.meaningful = true
		return true, nil
	case *ssa.SliceToArrayPointer:
		if length, exact := ssaExactSliceToArrayPointerLen(instruction); !exact || length != 0 {
			return false, nil
		}
		// *[0]T(s) is only the slice data-word projection. A nil slice remains
		// nil and no dynamic length failure exists.
		a.meaningful = true
		return true, nil
	case *ssa.Store:
		if !a.pointerProvenNonNil(instruction.Addr, instruction) {
			return false, nil
		}
		a.meaningful = true
		return true, nil
	case *ssa.Call:
		return a.call(instruction)
	default:
		// Heap allocation, panic/defer, indexing/slicing, type assertions, maps,
		// channels, selects, interface construction, and every currently
		// unknown operation remain conservatively unwind-capable.
		return false, nil
	}
}

func ssaExactSliceToArrayPointerLen(conversion *ssa.SliceToArrayPointer) (int64, bool) {
	if conversion == nil || conversion.X == nil || conversion.Type() == nil {
		return 0, false
	}
	slice, ok := types.Unalias(conversion.X.Type()).Underlying().(*types.Slice)
	if !ok {
		return 0, false
	}
	pointer, ok := types.Unalias(conversion.Type()).Underlying().(*types.Pointer)
	if !ok {
		return 0, false
	}
	array, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array)
	if !ok || !types.Identical(slice.Elem(), array.Elem()) {
		return 0, false
	}
	return array.Len(), true
}

func ssaExactZeroLengthSliceToArrayValueDeref(deref *ssa.UnOp) bool {
	if deref == nil || deref.Op != token.MUL || deref.X == nil || deref.Type() == nil {
		return false
	}
	conversion, ok := deref.X.(*ssa.SliceToArrayPointer)
	if !ok || conversion.Pos() != token.NoPos || deref.Pos() == token.NoPos {
		return false
	}
	pointer, ok := types.Unalias(conversion.Type()).Underlying().(*types.Pointer)
	if !ok || !types.Identical(pointer.Elem(), deref.Type()) {
		return false
	}
	length, exact := ssaExactSliceToArrayPointerLen(conversion)
	return exact && length == 0
}

func (a *ssaExactNoUnwindAnalysis) call(call *ssa.Call) (bool, error) {
	common := call.Common()
	if common == nil {
		return false, nil
	}
	if a.elidedCalls[call] {
		// The frozen frontend emits no call to this declaration. Any helper calls
		// introduced by the intrinsic are represented by independent lowered-call
		// graph edges, so their unwind facts still propagate to the owner. Operand
		// producers remain ordinary SSA instructions and were audited separately.
		a.meaningful = true
		return true, nil
	}
	if builtin, ok := common.Value.(*ssa.Builtin); ok {
		switch builtin.Name() {
		case "Sizeof", "Alignof", "Add":
			a.meaningful = true
			return true, nil
		case "len":
			if ssaExactLenBuiltinNoUnwind(call) {
				a.meaningful = true
				return true, nil
			}
			return false, nil
		case "cap":
			if ssaExactCapBuiltinNoUnwind(call) {
				a.meaningful = true
				return true, nil
			}
			return false, nil
		default:
			return false, nil
		}
	}
	if a.trustedInlineNoUnwind[call] {
		// The occurrence was already frozen against the target-owned selected
		// contract. ReentryNone proves that this exact direct C invocation cannot
		// initiate a managed Go unwind; the target's conservative default lane
		// remains unchanged for every other call site.
		a.meaningful = true
		return true, nil
	}
	if rawTarget := common.StaticCallee(); rawTarget != nil {
		target, resolved, err := a.canonicalizer.resolve(rawTarget)
		if err != nil {
			return false, err
		}
		if !resolved || target == nil || (!a.bodySet[target] && !a.leaves[target]) {
			return false, nil
		}
		if a.trustedLocal && a.bodySet[target] {
			dependencies, exact, err := a.contextualStaticCall(target, call)
			if err != nil {
				return false, err
			}
			if exact {
				for dependency := range dependencies {
					a.dependencies[dependency] = struct{}{}
				}
				a.contextualCalls[call] = dependencies
				a.meaningful = true
				return true, nil
			}
		}
		a.dependencies[target] = struct{}{}
		a.meaningful = true
		return true, nil
	}
	certificate, certified := a.closedCalls[call]
	if !certified || common.IsInvoke() || len(certificate.Targets) > 1 {
		return false, nil
	}
	if certificate.MayBeNil && !a.valueProvenNonNil(common.Value, call) {
		// A nil-only closed value is safe only because this non-nil branch is
		// unreachable. The same guard is required for a nullable singleton.
		return false, nil
	}
	if len(certificate.Targets) == 0 {
		if !certificate.MayBeNil {
			return false, nil
		}
	} else {
		target := certificate.Targets[0]
		if target == nil || (!a.bodySet[target] && !a.leaves[target]) {
			return false, nil
		}
		a.dependencies[target] = struct{}{}
	}
	a.meaningful = true
	return true, nil
}

// contextualStaticCall proves the narrow case where a static callee's global
// no-unwind result is conservative only because one of its pointer parameters
// may be nil. The proof substitutes only arguments that this exact call site
// already proves non-nil, scans the complete callee body with the ordinary
// strict recipe, and returns the callee's real dependencies to the outer fixed
// point. It does not inherit trustedLocal, recurse context-sensitively, or
// suppress an explicit panic/open call.
func (a *ssaExactNoUnwindAnalysis) contextualStaticCall(
	target *ssa.Function,
	call *ssa.Call,
) (map[*ssa.Function]struct{}, bool, error) {
	if a == nil || target == nil || call == nil || call.Common() == nil ||
		len(target.Blocks) == 0 || len(target.Params) != len(call.Common().Args) {
		return nil, false, nil
	}
	assumed := make(map[ssa.Value]bool)
	for index, parameter := range target.Params {
		if parameter == nil || !a.pointerProvenNonNil(call.Common().Args[index], call) {
			continue
		}
		assumed[parameter] = true
	}
	if len(assumed) == 0 {
		return nil, false, nil
	}
	nested := newSSAExactNoUnwindAnalysis(
		target,
		a.bodySet,
		a.leaves,
		a.closedCalls,
		a.trustedInlineNoUnwind,
		a.elidedCalls,
		a.safeFixedArrayIndexes,
		a.canonicalizer,
	)
	nested.assumedNonNil = assumed
	exact, err := nested.scan()
	if err != nil || !exact {
		return nil, false, err
	}
	return nested.dependencies, true, nil
}

func noUnwindBinOp(operation *ssa.BinOp) bool {
	if operation == nil {
		return false
	}
	switch operation.Op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ,
		token.AND, token.OR, token.XOR, token.AND_NOT:
		return true
	case token.ADD, token.SUB, token.MUL:
		// Go integer overflow is defined modulo the type width and these three
		// operations have no zero-divisor or negative-shift fault edge. Keep the
		// type gate exact so string concatenation cannot inherit this scalar rule.
		return noUnwindIntegerType(operation.X.Type()) &&
			noUnwindIntegerType(operation.Y.Type()) &&
			noUnwindIntegerType(operation.Type())
	case token.QUO, token.REM:
		// Integer division and remainder have exactly one implicit fault edge:
		// a zero divisor. Bind the proof to the actual SSA constant rather than
		// relying on dominance or an optimizer. Go defines signed min/-1 without
		// an unwind, so every exact non-zero integer constant is sufficient.
		return noUnwindIntegerType(operation.X.Type()) &&
			noUnwindIntegerType(operation.Y.Type()) &&
			noUnwindIntegerType(operation.Type()) &&
			noUnwindNonZeroIntegerConstant(operation.Y)
	case token.SHL, token.SHR:
		// Go integer shifts cannot divide by zero or overflow. Their only
		// implicit fault edge is a negative runtime count, so accept an exact
		// non-negative constant or a dynamically unsigned count and keep signed
		// dynamic counts fail-closed.
		return noUnwindIntegerType(operation.X.Type()) &&
			noUnwindIntegerType(operation.Y.Type()) &&
			noUnwindIntegerType(operation.Type()) &&
			noUnwindShiftCount(operation.Y)
	default:
		return false
	}
}

func noUnwindNonZeroIntegerConstant(value ssa.Value) bool {
	exact, ok := value.(*ssa.Const)
	return ok && exact.Value != nil && exact.Value.Kind() == constant.Int && constant.Sign(exact.Value) != 0
}

func noUnwindShiftCount(value ssa.Value) bool {
	if value == nil {
		return false
	}
	if exact, ok := value.(*ssa.Const); ok {
		return exact.Value != nil && constant.Sign(exact.Value) >= 0
	}
	basic, ok := types.Unalias(value.Type()).Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsInteger != 0 && basic.Info()&types.IsUnsigned != 0
}

func noUnwindIntegerType(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsInteger != 0
}

func noUnwindConversion(from, to types.Type) bool {
	return noUnwindConversionType(from) && noUnwindConversionType(to)
}

func noUnwindConversionType(typ types.Type) bool {
	underlying := types.Unalias(typ).Underlying()
	if _, ok := underlying.(*types.Pointer); ok {
		return true
	}
	basic, ok := underlying.(*types.Basic)
	if !ok {
		return false
	}
	return basic.Kind() == types.UnsafePointer || basic.Info()&types.IsInteger != 0
}

func (a *ssaExactNoUnwindAnalysis) pointerProvenNonNil(value ssa.Value, use ssa.Instruction) bool {
	value = noUnwindStripConversions(value)
	if a.assumedNonNil[value] {
		return true
	}
	switch value := value.(type) {
	case *ssa.Global, *ssa.Alloc:
		// Alloc itself is outside the recipe, but recognizing its result here
		// keeps the pointer rule local and does not make an allocating body pass.
		return true
	case *ssa.FieldAddr:
		return a.pointerProvenNonNil(value.X, value)
	case *ssa.IndexAddr:
		// An in-bounds element of a non-nil fixed array has a non-nil address.
		// Repeat the exact local proof here because loads and field-addresses use
		// the IndexAddr result as a pointer after the IndexAddr instruction itself
		// has been audited.
		return a.fixedArrayIndexProven(value.X, value.Index, value, use)
	}
	return a.valueProvenNonNil(value, use)
}

// fixedArrayIndexProven accepts only an exact fixed-array representation and
// an index whose complete SSA path proves 0 <= index < len(array). Slice,
// string, and dynamically bounded accesses remain outside the no-unwind
// recipe. A pointer-to-array base additionally needs the ordinary non-nil
// pointer proof.
func (a *ssaExactNoUnwindAnalysis) fixedArrayIndexProven(
	base, index ssa.Value,
	operation ssa.Instruction,
	use ssa.Instruction,
) bool {
	bound, pointerBase, ok := ssaExactFixedArrayBound(base)
	frozenBound, frozen := a.safeFixedArrayIndexes[operation]
	if !ok || !frozen || frozenBound != bound || bound <= 0 || index == nil ||
		operation == nil || operation.Parent() != a.fn || use == nil || use.Block() == nil {
		return false
	}
	if pointerBase && !a.pointerProvenNonNil(base, operation) {
		return false
	}
	return ProveSSAExactSafeFixedArrayIndex(a.fn, index, bound, operation)
}

func (a *ssaExactNoUnwindAnalysis) valueProvenNonNil(value ssa.Value, use ssa.Instruction) bool {
	useBlock := a.blocks[use]
	if useBlock == nil {
		return false
	}
	for _, block := range a.fn.Blocks {
		if len(block.Instrs) == 0 || len(block.Succs) != 2 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		subject, nonNilOnTrue, ok := noUnwindNilComparison(branch.Cond)
		if !ok || !a.sameGuardSubject(subject, value) {
			continue
		}
		successor := block.Succs[1]
		if nonNilOnTrue {
			successor = block.Succs[0]
		}
		if !successor.Dominates(useBlock) {
			continue
		}
		if !sameNoUnwindFieldLoad(subject, value) || a.guardValueStable(successor, useBlock, use) {
			return true
		}
	}
	return false
}

func noUnwindNilComparison(value ssa.Value) (subject ssa.Value, nonNilOnTrue bool, ok bool) {
	operation, ok := value.(*ssa.BinOp)
	if !ok || operation.Op != token.EQL && operation.Op != token.NEQ {
		return nil, false, false
	}
	if constant, nilValue := operation.X.(*ssa.Const); nilValue && constant.IsNil() {
		return operation.Y, operation.Op == token.NEQ, true
	}
	if constant, nilValue := operation.Y.(*ssa.Const); nilValue && constant.IsNil() {
		return operation.X, operation.Op == token.NEQ, true
	}
	return nil, false, false
}

func (a *ssaExactNoUnwindAnalysis) sameGuardSubject(left, right ssa.Value) bool {
	left = noUnwindStripConversions(left)
	right = noUnwindStripConversions(right)
	return left == right || sameNoUnwindFieldLoad(left, right)
}

func noUnwindStripConversions(value ssa.Value) ssa.Value {
	for {
		switch converted := value.(type) {
		case *ssa.Convert:
			if !noUnwindConversion(converted.X.Type(), converted.Type()) {
				return value
			}
			value = converted.X
		case *ssa.ChangeType:
			if !noUnwindConversion(converted.X.Type(), converted.Type()) {
				return value
			}
			value = converted.X
		default:
			return value
		}
	}
}

func sameNoUnwindFieldLoad(left, right ssa.Value) bool {
	leftLoad, ok := noUnwindFieldLoad(left)
	if !ok {
		return false
	}
	rightLoad, ok := noUnwindFieldLoad(right)
	if !ok {
		return false
	}
	return leftLoad.Field == rightLoad.Field &&
		noUnwindStripConversions(leftLoad.X) == noUnwindStripConversions(rightLoad.X)
}

func noUnwindFieldLoad(value ssa.Value) (*ssa.FieldAddr, bool) {
	load, ok := noUnwindStripConversions(value).(*ssa.UnOp)
	if !ok || load.Op != token.MUL {
		return nil, false
	}
	field, ok := noUnwindStripConversions(load.X).(*ssa.FieldAddr)
	return field, ok
}

// guardValueStable proves that a field value checked by one load cannot be
// clobbered before the equivalent load used at the call. It rejects every
// store and every potentially effectful call on a path from the guarded edge
// to the use; this is intentionally more conservative than alias analysis.
func (a *ssaExactNoUnwindAnalysis) guardValueStable(start, useBlock *ssa.BasicBlock, use ssa.Instruction) bool {
	canReachUse := make(map[*ssa.BasicBlock]bool)
	queue := []*ssa.BasicBlock{useBlock}
	canReachUse[useBlock] = true
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		for _, predecessor := range block.Preds {
			if !canReachUse[predecessor] {
				canReachUse[predecessor] = true
				queue = append(queue, predecessor)
			}
		}
	}
	if !canReachUse[start] {
		return false
	}
	seen := make(map[*ssa.BasicBlock]bool)
	queue = []*ssa.BasicBlock{start}
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		if seen[block] || !canReachUse[block] {
			continue
		}
		seen[block] = true
		for _, instruction := range block.Instrs {
			if instruction == use {
				break
			}
			if noUnwindMayClobberMemory(instruction) {
				return false
			}
		}
		if block == useBlock {
			continue
		}
		for _, successor := range block.Succs {
			queue = append(queue, successor)
		}
	}
	return seen[useBlock]
}

func noUnwindMayClobberMemory(instruction ssa.Instruction) bool {
	switch instruction := instruction.(type) {
	case *ssa.Store, *ssa.MapUpdate, *ssa.Send:
		return true
	case ssa.CallInstruction:
		if call, ok := instruction.(*ssa.Call); ok {
			if builtin, ok := call.Common().Value.(*ssa.Builtin); ok {
				switch builtin.Name() {
				case "Sizeof", "Alignof", "Add":
					return false
				case "len":
					return !ssaExactLenBuiltinNoUnwind(call)
				case "cap":
					return !ssaExactCapBuiltinNoUnwind(call)
				}
			}
		}
		return true
	default:
		return false
	}
}

// ssaExactLenBuiltinNoUnwind mirrors the Go len operand classes without
// trusting an interface or unresolved type parameter. Parameterized map,
// slice, channel, array, or pointer-to-array types are exact because their
// outer representation and len operation are fixed independently of element
// types. A bare type parameter can span distinct lowerings and stays closed.
func ssaExactLenBuiltinNoUnwind(call *ssa.Call) bool {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 1 || call.Type() == nil {
		return false
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "len" || !types.Identical(call.Type(), types.Typ[types.Int]) {
		return false
	}
	return ssaExactLenOperandNoUnwind(call.Common().Args[0].Type())
}

func ssaExactLenOperandNoUnwind(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch underlying := types.Unalias(typ).Underlying().(type) {
	case *types.Array, *types.Slice, *types.Map, *types.Chan:
		return true
	case *types.Basic:
		return underlying.Kind() == types.String
	case *types.Pointer:
		_, array := types.Unalias(underlying.Elem()).Underlying().(*types.Array)
		return array
	default:
		return false
	}
}

// ssaExactCapBuiltinNoUnwind mirrors cap's exact outer representations. The
// element type and channel direction do not affect the operation, while a bare
// type parameter or interface remains unresolved and therefore fails closed.
func ssaExactCapBuiltinNoUnwind(call *ssa.Call) bool {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 1 || call.Type() == nil {
		return false
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "cap" || !types.Identical(call.Type(), types.Typ[types.Int]) {
		return false
	}
	return ssaExactCapOperandNoUnwind(call.Common().Args[0].Type())
}

func ssaExactCapOperandNoUnwind(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch underlying := types.Unalias(typ).Underlying().(type) {
	case *types.Array, *types.Slice, *types.Chan:
		return true
	case *types.Pointer:
		_, array := types.Unalias(underlying.Elem()).Underlying().(*types.Array)
		return array
	default:
		return false
	}
}

func (a *ssaExactNoUnwindAnalysis) onlyUsedAsNonEvaluatedOperand(value ssa.Value) bool {
	switch a.nonEvalMemo[value] {
	case 1: // visiting: a cyclic Phi is not an exact non-evaluated slice.
		return false
	case 2:
		return true
	case 3:
		return false
	}
	a.nonEvalMemo[value] = 1
	referrers := value.Referrers()
	if referrers == nil || len(*referrers) == 0 {
		a.nonEvalMemo[value] = 3
		return false
	}
	semanticReferrer := false
	for _, referrer := range *referrers {
		if _, debug := referrer.(*ssa.DebugRef); debug {
			continue
		}
		semanticReferrer = true
		if call, ok := referrer.(*ssa.Call); ok {
			if builtin, ok := call.Common().Value.(*ssa.Builtin); ok &&
				(builtin.Name() == "Sizeof" || builtin.Name() == "Alignof") {
				continue
			}
		}
		referrerValue, ok := referrer.(ssa.Value)
		if !ok || !a.onlyUsedAsNonEvaluatedOperand(referrerValue) {
			a.nonEvalMemo[value] = 3
			return false
		}
	}
	if !semanticReferrer {
		a.nonEvalMemo[value] = 3
		return false
	}
	a.nonEvalMemo[value] = 2
	return true
}
