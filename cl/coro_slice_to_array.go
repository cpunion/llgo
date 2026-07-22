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
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// coroSliceToArrayPointerShape validates the exact language conversion shape
// shared by helper inventory, physical-ABI preflight, frame retention, and
// code generation. Keeping the array length derived from the result type
// avoids an instruction-name or source-pattern exception.
func coroSliceToArrayPointerShape(source, result types.Type) (*types.Array, string) {
	slice, ok := types.Unalias(source).Underlying().(*types.Slice)
	if !ok {
		return nil, "source is not a slice"
	}
	pointer, ok := types.Unalias(result).Underlying().(*types.Pointer)
	if !ok {
		return nil, "result is not a pointer"
	}
	array, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array)
	if !ok {
		return nil, "result does not point to an array"
	}
	if !types.Identical(slice.Elem(), array.Elem()) {
		return nil, "slice and array element types differ"
	}
	return array, ""
}

func coroSliceToArrayPointerLen(conversion *ssa.SliceToArrayPointer, typeOf func(types.Type) types.Type) (int64, bool) {
	if conversion == nil || conversion.X == nil || conversion.Type() == nil {
		return 0, false
	}
	source, result := conversion.X.Type(), conversion.Type()
	if typeOf != nil {
		source, result = typeOf(source), typeOf(result)
	}
	array, reason := coroSliceToArrayPointerShape(source, result)
	if reason != "" {
		return 0, false
	}
	return array.Len(), true
}

// coroSliceToArrayValueDeref recognizes the synthetic load used by x/tools for
// the value conversion [N]T(s). The preceding SliceToArrayPointer owns the
// length fault. Consequently N>0 is non-nil on its continuation, while N==0
// must not acquire a spurious nil fault for the legal nil-slice conversion.
func coroSliceToArrayValueDeref(deref *ssa.UnOp, typeOf func(types.Type) types.Type) (*ssa.SliceToArrayPointer, int64, bool) {
	if deref == nil || deref.Op != token.MUL || deref.X == nil {
		return nil, 0, false
	}
	conversion, ok := deref.X.(*ssa.SliceToArrayPointer)
	if !ok || conversion.Type() == nil || deref.Type() == nil ||
		conversion.Pos() != token.NoPos || deref.Pos() == token.NoPos {
		return nil, 0, false
	}
	pointerType, valueType := conversion.Type(), deref.Type()
	if typeOf != nil {
		pointerType, valueType = typeOf(pointerType), typeOf(valueType)
	}
	pointer, ok := types.Unalias(pointerType).Underlying().(*types.Pointer)
	if !ok || !types.Identical(pointer.Elem(), valueType) {
		return nil, 0, false
	}
	length, exact := coroSliceToArrayPointerLen(conversion, typeOf)
	return conversion, length, exact
}

func (p *context) compileCoroSliceToArrayPointer(
	b llssa.Builder,
	conversion *ssa.SliceToArrayPointer,
	x llssa.Expr,
	typ llssa.Type,
	plan coroPhysicalInstructionPlan,
) llssa.Expr {
	body := p.coroBody()
	if body == nil || conversion == nil || b == nil || b.Func != p.fn {
		panic("structured slice-to-array-pointer conversion escaped its physical coroutine body")
	}
	if p.compilation == nil || !p.compilation.EnableCoroExplicitStatusPanicABI ||
		body.abi.version < coroPhysicalABIVersionV1 {
		panic("slice-to-array-pointer fault requires the PhysicalABIV1 explicit-status panic ABI")
	}
	if plan.recipe != coroPhysicalInstructionSliceToArrayPointer || plan.bound < 0 ||
		plan.boundsGuard != (plan.bound != 0) {
		panic(fmt.Sprintf("invalid frozen slice-to-array-pointer recipe for %s", conversion))
	}
	if plan.boundsGuard {
		p.observeCoroPhysicalBoundsGuard(conversion)
		limit := b.Prog.IntVal(uint64(plan.bound), b.Prog.Int())
		tooShort := b.BinOp(token.LSS, b.SliceLen(x), limit)
		p.compileCoroFaultConditionGuard(b, tooShort, coroFaultSliceConvertV1)
	}
	return b.SliceToArrayPointerUnchecked(x, typ)
}
