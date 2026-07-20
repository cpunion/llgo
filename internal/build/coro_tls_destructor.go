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
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
)

// coroTLSField identifies one exact field of one concrete SSA struct type.
// Generic instances intentionally remain distinct: a destructor target for
// slot[A] says nothing about slot[B].
type coroTLSField struct {
	container types.Type
	index     int
	typ       types.Type
}

type coroTLSFieldAccesses struct {
	loads  []*ssa.UnOp
	stores []*ssa.Store
}

// proveCoroTLSDestructorClosedDynamicCalls recognizes only the compiler-owned
// TLS callback shape used by runtime/internal/clite/tls. The proof is derived
// from exact frozen SSA objects; source names are not used to invent targets.
//
// The proof is deliberately object-insensitive but field- and concrete-type-
// sensitive. That is sound for these unexported fields once every normal field
// write and every field-address use in the frozen program has been audited.
// Unsafe writes through a tracked aggregate pointer and interface publication
// fail closed, apart from the runtime's exact opaque-pointer ingress and its
// frozen read-only rootRange helper.
func proveCoroTLSDestructorClosedDynamicCalls(ctx *context) (map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate, error) {
	result := make(map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate)
	if ctx == nil || ctx.coroEmission == nil || ctx.coroSSAEmission == nil || ctx.prog == nil {
		return result, nil
	}
	functions, err := coroFrozenGoBodies(ctx)
	if err != nil {
		return nil, err
	}
	for _, owner := range functions {
		if !coroTLSConcreteFunction(owner) {
			continue
		}
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil || call.Common().StaticCallee() == nil {
					continue
				}
				for argument, value := range call.Common().Args {
					parameter, ok := staticCallArgumentParameterType(call, argument)
					if !ok || ctx.prog.TypeBackground(parameter) != llssa.InC {
						continue
					}
					if _, functionType := types.Unalias(parameter).Underlying().(*types.Signature); !functionType {
						continue
					}
					callback, ok := exactCoroStaticFunctionValue(ctx, value)
					if !ok || !coroTLSConcreteFunction(callback) {
						continue
					}
					certifiedCall, certificate, candidate, err := proveOneCoroTLSDestructorCallback(ctx, functions, callback)
					if err != nil {
						return nil, fmt.Errorf("prove TLS direct-plain callback %q in %q: %w", callback.Name(), owner.Name(), err)
					}
					if !candidate {
						continue
					}
					if previous, exists := result[certifiedCall]; exists && !sameCoroClosedDynamicCallCertificate(previous, certificate) {
						return nil, fmt.Errorf("TLS dynamic call in %q has conflicting frozen certificates", callback.Name())
					}
					result[certifiedCall] = cloneCoroClosedDynamicCallCertificate(certificate)
				}
			}
		}
	}
	return result, nil
}

func coroFrozenGoBodies(ctx *context) ([]*ssa.Function, error) {
	functions := make([]*ssa.Function, 0, len(ctx.coroSSAEmission.Functions()))
	for _, fn := range ctx.coroSSAEmission.Functions() {
		goBody, err := frozenGoEmittedBody(ctx.coroEmission, fn)
		if err != nil {
			return nil, fmt.Errorf("classify frozen Go body %q: %w", fn.Name(), err)
		}
		if goBody {
			functions = append(functions, fn)
		}
	}
	return functions, nil
}

func coroTLSConcreteFunction(fn *ssa.Function) bool {
	if fn == nil || fn.Signature == nil || len(fn.Blocks) == 0 || len(fn.FreeVars) != 0 {
		return false
	}
	return (fn.Signature.TypeParams() == nil || fn.Signature.TypeParams().Len() == 0) &&
		(fn.Signature.RecvTypeParams() == nil || fn.Signature.RecvTypeParams().Len() == 0)
}

func proveOneCoroTLSDestructorCallback(
	ctx *context,
	functions []*ssa.Function,
	callback *ssa.Function,
) (ssa.CallInstruction, coro.SSAClosedDynamicCallCertificate, bool, error) {
	if !coroTLSFunctionInOwnedPackage(ctx, callback) {
		return nil, coro.SSAClosedDynamicCallCertificate{}, false, nil
	}
	goBody, err := frozenGoEmittedBody(ctx.coroEmission, callback)
	if err != nil {
		return nil, coro.SSAClosedDynamicCallCertificate{}, false, err
	}
	if !goBody || len(callback.FreeVars) != 0 {
		return nil, coro.SSAClosedDynamicCallCertificate{}, false, nil
	}

	var dynamicCalls []ssa.CallInstruction
	var fieldCalls []ssa.CallInstruction
	var slotField coroTLSField
	for _, block := range callback.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil {
				continue
			}
			if _, builtin := call.Common().Value.(*ssa.Builtin); builtin || call.Common().StaticCallee() != nil {
				continue
			}
			dynamicCalls = append(dynamicCalls, call)
			if _, field, ok := coroTLSExactFieldLoad(call.Common().Value); ok {
				fieldCalls = append(fieldCalls, call)
				slotField = field
			}
		}
	}
	if len(fieldCalls) == 0 {
		return nil, coro.SSAClosedDynamicCallCertificate{}, false, nil
	}
	if len(dynamicCalls) != 1 || len(fieldCalls) != 1 {
		// A field call alone does not make an arbitrary C callback part of the
		// TLS destructor protocol. A real protocol callback must have the exact
		// single-dynamic-call shape; otherwise leave it to the ordinary C callback
		// closure proof instead of turning unrelated callbacks into TLS errors.
		return nil, coro.SSAClosedDynamicCallCertificate{}, false, nil
	}
	dynamicCall := fieldCalls[0]
	if _, ordinary := dynamicCall.(*ssa.Call); !ordinary || dynamicCall.Common().IsInvoke() {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("field-loaded destructor must be an ordinary dynamic *ssa.Call")
	}
	calleeLoad, _, _ := coroTLSExactFieldLoad(dynamicCall.Common().Value)

	slotAccesses, err := collectCoroTLSFieldAccesses(functions, slotField)
	if err != nil {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("audit destination destructor field: %w", err)
	}
	if err := auditCoroTLSSlotLoads(slotAccesses.loads, calleeLoad, dynamicCall); err != nil {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, err
	}
	var sourceField coroTLSField
	nonnilStores := 0
	nilStores := 0
	for _, store := range slotAccesses.stores {
		if coroTLSNilFunctionValue(store.Val) {
			nilStores++
			continue
		}
		_, field, ok := coroTLSExactFieldLoad(store.Val)
		if !ok {
			return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("destination destructor field has an unknown non-nil write in %q", store.Parent().Name())
		}
		if nonnilStores != 0 && !sameCoroTLSField(sourceField, field) {
			return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("destination destructor field has multiple source fields")
		}
		sourceField = field
		nonnilStores++
	}
	if nonnilStores != 1 || nilStores == 0 {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("destination destructor field writes are not the exact source-plus-nil pattern (source=%d nil=%d)", nonnilStores, nilStores)
	}
	if sameCoroTLSField(slotField, sourceField) {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("destination destructor field feeds itself")
	}

	sourceAccesses, err := collectCoroTLSFieldAccesses(functions, sourceField)
	if err != nil {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("audit source destructor field: %w", err)
	}
	if err := auditCoroTLSSourceLoads(sourceAccesses.loads, slotField); err != nil {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, err
	}
	var formal *ssa.Parameter
	formalStores := 0
	for _, store := range sourceAccesses.stores {
		if coroTLSNilFunctionValue(store.Val) {
			continue
		}
		parameter, ok := store.Val.(*ssa.Parameter)
		if !ok || (formal != nil && formal != parameter) {
			return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("source destructor field has a write not owned by one exact formal parameter")
		}
		formal = parameter
		formalStores++
	}
	if formal == nil || formalStores != 1 || formal.Parent() == nil {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("source destructor field is not initialized exactly once from an allocator formal")
	}
	if err := auditCoroTLSFormalUses(formal, sourceField); err != nil {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, err
	}
	formalIndex := -1
	for index, parameter := range formal.Parent().Params {
		if parameter == formal {
			formalIndex = index
			break
		}
	}
	if formalIndex < 0 {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("allocator destructor formal is absent from its SSA parameter list")
	}
	if !coroTLSFunctionTypeMatchesSignature(formal.Type(), dynamicCall.Common().Signature()) ||
		!coroTLSFunctionTypeMatchesSignature(slotField.typ, dynamicCall.Common().Signature()) ||
		!types.Identical(types.Unalias(formal.Type()).Underlying(), types.Unalias(sourceField.typ).Underlying()) {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, fmt.Errorf("allocator, source field, destination field, and dynamic call signatures differ")
	}

	certificate, err := collectCoroTLSAllocatorTargets(ctx, functions, formal.Parent(), formalIndex, formal.Type())
	if err != nil {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, err
	}
	if err := auditCoroTLSTrackedEscapes(ctx, functions, slotField, sourceField); err != nil {
		return nil, coro.SSAClosedDynamicCallCertificate{}, true, err
	}
	// This exact field-flow proof is also the authority that the compiler-owned
	// pthread destructor callback invokes the selected descriptor synchronously
	// on its current stack. No function-name or target-effect inference grants
	// this call-site protocol to other descriptor consumers.
	certificate.SyncDispatch = true
	return dynamicCall, certificate, true, nil
}

func coroTLSFunctionInOwnedPackage(ctx *context, fn *ssa.Function) bool {
	if fn == nil {
		return false
	}
	identity := fn
	if origin := fn.Origin(); origin != nil {
		identity = origin
	}
	if identity.Pkg == nil || identity.Pkg.Pkg == nil {
		return false
	}
	expected := strings.TrimSuffix(llssa.PkgRuntime, "/internal/runtime") + "/internal/clite/tls"
	if ctx != nil && ctx.coroTLSDestructorFixturePkg != "" {
		expected = ctx.coroTLSDestructorFixturePkg
	}
	return llssa.PathOf(identity.Pkg.Pkg) == expected
}

func coroTLSExactFieldLoad(value ssa.Value) (*ssa.UnOp, coroTLSField, bool) {
	load, ok := value.(*ssa.UnOp)
	if !ok || load.Op != token.MUL {
		return nil, coroTLSField{}, false
	}
	field, ok := load.X.(*ssa.FieldAddr)
	if !ok {
		return nil, coroTLSField{}, false
	}
	key, ok := coroTLSFieldOf(field)
	return load, key, ok
}

func coroTLSFieldOf(field *ssa.FieldAddr) (coroTLSField, bool) {
	if field == nil || field.X == nil || field.X.Type() == nil {
		return coroTLSField{}, false
	}
	pointer, ok := types.Unalias(field.X.Type()).Underlying().(*types.Pointer)
	if !ok {
		return coroTLSField{}, false
	}
	container := types.Unalias(pointer.Elem())
	structure, ok := container.Underlying().(*types.Struct)
	if !ok || field.Field < 0 || field.Field >= structure.NumFields() {
		return coroTLSField{}, false
	}
	typ := structure.Field(field.Field).Type()
	if _, ok := types.Unalias(typ).Underlying().(*types.Signature); !ok {
		return coroTLSField{}, false
	}
	return coroTLSField{container: container, index: field.Field, typ: typ}, true
}

func sameCoroTLSField(left, right coroTLSField) bool {
	return left.index == right.index && left.container != nil && right.container != nil && types.Identical(left.container, right.container)
}

func collectCoroTLSFieldAccesses(functions []*ssa.Function, field coroTLSField) (coroTLSFieldAccesses, error) {
	var result coroTLSFieldAccesses
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				address, ok := instruction.(*ssa.FieldAddr)
				if !ok {
					continue
				}
				candidate, ok := coroTLSFieldOf(address)
				if !ok || !sameCoroTLSField(field, candidate) {
					continue
				}
				refs := address.Referrers()
				if refs == nil {
					return coroTLSFieldAccesses{}, fmt.Errorf("field address in %q has no frozen referrer set", owner.Name())
				}
				for _, ref := range *refs {
					switch ref := ref.(type) {
					case *ssa.DebugRef:
					case *ssa.UnOp:
						if ref.X != address || ref.Op != token.MUL {
							return coroTLSFieldAccesses{}, fmt.Errorf("field address has a non-load unary use in %q", owner.Name())
						}
						result.loads = append(result.loads, ref)
					case *ssa.Store:
						if ref.Addr != address {
							return coroTLSFieldAccesses{}, fmt.Errorf("field address escapes as a stored value in %q", owner.Name())
						}
						result.stores = append(result.stores, ref)
					default:
						return coroTLSFieldAccesses{}, fmt.Errorf("field address escapes through %T in %q", ref, owner.Name())
					}
				}
			}
		}
	}
	return result, nil
}

func auditCoroTLSSlotLoads(loads []*ssa.UnOp, callee *ssa.UnOp, call ssa.CallInstruction) error {
	if len(loads) < 2 || callee == nil {
		return fmt.Errorf("destination destructor field lacks an exact nil guard and call load")
	}
	guarded := false
	called := false
	for _, load := range loads {
		refs := load.Referrers()
		if refs == nil || len(*refs) == 0 {
			return fmt.Errorf("destination destructor load in %q has no use", load.Parent().Name())
		}
		for _, ref := range *refs {
			if _, debug := ref.(*ssa.DebugRef); debug {
				continue
			}
			if load == callee && ref == call {
				called = true
				continue
			}
			comparison, ok := ref.(*ssa.BinOp)
			if !ok || (comparison.Op != token.EQL && comparison.Op != token.NEQ) || !coroTLSComparisonWithNil(comparison, load) {
				return fmt.Errorf("destination destructor load escapes through %T in %q", ref, load.Parent().Name())
			}
			guarded = guarded || coroTLSComparisonGuardsCall(comparison, call)
		}
	}
	if !guarded || !called {
		return fmt.Errorf("destination destructor field is not control-flow nil-guarded before its exact dynamic call")
	}
	return nil
}

func coroTLSComparisonGuardsCall(comparison *ssa.BinOp, call ssa.CallInstruction) bool {
	if comparison == nil || call == nil || comparison.Block() == nil || call.Block() == nil {
		return false
	}
	refs := comparison.Referrers()
	if refs == nil {
		return false
	}
	var branch *ssa.If
	for _, ref := range *refs {
		if _, debug := ref.(*ssa.DebugRef); debug {
			continue
		}
		candidate, ok := ref.(*ssa.If)
		if !ok || candidate.Cond != comparison || branch != nil {
			return false
		}
		branch = candidate
	}
	if branch == nil || len(branch.Block().Succs) != 2 {
		return false
	}
	nonNilSuccessor := 0
	if comparison.Op == token.EQL {
		nonNilSuccessor = 1
	}
	return branch.Block().Succs[nonNilSuccessor].Dominates(call.Block())
}

func coroTLSComparisonWithNil(comparison *ssa.BinOp, value ssa.Value) bool {
	if comparison == nil {
		return false
	}
	other := comparison.X
	if other == value {
		other = comparison.Y
	} else if comparison.Y != value {
		return false
	}
	constant, ok := other.(*ssa.Const)
	return ok && constant.IsNil()
}

func auditCoroTLSSourceLoads(loads []*ssa.UnOp, destination coroTLSField) error {
	if len(loads) == 0 {
		return fmt.Errorf("source destructor field is never copied to the destination field")
	}
	for _, load := range loads {
		refs := load.Referrers()
		if refs == nil || len(*refs) == 0 {
			return fmt.Errorf("source destructor load in %q has no use", load.Parent().Name())
		}
		for _, ref := range *refs {
			if _, debug := ref.(*ssa.DebugRef); debug {
				continue
			}
			store, ok := ref.(*ssa.Store)
			if !ok || store.Val != load {
				return fmt.Errorf("source destructor load escapes through %T in %q", ref, load.Parent().Name())
			}
			address, ok := store.Addr.(*ssa.FieldAddr)
			field, fieldOK := coroTLSFieldOf(address)
			if !ok || !fieldOK || !sameCoroTLSField(field, destination) {
				return fmt.Errorf("source destructor load is stored outside the exact destination field in %q", load.Parent().Name())
			}
		}
	}
	return nil
}

func auditCoroTLSFormalUses(formal *ssa.Parameter, source coroTLSField) error {
	refs := formal.Referrers()
	if refs == nil || len(*refs) == 0 {
		return fmt.Errorf("allocator destructor formal has no uses")
	}
	for _, ref := range *refs {
		if _, debug := ref.(*ssa.DebugRef); debug {
			continue
		}
		store, ok := ref.(*ssa.Store)
		if !ok || store.Val != formal {
			return fmt.Errorf("allocator destructor formal escapes through %T in %q", ref, formal.Parent().Name())
		}
		address, ok := store.Addr.(*ssa.FieldAddr)
		field, fieldOK := coroTLSFieldOf(address)
		if !ok || !fieldOK || !sameCoroTLSField(field, source) {
			return fmt.Errorf("allocator destructor formal is stored outside the exact source field")
		}
	}
	return nil
}

func collectCoroTLSAllocatorTargets(
	ctx *context,
	functions []*ssa.Function,
	allocator *ssa.Function,
	formalIndex int,
	formalType types.Type,
) (coro.SSAClosedDynamicCallCertificate, error) {
	if err := auditCoroTLSAllocatorUses(ctx, functions, allocator); err != nil {
		return coro.SSAClosedDynamicCallCertificate{}, err
	}
	certificate := coro.SSAClosedDynamicCallCertificate{MayBeNil: true}
	callSites := 0
	var target *ssa.Function
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil || call.Common().StaticCallee() == nil {
					continue
				}
				resolved, ok := ctx.coroEmission.Resolve(call.Common().StaticCallee())
				if !ok || resolved != allocator {
					continue
				}
				if _, ordinary := call.(*ssa.Call); !ordinary || formalIndex >= len(call.Common().Args) {
					return coro.SSAClosedDynamicCallCertificate{}, fmt.Errorf("allocator destructor formal is reached through go/defer or a malformed call in %q", owner.Name())
				}
				callSites++
				certificate.SyncOnlyCallArguments = append(certificate.SyncOnlyCallArguments, coro.SSASyncOnlyCallArgument{
					Call:     call,
					Argument: formalIndex,
				})
				actual := call.Common().Args[formalIndex]
				if coroTLSNilFunctionValue(actual) {
					continue
				}
				candidate, ok := exactCoroStaticFunctionValue(ctx, actual)
				if !ok || candidate == nil || len(candidate.FreeVars) != 0 {
					return coro.SSAClosedDynamicCallCertificate{}, fmt.Errorf("allocator destructor actual in %q is not nil or one exact no-capture function", owner.Name())
				}
				goBody, err := frozenGoEmittedBody(ctx.coroEmission, candidate)
				if err != nil {
					return coro.SSAClosedDynamicCallCertificate{}, err
				}
				if !goBody || !coroTLSFunctionTypeMatchesSignature(formalType, candidate.Signature) {
					return coro.SSAClosedDynamicCallCertificate{}, fmt.Errorf("allocator destructor target %q is not an owned exact-signature Go body", candidate.Name())
				}
				if target != nil && target != candidate {
					return coro.SSAClosedDynamicCallCertificate{}, fmt.Errorf("allocator destructor field has multiple non-nil targets %q and %q", target.Name(), candidate.Name())
				}
				target = candidate
			}
		}
	}
	if callSites == 0 {
		return coro.SSAClosedDynamicCallCertificate{}, fmt.Errorf("allocator destructor formal has no exact frozen call sites")
	}
	if target != nil {
		certificate.Targets = []*ssa.Function{target}
	}
	return certificate, nil
}

func auditCoroTLSAllocatorUses(ctx *context, functions []*ssa.Function, allocator *ssa.Function) error {
	if allocator == nil {
		return fmt.Errorf("allocator function is nil")
	}
	operands := make([]*ssa.Value, 0, 8)
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				operands = instruction.Operands(operands[:0])
				usesAllocator := false
				for _, operand := range operands {
					if operand != nil && *operand == allocator {
						usesAllocator = true
						break
					}
				}
				if !usesAllocator {
					continue
				}
				call, ok := instruction.(*ssa.Call)
				if !ok || call.Common() == nil || call.Common().Value != allocator || call.Common().StaticCallee() == nil {
					return fmt.Errorf("allocator function escapes through %T in %q", instruction, owner.Name())
				}
				resolved, ok := ctx.coroEmission.Resolve(call.Common().StaticCallee())
				if !ok || resolved != allocator {
					return fmt.Errorf("allocator function has a non-exact static use in %q", owner.Name())
				}
			}
		}
	}
	return nil
}

func coroTLSNilFunctionValue(value ssa.Value) bool {
	for value != nil {
		switch current := value.(type) {
		case *ssa.Const:
			return current.IsNil()
		case *ssa.ChangeType:
			value = current.X
		case *ssa.Convert:
			value = current.X
		default:
			return false
		}
	}
	return false
}

func coroTLSFunctionTypeMatchesSignature(typ types.Type, signature *types.Signature) bool {
	if typ == nil || signature == nil {
		return false
	}
	function, ok := types.Unalias(typ).Underlying().(*types.Signature)
	return ok && types.Identical(function, signature)
}

func auditCoroTLSTrackedEscapes(
	ctx *context,
	functions []*ssa.Function,
	slot, source coroTLSField,
) error {
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				switch instruction := instruction.(type) {
				case *ssa.Store:
					if coroTLSExactType(instruction.Val.Type(), slot.container) {
						return fmt.Errorf("tracked TLS aggregate has a whole-value write in %q", owner.Name())
					}
				case *ssa.MakeInterface:
					if coroTLSTypeContains(instruction.X.Type(), slot.container) || coroTLSTypeContains(instruction.X.Type(), source.container) {
						return fmt.Errorf("tracked TLS aggregate escapes through interface conversion in %q", owner.Name())
					}
				case *ssa.TypeAssert:
					if coroTLSTypeContains(instruction.AssertedType, slot.container) || coroTLSTypeContains(instruction.AssertedType, source.container) {
						return fmt.Errorf("tracked TLS aggregate enters through interface assertion in %q", owner.Name())
					}
				case *ssa.Convert:
					fromSlot := coroTLSPointerTo(instruction.X.Type(), slot.container)
					toSlot := coroTLSPointerTo(instruction.Type(), slot.container)
					fromSource := coroTLSPointerTo(instruction.X.Type(), source.container)
					toSource := coroTLSPointerTo(instruction.Type(), source.container)
					if !fromSlot && !toSlot && !fromSource && !toSource {
						continue
					}
					if toSlot && coroTLSUnsafePointerLike(instruction.X.Type()) &&
						coroTLSExactOpaqueSlotIngress(ctx, owner, slot.container) {
						// Opaque pthread/C allocation pointers enter typed Go code in
						// these exact compiler-owned TLS accessors. Every typed
						// destructor-field write is still enumerated above.
						continue
					}
					if fromSlot && coroTLSUnsafePointerLike(instruction.Type()) &&
						coroTLSExactRootRangeHelper(ctx, owner, slot.container) {
						// rootRange computes the frozen GC scan interval. It does not
						// publish the destructor field address or write through it.
						continue
					}
					return fmt.Errorf("tracked TLS aggregate crosses unsafe conversion in %q", owner.Name())
				case *ssa.ChangeType:
					if coroTLSPointerTo(instruction.X.Type(), slot.container) || coroTLSPointerTo(instruction.Type(), slot.container) ||
						coroTLSPointerTo(instruction.X.Type(), source.container) || coroTLSPointerTo(instruction.Type(), source.container) {
						return fmt.Errorf("tracked TLS aggregate crosses named pointer conversion in %q", owner.Name())
					}
				case *ssa.MakeClosure:
					for _, binding := range instruction.Bindings {
						if coroTLSTypeContains(binding.Type(), slot.container) || coroTLSTypeContains(binding.Type(), source.container) {
							return fmt.Errorf("tracked TLS aggregate escapes into closure in %q", owner.Name())
						}
					}
				case ssa.CallInstruction:
					if !coroTLSCallCarriesTrackedPointer(instruction, slot.container, source.container) {
						continue
					}
					if builtin, ok := instruction.Common().Value.(*ssa.Builtin); ok && builtin.Name() == "ssa:wrapnilchk" {
						// The SSA builder's value-receiver wrapper checks then dereferences
						// its receiver; it neither publishes nor mutates the aggregate.
						continue
					}
					call, ordinary := instruction.(*ssa.Call)
					if !ordinary || call.Common() == nil || call.Common().StaticCallee() == nil {
						return fmt.Errorf("tracked TLS aggregate pointer escapes through a dynamic, go, or defer call %q (%T) in %q", instruction.String(), instruction, owner.Name())
					}
					callee, ok := ctx.coroEmission.Resolve(call.Common().StaticCallee())
					if !ok {
						return fmt.Errorf("tracked TLS aggregate pointer reaches an unresolved callee in %q", owner.Name())
					}
					goBody, err := frozenGoEmittedBody(ctx.coroEmission, callee)
					if err != nil {
						return err
					}
					if !goBody {
						return fmt.Errorf("tracked TLS aggregate pointer escapes to a non-Go callee in %q", owner.Name())
					}
				}
			}
		}
	}
	return nil
}

func coroTLSExactType(typ, tracked types.Type) bool {
	return typ != nil && tracked != nil && types.Identical(types.Unalias(typ), tracked)
}

func coroTLSCallCarriesTrackedPointer(call ssa.CallInstruction, tracked ...types.Type) bool {
	if call == nil || call.Common() == nil {
		return false
	}
	for _, argument := range call.Common().Args {
		for _, typ := range tracked {
			if coroTLSPointerTo(argument.Type(), typ) {
				return true
			}
		}
	}
	return false
}

func coroTLSPointerTo(typ, container types.Type) bool {
	if typ == nil || container == nil {
		return false
	}
	pointer, ok := types.Unalias(typ).Underlying().(*types.Pointer)
	return ok && types.Identical(types.Unalias(pointer.Elem()), container)
}

func coroTLSUnsafePointerLike(typ types.Type) bool {
	if typ == nil {
		return false
	}
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.UnsafePointer
}

func coroTLSTypeContains(typ, tracked types.Type) bool {
	if typ == nil || tracked == nil {
		return false
	}
	typ = types.Unalias(typ)
	if types.Identical(typ, tracked) {
		return true
	}
	if pointer, ok := typ.Underlying().(*types.Pointer); ok {
		return types.Identical(types.Unalias(pointer.Elem()), tracked)
	}
	return false
}

func coroTLSExactOpaqueSlotIngress(ctx *context, fn *ssa.Function, slot types.Type) bool {
	if !coroTLSFunctionInOwnedPackage(ctx, fn) || fn.Signature == nil {
		return false
	}
	identity := fn
	if origin := fn.Origin(); origin != nil {
		identity = origin
	}
	switch identity.Name() {
	case "Get", "Clear", "ensureSlot", "slotDestructor":
		return true
	default:
		return false
	}
}

func coroTLSExactRootRangeHelper(ctx *context, fn *ssa.Function, slot types.Type) bool {
	if !coroTLSFunctionInOwnedPackage(ctx, fn) || fn.Signature == nil {
		return false
	}
	identity := fn
	if origin := fn.Origin(); origin != nil {
		identity = origin
	}
	if identity.Name() != "rootRange" {
		return false
	}
	receiver := fn.Signature.Recv()
	if receiver == nil || !coroTLSPointerTo(receiver.Type(), slot) {
		return false
	}
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			switch instruction := instruction.(type) {
			case *ssa.Store, *ssa.MapUpdate, *ssa.Send, *ssa.Go, *ssa.Defer:
				return false
			case ssa.CallInstruction:
				if instruction.Common() == nil {
					return false
				}
				if _, builtin := instruction.Common().Value.(*ssa.Builtin); !builtin {
					return false
				}
			}
		}
	}
	return true
}

func cloneCoroClosedDynamicCallCertificate(certificate coro.SSAClosedDynamicCallCertificate) coro.SSAClosedDynamicCallCertificate {
	return coro.SSAClosedDynamicCallCertificate{
		Targets:               append([]*ssa.Function(nil), certificate.Targets...),
		MayBeNil:              certificate.MayBeNil,
		SyncDispatch:          certificate.SyncDispatch,
		SyncOnlyCallArguments: append([]coro.SSASyncOnlyCallArgument(nil), certificate.SyncOnlyCallArguments...),
	}
}

func sameCoroClosedDynamicCallCertificate(left, right coro.SSAClosedDynamicCallCertificate) bool {
	if left.MayBeNil != right.MayBeNil || left.SyncDispatch != right.SyncDispatch ||
		len(left.Targets) != len(right.Targets) || len(left.SyncOnlyCallArguments) != len(right.SyncOnlyCallArguments) {
		return false
	}
	for index := range left.Targets {
		if left.Targets[index] != right.Targets[index] {
			return false
		}
	}
	for index := range left.SyncOnlyCallArguments {
		if left.SyncOnlyCallArguments[index] != right.SyncOnlyCallArguments[index] {
			return false
		}
	}
	return true
}

func validateRequiredCoroClosedDynamicCalls(
	plan *coro.SSAPlan,
	certificates map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate,
	globalSlots map[ssa.CallInstruction]coroGlobalFunctionSlotProof,
) error {
	if len(certificates) == 0 && len(globalSlots) == 0 {
		return nil
	}
	if plan == nil {
		return fmt.Errorf("compiler closed dynamic call validation requires a coroutine plan")
	}
	stores, err := collectCoroGlobalFunctionSlotStores(globalSlots)
	if err != nil {
		return fmt.Errorf("compiler conditional global function-slot Store validation: %w", err)
	}
	for call, certificate := range certificates {
		callPlan, ok := plan.CallPlan(call)
		if !ok || callPlan.Rep != coro.Dispatch || callPlan.Open || callPlan.SyncDispatch != certificate.SyncDispatch ||
			callPlan.MayBeNil != certificate.MayBeNil || len(callPlan.Targets) != len(certificate.Targets) {
			return fmt.Errorf("compiler closed dynamic call in %q did not retain its exact closed Dispatch plan", call.Parent().Name())
		}
		for index, target := range certificate.Targets {
			id, ok := plan.FunctionID(target)
			if !ok || callPlan.Targets[index] != id {
				return fmt.Errorf("compiler closed dynamic call in %q lost target %q", call.Parent().Name(), target.Name())
			}
			if !certificate.SyncDispatch {
				continue
			}
			function, ok := plan.FunctionPlan(target)
			if !ok || function.External != coro.Defined || function.Effect != coro.NoSuspend || function.Exec.Contains(coro.NeedsPreempt) ||
				function.FuncRep != coro.Dispatch || function.Primary != coro.PrimaryPlain || function.Emission != coro.EmitPlain {
				return fmt.Errorf("compiler TLS destructor target %q is not a defined non-suspending descriptor-backed plain body (external=%s effect=%s exec=%s representation=%s primary=%s emission=%s)",
					target.Name(), function.External, function.Effect, function.Exec, function.FuncRep, function.Primary, function.Emission)
			}
		}
	}
	for call, proof := range globalSlots {
		certificate, certified := certificates[call]
		if !certified || proof.call != call || proof.global == nil || proof.identityID == "" ||
			proof.physicalSymbol == "" || len(proof.members) == 0 ||
			!sameCoroClosedDynamicCallCertificate(certificate, proof.certificate) {
			return fmt.Errorf("compiler global function-slot proof in %q lost its exact closed dynamic certificate", call.Parent().Name())
		}
		global, direct := coroDirectGlobalFunctionSlotLoad(call.Common().Value)
		member := false
		for _, candidate := range proof.members {
			member = member || candidate == global
		}
		if !direct || !member {
			return fmt.Errorf("compiler global function-slot proof in %q no longer names its exact package-level cell", call.Parent().Name())
		}
		for _, hazard := range proof.inactive {
			if hazard.owner == nil {
				return fmt.Errorf("compiler global function-slot proof for %q has a nil conditional owner", proof.physicalSymbol)
			}
			function, planned := plan.FunctionPlan(hazard.owner)
			if !planned || function.Emission != coro.EmitNone {
				return fmt.Errorf("compiler global function-slot proof for %q omitted an active writer/escape in %q: %s (emission=%s)",
					proof.physicalSymbol, hazard.owner.Name(), hazard.reason, function.Emission)
			}
		}
	}
	for _, publication := range stores {
		owner, ownerPlanned := plan.FunctionPlan(publication.owner)
		if !ownerPlanned {
			return fmt.Errorf("compiler conditional global function-slot Store owner %q is absent from the final plan", publication.owner.Name())
		}
		plannedTarget, certified := plan.ConditionalManagedStoreTarget(publication.store)
		if !certified || plannedTarget != publication.target {
			return fmt.Errorf("compiler conditional global function-slot Store in %q lost its exact target", publication.owner.Name())
		}
		target, targetPlanned := plan.FunctionPlan(publication.target)
		value, valuePlanned := plan.ValuePlan(publication.target)
		if !targetPlanned || target.External != coro.Defined ||
			!valuePlanned || len(value.Funcs) != 1 || value.Funcs[0].Rep != coro.Dispatch {
			return fmt.Errorf("compiler conditional global function-slot Store target %q lost its managed descriptor plan (owner=%s target=%+v value=%+v/%t)",
				publication.target.Name(), owner.Emission, target, value, valuePlanned)
		}
		if target.ManagedDemand == coro.NoDemand {
			if owner.Emission != coro.EmitNone && !plan.ElidesConditionalManagedStore(publication.store) {
				return fmt.Errorf("compiler conditional global function-slot Store in %q did not retain its dormant-publication elision", publication.owner.Name())
			}
			continue
		}
		if plan.ElidesConditionalManagedStore(publication.store) {
			return fmt.Errorf("compiler conditional global function-slot Store target %q is live without managed demand", publication.target.Name())
		}
	}
	return nil
}
