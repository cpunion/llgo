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
	"go/constant"
	"go/token"
	"go/types"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// coroSemanticInstructionPlan is the owner-scoped, pre-analysis recipe for
// one source instruction. It is deliberately smaller than Go SSA: operands,
// results, Phi edges and ordinary CFG remain owned by x/tools. The recipe is
// the single production authority for local Effect/Exec and for the semantic
// identity later copied into LoweringFacts and the physical function plan.
type coroSemanticInstructionPlan struct {
	class        coro.OpClass
	recipe       coro.RecipeID
	effect       coro.Effect
	exec         coro.ExecFlags
	materialized bool
	debug        bool
	evaluated    bool
	// outcomePlainLeaf records that this exact semantic recipe can execute in
	// a bounded synchronous outcome body without allocating, calling a helper,
	// parking, or introducing an unmodelled implicit panic edge. It is decided
	// only here, while classifying raw SSA, and is consumed from the frozen
	// ProgramIR by analysis and physical emission.
	outcomePlainLeaf bool
	// staticOutcome records that this local instruction can participate in an
	// unbounded synchronous outcome twin. Calls still require a whole-program
	// exact-target proof; this bit only closes the local lowering vocabulary.
	staticOutcome bool
}

func coroOutcomePlainBasicInfo(typ types.Type) types.BasicInfo {
	if typ == nil {
		return 0
	}
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	if !ok {
		return 0
	}
	return basic.Info()
}

func coroOutcomePlainPointerLike(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch underlying := types.Unalias(typ).Underlying().(type) {
	case *types.Pointer, *types.Chan, *types.Map, *types.Signature, *types.Slice:
		return true
	case *types.Basic:
		return underlying.Kind() == types.UnsafePointer
	default:
		return false
	}
}

// coroOutcomePlainScalarBinOp admits only operators whose LLSSA lowering is a
// closed scalar LLVM expression. String/interface/aggregate comparison and
// unproven division/shift operations are intentionally excluded: those
// recipes can introduce runtime helpers or source-language panic edges.
func coroOutcomePlainScalarBinOp(instruction *ssa.BinOp) bool {
	if instruction == nil {
		return false
	}
	if _, folded := foldConstComparison(instruction); folded {
		return true
	}
	info := coroOutcomePlainBasicInfo(instruction.X.Type())
	switch instruction.Op {
	case token.ADD, token.SUB, token.MUL:
		return info&(types.IsInteger|types.IsFloat|types.IsComplex) != 0
	case token.QUO:
		return info&types.IsFloat != 0 ||
			(info&types.IsInteger != 0 && ssaIntegerValueProvenNonZeroAt(instruction.Y, instruction))
	case token.REM:
		return info&types.IsInteger != 0 && ssaIntegerValueProvenNonZeroAt(instruction.Y, instruction)
	case token.AND, token.OR, token.XOR, token.AND_NOT:
		return info&types.IsInteger != 0
	case token.SHL, token.SHR:
		return info&types.IsInteger != 0 && coroOutcomePlainNonNegativeShiftCount(instruction.Y)
	case token.EQL, token.NEQ:
		return info&(types.IsBoolean|types.IsInteger|types.IsFloat|types.IsComplex) != 0 ||
			coroOutcomePlainPointerLike(instruction.X.Type())
	case token.LSS, token.LEQ, token.GTR, token.GEQ:
		return info&(types.IsInteger|types.IsFloat) != 0
	default:
		return false
	}
}

func coroOutcomePlainNonNegativeShiftCount(value ssa.Value) bool {
	if value == nil {
		return false
	}
	if coroOutcomePlainBasicInfo(value.Type())&types.IsUnsigned != 0 {
		return true
	}
	literal, ok := value.(*ssa.Const)
	return ok && literal.Value != nil && constant.Sign(literal.Value) >= 0
}

func coroOutcomePlainScalarUnOp(instruction *ssa.UnOp) bool {
	if instruction == nil {
		return false
	}
	info := coroOutcomePlainBasicInfo(instruction.X.Type())
	switch instruction.Op {
	case token.MUL:
		// The physical SitePlan owns the exact non-nil proof or the
		// allocation-free explicit-status nil-fault edge.
		_, ok := types.Unalias(instruction.X.Type()).Underlying().(*types.Pointer)
		return ok
	case token.SUB:
		return info&(types.IsInteger|types.IsFloat|types.IsComplex) != 0
	case token.XOR:
		return info&types.IsInteger != 0
	case token.NOT:
		return info&types.IsBoolean != 0
	default:
		return false
	}
}

func coroOutcomePlainScalarChangeType(instruction *ssa.ChangeType) bool {
	if instruction == nil {
		return false
	}
	return (coroOutcomePlainBasicInfo(instruction.X.Type()) != 0 &&
		coroOutcomePlainBasicInfo(instruction.Type()) != 0) ||
		(coroOutcomePlainPointerLike(instruction.X.Type()) &&
			coroOutcomePlainPointerLike(instruction.Type()))
}

func coroOutcomePlainScalarConvert(instruction *ssa.Convert) bool {
	if instruction == nil {
		return false
	}
	from := coroOutcomePlainBasicInfo(instruction.X.Type())
	to := coroOutcomePlainBasicInfo(instruction.Type())
	if from&(types.IsInteger|types.IsFloat) != 0 &&
		to&(types.IsInteger|types.IsFloat) != 0 {
		return true
	}
	fromInline := from&types.IsInteger != 0 || coroOutcomePlainPointerLike(instruction.X.Type())
	toInline := to&types.IsInteger != 0 || coroOutcomePlainPointerLike(instruction.Type())
	return fromInline && toInline
}

// planCoroSemanticInstruction is the only raw-SSA semantic recipe classifier.
// It runs while the emission closure is still open. Analysis, preflight and
// emission consume the frozen result and must not repeat this switch.
func planCoroSemanticInstruction(instruction ssa.Instruction) (plan coroSemanticInstructionPlan, err error) {
	// A raw instruction handed to this classifier is part of the evaluated
	// source program. Frontend-only, unevaluated operands are represented by
	// freezeSemanticInstruction with their own explicit recipe instead. Keep
	// this invariant here so structural preflight callers cannot silently skip
	// every instruction merely because they do not have an owner-scoped frozen
	// ProgramIR.
	defer func() {
		if err == nil {
			plan.evaluated = true
		}
	}()
	ordinary := func(recipe string) (coroSemanticInstructionPlan, error) {
		return coroSemanticInstructionPlan{
			class:         coro.OpPure,
			recipe:        coro.RecipeID(recipe),
			effect:        coro.NoSuspend,
			staticOutcome: true,
		}, nil
	}
	leaf := func(recipe string, safe bool) (coroSemanticInstructionPlan, error) {
		plan, err := ordinary(recipe)
		plan.outcomePlainLeaf = safe
		// The bounded leaf vocabulary is intentionally narrower than the
		// synchronous outcome vocabulary. Operations such as string arithmetic,
		// interface comparison, and checked integer arithmetic may lower through
		// exact runtime helpers or explicit fault edges, but neither fact makes the
		// source operation suspend. Static-outcome closure accounts for those helper
		// calls independently, so do not erase the ordinary synchronous capability
		// merely because this instruction is not an allocation-free atomic leaf.
		return plan, err
	}
	control := func(recipe string, exec coro.ExecFlags) (coroSemanticInstructionPlan, error) {
		return coroSemanticInstructionPlan{
			class:        coro.OpControl,
			recipe:       coro.RecipeID(recipe),
			effect:       coro.NoSuspend,
			exec:         exec,
			materialized: true,
		}, nil
	}
	if instruction == nil {
		return coroSemanticInstructionPlan{}, fmt.Errorf("semantic instruction plan requires one source instruction")
	}
	switch instruction := instruction.(type) {
	case *ssa.Alloc:
		return ordinary("cl.ssa.alloc.v1")
	case *ssa.Phi:
		return leaf("cl.ssa.phi.v1", true)
	case *ssa.Call:
		plan, err := ordinary("cl.ssa.call.v1")
		plan.staticOutcome = true
		if err != nil {
			return plan, err
		}
		if common := instruction.Common(); common != nil {
			if builtin, ok := common.Value.(*ssa.Builtin); ok && builtin.Name() == "panic" {
				plan.class = coro.OpControl
				plan.exec = coro.MayUnwind
				plan.materialized = true
				plan.recipe = coro.RecipeID("cl.ssa.builtin-panic.v0")
				plan.staticOutcome = true
			}
		}
		return plan, nil
	case *ssa.BinOp:
		return leaf("cl.ssa.binop.v1", coroOutcomePlainScalarBinOp(instruction))
	case *ssa.UnOp:
		if instruction.Op == token.ARROW {
			return coroSemanticInstructionPlan{
				class:        coro.OpChannel,
				recipe:       coro.RecipeID("cl.ssa.channel-recv.v0"),
				effect:       coro.MayPark,
				materialized: true,
			}, nil
		}
		return leaf("cl.ssa.unop.v1", coroOutcomePlainScalarUnOp(instruction))
	case *ssa.ChangeType:
		return leaf("cl.ssa.change-type.v1", coroOutcomePlainScalarChangeType(instruction))
	case *ssa.Convert:
		return leaf("cl.ssa.convert.v1", coroOutcomePlainScalarConvert(instruction))
	case *ssa.MultiConvert:
		return ordinary("cl.ssa.multi-convert.v1")
	case *ssa.ChangeInterface:
		return ordinary("cl.ssa.change-interface.v1")
	case *ssa.SliceToArrayPointer:
		return ordinary("cl.ssa.slice-to-array-pointer.v1")
	case *ssa.MakeInterface:
		plan, err := ordinary("cl.ssa.make-interface.v1")
		// The source operation itself is synchronous. Any backing allocation or
		// itab construction remains an explicit owner-scoped lowered-call edge;
		// static-outcome planning closes those helpers independently.
		plan.staticOutcome = true
		return plan, err
	case *ssa.MakeClosure:
		return ordinary("cl.ssa.make-closure.v1")
	case *ssa.MakeMap:
		return ordinary("cl.ssa.make-map.v1")
	case *ssa.MakeChan:
		return ordinary("cl.ssa.make-chan.v1")
	case *ssa.MakeSlice:
		return ordinary("cl.ssa.make-slice.v1")
	case *ssa.Slice:
		return ordinary("cl.ssa.slice.v1")
	case *ssa.FieldAddr:
		return leaf("cl.ssa.field-addr.v1", true)
	case *ssa.Field:
		return leaf("cl.ssa.field.v1", true)
	case *ssa.IndexAddr:
		return ordinary("cl.ssa.index-addr.v1")
	case *ssa.Index:
		return ordinary("cl.ssa.index.v1")
	case *ssa.Lookup:
		return ordinary("cl.ssa.lookup.v1")
	case *ssa.Select:
		effect := coro.NoSuspend
		recipe := "cl.ssa.select.v0"
		if instruction.Blocking {
			effect = coro.MayPark
		}
		return coroSemanticInstructionPlan{
			class:  coro.OpSelect,
			recipe: coro.RecipeID(recipe),
			effect: effect,
			// The current select helper ABI still samples owner-local completion
			// through the installed runtime G. Single-channel send/receive and
			// close already carry an explicit task and need no such seed.
			exec:         coro.NeedsRuntimeContext,
			materialized: true,
		}, nil
	case *ssa.Range:
		return ordinary("cl.ssa.range.v1")
	case *ssa.Next:
		return ordinary("cl.ssa.next.v1")
	case *ssa.TypeAssert:
		return ordinary("cl.ssa.type-assert.v1")
	case *ssa.Extract:
		return leaf("cl.ssa.extract.v1", true)
	case *ssa.Jump:
		return leaf("cl.ssa.jump.v1", true)
	case *ssa.If:
		return leaf("cl.ssa.if.v1", true)
	case *ssa.Return:
		plan, err := control("cl.ssa.return.v1", 0)
		plan.materialized = false
		plan.outcomePlainLeaf = true
		plan.staticOutcome = true
		return plan, err
	case *ssa.RunDefers:
		plan, err := control("cl.ssa.run-defers.v0", coro.NeedsCleanupFrame)
		// Whole-function projection admits this only when no evaluated Defer site
		// remains. In that case x/tools' synthetic RunDefers is a no-op and the
		// physical static-outcome recipe removes it explicitly.
		plan.staticOutcome = true
		return plan, err
	case *ssa.Panic:
		plan, err := control("cl.ssa.panic.v0", coro.MayUnwind)
		plan.outcomePlainLeaf = true
		plan.staticOutcome = true
		return plan, err
	case *ssa.Go:
		return coroSemanticInstructionPlan{
			class:        coro.OpSpawn,
			recipe:       coro.RecipeID("cl.ssa.spawn.v0"),
			effect:       coro.NoSuspend,
			materialized: true,
		}, nil
	case *ssa.Defer:
		return control("cl.ssa.defer.v0", coro.NeedsCleanupFrame)
	case *ssa.Send:
		return coroSemanticInstructionPlan{
			class:        coro.OpChannel,
			recipe:       coro.RecipeID("cl.ssa.channel-send.v0"),
			effect:       coro.MayPark,
			materialized: true,
		}, nil
	case *ssa.Store:
		return leaf("cl.ssa.store.v1", true)
	case *ssa.MapUpdate:
		return ordinary("cl.ssa.map-update.v1")
	case *ssa.DebugRef:
		return coroSemanticInstructionPlan{
			class:  coro.OpPure,
			recipe: coro.RecipeID("cl.ssa.debug-ref.v1"),
			effect: coro.NoSuspend,
			debug:  true,
		}, nil
	default:
		return coroSemanticInstructionPlan{}, fmt.Errorf("unsupported source instruction type %T", instruction)
	}
}

func coroSemanticCFGHasCycle(blocks []*ssa.BasicBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	indegree := make([]int, len(blocks))
	for _, block := range blocks {
		if block == nil {
			continue
		}
		for _, successor := range block.Succs {
			if successor == nil || successor.Index < 0 || successor.Index >= len(indegree) {
				return true
			}
			indegree[successor.Index]++
		}
	}
	queue := make([]*ssa.BasicBlock, 0, len(blocks))
	for index, block := range blocks {
		if block != nil && indegree[index] == 0 {
			queue = append(queue, block)
		}
	}
	visited := 0
	for head := 0; head < len(queue); head++ {
		block := queue[head]
		visited++
		for _, successor := range block.Succs {
			indegree[successor.Index]--
			if indegree[successor.Index] == 0 {
				queue = append(queue, successor)
			}
		}
	}
	return visited != len(blocks)
}
