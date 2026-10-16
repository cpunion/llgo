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
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// MaterializedGenericInstance proves one exact, concrete x/tools generic body,
// including literals materialized in generic functions or receiver methods.
// Identity is shared by effect/spawn analysis and physical ABI validation;
// concrete transport support remains the backend's separate responsibility.
func MaterializedGenericInstance(fn *ssa.Function) bool {
	if fn == nil || fn.Origin() == nil || fn.Origin() == fn || fn.Signature == nil {
		return false
	}
	typeArgs := fn.TypeArgs()
	if parent := fn.Parent(); parent != nil {
		// Current x/tools materializes a function literal inside each
		// instantiated generic body. A generic-function child repeats its parent's
		// TypeParams/TypeArgs, while a generic-receiver-method child keeps the
		// substitution exclusively on Parent(). Bind both current representations
		// to the complete parent/Origin/AnonFuncs graph; an arbitrary nested
		// function cannot acquire a dispatch ABI merely from concrete-looking
		// value types.
		if !MaterializedGenericInstance(parent) || fn.Synthetic != "" || fn.Object() != nil {
			return false
		}
		switch {
		case len(typeArgs) == 0:
			if typeParamListLen(fn.TypeParams()) != 0 {
				return false
			}
			typeArgs = parent.TypeArgs()
		case len(typeArgs) == len(parent.TypeArgs()):
			if typeParamListLen(fn.TypeParams()) != len(typeArgs) {
				return false
			}
			for index, argument := range typeArgs {
				if !types.Identical(argument, parent.TypeArgs()[index]) {
					return false
				}
			}
		default:
			return false
		}
		if _, ok := fn.Syntax().(*ast.FuncLit); !ok {
			return false
		}
		originParent := fn.Origin().Parent()
		if originParent == nil || originParent != parent.Origin() {
			return false
		}
		found := false
		for _, child := range parent.AnonFuncs {
			if child == fn {
				if found {
					return false
				}
				found = true
			}
		}
		if !found {
			return false
		}
	} else if !strings.HasPrefix(fn.Synthetic, "instance of ") {
		return false
	}
	if !GroundGenericTypeArgs(typeArgs) {
		return false
	}
	// x/tools erases ordinary declaration type parameters from an instantiated
	// callable signature. For an instantiated generic receiver method, and for
	// its parentless method body only, it keeps the origin's RecvTypeParams
	// metadata even though Recv and every physical parameter/result are already
	// concrete. Judge materialization from that callable value shape, not from
	// the stale declaration metadata alone.
	if params := fn.Signature.TypeParams(); params != nil && params.Len() != 0 {
		return false
	}
	if params := fn.Signature.RecvTypeParams(); params != nil && params.Len() != 0 &&
		(fn.Parent() != nil || fn.Signature.Recv() == nil) {
		return false
	}
	parameters := fn.Signature.Params()
	offset := 0
	if receiver := fn.Signature.Recv(); receiver != nil {
		if len(fn.Params) == 0 || fn.Params[0] == nil ||
			!types.Identical(fn.Params[0].Type(), receiver.Type()) ||
			TypeContainsUnresolvedTypeParam(receiver.Type(), make(map[types.Type]bool)) {
			return false
		}
		offset = 1
	}
	if len(fn.Params) != parameters.Len()+offset {
		return false
	}
	for index := 0; index < parameters.Len(); index++ {
		parameter := fn.Params[index+offset]
		if parameter == nil || !types.Identical(parameter.Type(), parameters.At(index).Type()) ||
			TypeContainsUnresolvedTypeParam(parameter.Type(), make(map[types.Type]bool)) {
			return false
		}
	}
	if TypeContainsUnresolvedTypeParam(fn.Signature.Results(), make(map[types.Type]bool)) {
		return false
	}
	for _, free := range fn.FreeVars {
		if free == nil || TypeContainsUnresolvedTypeParam(free.Type(), make(map[types.Type]bool)) {
			return false
		}
	}
	return true
}

// GroundGenericTypeArgs rejects missing arguments and unresolved type parameters
// even behind pointers, named types or nested composite values.
func GroundGenericTypeArgs(arguments []types.Type) bool {
	if len(arguments) == 0 {
		return false
	}
	for _, argument := range arguments {
		if TypeContainsUnresolvedTypeParam(argument, make(map[types.Type]bool)) {
			return false
		}
	}
	return true
}

// TypeContainsUnresolvedTypeParam is deliberately deeper than the
// physical-value validator: a pointer has a fixed transport width, but
// *Box[T] is still not a materialized generic identity. This proof is used
// only for instantiation identity and therefore follows referents and named
// underlying types all the way to a TypeParam.
func TypeContainsUnresolvedTypeParam(typ types.Type, visiting map[types.Type]bool) bool {
	if typ == nil {
		return true
	}
	typ = types.Unalias(typ)
	if visiting[typ] {
		return false
	}
	visiting[typ] = true
	defer delete(visiting, typ)

	switch value := typ.(type) {
	case *types.TypeParam:
		return true
	case *types.Named:
		if arguments := value.TypeArgs(); arguments != nil {
			for index := 0; index < arguments.Len(); index++ {
				if TypeContainsUnresolvedTypeParam(arguments.At(index), visiting) {
					return true
				}
			}
		}
		return TypeContainsUnresolvedTypeParam(value.Underlying(), visiting)
	case *types.Pointer:
		return TypeContainsUnresolvedTypeParam(value.Elem(), visiting)
	case *types.Array:
		return TypeContainsUnresolvedTypeParam(value.Elem(), visiting)
	case *types.Slice:
		return TypeContainsUnresolvedTypeParam(value.Elem(), visiting)
	case *types.Chan:
		return TypeContainsUnresolvedTypeParam(value.Elem(), visiting)
	case *types.Map:
		return TypeContainsUnresolvedTypeParam(value.Key(), visiting) ||
			TypeContainsUnresolvedTypeParam(value.Elem(), visiting)
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			if TypeContainsUnresolvedTypeParam(value.Field(index).Type(), visiting) {
				return true
			}
		}
	case *types.Signature:
		if typeParamListLen(value.TypeParams()) != 0 || typeParamListLen(value.RecvTypeParams()) != 0 {
			return true
		}
		if value.Recv() != nil && TypeContainsUnresolvedTypeParam(value.Recv().Type(), visiting) {
			return true
		}
		for _, tuple := range []*types.Tuple{value.Params(), value.Results()} {
			for index := 0; index < tuple.Len(); index++ {
				if TypeContainsUnresolvedTypeParam(tuple.At(index).Type(), visiting) {
					return true
				}
			}
		}
	case *types.Tuple:
		for index := 0; index < value.Len(); index++ {
			if TypeContainsUnresolvedTypeParam(value.At(index).Type(), visiting) {
				return true
			}
		}
	case *types.Interface:
		value.Complete()
		for index := 0; index < value.NumMethods(); index++ {
			if TypeContainsUnresolvedTypeParam(value.Method(index).Type(), visiting) {
				return true
			}
		}
		for index := 0; index < value.NumEmbeddeds(); index++ {
			if TypeContainsUnresolvedTypeParam(value.EmbeddedType(index), visiting) {
				return true
			}
		}
	case *types.Union:
		for index := 0; index < value.Len(); index++ {
			if TypeContainsUnresolvedTypeParam(value.Term(index).Type(), visiting) {
				return true
			}
		}
	}
	return false
}
