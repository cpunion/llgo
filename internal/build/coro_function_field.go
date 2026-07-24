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

package build

import (
	"fmt"
	"go/types"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// proveCoroPrivateFunctionFieldCalls closes ordinary calls through one exact
// private function field when the frozen Go universe contains every typed
// access to that field. It is the general, synchronous-call counterpart of the
// more specialized TLS destructor proof:
//
//   - the concrete named container and field are both package-private;
//   - every field address is used only by a direct load or store;
//   - every non-nil store publishes the same exact context-free Go body;
//   - every load is consumed only as an ordinary dynamic-call callee; and
//   - the aggregate never crosses an unsafe, interface, closure, or non-Go
//     boundary that could hide another write.
//
// The certificate remains nullable because this object-insensitive proof does
// not claim that every possible zero-valued aggregate was initialized. A nil
// descriptor therefore retains the ordinary Go call panic while every normal
// return has the one frozen target.
func proveCoroPrivateFunctionFieldCalls(
	ctx *context,
) (map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate, error) {
	result := make(map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate)
	if ctx == nil || ctx.coroEmission == nil || ctx.coroSSAEmission == nil || ctx.prog == nil {
		return result, nil
	}
	functions, err := coroFrozenGoBodies(ctx)
	if err != nil {
		return nil, err
	}

	fields := make(map[coroPrivateFunctionFieldKey]coroTLSField)
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(*ssa.Call)
				if !ok || call.Common() == nil || call.Common().StaticCallee() != nil ||
					call.Common().IsInvoke() {
					continue
				}
				_, field, loaded := coroTLSExactFieldLoad(call.Common().Value)
				key, private := coroPrivateFunctionField(field)
				if loaded && private {
					fields[key] = field
				}
			}
		}
	}

	for _, field := range fields {
		accesses, err := collectCoroTLSFieldAccesses(functions, field)
		if err != nil {
			// This is an optional proof for ordinary Go code. An escaped field
			// remains an ordinary open descriptor; it is not a malformed
			// compiler-owned protocol.
			continue
		}
		target, calls, proved, err := proveCoroPrivateFunctionFieldAccesses(ctx, field, accesses)
		if err != nil {
			return nil, err
		}
		if !proved {
			continue
		}
		if err := auditCoroTLSTrackedEscapes(ctx, functions, field, field); err != nil {
			continue
		}
		certificate := coro.SSAClosedDynamicCallCertificate{
			Targets:  []*ssa.Function{target},
			MayBeNil: true,
		}
		for _, call := range calls {
			if previous, exists := result[call]; exists &&
				!sameCoroClosedDynamicCallCertificate(previous, certificate) {
				return nil, fmt.Errorf(
					"private function-field call in %q has conflicting frozen certificates",
					call.Parent().Name(),
				)
			}
			result[call] = cloneCoroClosedDynamicCallCertificate(certificate)
		}
	}
	return result, nil
}

// Comparable identity is used only to deduplicate fields before the full
// types.Identical check performed by collectCoroTLSFieldAccesses.
type coroPrivateFunctionFieldKey struct {
	object *types.TypeName
	index  int
}

func coroPrivateFunctionField(field coroTLSField) (coroPrivateFunctionFieldKey, bool) {
	named, ok := types.Unalias(field.container).(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil || named.Obj().Exported() {
		return coroPrivateFunctionFieldKey{}, false
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok || field.index < 0 || field.index >= structure.NumFields() {
		return coroPrivateFunctionFieldKey{}, false
	}
	member := structure.Field(field.index)
	if member == nil || member.Pkg() != named.Obj().Pkg() || member.Exported() {
		return coroPrivateFunctionFieldKey{}, false
	}
	if _, ok := types.Unalias(member.Type()).Underlying().(*types.Signature); !ok {
		return coroPrivateFunctionFieldKey{}, false
	}
	return coroPrivateFunctionFieldKey{object: named.Obj(), index: field.index}, true
}

func proveCoroPrivateFunctionFieldAccesses(
	ctx *context,
	field coroTLSField,
	accesses coroTLSFieldAccesses,
) (*ssa.Function, []ssa.CallInstruction, bool, error) {
	var target *ssa.Function
	for _, store := range accesses.stores {
		if coroTLSNilFunctionValue(store.Val) {
			continue
		}
		candidate, ok := exactCoroStaticFunctionValue(ctx, store.Val)
		if !ok || candidate == nil || len(candidate.FreeVars) != 0 ||
			!coroTLSFunctionTypeMatchesSignature(field.typ, candidate.Signature) {
			return nil, nil, false, nil
		}
		goBody, err := frozenGoEmittedBody(ctx.coroEmission, candidate)
		if err != nil {
			return nil, nil, false, err
		}
		if !goBody || target != nil && target != candidate {
			return nil, nil, false, nil
		}
		target = candidate
	}
	if target == nil {
		return nil, nil, false, nil
	}

	calls := make([]ssa.CallInstruction, 0, len(accesses.loads))
	for _, load := range accesses.loads {
		refs := load.Referrers()
		if refs == nil || len(*refs) == 0 {
			return nil, nil, false, nil
		}
		used := false
		for _, ref := range *refs {
			if _, debug := ref.(*ssa.DebugRef); debug {
				continue
			}
			call, ok := ref.(*ssa.Call)
			if !ok || call.Common() == nil || call.Common().Value != load ||
				call.Common().StaticCallee() != nil || call.Common().IsInvoke() ||
				!types.Identical(call.Common().Signature(), target.Signature) {
				return nil, nil, false, nil
			}
			calls = append(calls, call)
			used = true
		}
		if !used {
			return nil, nil, false, nil
		}
	}
	return target, calls, len(calls) != 0, nil
}
