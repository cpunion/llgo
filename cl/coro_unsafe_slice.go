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

// validateUnsafeSliceBuiltin freezes x/tools' exact unsafe.Slice SSA shape.
// The logical AssertRuntimeError edge belongs to ordinary LLSSA lowering; the
// PhysicalABIV1 path below replaces it completely with compiler-owned terminal
// branches, so no native-stack panic helper may remain in the coroutine body.
func (a *coroPhysicalPureSSAAudit) validateUnsafeSliceBuiltin(call *ssa.Call) string {
	if call == nil || call.Common() == nil || call.Type() == nil {
		return "unsafe.Slice builtin has an incomplete call/result shape"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "Slice" || len(call.Common().Args) != 2 {
		return "unsafe.Slice validation requires the exact two-argument builtin call"
	}
	pointerValue, lengthValue := call.Common().Args[0], call.Common().Args[1]
	if pointerValue == nil || lengthValue == nil {
		return "unsafe.Slice has a nil pointer or length SSA operand"
	}
	pointerType, ok := types.Unalias(a.typeOf(pointerValue.Type())).Underlying().(*types.Pointer)
	if !ok {
		return "unsafe.Slice first operand is not pointer-shaped"
	}
	lengthType, ok := types.Unalias(a.typeOf(lengthValue.Type())).Underlying().(*types.Basic)
	if !ok || lengthType.Info()&types.IsInteger == 0 || lengthType.Info()&types.IsUntyped != 0 {
		return "unsafe.Slice length is not a concrete integer"
	}
	resultType, ok := types.Unalias(a.typeOf(call.Type())).Underlying().(*types.Slice)
	if !ok {
		return "unsafe.Slice result is not slice-shaped"
	}
	if !types.Identical(a.typeOf(pointerType.Elem()), a.typeOf(resultType.Elem())) {
		return "unsafe.Slice pointer and result element types differ"
	}
	for name, typ := range map[string]types.Type{
		"pointer": pointerValue.Type(),
		"length":  lengthValue.Type(),
		"result":  call.Type(),
	} {
		if err := validateCoroPhysicalSSAValueType(a.typeOf(typ)); err != nil {
			return fmt.Sprintf("unsafe.Slice %s has unsupported physical type: %v", name, err)
		}
	}
	if !a.allowImplicitNilFault {
		return "unsafe.Slice requires the explicit-status panic ABI"
	}
	return a.requireOnlyCompilerElidedRuntimeHelpers(call, "AssertRuntimeError")
}

// compileCoroUnsafeSlice lowers unsafe.Slice as pure pointer/integer SSA plus
// ordered explicit-status faults. Both source operands have already been
// evaluated in Go order. The slice aggregate is formed only in the continuation
// dominated by all target-width length, nil, multiplication, and address-span
// checks.
func (p *context) compileCoroUnsafeSlice(
	b llssa.Builder,
	call *ssa.CallCommon,
	pointerValue, lengthValue llssa.Expr,
) llssa.Expr {
	if p == nil || p.currentCoro == nil || b == nil || b.Func != p.fn || call == nil ||
		p.compilation == nil || !p.compilation.EnableCoroExplicitStatusPanicABI ||
		p.currentCoro.abi.version < coroPhysicalABIVersionV1 {
		panic("unsafe.Slice coroutine lowering requires the PhysicalABIV1 explicit-status ABI")
	}
	results := call.Signature().Results()
	if results == nil || results.Len() != 1 || len(call.Args) != 2 ||
		pointerValue.IsNil() || lengthValue.IsNil() {
		panic("unsafe.Slice coroutine lowering lost its exact call shape")
	}
	resultType := p.patchType(results.At(0).Type())
	resultSlice, ok := types.Unalias(resultType).Underlying().(*types.Slice)
	if !ok {
		panic("unsafe.Slice coroutine result is not slice-shaped")
	}
	pointerType, ok := types.Unalias(p.patchType(call.Args[0].Type())).Underlying().(*types.Pointer)
	if !ok || !types.Identical(p.patchType(pointerType.Elem()), p.patchType(resultSlice.Elem())) {
		panic("unsafe.Slice coroutine pointer/result element types differ")
	}
	elemSize := p.prog.SizeOf(p.type_(resultSlice.Elem(), llssa.InGo))
	length, preLenFault, nilFault, spanLenFault := b.UnsafeSliceGuardConditions(
		pointerValue,
		lengthValue,
		elemSize,
	)
	p.compileCoroFaultConditionGuard(b, preLenFault, coroFaultUnsafeSliceLenV1)
	p.compileCoroFaultConditionGuard(b, nilFault, coroFaultUnsafeSliceNilV1)
	if elemSize != 0 {
		p.compileCoroFaultConditionGuard(b, spanLenFault, coroFaultUnsafeSliceLenV1)
	}
	return b.Aggregate(p.type_(resultType, llssa.InGo), pointerValue, length, length)
}
