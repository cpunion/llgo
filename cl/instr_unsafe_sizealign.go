//go:build !llgo

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
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func unsafeLayoutCall(instruction ssa.Instruction) (*ssa.Call, bool) {
	call, ok := instruction.(*ssa.Call)
	if !ok || len(call.Call.Args) != 1 {
		return nil, false
	}
	builtin, ok := call.Call.Value.(*ssa.Builtin)
	if !ok {
		return nil, false
	}
	switch builtin.Name() {
	case "Sizeof", "Alignof", "Offsetof":
		return call, true
	default:
		return nil, false
	}
}

// unsafeLayoutStorageRoot recognizes a compiler-created local aggregate whose
// initialization can belong entirely to an unevaluated layout operand. It
// deliberately excludes arbitrary pointers: suppressing a store through a
// caller-owned pointer would require source-expression ownership, not merely
// SSA value reachability.
func unsafeLayoutStorageRoot(value ssa.Value, candidates map[ssa.Instruction]none) ssa.Instruction {
	seen := make(map[ssa.Value]none)
	var visit func(ssa.Value) ssa.Instruction
	visit = func(value ssa.Value) ssa.Instruction {
		if value == nil {
			return nil
		}
		if _, duplicate := seen[value]; duplicate {
			return nil
		}
		seen[value] = none{}
		switch value := value.(type) {
		case *ssa.Alloc, *ssa.MakeMap, *ssa.MakeSlice:
			instruction := value.(ssa.Instruction)
			if _, owned := candidates[instruction]; owned {
				return instruction
			}
		case *ssa.FieldAddr:
			return visit(value.X)
		case *ssa.IndexAddr:
			return visit(value.X)
		case *ssa.Slice:
			return visit(value.X)
		case *ssa.ChangeType:
			return visit(value.X)
		case *ssa.Convert:
			return visit(value.X)
		case *ssa.Phi:
			var root ssa.Instruction
			for _, edge := range value.Edges {
				candidate := visit(edge)
				if candidate == nil || root != nil && candidate != root {
					return nil
				}
				root = candidate
			}
			return root
		}
		return nil
	}
	return visit(value)
}

// collectUnsafeLayoutUnevaluatedSSA returns the SSA instructions that only
// exist to form an operand of unsafe.Sizeof, unsafe.Alignof, or unsafe.Offsetof.
// x/tools/go/ssa deliberately retains those value-producing instructions (for
// example *p or a composite-literal field), even though the Go specification
// says that the operand is not evaluated.
//
// Start with the complete backwards value graph of every layout builtin
// operand, then compute the greatest subset whose uses are all either inside
// that graph or exact layout-builtin argument edges. Pruning is important when
// one SSA value is shared with a real use: the real use and all dependencies
// needed by it must still be emitted. DebugRefs are non-semantic uses and are
// omitted when the value they describe is omitted.
func collectUnsafeLayoutUnevaluatedSSA(fn *ssa.Function) map[ssa.Instruction]none {
	if fn == nil {
		return nil
	}
	candidates := make(map[ssa.Instruction]none)
	var addDependencies func(ssa.Value)
	addDependencies = func(value ssa.Value) {
		instruction, ok := value.(ssa.Instruction)
		if !ok {
			return
		}
		if _, exists := candidates[instruction]; exists {
			return
		}
		candidates[instruction] = none{}
		for _, operand := range instruction.Operands(nil) {
			if operand != nil && *operand != nil {
				addDependencies(*operand)
			}
		}
	}
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := unsafeLayoutCall(instruction); ok {
				addDependencies(call.Call.Args[0])
			}
		}
	}

	// A composite literal is represented as a local allocation plus Stores.
	// Those Stores are not value dependencies of the final load, so discover
	// mutations of candidate-owned storage explicitly and pull in their value
	// producers. The closure also covers map/slice literals nested in a field.
	mutationRoots := make(map[ssa.Instruction]ssa.Instruction)
	for changed := true; changed; {
		changed = false
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if _, known := mutationRoots[instruction]; known {
					continue
				}
				var storage ssa.Value
				switch mutation := instruction.(type) {
				case *ssa.Store:
					storage = mutation.Addr
				case *ssa.MapUpdate:
					storage = mutation.Map
				default:
					continue
				}
				root := unsafeLayoutStorageRoot(storage, candidates)
				if root == nil {
					continue
				}
				mutationRoots[instruction] = root
				candidates[instruction] = none{}
				for _, operand := range instruction.Operands(nil) {
					if operand != nil && *operand != nil {
						addDependencies(*operand)
					}
				}
				changed = true
			}
		}
	}

	for changed := true; changed; {
		changed = false
		for instruction := range candidates {
			value, ok := instruction.(ssa.Value)
			if !ok {
				if root, mutation := mutationRoots[instruction]; mutation {
					if _, rootOwned := candidates[root]; rootOwned {
						continue
					}
				}
				delete(candidates, instruction)
				changed = true
				continue
			}
			referrers := value.Referrers()
			if referrers == nil {
				delete(candidates, instruction)
				changed = true
				continue
			}
			for _, referrer := range *referrers {
				if _, ok := referrer.(*ssa.DebugRef); ok {
					continue
				}
				if call, ok := unsafeLayoutCall(referrer); ok && call.Call.Args[0] == value {
					continue
				}
				if _, ok := candidates[referrer]; ok {
					continue
				}
				delete(candidates, instruction)
				changed = true
				break
			}
		}
	}

	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			debug, ok := instruction.(*ssa.DebugRef)
			if !ok {
				continue
			}
			if valueInstruction, ok := debug.X.(ssa.Instruction); ok {
				if _, omitted := candidates[valueInstruction]; omitted {
					candidates[debug] = none{}
				}
			}
		}
	}
	return candidates
}

// freezeUnsafeLayoutUnevaluatedSSA records the one exact type-only lowering
// slice for fn while the emission universe is still being constructed.  The
// returned map is universe-owned and must be treated as immutable.
func (u *EmissionUniverse) freezeUnsafeLayoutUnevaluatedSSA(fn *ssa.Function) map[ssa.Instruction]none {
	if u == nil || fn == nil {
		return nil
	}
	if frozen, ok := u.unsafeLayoutUnevaluated[fn]; ok {
		return frozen
	}
	if u.unsafeLayoutUnevaluated == nil {
		u.unsafeLayoutUnevaluated = make(map[*ssa.Function]map[ssa.Instruction]none)
	}
	frozen := collectUnsafeLayoutUnevaluatedSSA(fn)
	u.unsafeLayoutUnevaluated[fn] = frozen
	return frozen
}

// frozenUnsafeLayoutUnevaluatedSSA returns the already frozen exact set. It
// deliberately does not recompute: active coroutine consumers must agree with
// the inventory that constructed the whole-program plan.
func (u *EmissionUniverse) frozenUnsafeLayoutUnevaluatedSSA(fn *ssa.Function) (map[ssa.Instruction]none, bool) {
	if u == nil || fn == nil {
		return nil, false
	}
	frozen, ok := u.unsafeLayoutUnevaluated[fn]
	return frozen, ok
}

func (p *context) compileUnsafeSizeAlignBuiltin(name string, arg ssa.Value) llssa.Expr {
	typ := p.type_(types.Default(arg.Type()), llssa.InGo)
	resultType := p.prog.Uintptr()
	switch name {
	case "Sizeof":
		return p.prog.IntVal(p.prog.SizeOf(typ), resultType)
	case "Alignof":
		return p.prog.IntVal(p.prog.AlignOf(typ), resultType)
	default:
		panic("compileUnsafeSizeAlignBuiltin called for a different builtin")
	}
}
