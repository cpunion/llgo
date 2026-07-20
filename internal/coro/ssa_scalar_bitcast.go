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

// SSAExactScalarBitcast freezes the one x/tools SSA allocation that is only a
// same-width scalar bit reinterpretation slot. x/tools conservatively marks
// the address-taken parameter Heap even though the complete use graph cannot
// escape. Consumers may lower this exact allocation as local storage; no
// other Heap allocation inherits that capability.
type SSAExactScalarBitcast struct {
	Allocation *ssa.Alloc
}

// ProveSSAExactScalarBitcast accepts only the complete single-block shape
//
//	local source; *local = parameter
//	return *(*target)(unsafe.Pointer(local))
//
// for int32/float32 or int64/float64 in either direction. Exact referrer sets
// rule out stores, calls, arithmetic, extra loads, and result reuse inside the
// transform body.
func ProveSSAExactScalarBitcast(function *ssa.Function) (SSAExactScalarBitcast, bool) {
	var proof SSAExactScalarBitcast
	if function == nil || len(function.Blocks) != 1 || len(function.Params) != 1 || function.Signature == nil ||
		function.Signature.Recv() != nil || function.Signature.Variadic() ||
		function.Signature.Params().Len() != 1 || function.Signature.Results().Len() != 1 {
		return proof, false
	}
	source := function.Signature.Params().At(0).Type()
	target := function.Signature.Results().At(0).Type()
	if !ssaExactScalarBitcastTypes(source, target) || !types.Identical(function.Params[0].Type(), source) {
		return proof, false
	}

	var store *ssa.Store
	var sourcePointer, targetPointer *ssa.Convert
	var load *ssa.UnOp
	var terminal *ssa.Return
	for _, instruction := range function.Blocks[0].Instrs {
		switch instruction := instruction.(type) {
		case *ssa.DebugRef:
		case *ssa.Alloc:
			if proof.Allocation != nil {
				return SSAExactScalarBitcast{}, false
			}
			pointer, ok := types.Unalias(instruction.Type()).Underlying().(*types.Pointer)
			if !ok || !types.Identical(pointer.Elem(), source) {
				return SSAExactScalarBitcast{}, false
			}
			proof.Allocation = instruction
		case *ssa.Store:
			if store != nil {
				return SSAExactScalarBitcast{}, false
			}
			store = instruction
		case *ssa.Convert:
			switch {
			case ssaExactUnsafePointer(instruction.Type()):
				if sourcePointer != nil {
					return SSAExactScalarBitcast{}, false
				}
				sourcePointer = instruction
			default:
				pointer, ok := types.Unalias(instruction.Type()).Underlying().(*types.Pointer)
				if !ok || !types.Identical(pointer.Elem(), target) || targetPointer != nil {
					return SSAExactScalarBitcast{}, false
				}
				targetPointer = instruction
			}
		case *ssa.UnOp:
			if instruction.Op != token.MUL || load != nil || !types.Identical(instruction.Type(), target) {
				return SSAExactScalarBitcast{}, false
			}
			load = instruction
		case *ssa.Return:
			if terminal != nil {
				return SSAExactScalarBitcast{}, false
			}
			terminal = instruction
		default:
			return SSAExactScalarBitcast{}, false
		}
	}
	if proof.Allocation == nil || store == nil || sourcePointer == nil || targetPointer == nil || load == nil || terminal == nil ||
		store.Addr != proof.Allocation || store.Val != function.Params[0] || sourcePointer.X != proof.Allocation ||
		targetPointer.X != sourcePointer || load.X != targetPointer || len(terminal.Results) != 1 || terminal.Results[0] != load {
		return SSAExactScalarBitcast{}, false
	}
	if !ssaExactRefs(function.Params[0], store) ||
		!ssaExactRefs(proof.Allocation, store, sourcePointer) ||
		!ssaExactRefs(sourcePointer, targetPointer) ||
		!ssaExactRefs(targetPointer, load) ||
		!ssaExactRefs(load, terminal) {
		return SSAExactScalarBitcast{}, false
	}
	return proof, true
}

func ssaExactScalarBitcastTypes(source, target types.Type) bool {
	sourceBasic, sourceOK := types.Unalias(source).Underlying().(*types.Basic)
	targetBasic, targetOK := types.Unalias(target).Underlying().(*types.Basic)
	if !sourceOK || !targetOK {
		return false
	}
	switch sourceBasic.Kind() {
	case types.Int32:
		return targetBasic.Kind() == types.Float32
	case types.Float32:
		return targetBasic.Kind() == types.Int32
	case types.Int64:
		return targetBasic.Kind() == types.Float64
	case types.Float64:
		return targetBasic.Kind() == types.Int64
	default:
		return false
	}
}

func ssaExactUnsafePointer(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.UnsafePointer
}

func ssaExactRefs(value ssa.Value, expected ...ssa.Instruction) bool {
	if value == nil || value.Referrers() == nil {
		return false
	}
	actual := make(map[ssa.Instruction]int, len(expected))
	for _, reference := range *value.Referrers() {
		if _, debug := reference.(*ssa.DebugRef); debug {
			continue
		}
		actual[reference]++
	}
	if len(actual) != len(expected) {
		return false
	}
	for _, instruction := range expected {
		if actual[instruction] != 1 {
			return false
		}
	}
	return true
}
