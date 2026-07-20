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
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// validateUnsafeStringBuiltin freezes x/tools' exact unsafe.String SSA shape.
// The ordinary AssertRuntimeError calls are replaced by ordered explicit-
// status terminal edges in a physical coroutine.
func (a *coroPhysicalPureSSAAudit) validateUnsafeStringBuiltin(call *ssa.Call) string {
	if call == nil || call.Common() == nil || call.Type() == nil {
		return "unsafe.String builtin has an incomplete call/result shape"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "String" || len(call.Common().Args) != 2 {
		return "unsafe.String validation requires the exact two-argument builtin call"
	}
	pointerValue, lengthValue := call.Common().Args[0], call.Common().Args[1]
	if pointerValue == nil || lengthValue == nil {
		return "unsafe.String has a nil pointer or length SSA operand"
	}
	pointerType, ok := types.Unalias(a.typeOf(pointerValue.Type())).Underlying().(*types.Pointer)
	if !ok || !types.Identical(types.Unalias(a.typeOf(pointerType.Elem())), types.Typ[types.Byte]) {
		return "unsafe.String first operand is not *byte"
	}
	lengthType, ok := types.Unalias(a.typeOf(lengthValue.Type())).Underlying().(*types.Basic)
	if !ok || lengthType.Info()&types.IsInteger == 0 || lengthType.Info()&types.IsUntyped != 0 {
		return "unsafe.String length is not a concrete integer"
	}
	resultType, ok := types.Unalias(a.typeOf(call.Type())).Underlying().(*types.Basic)
	if !ok || resultType.Kind() != types.String {
		return "unsafe.String result is not string-shaped"
	}
	for name, typ := range map[string]types.Type{
		"pointer": pointerValue.Type(),
		"length":  lengthValue.Type(),
		"result":  call.Type(),
	} {
		if err := validateCoroPhysicalSSAValueType(a.typeOf(typ)); err != nil {
			return fmt.Sprintf("unsafe.String %s has unsupported physical type: %v", name, err)
		}
	}
	if !a.allowImplicitNilFault {
		return "unsafe.String requires the explicit-status panic ABI"
	}
	return a.requireOnlyCompilerElidedRuntimeHelpers(call, "AssertRuntimeError")
}

// compileCoroUnsafeString shares the target-width span arithmetic used by
// unsafe.Slice with element width one, but publishes the distinct Go-required
// unsafe.String panic payloads and forms a two-word string header only after
// all guards succeed.
func (p *context) compileCoroUnsafeString(
	b llssa.Builder,
	call *ssa.CallCommon,
	pointerValue, lengthValue llssa.Expr,
) llssa.Expr {
	if p == nil || p.currentCoro == nil || b == nil || b.Func != p.fn || call == nil ||
		p.compilation == nil || !p.compilation.EnableCoroExplicitStatusPanicABI ||
		p.currentCoro.abi.version < coroPhysicalABIVersionV1 {
		panic("unsafe.String coroutine lowering requires the PhysicalABIV1 explicit-status ABI")
	}
	results := call.Signature().Results()
	if results == nil || results.Len() != 1 || len(call.Args) != 2 || pointerValue.IsNil() || lengthValue.IsNil() {
		panic("unsafe.String coroutine lowering lost its exact call shape")
	}
	resultType := p.patchType(results.At(0).Type())
	resultBasic, ok := types.Unalias(resultType).Underlying().(*types.Basic)
	if !ok || resultBasic.Kind() != types.String {
		panic("unsafe.String coroutine result is not string-shaped")
	}
	pointerType, ok := types.Unalias(p.patchType(call.Args[0].Type())).Underlying().(*types.Pointer)
	if !ok || !types.Identical(types.Unalias(p.patchType(pointerType.Elem())), types.Typ[types.Byte]) {
		panic("unsafe.String coroutine pointer is not *byte")
	}
	length, preLenFault, nilFault, spanLenFault := b.UnsafeSliceGuardConditions(pointerValue, lengthValue, 1)
	p.compileCoroFaultConditionGuard(b, preLenFault, coroFaultUnsafeStringLenV1)
	p.compileCoroFaultConditionGuard(b, nilFault, coroFaultUnsafeStringNilV1)
	p.compileCoroFaultConditionGuard(b, spanLenFault, coroFaultUnsafeStringLenV1)
	return b.Aggregate(p.type_(resultType, llssa.InGo), pointerValue, length)
}
