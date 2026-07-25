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
			class:  coro.OpPure,
			recipe: coro.RecipeID(recipe),
			effect: coro.NoSuspend,
		}, nil
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
		return ordinary("cl.ssa.phi.v1")
	case *ssa.Call:
		plan, err := ordinary("cl.ssa.call.v1")
		if err != nil {
			return plan, err
		}
		if common := instruction.Common(); common != nil {
			if builtin, ok := common.Value.(*ssa.Builtin); ok && builtin.Name() == "panic" {
				plan.class = coro.OpControl
				plan.exec = coro.MayUnwind
				plan.materialized = true
				plan.recipe = coro.RecipeID("cl.ssa.builtin-panic.v0")
			}
		}
		return plan, nil
	case *ssa.BinOp:
		return ordinary("cl.ssa.binop.v1")
	case *ssa.UnOp:
		if instruction.Op == token.ARROW {
			return coroSemanticInstructionPlan{
				class:        coro.OpChannel,
				recipe:       coro.RecipeID("cl.ssa.channel-recv.v0"),
				effect:       coro.MayPark,
				materialized: true,
			}, nil
		}
		return ordinary("cl.ssa.unop.v1")
	case *ssa.ChangeType:
		return ordinary("cl.ssa.change-type.v1")
	case *ssa.Convert:
		return ordinary("cl.ssa.convert.v1")
	case *ssa.MultiConvert:
		return ordinary("cl.ssa.multi-convert.v1")
	case *ssa.ChangeInterface:
		return ordinary("cl.ssa.change-interface.v1")
	case *ssa.SliceToArrayPointer:
		return ordinary("cl.ssa.slice-to-array-pointer.v1")
	case *ssa.MakeInterface:
		return ordinary("cl.ssa.make-interface.v1")
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
		return ordinary("cl.ssa.field-addr.v1")
	case *ssa.Field:
		return ordinary("cl.ssa.field.v1")
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
			class:        coro.OpSelect,
			recipe:       coro.RecipeID(recipe),
			effect:       effect,
			materialized: true,
		}, nil
	case *ssa.Range:
		return ordinary("cl.ssa.range.v1")
	case *ssa.Next:
		return ordinary("cl.ssa.next.v1")
	case *ssa.TypeAssert:
		return ordinary("cl.ssa.type-assert.v1")
	case *ssa.Extract:
		return ordinary("cl.ssa.extract.v1")
	case *ssa.Jump:
		return ordinary("cl.ssa.jump.v1")
	case *ssa.If:
		return ordinary("cl.ssa.if.v1")
	case *ssa.Return:
		plan, err := control("cl.ssa.return.v1", 0)
		plan.materialized = false
		return plan, err
	case *ssa.RunDefers:
		return control("cl.ssa.run-defers.v0", coro.NeedsCleanupFrame)
	case *ssa.Panic:
		return control("cl.ssa.panic.v0", coro.MayUnwind)
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
		return ordinary("cl.ssa.store.v1")
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
