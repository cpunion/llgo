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
	"strconv"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroWorkerForeignThunkPrefixV1 = "__llgo_coro_worker_foreign_thunk_v1_"

type coroWorkerForeignCallShape struct {
	target    *ssa.Function
	signature *types.Signature
	argc      int
	result    types.Type
}

func coroWorkerTypeParamLen(list *types.TypeParamList) int {
	if list == nil {
		return 0
	}
	return list.Len()
}

func coroWorkerTargetPointerSize(universe *EmissionUniverse) int {
	if universe == nil || universe.prog == nil {
		return 0
	}
	return universe.prog.PointerSize()
}

func coroWorkerWordType(typ types.Type, pointerSize int) bool {
	if typ == nil || pointerSize <= 0 {
		return false
	}
	underlying := types.Unalias(typ).Underlying()
	switch underlying := underlying.(type) {
	case *types.Pointer:
		return true
	case *types.Basic:
		if underlying.Kind() == types.UnsafePointer {
			return true
		}
		if underlying.Info()&types.IsInteger == 0 || underlying.Info()&types.IsUntyped != 0 {
			return false
		}
		sizes := &types.StdSizes{WordSize: int64(pointerSize), MaxAlign: int64(pointerSize)}
		size := sizes.Sizeof(typ)
		return size > 0 && size <= int64(pointerSize)
	default:
		return false
	}
}

// coroWorkerArgumentWordType additionally admits an explicitly C-background
// named callback. LLGo represents such a value as one raw C function pointer,
// not as a managed Go closure/descriptor. This is needed for registration APIs
// that transport—but do not invoke—the callback during the worker call. Plain
// Go function types remain rejected.
func coroWorkerArgumentWordType(universe *EmissionUniverse, typ types.Type, pointerSize int) bool {
	if coroWorkerWordType(typ, pointerSize) {
		return true
	}
	if universe == nil || universe.prog == nil || pointerSize <= 0 ||
		universe.prog.TypeBackground(typ) != llssa.InC {
		return false
	}
	signature, ok := types.Unalias(typ).Underlying().(*types.Signature)
	return ok && signature != nil && !signature.Variadic()
}

// coroWorkerResultWordType deliberately excludes pointer-shaped results. The
// native completion queue transports only untraced uintptr words; until a
// result-provenance capability can prove that a returned pointer is either
// non-Go storage or still owned by an exact retained root, reconstructing a Go
// pointer after the worker acknowledgement would create an unrooted interval.
func coroWorkerResultWordType(typ types.Type, pointerSize int) bool {
	if typ == nil || pointerSize <= 0 {
		return false
	}
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsInteger == 0 || basic.Info()&types.IsUntyped != 0 {
		return false
	}
	sizes := &types.StdSizes{WordSize: int64(pointerSize), MaxAlign: int64(pointerSize)}
	size := sizes.Sizeof(typ)
	return size > 0 && size <= int64(pointerSize)
}

// validateCoroWorkerForeignAuthorization accepts exactly one of the legacy
// worker certificate and the target-neutral callable declaration contract.
// The latter is deliberately stricter than its general SSA classification:
// this lowering moves the physical call to an arbitrary bounded worker and
// waits for that invocation to return, so it cannot implement affinity,
// managed reentry, retained storage, asynchronous completion, or no-return
// semantics. Plan and frontend certificates are compared as complete values;
// an ID match alone cannot hide stale behavior or physical-ABI fields.
func validateCoroWorkerForeignAuthorization(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	target *ssa.Function,
) error {
	if plan == nil || universe == nil || target == nil {
		return fmt.Errorf("requires an exact coroutine plan, emission universe, and foreign target")
	}

	planLegacy, planLegacyCertified := plan.ForeignWorkerCertificate(target)
	universeLegacy, universeLegacyCertified, legacyErr := universe.CoroForeignWorkerCertificate(target)
	if legacyErr != nil {
		return fmt.Errorf("resolve frozen legacy worker-safe certificate: %w", legacyErr)
	}
	planCallable, planCallableCertified := plan.CallableContractCertificate(target)
	universeCallable, universeCallableCertified, callableErr := universe.CoroCallableContractCertificate(target)
	if callableErr != nil {
		return fmt.Errorf("resolve frozen callable contract certificate: %w", callableErr)
	}

	legacyPresent := planLegacyCertified || planLegacy != "" || universeLegacyCertified ||
		universeLegacy != (CoroForeignWorkerCertificate{})
	callablePresent := planCallableCertified || !planCallable.IsZero() || universeCallableCertified ||
		!universeCallable.IsZero()
	if legacyPresent && callablePresent {
		return fmt.Errorf("generic callable contract and legacy worker-safe certificates are mutually exclusive")
	}

	if legacyPresent {
		if !planLegacyCertified || planLegacy == "" {
			return fmt.Errorf("target has no exact legacy worker-safe certificate in the coroutine plan")
		}
		if !universeLegacyCertified || universeLegacy.ID == "" || universeLegacy.PhysicalSymbol == "" || universeLegacy.ABISignature == "" {
			return fmt.Errorf("target has no exact legacy worker-safe certificate in the frozen emission universe")
		}
		if planLegacy != universeLegacy.ID {
			return fmt.Errorf("legacy worker-safe certificate identity differs between the coroutine plan and frozen emission universe")
		}
		return nil
	}

	if !callablePresent {
		return fmt.Errorf("target has no exact worker-safe certificate or compatible callable declaration contract")
	}
	if !planCallableCertified || planCallable.IsZero() {
		return fmt.Errorf("target has no exact callable contract certificate in the coroutine plan")
	}
	if !universeCallableCertified || universeCallable.IsZero() {
		return fmt.Errorf("target has no exact callable contract certificate in the frozen emission universe")
	}
	if planCallable != universeCallable {
		return fmt.Errorf("callable contract certificate differs between the coroutine plan and frozen emission universe")
	}
	if err := universeCallable.Validate(); err != nil {
		return fmt.Errorf("invalid callable contract certificate: %w", err)
	}
	if universeCallable.Scope != coro.CallableContractScopeDeclaration {
		return fmt.Errorf("callable contract scope %q does not authorize a worker C declaration", universeCallable.Scope)
	}
	if universeCallable.CallableABIExplicit {
		if _, addressOnly := parseCoroWorkerWordCallableABI(universeCallable.CallableABI); addressOnly {
			return fmt.Errorf(
				"callable ABI %q is address-only and may be consumed only by the FuncPCABI0-to-llgo.syscall worker path, not an ordinary typed foreign call",
				universeCallable.CallableABI,
			)
		}
	}
	contract := universeCallable.Contract
	if contract.Progress != coro.ProgressMayBlock {
		return fmt.Errorf("callable progress %q does not authorize bounded worker lowering; require %q", contract.Progress, coro.ProgressMayBlock)
	}
	if contract.Affinity != coro.AffinityAnyThread {
		return fmt.Errorf("callable affinity %q does not authorize arbitrary worker-thread execution; require %q", contract.Affinity, coro.AffinityAnyThread)
	}
	if contract.Reentry != coro.ReentryNone {
		return fmt.Errorf("callable reentry %q does not authorize callback-free worker execution; require %q", contract.Reentry, coro.ReentryNone)
	}
	switch contract.Memory {
	case coro.MemoryByValue, coro.MemoryBorrowUntilReturn, coro.MemoryBorrowUntilComplete:
		return nil
	default:
		return fmt.Errorf("callable memory lifetime %q does not authorize bounded worker transport", contract.Memory)
	}
}

// validateCoroWorkerForeignCall recognizes only an ordinary, closed CallForeign
// edge to one exact frontend C declaration. recognized distinguishes a malformed
// foreign edge (which must fail closed) from an unrelated call.
func validateCoroWorkerForeignCall(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	call *ssa.Call,
	pointerSize int,
) (shape coroWorkerForeignCallShape, recognized bool, err error) {
	if plan == nil || universe == nil || call == nil || call.Common() == nil {
		return shape, false, nil
	}
	callPlan, planned := plan.CallPlan(call)
	if !planned || callPlan.Kind != coro.CallForeign {
		return shape, false, nil
	}
	recognized = true
	common := call.Common()
	if call.Parent() == nil {
		return shape, true, fmt.Errorf("call has no exact SSA owner")
	}
	raw := common.StaticCallee()
	if raw == nil || common.IsInvoke() || common.Method != nil {
		return shape, true, fmt.Errorf("requires one exact static ordinary call")
	}
	if callPlan.Open || callPlan.MayBeNil || callPlan.Rep != coro.DirectPlain || len(callPlan.Targets) != 1 {
		return shape, true, fmt.Errorf(
			"requires one closed non-nil direct-plain target, got open=%t may-be-nil=%t representation=%s targets=%d",
			callPlan.Open, callPlan.MayBeNil, callPlan.Rep, len(callPlan.Targets),
		)
	}
	target, frozen := universe.Resolve(raw)
	if !frozen || target == nil {
		return shape, true, fmt.Errorf("static target is absent from the frozen emission universe")
	}
	plannedTarget, ok := plan.Function(callPlan.Targets[0])
	if !ok || plannedTarget == nil || plannedTarget != target {
		return shape, true, fmt.Errorf("call target %q does not identify the frozen static declaration", callPlan.Targets[0])
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.ID != callPlan.Targets[0] {
		return shape, true, fmt.Errorf("target has no canonical function plan")
	}
	background, classified, backgroundErr := universe.FunctionBackground(target)
	if backgroundErr != nil {
		return shape, true, fmt.Errorf("classify target frontend ABI: %w", backgroundErr)
	}
	if !classified || background != llssa.InC {
		return shape, true, fmt.Errorf("target is not one exact frontend C declaration")
	}
	if authorizationErr := validateCoroWorkerForeignAuthorization(plan, universe, target); authorizationErr != nil {
		return shape, true, authorizationErr
	}
	if targetPlan.External != coro.ExternalUnknownForeign || targetPlan.Emission != coro.EmitExternal ||
		targetPlan.Effect != coro.NoSuspend || targetPlan.Exec != coro.BlockForeign|coro.IRQUnsafe {
		return shape, true, fmt.Errorf(
			"target %q is not an exact blocking foreign declaration (external=%s emission=%s effect=%s exec=%s)",
			targetPlan.ID, targetPlan.External, targetPlan.Emission, targetPlan.Effect, targetPlan.Exec,
		)
	}
	if target.Signature == nil || target.Signature.Recv() != nil || target.Signature.Variadic() ||
		coroWorkerTypeParamLen(target.Signature.TypeParams()) != 0 ||
		coroWorkerTypeParamLen(target.Signature.RecvTypeParams()) != 0 ||
		len(target.FreeVars) != 0 || target.Origin() != nil || len(target.TypeArgs()) != 0 {
		return shape, true, fmt.Errorf("target is not a receiver-free, non-variadic, non-generic C declaration")
	}
	signature, signatureErr := universe.coroPhysicalSourceSignature(target)
	if signatureErr != nil {
		return shape, true, fmt.Errorf("derive target effective signature: %w", signatureErr)
	}
	if signature == nil || signature.Recv() != nil || signature.Variadic() {
		return shape, true, fmt.Errorf("requires a non-variadic signature with zero to %d arguments", coroWorkerMaxArgsV1)
	}
	shape.argc = 0
	if signature.Params() != nil {
		shape.argc = signature.Params().Len()
	}
	if shape.argc != len(common.Args) || shape.argc > coroWorkerMaxArgsV1 {
		return shape, true, fmt.Errorf("requires a non-variadic signature with zero to %d arguments", coroWorkerMaxArgsV1)
	}
	owner := universe.ownerOf(call.Parent())
	ownerContext, contextErr := universe.functionABIContext(call.Parent(), owner)
	if contextErr != nil {
		return shape, true, fmt.Errorf("derive call-site effective signature: %w", contextErr)
	}
	callSignature, ok := ownerContext.patchType(common.Signature()).(*types.Signature)
	if !ok || !types.Identical(coroPhysicalNormalizeSourceSignature(callSignature), signature) {
		return shape, true, fmt.Errorf("call-site and target effective C signatures differ")
	}
	for index, argument := range common.Args {
		if argument == nil {
			return shape, true, fmt.Errorf("argument %d is nil", index)
		}
		argumentType := ownerContext.patchType(argument.Type())
		parameterType := signature.Params().At(index).Type()
		if !types.Identical(argumentType, parameterType) {
			return shape, true, fmt.Errorf("argument %d type does not match the effective C parameter", index)
		}
		if !coroWorkerArgumentWordType(universe, parameterType, pointerSize) {
			return shape, true, fmt.Errorf("argument %d type %s is not losslessly word-packable integer/pointer data", index, parameterType)
		}
	}
	results := signature.Results()
	if results != nil && results.Len() > 1 {
		return shape, true, fmt.Errorf("requires zero or one result")
	}
	if results != nil && results.Len() == 1 {
		shape.result = results.At(0).Type()
		if !coroWorkerResultWordType(shape.result, pointerSize) {
			return coroWorkerForeignCallShape{}, true, fmt.Errorf(
				"result type %s is not losslessly word-packable integer data", shape.result,
			)
		}
	}
	shape.target = target
	shape.signature = signature
	return shape, true, nil
}

func coroWorkerForeignThunkSignature(argc int) *types.Signature {
	params := make([]*types.Var, argc)
	for index := range params {
		params[index] = types.NewParam(token.NoPos, nil, fmt.Sprintf("a%d", index), types.Typ[types.Uintptr])
	}
	result := types.NewVar(token.NoPos, nil, "result", types.Typ[types.Uintptr])
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), types.NewTuple(result), false)
}

func (p *context) coroWorkerForeignThunk(shape coroWorkerForeignCallShape, target llssa.Function) llssa.Function {
	if p == nil || shape.target == nil || shape.signature == nil || target == nil {
		panic("coroutine foreign worker thunk requires an exact target and signature")
	}
	key := framedEmissionKey(
		"cl-coro-worker-foreign-thunk-v1",
		target.Name(),
		structuralEmissionABITypeKey(shape.signature),
		strconv.Itoa(p.prog.PointerSize()),
	)
	name := coroWorkerForeignThunkPrefixV1 + emissionDigest(key)
	thunk := p.pkg.NewFuncEx(name, coroWorkerForeignThunkSignature(shape.argc), llssa.InC, false, true)
	if thunk.HasBody() {
		return thunk
	}
	b := thunk.MakeBody(1)
	args := make([]llssa.Expr, shape.argc)
	for index := range args {
		args[index] = b.Convert(p.type_(shape.signature.Params().At(index).Type(), llssa.InC), thunk.Param(index))
	}
	ret := b.Call(target.Expr, args...)
	if shape.result == nil {
		b.Return(p.prog.Zero(p.prog.Uintptr()))
	} else {
		b.Return(b.Convert(p.prog.Uintptr(), ret))
	}
	b.EndBuild()
	b.Dispose()
	return thunk
}

func (p *context) compileCoroWorkerForeignCall(
	b llssa.Builder, call *ssa.Call, shape coroWorkerForeignCallShape,
) llssa.Expr {
	if p == nil || !p.hasCoroPhysicalBody() || call == nil || shape.target == nil || shape.signature == nil {
		panic("coroutine foreign worker lowering escaped its frozen physical operation recipe")
	}
	target, _, kind := p.compileFunction(shape.target)
	if kind != cFunc || target == nil {
		panic("coroutine foreign worker lowering lost its exact C target")
	}
	thunk := p.coroWorkerForeignThunk(shape, target)
	oldInCFunc := p.inCFunc
	p.inCFunc = true
	compiled := p.compileValues(b, call.Common().Args, fnNormal)
	p.inCFunc = oldInCFunc
	words := make([]llssa.Expr, len(compiled))
	for index, argument := range compiled {
		words[index] = b.Convert(p.prog.Uintptr(), argument)
	}
	function := b.Convert(p.prog.Uintptr(), thunk.Expr)
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
	result := p.compileCoroWorkerWordCall(b, function, words, keepaliveSlots)
	if shape.result == nil {
		return llssa.Expr{}
	}
	return b.Convert(p.type_(shape.result, llssa.InC), result.r1)
}
