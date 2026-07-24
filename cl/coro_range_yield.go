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
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// validateCoroExactRangeYield recognizes the compiler-owned callback body
// synthesized for Go's range-over-function statement. It is a normal managed
// descriptor: the iterator invokes it synchronously in the same logical G, and
// the universal descriptor selects a plain or coroutine primary from its
// ordinary inferred effects.
//
// The proof is structural rather than name-based. Source code cannot create a
// Function whose Syntax is the owning *ast.RangeStmt, whose Synthetic class is
// range-over-func yield, and which is an exact child in the parent's AnonFuncs
// graph. The first captured *int is x/tools' range state cell; it is also the
// state used by the existing defer-drain and invalid-resume lowering.
func validateCoroExactRangeYield(fn *ssa.Function) error {
	if fn == nil || fn.Synthetic != rangeOverFuncYieldSynthetic || fn.Parent() == nil ||
		fn.Object() != nil || fn.Signature == nil || len(fn.Blocks) == 0 {
		return fmt.Errorf("requires one bodyful compiler range-yield child")
	}
	statement, ok := fn.Syntax().(*ast.RangeStmt)
	if !ok || statement.X == nil || statement.Body == nil {
		return fmt.Errorf("synthetic body is not bound to its exact range statement")
	}
	parent := fn.Parent()
	children := 0
	for _, child := range parent.AnonFuncs {
		if child == fn {
			children++
		}
	}
	if children != 1 {
		return fmt.Errorf("range-yield body occurs %d times in its parent child graph", children)
	}
	if fn.Pkg != parent.Pkg {
		return fmt.Errorf("range-yield body and parent belong to different SSA packages")
	}

	signature := fn.Signature
	if signature.Recv() != nil || signature.Variadic() ||
		typeParamCount(signature.TypeParams()) != 0 ||
		typeParamCount(signature.RecvTypeParams()) != 0 ||
		signature.Params() == nil || signature.Params().Len() > 2 ||
		signature.Results() == nil || signature.Results().Len() != 1 ||
		!types.Identical(signature.Results().At(0).Type(), types.Typ[types.Bool]) {
		return fmt.Errorf("range-yield signature is not func([value[, value]]) bool")
	}
	if len(fn.FreeVars) == 0 {
		return fmt.Errorf("range-yield body has no compiler state capture")
	}
	state, ok := types.Unalias(fn.FreeVars[0].Type()).Underlying().(*types.Pointer)
	if !ok || !types.Identical(state.Elem(), types.Typ[types.Int]) {
		return fmt.Errorf("range-yield body has no leading *int state cell")
	}
	for _, tuple := range []*types.Tuple{signature.Params(), signature.Results()} {
		for index := 0; index < tuple.Len(); index++ {
			typ := tuple.At(index).Type()
			if coroTypeContainsUnresolvedTypeParam(typ, make(map[types.Type]bool)) {
				return fmt.Errorf("range-yield signature retains an unresolved type parameter")
			}
			if err := validateCoroPhysicalValueType(typ, make(map[types.Type]bool)); err != nil {
				return fmt.Errorf("range-yield signature field: %w", err)
			}
		}
	}
	for _, free := range fn.FreeVars {
		if free == nil || free.Parent() != fn ||
			coroTypeContainsUnresolvedTypeParam(free.Type(), make(map[types.Type]bool)) {
			return fmt.Errorf("range-yield capture is incomplete or unresolved")
		}
		if err := validateCoroPhysicalValueType(free.Type(), make(map[types.Type]bool)); err != nil {
			return fmt.Errorf("range-yield capture: %w", err)
		}
	}

	if origin := fn.Origin(); origin != nil && origin != fn {
		parentOrigin := parent.Origin()
		if parentOrigin == nil || origin.Parent() != parentOrigin ||
			origin.Synthetic != rangeOverFuncYieldSynthetic {
			return fmt.Errorf("instantiated range-yield origin is outside its parent origin")
		}
	}
	if len(fn.TypeArgs()) != 0 {
		if !coroMaterializedGenericInstance(parent) || len(fn.TypeArgs()) != len(parent.TypeArgs()) {
			return fmt.Errorf("range-yield type arguments are not owned by one materialized parent")
		}
		for index, argument := range fn.TypeArgs() {
			if !types.Identical(argument, parent.TypeArgs()[index]) {
				return fmt.Errorf("range-yield type argument %d differs from its parent", index)
			}
		}
	}
	return nil
}
