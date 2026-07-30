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
	"sort"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// coroInterfaceDispatchPlan is the immutable source-level proof required by
// receiver-aware interface dispatch. It deliberately contains no LLVM or
// scheduler state. A frontend consumer patches sourceCallSignature exactly
// once, then selects the ordinary or coroutine physical entry recorded by each
// candidate's FunctionPlan.
//
// mayBeNil preserves the ordinary Go nil-interface panic check. It is not an
// unresolved-target marker: every accepted candidate set is closed and
// nonempty.
type coroInterfaceDispatchPlan struct {
	call                ssa.CallInstruction
	receiver            ssa.Value
	iface               *types.Interface
	method              *types.Func
	sourceCallSignature *types.Signature
	mayBeNil            bool
	candidates          []coroInterfaceDispatchCandidate
}

type coroInterfaceDispatchCandidate struct {
	id             coro.FunctionID
	function       *ssa.Function
	plan           coro.FunctionPlan
	receiver       types.Type
	targetReceiver types.Type
	methodEntry    *ssa.Function
}

// resolveCoroInterfaceDispatchPlan freezes one interface operation recipe for
// frontend code generation. Ordinary calls, defers, and goroutine spawns share
// the exact receiver-aware candidate proof; their carrier alone determines
// whether the recipe executes now, from the frame-resident cleanup drainer, or
// as an independent scheduler root. The returned candidates are sorted by
// FunctionID, independent of SSA or map enumeration order. The source
// signature is receiver-free and shared by every candidate.
func resolveCoroInterfaceDispatchPlan(plan *coro.SSAPlan, universe *EmissionUniverse, call ssa.CallInstruction) (*coroInterfaceDispatchPlan, error) {
	if plan == nil || coroInterfaceDispatchCallIsNil(call) || call.Common() == nil {
		return nil, fmt.Errorf("coroutine interface dispatch requires an exact call and compilation plan")
	}
	kind := coro.CallDirect
	switch call.(type) {
	case *ssa.Defer:
		kind = coro.CallDefer
	case *ssa.Go:
		kind = coro.CallSpawn
	}
	common := call.Common()
	if !common.IsInvoke() || common.StaticCallee() != nil || common.Method == nil {
		return nil, fmt.Errorf("coroutine interface dispatch requires an interface invoke")
	}
	if call.Parent() == nil {
		return nil, fmt.Errorf("coroutine interface dispatch requires an invoke owned by an SSA function")
	}
	if universe != nil && universe.ownerOf(call.Parent()) == nil {
		return nil, fmt.Errorf("coroutine interface dispatch invoke owner is absent from the emission universe")
	}
	iface, ok := types.Unalias(common.Value.Type()).Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("coroutine interface dispatch receiver type %s is not an interface", common.Value.Type())
	}
	iface.Complete()

	callPlan, ok := plan.CallPlan(call)
	if !ok || callPlan.Call != call {
		return nil, fmt.Errorf("coroutine interface dispatch invoke has no exact compilation CallPlan")
	}
	if callPlan.Kind != kind || callPlan.Rep != coro.Dispatch || callPlan.Open || len(callPlan.Targets) == 0 {
		return nil, fmt.Errorf(
			"coroutine interface dispatch requires a closed nonempty Dispatch CallPlan for kind %v, got kind=%v representation=%s open=%t may-be-nil=%t targets=%d",
			kind, callPlan.Kind, callPlan.Rep, callPlan.Open, callPlan.MayBeNil, len(callPlan.Targets),
		)
	}

	sourceSignature, err := coroInterfaceDispatchSourceSignature(common)
	if err != nil {
		return nil, err
	}
	result := &coroInterfaceDispatchPlan{
		call:                call,
		receiver:            common.Value,
		iface:               iface,
		method:              common.Method,
		sourceCallSignature: sourceSignature,
		mayBeNil:            callPlan.MayBeNil,
		candidates:          make([]coroInterfaceDispatchCandidate, 0, len(callPlan.Targets)),
	}

	ids := append([]coro.FunctionID(nil), callPlan.Targets...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for index, id := range ids {
		if index != 0 && ids[index-1] == id {
			return nil, fmt.Errorf("coroutine interface dispatch repeats target ID %q", id)
		}
		target, found := plan.Function(id)
		if !found || target == nil {
			return nil, fmt.Errorf("coroutine interface dispatch target %q is absent from the compilation plan", id)
		}
		targetPlan, found := plan.FunctionPlan(target)
		if !found || targetPlan.ID != id {
			return nil, fmt.Errorf("coroutine interface dispatch target %q has no exact function plan", id)
		}
		receiver, targetReceiver, methodEntry, err := validateCoroInterfaceDispatchCandidate(
			common, iface, sourceSignature, universe, call.Parent(), id, target, targetPlan,
		)
		if err != nil {
			return nil, err
		}
		result.candidates = append(result.candidates, coroInterfaceDispatchCandidate{
			id:             id,
			function:       target,
			plan:           targetPlan,
			receiver:       receiver,
			targetReceiver: targetReceiver,
			methodEntry:    methodEntry,
		})
	}
	return result, nil
}

func coroInterfaceDispatchCallIsNil(call ssa.CallInstruction) bool {
	if call == nil {
		return true
	}
	switch call := call.(type) {
	case *ssa.Call:
		return call == nil
	case *ssa.Defer:
		return call == nil
	case *ssa.Go:
		return call == nil
	default:
		return false
	}
}

func coroInterfaceDispatchSourceSignature(common *ssa.CallCommon) (*types.Signature, error) {
	if common == nil || common.Method == nil {
		return nil, fmt.Errorf("coroutine interface dispatch requires an exact invoke method")
	}
	signature := coroInterfaceDispatchCallableSignature(common.Signature())
	methodSignature, _ := common.Method.Type().(*types.Signature)
	methodSignature = coroInterfaceDispatchCallableSignature(methodSignature)
	if signature == nil || methodSignature == nil || !types.Identical(signature, methodSignature) {
		return nil, fmt.Errorf("coroutine interface dispatch call signature %v does not match method signature %v", signature, methodSignature)
	}
	if signature.Variadic() {
		return nil, fmt.Errorf("coroutine interface dispatch variadic method %q is not implemented", common.Method.Id())
	}
	if list := signature.TypeParams(); list != nil && list.Len() != 0 {
		return nil, fmt.Errorf("coroutine interface dispatch generic call signature is not materialized")
	}
	if len(common.Args) != signature.Params().Len() {
		return nil, fmt.Errorf("coroutine interface dispatch has %d arguments for %d source parameters", len(common.Args), signature.Params().Len())
	}
	for index, argument := range common.Args {
		if argument == nil || !types.Identical(argument.Type(), signature.Params().At(index).Type()) {
			return nil, fmt.Errorf("coroutine interface dispatch argument %d does not match source parameter type %s", index, signature.Params().At(index).Type())
		}
	}
	return coroInterfaceDispatchCanonicalSignature(signature), nil
}

func validateCoroInterfaceDispatchCandidate(
	common *ssa.CallCommon,
	iface *types.Interface,
	sourceSignature *types.Signature,
	universe *EmissionUniverse,
	caller *ssa.Function,
	id coro.FunctionID,
	target *ssa.Function,
	plan coro.FunctionPlan,
) (types.Type, types.Type, *ssa.Function, error) {
	fail := func(format string, args ...any) (types.Type, types.Type, *ssa.Function, error) {
		name := "<nil>"
		if target != nil {
			name = target.String()
		}
		return nil, nil, nil, fmt.Errorf("coroutine interface dispatch target %q (%s): %s", id, name, fmt.Sprintf(format, args...))
	}
	if common == nil || common.Method == nil || iface == nil || sourceSignature == nil || target == nil || target.Signature == nil {
		return fail("missing method, receiver interface, source signature, or target signature")
	}
	if plan.ID != id {
		return fail("function plan ID is %q", plan.ID)
	}
	if plan.External != coro.Defined || plan.FuncRep != coro.Dispatch {
		return fail("requires a defined Dispatch body, got external=%s representation=%s", plan.External, plan.FuncRep)
	}
	switch {
	case plan.Emission == coro.EmitPlain && plan.Primary == coro.PrimaryPlain:
		if plan.Effect != coro.NoSuspend || plan.Effect.IsOpaque() {
			return fail("plain candidate effect %s is not exact no-suspend", plan.Effect)
		}
		if plan.Demand == coro.NoDemand {
			return fail("plain candidate is not demanded")
		}
		if plan.Exec.Contains(coro.NeedsPreempt) || plan.Exec.IsOpaque() {
			return fail("plain candidate execution constraints %s require coroutine or open lowering", plan.Exec)
		}
	case plan.Emission == coro.EmitCoroutine && plan.Primary == coro.PrimaryCoroutine:
		// A RawPlainEntry is an alternate physical entry for exact raw ABI
		// consumers.  BothDemand therefore still has a managed coroutine
		// primary, which is the only entry an ordinary interface invoke may
		// select.
		if !plan.Demand.Contains(coro.AsyncDemand) {
			return fail("coroutine candidate demand is %s, want managed async", plan.Demand)
		}
		if !plan.Effect.MaySuspend() || plan.Effect.IsOpaque() {
			return fail("coroutine candidate effect %s is not an exact suspend effect", plan.Effect)
		}
		if plan.Exec.IsOpaque() {
			return fail("coroutine candidate execution constraints %s are opaque", plan.Exec)
		}
	default:
		return fail(
			"requires either a plain/no-suspend or coroutine/async body, got emission=%s primary=%s demand=%s effect=%s",
			plan.Emission, plan.Primary, plan.Demand, plan.Effect,
		)
	}
	if len(target.Blocks) == 0 {
		return fail("requires one defined SSA body")
	}
	if target.Parent() != nil || len(target.FreeVars) != 0 {
		return fail("captured or nested methods require an environment adapter")
	}
	if target.Signature.Variadic() {
		return fail("variadic methods are not implemented")
	}
	directive, err := coroRawABIDirective(target, universe)
	if err != nil {
		return fail("classify ABI directive: %v", err)
	}
	if directive != "" {
		return fail("ABI directive %q requires an explicit boundary adapter", directive)
	}
	genericInstance := coroMaterializedGenericCallable(target)
	if params := target.TypeParams(); params != nil && params.Len() != 0 && !genericInstance {
		return fail("generic declarations are not materialized method bodies")
	}
	if params := target.Signature.TypeParams(); params != nil && params.Len() != 0 && !genericInstance {
		return fail("generic method signatures are not materialized")
	}
	if params := target.Signature.RecvTypeParams(); params != nil && params.Len() != 0 && !genericInstance {
		return fail("generic receiver methods are not materialized")
	}
	if (len(target.TypeArgs()) != 0 || target.Origin() != nil) && !genericInstance {
		return fail("generic instances require a frozen instantiated interface ABI")
	}

	recv := target.Signature.Recv()
	if recv == nil {
		return fail("candidate is not a declared method")
	}
	method, ok := target.Object().(*types.Func)
	if !ok || method == nil {
		return fail("candidate has no exact method object")
	}
	if method.Name() != common.Method.Name() || (universe == nil && method.Id() != common.Method.Id()) {
		return fail("method ID %q does not match invoke method ID %q", method.Id(), common.Method.Id())
	}
	targetReceiver := recv.Type()
	dynamicReceiver := targetReceiver
	implements, implementsErr := coroInterfaceDispatchCandidateImplements(universe, dynamicReceiver, iface)
	if implementsErr != nil {
		return fail("prove receiver %s implements invoke interface %s: %v", dynamicReceiver, iface, implementsErr)
	}
	if !implements {
		if _, pointer := types.Unalias(dynamicReceiver).Underlying().(*types.Pointer); pointer {
			return fail("receiver %s does not implement invoke interface %s", dynamicReceiver, iface)
		}
		promoted := types.NewPointer(dynamicReceiver)
		promotedImplements, promotedErr := coroInterfaceDispatchCandidateImplements(universe, promoted, iface)
		if promotedErr != nil {
			return fail("prove promoted receiver %s implements invoke interface %s: %v", promoted, iface, promotedErr)
		}
		if !promotedImplements {
			return fail("receiver %s and promoted receiver %s do not implement invoke interface %s", dynamicReceiver, promoted, iface)
		}
		dynamicReceiver = promoted
	}
	selection := types.NewMethodSet(dynamicReceiver).Lookup(method.Pkg(), method.Name())
	if selection == nil {
		return fail("dynamic receiver method set has no method %q", common.Method.Id())
	}
	selectedMethod, ok := selection.Obj().(*types.Func)
	if !ok || selectedMethod == nil || selectedMethod.Id() != method.Id() ||
		(universe == nil && selectedMethod.Id() != common.Method.Id()) {
		return fail("receiver method selection does not resolve exact method ID %q", method.Id())
	}
	methodEntry := target.Prog.MethodValue(selection)
	if methodEntry == nil || methodEntry.Prog != target.Prog || methodEntry.Signature == nil || len(methodEntry.FreeVars) != 0 {
		return fail("dynamic receiver method selection has no exact non-capturing SSA entry")
	}
	entryReceiver := methodEntry.Signature.Recv()
	if entryReceiver == nil || !types.Identical(entryReceiver.Type(), dynamicReceiver) {
		return fail("method entry receiver %v does not match dynamic receiver %s", entryReceiver, dynamicReceiver)
	}
	entrySignature, err := coroInterfaceDispatchEffectiveCallableSignature(universe, methodEntry, methodEntry.Signature)
	if err != nil {
		return fail("derive effective method-entry signature: %v", err)
	}
	effectiveSourceSignature, err := coroInterfaceDispatchEffectiveCallableSignature(universe, caller, sourceSignature)
	if err != nil {
		return fail("derive effective source call signature: %v", err)
	}
	if entrySignature == nil || !coroInterfaceDispatchSignaturesIdentical(effectiveSourceSignature, entrySignature) {
		return fail("effective method entry signature %v does not match source call signature %v", entrySignature, effectiveSourceSignature)
	}

	targetSignature, err := coroInterfaceDispatchEffectiveCallableSignature(universe, target, target.Signature)
	if err != nil {
		return fail("derive effective target signature: %v", err)
	}
	if targetSignature == nil || !coroInterfaceDispatchSignaturesIdentical(effectiveSourceSignature, coroInterfaceDispatchCanonicalSignature(targetSignature)) {
		return fail("effective source call signature %v does not match receiver-free target signature %v", effectiveSourceSignature, targetSignature)
	}
	if len(target.Params) != target.Signature.Params().Len()+1 || target.Params[0] == nil || !types.Identical(target.Params[0].Type(), recv.Type()) {
		return fail("SSA parameters do not contain the exact declared receiver")
	}
	for index := 0; index < target.Signature.Params().Len(); index++ {
		parameter := target.Params[index+1]
		if parameter == nil || !types.Identical(parameter.Type(), target.Signature.Params().At(index).Type()) {
			return fail("SSA parameter %d does not match declared method parameter %d", index+1, index)
		}
	}
	return dynamicReceiver, targetReceiver, methodEntry, nil
}

func coroInterfaceDispatchCandidateImplements(
	universe *EmissionUniverse,
	candidate types.Type,
	iface *types.Interface,
) (bool, error) {
	if universe != nil {
		return universe.CoroDynamicImplements(candidate, iface)
	}
	return types.Implements(candidate, iface), nil
}

func coroInterfaceDispatchEffectiveCallableSignature(
	universe *EmissionUniverse,
	caller *ssa.Function,
	typ types.Type,
) (*types.Signature, error) {
	if typ == nil {
		return nil, nil
	}
	if universe != nil {
		if caller == nil {
			return nil, fmt.Errorf("effective interface signature requires an SSA owner")
		}
		owner := universe.ownerOf(caller)
		if owner == nil {
			return nil, fmt.Errorf("function %q is absent from the emission universe", caller.Name())
		}
		typ = universe.effectiveType(owner, caller, typ)
	}
	signature, _ := types.Unalias(typ).(*types.Signature)
	return coroInterfaceDispatchCanonicalSignature(coroInterfaceDispatchCallableSignature(signature)), nil
}

func coroInterfaceDispatchSignaturesIdentical(left, right *types.Signature) bool {
	if left == nil || right == nil {
		return left == right
	}
	return structuralEmissionABITypeKey(left) == structuralEmissionABITypeKey(right)
}

func coroInterfaceDispatchCallableSignature(signature *types.Signature) *types.Signature {
	if signature == nil {
		return nil
	}
	return types.NewSignatureType(nil, nil, nil, signature.Params(), signature.Results(), signature.Variadic())
}

// coroInterfaceDispatchCanonicalSignature removes source variable names while
// retaining the exact source types that the frontend must patch. This makes a
// single signature safe to share across candidates from different packages.
func coroInterfaceDispatchCanonicalSignature(signature *types.Signature) *types.Signature {
	if signature == nil {
		return nil
	}
	canonicalTuple := func(tuple *types.Tuple) *types.Tuple {
		variables := make([]*types.Var, tuple.Len())
		for index := range variables {
			variables[index] = types.NewVar(token.NoPos, nil, "", tuple.At(index).Type())
		}
		return types.NewTuple(variables...)
	}
	return types.NewSignatureType(nil, nil, nil, canonicalTuple(signature.Params()), canonicalTuple(signature.Results()), signature.Variadic())
}
