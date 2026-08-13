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

// SSABorrowedAllocationProof certifies that an allocation which x/tools
// conservatively marked Heap has no address path surviving its owning function
// return. Every static callee receiving that address is inspected transitively;
// dynamic calls, goroutines, defers, returns, stores outside the same tainted
// object, and body-less declarations fail closed.
//
// This is a storage-lifetime proof only. The physical planner still decides
// whether the object belongs in an LLVM coroutine frame or on an outcome-plain
// native stack, and separately enforces the target's stack-size/root profile.
type SSABorrowedAllocationProof struct {
	Allocation       *ssa.Alloc
	FunctionsVisited uint32
	ParametersProven uint32
}

type ssaBorrowParameterKey struct {
	function *ssa.Function
	index    int
}

type ssaBorrowProofState uint8

const (
	ssaBorrowProofUnknown ssaBorrowProofState = iota
	ssaBorrowProofVisiting
	ssaBorrowProofRejected
	ssaBorrowProofAccepted
)

type ssaBorrowedAllocationAnalyzer struct {
	parameters map[ssaBorrowParameterKey]ssaBorrowProofState
	functions  map[*ssa.Function]struct{}
}

// ProveSSABorrowedAllocation derives a closed interprocedural borrow proof for
// one exact SSA allocation. Non-heap allocations already have local identity
// and therefore deliberately do not receive this reclassification proof.
func ProveSSABorrowedAllocation(allocation *ssa.Alloc) (SSABorrowedAllocationProof, bool) {
	proof := SSABorrowedAllocationProof{Allocation: allocation}
	if allocation == nil || !allocation.Heap || allocation.Parent() == nil ||
		allocation.Referrers() == nil {
		return proof, false
	}
	analyzer := &ssaBorrowedAllocationAnalyzer{
		parameters: make(map[ssaBorrowParameterKey]ssaBorrowProofState),
		functions:  make(map[*ssa.Function]struct{}),
	}
	if !analyzer.proveAddressValue(allocation.Parent(), allocation) {
		return proof, false
	}
	proof.FunctionsVisited = uint32(len(analyzer.functions))
	for _, state := range analyzer.parameters {
		if state == ssaBorrowProofAccepted {
			proof.ParametersProven++
		}
	}
	return proof, true
}

func (analyzer *ssaBorrowedAllocationAnalyzer) proveParameter(function *ssa.Function, index int) bool {
	if analyzer == nil || function == nil || len(function.Blocks) == 0 ||
		index < 0 || index >= len(function.Params) || function.Params[index] == nil {
		return false
	}
	key := ssaBorrowParameterKey{function: function, index: index}
	switch analyzer.parameters[key] {
	case ssaBorrowProofVisiting, ssaBorrowProofAccepted:
		// Borrow is a coinductive property: a recursive cycle is safe unless an
		// edge leaving that cycle is later rejected.
		return true
	case ssaBorrowProofRejected:
		return false
	}
	analyzer.parameters[key] = ssaBorrowProofVisiting
	if !analyzer.proveAddressValue(function, function.Params[index]) {
		analyzer.parameters[key] = ssaBorrowProofRejected
		return false
	}
	analyzer.parameters[key] = ssaBorrowProofAccepted
	return true
}

func (analyzer *ssaBorrowedAllocationAnalyzer) proveAddressValue(
	function *ssa.Function,
	root ssa.Value,
) bool {
	if analyzer == nil || function == nil || root == nil || root.Parent() != function ||
		root.Referrers() == nil {
		return false
	}
	analyzer.functions[function] = struct{}{}

	// Values in tainted carry the root address itself or an aggregate which may
	// contain its exact Go pointer type. A fresh allocation's unrelated pointer
	// fields cannot acquire the root through ordinary typed assignment: any
	// static callee which tries to write the root into other storage is inspected
	// below and rejected. Keeping this distinction is important for transaction
	// objects whose ordinary endpoint fields lead to a much larger runtime graph
	// while only one exact self-certificate field can carry the allocation.
	tainted := map[ssa.Value]bool{root: true}
	// interior is deliberately narrower than tainted: it contains only address
	// expressions structurally rooted in the fresh allocation, never a pointer
	// loaded from one of its fields. This distinction makes an exact self-field
	// store local while still rejecting a store through an arbitrary loaded
	// pointer.
	interior := map[ssa.Value]bool{root: true}
	queue := []ssa.Value{root}
	for head := 0; head < len(queue); head++ {
		value := queue[head]
		refs := value.Referrers()
		if refs == nil {
			return false
		}
		for _, reference := range *refs {
			derived, ok := ssaBorrowDerivedValue(reference, value, root.Type())
			if !ok || derived == nil || tainted[derived] {
				continue
			}
			tainted[derived] = true
			if interior[value] && ssaBorrowDerivedInteriorAddress(reference, value) {
				interior[derived] = true
			}
			queue = append(queue, derived)
		}
	}

	for value := range tainted {
		refs := value.Referrers()
		if refs == nil {
			return false
		}
		for _, reference := range *refs {
			if derived, ok := ssaBorrowDerivedValue(reference, value, root.Type()); ok &&
				derived != nil && tainted[derived] {
				continue
			}
			switch reference := reference.(type) {
			case *ssa.DebugRef:
			case *ssa.Field:
				if reference.X != value {
					return false
				}
			case *ssa.Index:
				if reference.X != value {
					return false
				}
			case *ssa.Extract:
				if reference.Tuple != value {
					return false
				}
			case *ssa.UnOp:
				if reference.X != value || reference.Op != token.MUL {
					return false
				}
			case *ssa.BinOp:
				if reference.X != value && reference.Y != value ||
					(reference.Op != token.EQL && reference.Op != token.NEQ) {
					return false
				}
			case *ssa.Store:
				switch {
				case reference.Addr == value:
					// Writing ordinary data into the borrowed object does not
					// change the lifetime of its address.
				case reference.Val == value && interior[reference.Addr] &&
					ssaBorrowTypeMayCarryRoot(value.Type(), root.Type()):
					// An exact self pointer (or an aggregate containing that exact
					// typed pointer) may live inside the fresh object itself. Loads
					// of such a field remain tainted and are checked independently.
				default:
					// The address may merely have been loaded from a pointer field
					// and therefore need not denote storage inside the borrowed
					// object. Converted/boxed roots also fail this exact typed-self
					// exception.
					return false
				}
			case *ssa.Call:
				if !analyzer.proveCallArgument(reference, value) {
					return false
				}
			case *ssa.Return, *ssa.Go, *ssa.Defer, *ssa.Send, *ssa.MapUpdate,
				*ssa.MakeClosure:
				return false
			default:
				// Every value-producing address/aggregate propagation is
				// handled by ssaBorrowDerivedValue. An unknown observer is an
				// open lifetime edge and must fail closed.
				return false
			}
		}
	}
	return true
}

func ssaBorrowDerivedInteriorAddress(reference ssa.Instruction, source ssa.Value) bool {
	switch reference := reference.(type) {
	case *ssa.FieldAddr:
		return reference.X == source
	case *ssa.IndexAddr:
		return reference.X == source
	case *ssa.ChangeType:
		return reference.X == source && ssaBorrowPointerIdentityType(reference.Type()) &&
			ssaBorrowPointerIdentityType(source.Type())
	case *ssa.Convert:
		return reference.X == source && ssaBorrowPointerIdentityType(reference.Type()) &&
			ssaBorrowPointerIdentityType(source.Type())
	}
	return false
}

func ssaBorrowPointerIdentityType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch typ := types.Unalias(typ).Underlying().(type) {
	case *types.Pointer:
		return typ != nil
	case *types.Basic:
		return typ.Kind() == types.UnsafePointer
	}
	return false
}

func (analyzer *ssaBorrowedAllocationAnalyzer) proveCallArgument(call *ssa.Call, value ssa.Value) bool {
	if analyzer == nil || call == nil || call.Common() == nil || call.Common().IsInvoke() {
		return false
	}
	common := call.Common()
	callee := common.StaticCallee()
	if callee == nil {
		builtin, ok := common.Value.(*ssa.Builtin)
		if !ok || (builtin.Name() != "len" && builtin.Name() != "cap") {
			return false
		}
		return true
	}
	found := false
	for index, argument := range common.Args {
		if argument != value {
			continue
		}
		found = true
		if !analyzer.proveParameter(callee, index) {
			return false
		}
	}
	return found
}

func ssaBorrowDerivedValue(reference ssa.Instruction, source ssa.Value, rootType types.Type) (ssa.Value, bool) {
	if reference == nil || source == nil || rootType == nil {
		return nil, false
	}
	var derived ssa.Value
	switch reference := reference.(type) {
	case *ssa.FieldAddr:
		if reference.X == source {
			derived = reference
		}
	case *ssa.IndexAddr:
		if reference.X == source {
			derived = reference
		}
	case *ssa.Field:
		if reference.X == source && ssaBorrowTypeMayCarryRoot(reference.Type(), rootType) {
			derived = reference
		}
	case *ssa.Index:
		if reference.X == source && ssaBorrowTypeMayCarryRoot(reference.Type(), rootType) {
			derived = reference
		}
	case *ssa.Extract:
		if reference.Tuple == source && ssaBorrowTypeMayCarryRoot(reference.Type(), rootType) {
			derived = reference
		}
	case *ssa.UnOp:
		if reference.X == source && reference.Op == token.MUL &&
			ssaBorrowTypeMayCarryRoot(reference.Type(), rootType) {
			derived = reference
		}
	case *ssa.ChangeType:
		if reference.X == source {
			derived = reference
		}
	case *ssa.Convert:
		if reference.X == source {
			// Pointer-to-uintptr conversion still carries the lifetime even
			// though the result's Go type no longer contains a pointer.
			derived = reference
		}
	case *ssa.MultiConvert:
		if reference.X == source {
			derived = reference
		}
	case *ssa.ChangeInterface:
		if reference.X == source {
			derived = reference
		}
	case *ssa.MakeInterface:
		if reference.X == source {
			derived = reference
		}
	case *ssa.Slice:
		if reference.X == source {
			derived = reference
		}
	case *ssa.SliceToArrayPointer:
		if reference.X == source {
			derived = reference
		}
	case *ssa.TypeAssert:
		if reference.X == source && ssaBorrowTypeMayCarryRoot(reference.Type(), rootType) {
			derived = reference
		}
	case *ssa.Phi:
		for _, edge := range reference.Edges {
			if edge == source {
				derived = reference
				break
			}
		}
	}
	return derived, derived != nil
}

func ssaBorrowTypeMayCarryRoot(typ, rootType types.Type) bool {
	if typ == nil || rootType == nil {
		return false
	}
	typ = types.Unalias(typ)
	rootType = types.Unalias(rootType)
	if ssaBorrowTypesMayMatch(typ, rootType) {
		return true
	}
	switch typ := types.Unalias(typ).Underlying().(type) {
	case *types.Interface:
		// Boxing the root itself is handled by MakeInterface propagation. An
		// interface loaded from the fresh object can carry it only after such a
		// box was stored, and that store is rejected by the lifetime proof.
		return false
	case *types.Pointer:
		return ssaBorrowTypesMayMatch(typ, rootType)
	case *types.Slice, *types.Map, *types.Chan, *types.Signature:
		return false
	case *types.Basic:
		// unsafe.Pointer/uintptr conversions are explicitly propagated from a
		// tainted source. A fresh field of either type is not implicitly an alias.
		return false
	case *types.Array:
		return ssaBorrowTypeMayCarryRoot(typ.Elem(), rootType)
	case *types.Struct:
		for index := 0; index < typ.NumFields(); index++ {
			if ssaBorrowTypeMayCarryRoot(typ.Field(index).Type(), rootType) {
				return true
			}
		}
	case *types.Tuple:
		for index := 0; index < typ.Len(); index++ {
			if ssaBorrowTypeMayCarryRoot(typ.At(index).Type(), rootType) {
				return true
			}
		}
	case *types.TypeParam:
		return true
	}
	return false
}

// ssaBorrowTypesMayMatch answers only the identity facts needed by the borrow
// proof. x/tools SSA deliberately uses private opaque types for synthetic
// values such as range-function defer stacks. Passing either of those types to
// go/types.Identical panics because it is not one of go/types' closed concrete
// implementations. Unknown or synthetic shapes therefore return true here:
// the caller treats them as possibly carrying the root, follows the extra use,
// and fails the lifetime proof closed if it cannot account for that use.
func ssaBorrowTypesMayMatch(left, right types.Type) bool {
	if left == nil || right == nil {
		return false
	}
	left = types.Unalias(left)
	right = types.Unalias(right)
	if left == right {
		return true
	}
	switch left := left.(type) {
	case *types.Basic:
		right, ok := right.(*types.Basic)
		return ok && left.Kind() == right.Kind()
	case *types.Array:
		right, ok := right.(*types.Array)
		return ok && left.Len() == right.Len() &&
			ssaBorrowTypesMayMatch(left.Elem(), right.Elem())
	case *types.Slice:
		right, ok := right.(*types.Slice)
		return ok && ssaBorrowTypesMayMatch(left.Elem(), right.Elem())
	case *types.Pointer:
		right, ok := right.(*types.Pointer)
		return ok && ssaBorrowTypesMayMatch(left.Elem(), right.Elem())
	case *types.Map:
		right, ok := right.(*types.Map)
		return ok && ssaBorrowTypesMayMatch(left.Key(), right.Key()) &&
			ssaBorrowTypesMayMatch(left.Elem(), right.Elem())
	case *types.Chan:
		right, ok := right.(*types.Chan)
		return ok && left.Dir() == right.Dir() &&
			ssaBorrowTypesMayMatch(left.Elem(), right.Elem())
	case *types.Named:
		right, ok := right.(*types.Named)
		if !ok {
			return false
		}
		// The origin object is the exact declaration identity. Different generic
		// arguments could make these types unequal, but returning true is the
		// conservative lifetime answer and avoids inspecting an opaque argument.
		return left.Origin().Obj() == right.Origin().Obj()
	case *types.Struct:
		_, ok := right.(*types.Struct)
		return ok
	case *types.Tuple:
		_, ok := right.(*types.Tuple)
		return ok
	case *types.Signature:
		_, ok := right.(*types.Signature)
		return ok
	case *types.Interface:
		_, ok := right.(*types.Interface)
		return ok
	case *types.TypeParam:
		_, ok := right.(*types.TypeParam)
		return ok
	case *types.Union:
		_, ok := right.(*types.Union)
		return ok
	default:
		// Private SSA type. It is intentionally opaque to this proof.
		return true
	}
}
