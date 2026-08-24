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
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// SSAImmutableCaptureSnapshot proves that one lexical closure capture may be
// read once when the closure body starts and then transported as an immutable
// value. FreeVar remains the source-level pointer-to-cell identity; Loads is
// the complete set of dereferences which may consume the snapshot instead.
//
// The proof is deliberately narrower than escape analysis. It accepts only a
// compiler-created allocation/phi graph, rejects every address escape, proves
// the closure body only dereferences the cell, and rejects any store reachable
// after closure construction without first executing the originating Alloc
// again. Re-executing an Alloc denotes a fresh Go object, which is what makes
// Go 1.22+ per-iteration loop variables eligible without treating an older
// captured cell as mutable.
type SSAImmutableCaptureSnapshot struct {
	Index     int
	FreeVar   *ssa.FreeVar
	Loads     []*ssa.UnOp
	Producers []*ssa.MakeClosure
}

// ProveSSAImmutableCaptureSnapshots returns the independently proven capture
// snapshots for one source lexical closure. Captures which cannot close the
// proof are simply omitted; callers must preserve their ordinary shared-cell
// representation. The result follows FreeVars order and is deterministic.
func ProveSSAImmutableCaptureSnapshots(function *ssa.Function) []SSAImmutableCaptureSnapshot {
	if function == nil || function.Parent() == nil || function.Synthetic != "" ||
		len(function.FreeVars) == 0 || len(function.Blocks) == 0 {
		return nil
	}
	producers := ssaLexicalClosureProducers(function)
	if len(producers) == 0 {
		return nil
	}
	result := make([]SSAImmutableCaptureSnapshot, 0, len(function.FreeVars))
	for index, free := range function.FreeVars {
		loads, readOnly := ssaCaptureReadOnlyLoads(function, free)
		if !readOnly || len(loads) == 0 {
			continue
		}
		eligible := true
		for _, producer := range producers {
			if index >= len(producer.Bindings) ||
				!types.Identical(producer.Bindings[index].Type(), free.Type()) ||
				!ssaCaptureBindingStable(producer.Bindings[index], function, index, producer) {
				eligible = false
				break
			}
		}
		if eligible {
			result = append(result, SSAImmutableCaptureSnapshot{
				Index: index, FreeVar: free, Loads: loads, Producers: producers,
			})
		}
	}
	return result
}

func ssaLexicalClosureProducers(target *ssa.Function) []*ssa.MakeClosure {
	parent := target.Parent()
	if parent == nil {
		return nil
	}
	var producers []*ssa.MakeClosure
	for _, block := range parent.Blocks {
		for _, instruction := range block.Instrs {
			closure, ok := instruction.(*ssa.MakeClosure)
			if !ok || closure.Fn != target || len(closure.Bindings) != len(target.FreeVars) {
				continue
			}
			producers = append(producers, closure)
		}
	}
	return producers
}

func ssaCaptureReadOnlyLoads(
	function *ssa.Function,
	free *ssa.FreeVar,
) ([]*ssa.UnOp, bool) {
	if free == nil || free.Parent() != function || free.Referrers() == nil {
		return nil, false
	}
	if _, pointer := types.Unalias(free.Type()).Underlying().(*types.Pointer); !pointer {
		return nil, false
	}
	var loads []*ssa.UnOp
	for _, reference := range *free.Referrers() {
		switch reference := reference.(type) {
		case *ssa.DebugRef:
		case *ssa.UnOp:
			if reference.X != free || reference.Op != token.MUL {
				return nil, false
			}
			loads = append(loads, reference)
		default:
			// Passing &captured, storing through it, nesting it in another
			// closure, or converting it to an opaque address all retain shared
			// cell semantics and therefore fail closed.
			return nil, false
		}
	}
	return loads, true
}

type ssaCaptureAliasProof struct {
	aliases map[ssa.Value]bool
	origins map[ssa.Value]map[*ssa.Alloc]bool
	stores  map[*ssa.Store]map[*ssa.Alloc]bool
}

func ssaCaptureBindingStable(
	binding ssa.Value,
	target *ssa.Function,
	index int,
	requiredProducer *ssa.MakeClosure,
) bool {
	if binding == nil || target == nil || requiredProducer == nil ||
		binding.Parent() != requiredProducer.Parent() || binding.Referrers() == nil {
		return false
	}
	pointer, ok := types.Unalias(binding.Type()).Underlying().(*types.Pointer)
	if !ok || pointer.Elem() == nil {
		return false
	}
	proof, ok := buildSSACaptureAliasProof(binding, target, index)
	if !ok || !proof.aliases[binding] {
		return false
	}
	foundRequired := false
	producers := make(map[*ssa.MakeClosure]bool)
	for alias := range proof.aliases {
		refs := alias.Referrers()
		if refs == nil {
			return false
		}
		for _, reference := range *refs {
			closure, ok := reference.(*ssa.MakeClosure)
			if !ok {
				continue
			}
			bindingIndex, exact := ssaCaptureClosureBindingIndex(closure, alias)
			if !exact || closure.Fn != target || bindingIndex != index {
				return false
			}
			producers[closure] = true
			if closure == requiredProducer {
				foundRequired = true
			}
		}
	}
	if !foundRequired {
		return false
	}
	for store, origins := range proof.stores {
		for producer := range producers {
			for origin := range origins {
				if ssaInstructionReachableWithoutAllocation(producer, store, origin) {
					return false
				}
			}
		}
	}
	return true
}

func buildSSACaptureAliasProof(
	binding ssa.Value,
	target *ssa.Function,
	index int,
) (ssaCaptureAliasProof, bool) {
	proof := ssaCaptureAliasProof{
		aliases: make(map[ssa.Value]bool),
		origins: make(map[ssa.Value]map[*ssa.Alloc]bool),
		stores:  make(map[*ssa.Store]map[*ssa.Alloc]bool),
	}
	parent := binding.Parent()
	queue := []ssa.Value{binding}
	for head := 0; head < len(queue); head++ {
		value := queue[head]
		if value == nil || value.Parent() != parent || value.Referrers() == nil ||
			!types.Identical(value.Type(), binding.Type()) {
			return ssaCaptureAliasProof{}, false
		}
		if proof.aliases[value] {
			continue
		}
		proof.aliases[value] = true
		switch value := value.(type) {
		case *ssa.Alloc:
			if !value.Heap {
				return ssaCaptureAliasProof{}, false
			}
		case *ssa.Phi:
			if len(value.Edges) == 0 {
				return ssaCaptureAliasProof{}, false
			}
			queue = append(queue, value.Edges...)
		default:
			return ssaCaptureAliasProof{}, false
		}
	}

	// Close the graph in the forward direction as well. A fresh cell which
	// flows into another phi can otherwise be mutated or published through
	// that alias without appearing in binding's backward slice.
	for changed := true; changed; {
		changed = false
		for value := range proof.aliases {
			for _, reference := range *value.Referrers() {
				phi, ok := reference.(*ssa.Phi)
				if !ok || !ssaPhiContains(phi, value) || proof.aliases[phi] {
					continue
				}
				if phi.Parent() != parent || !types.Identical(phi.Type(), binding.Type()) {
					return ssaCaptureAliasProof{}, false
				}
				proof.aliases[phi] = true
				changed = true
			}
		}
	}
	for value := range proof.aliases {
		if phi, ok := value.(*ssa.Phi); ok {
			for _, edge := range phi.Edges {
				if !proof.aliases[edge] {
					return ssaCaptureAliasProof{}, false
				}
			}
		}
	}

	// Origin sets are a monotone fixed point over Alloc and Phi nodes. Cyclic
	// loop phis therefore retain exact fresh-allocation provenance without an
	// ad-hoc induction-variable pattern.
	for value := range proof.aliases {
		if allocation, ok := value.(*ssa.Alloc); ok {
			proof.origins[value] = map[*ssa.Alloc]bool{allocation: true}
		} else {
			proof.origins[value] = make(map[*ssa.Alloc]bool)
		}
	}
	for changed := true; changed; {
		changed = false
		for value := range proof.aliases {
			phi, ok := value.(*ssa.Phi)
			if !ok {
				continue
			}
			for _, edge := range phi.Edges {
				for origin := range proof.origins[edge] {
					if !proof.origins[value][origin] {
						proof.origins[value][origin] = true
						changed = true
					}
				}
			}
		}
	}
	for value := range proof.aliases {
		if len(proof.origins[value]) == 0 {
			return ssaCaptureAliasProof{}, false
		}
	}

	for alias := range proof.aliases {
		for _, reference := range *alias.Referrers() {
			switch reference := reference.(type) {
			case *ssa.DebugRef:
			case *ssa.Phi:
				if !proof.aliases[reference] || !ssaPhiContains(reference, alias) {
					return ssaCaptureAliasProof{}, false
				}
			case *ssa.UnOp:
				if reference.X != alias || reference.Op != token.MUL {
					return ssaCaptureAliasProof{}, false
				}
			case *ssa.Store:
				if reference.Addr != alias {
					return ssaCaptureAliasProof{}, false
				}
				proof.stores[reference] = proof.origins[alias]
			case *ssa.MakeClosure:
				bindingIndex, exact := ssaCaptureClosureBindingIndex(reference, alias)
				if !exact || reference.Fn != target || bindingIndex != index {
					return ssaCaptureAliasProof{}, false
				}
			default:
				return ssaCaptureAliasProof{}, false
			}
		}
	}
	return proof, true
}

func ssaPhiContains(phi *ssa.Phi, value ssa.Value) bool {
	if phi == nil || value == nil {
		return false
	}
	for _, edge := range phi.Edges {
		if edge == value {
			return true
		}
	}
	return false
}

func ssaCaptureClosureBindingIndex(closure *ssa.MakeClosure, value ssa.Value) (int, bool) {
	if closure == nil || value == nil {
		return 0, false
	}
	index := -1
	for candidate, binding := range closure.Bindings {
		if binding != value {
			continue
		}
		if index != -1 {
			return 0, false
		}
		index = candidate
	}
	return index, index != -1
}

// ssaInstructionReachableWithoutAllocation asks whether target can execute
// after start while retaining the same allocation instance. Encountering the
// Alloc instruction ends that path because its next execution creates a fresh
// Go object. This instruction-level cut is essential for loop bodies where a
// source allocation and initialization precede the next iteration's closure.
func ssaInstructionReachableWithoutAllocation(
	start, target ssa.Instruction,
	allocation *ssa.Alloc,
) bool {
	if start == nil || target == nil || allocation == nil || start.Parent() == nil ||
		start.Parent() != target.Parent() || start.Parent() != allocation.Parent() ||
		start.Block() == nil || target.Block() == nil || allocation.Block() == nil {
		return true
	}
	startIndex := ssaBlockInstructionIndex(start.Block(), start)
	if startIndex < 0 {
		return true
	}
	type point struct {
		block *ssa.BasicBlock
		index int
	}
	queue := []point{{block: start.Block(), index: startIndex + 1}}
	seen := make(map[point]bool)
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		if current.block == nil || current.index < 0 || current.index > len(current.block.Instrs) || seen[current] {
			continue
		}
		seen[current] = true
		cut := false
		for index := current.index; index < len(current.block.Instrs); index++ {
			instruction := current.block.Instrs[index]
			if instruction == allocation {
				cut = true
				break
			}
			if instruction == target {
				return true
			}
		}
		if cut {
			continue
		}
		for _, successor := range current.block.Succs {
			queue = append(queue, point{block: successor})
		}
	}
	return false
}

func ssaBlockInstructionIndex(block *ssa.BasicBlock, target ssa.Instruction) int {
	if block == nil || target == nil {
		return -1
	}
	for index, instruction := range block.Instrs {
		if instruction == target {
			return index
		}
	}
	return -1
}
