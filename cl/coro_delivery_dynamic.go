/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"fmt"
	"go/types"
	"strings"
	"unicode/utf8"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// This typed recipe belongs to one exact source occurrence, not to the
// function signature's entire call family. The sole value-flow pass decides
// whether its helpers survive; emission consumes that frozen decision.
type coroDynamicDeliveryRecipe struct {
	signature   *types.Signature
	result      *types.Struct
	initializer *types.Signature
	drop        *types.Signature
	initialize  string
	release     string
	allocate    string
	largeResult bool
}

// Publication reads the same frozen recipe as static custody. The cold tail
// contains ordinary typed helper descriptors, not another result transport or
// a reverse lookup from the producer's code address.
func (p *context) coroDeliveryDescriptorOptions(b llssa.Builder, entry plannedFunctionSymbol, signature *types.Signature) *llssa.CoroDispatchDeliveryOptions {
	recipe, present, err := p.emissionUniverse.coroProgramIR.sourceDelivery(entry.function)
	if err != nil {
		panic(err)
	}
	if !present {
		return nil
	}
	// The shared callable factory has already validated and bound the producer's
	// closure environment. Custody retains that same funcval env for drop;
	// initializer/drop helpers themselves remain exact context-free entries.
	if entry.plan.FuncRep != coro.Dispatch ||
		entry.plan.ManagedEntry != coro.ManagedEntryCoroutine && entry.plan.ManagedEntry != coro.ManagedEntryOutcomePlain ||
		p.immutablePlan().HasRawPlainVariant(entry.function) || !types.Identical(signature, canonicalCoroPlainDispatchSignature(recipe.signature)) {
		panic("resource descriptor publication requires its exact guarded managed entry")
	}
	_, initializer, drop, err := coro.DeliveryHelperSignatures(recipe.signature)
	if err != nil {
		panic(err)
	}
	options := &llssa.CoroDispatchDeliveryOptions{Version: llssa.CoroDispatchDeliveryVersionV2}
	for ordinal, signature := range []*types.Signature{initializer, drop} {
		abi, err := newCoroPlainDispatchEffectiveABI(p, signature)
		if err != nil {
			panic(err)
		}
		callable := p.emitCoroRecipeDeliveryHelper(b, recipe, ordinal, signature)
		helper := llssa.CoroDispatchDeliveryHelper{Descriptor: callable.descriptor, Signature: signature, ABIHash: abi.hash}
		if ordinal == 0 {
			options.Initializer = helper
		} else {
			options.Drop = helper
		}
	}
	return options
}

func (builder coroProgramIRBuilder) freezeDynamicDeliveryConsumer(ir *coroProgramIR, call ssa.CallInstruction) (*coroDynamicDeliveryRecipe, error) {
	u := builder.canonical.universe
	if call.Common().StaticCallee() != nil || call.Common().Signature().Results().Len() == 0 {
		return nil, nil
	}
	if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
		return nil, nil
	}
	switch call.(type) {
	case *ssa.Call:
	case *ssa.Defer:
	default:
		return nil, nil // Spawn owns an independent discarded-result entry.
	}
	cleanupOwner := coro.SSAResultDeliveryOwner(call)
	if cleanupOwner == nil {
		return nil, fmt.Errorf("dynamic delivery call has no bound cleanup owner")
	}
	owner := u.ownerOf(call.Parent())
	signature, ok := u.effectiveType(owner, call.Parent(), call.Common().Signature()).(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("dynamic delivery call has no frozen source signature")
	}
	if call.Common().IsInvoke() {
		// The interface payload supplies the receiver through the existing env
		// word. Its helpers expose only the Go-visible invocation arguments.
		signature = coroInterfaceDispatchCallableSignature(signature)
	}
	// Raw C function values do not publish managed delivery capabilities.
	if llssa.IsNativeFuncBackground(u.prog.TypeBackground(signature)) {
		return nil, nil
	}
	signature, err := coroConcreteManagedCallableSignature(signature)
	if err != nil {
		return nil, fmt.Errorf("dynamic delivery call %q: %w", call.String(), err)
	}
	signature = canonicalCoroPlainDispatchSignature(signature)
	if signature.Variadic() {
		// SSA already packed the variadic arguments into the final slice.
		// Only the helper's callable shape is normalized; the public
		// descriptor keeps its Go-visible variadic bit for reflection.
		signature = types.NewSignatureType(nil, nil, nil, signature.Params(), signature.Results(), false)
	}
	result, initializer, drop, err := coro.DeliveryHelperSignatures(signature)
	if err != nil {
		return nil, fmt.Errorf("dynamic delivery call %q: %w", call.String(), err)
	}
	ordinal, err := coro.SemanticInstructionOrdinal(call)
	if err != nil {
		return nil, err
	}
	identity := fmt.Sprintf(":dynamic:%d:%d", call.Block().Index, ordinal)
	if cleanupOwner != call.Parent() {
		// Sibling/nested yields may have the same block and instruction index
		// while registering into the same outer drainer.
		identity += ":" + emissionDigest(u.finalIdentity(call.Parent()))
	}
	recipe := &coroDynamicDeliveryRecipe{
		signature: signature, result: result, initializer: initializer, drop: drop,
		initialize: coroDeliveryInitializeCall + identity, release: coroDeliveryDropCall + identity,
	}
	for _, name := range []string{recipe.initialize, recipe.release} {
		if err := builder.freezeDeliveryHelperCall(cleanupOwner, name, call, nil); err != nil {
			return nil, err
		}
	}
	var extra []coroPlannedRuntimeHelper
	if _, deferred := call.(*ssa.Defer); deferred && u.prog.LocalGoTypeExceedsNativeStack(result) {
		recipe.largeResult = true
		// Consume only a runtime dependency already present in the frozen
		// inventory. An active consumer without this binding fails physical
		// preflight; a closed nonresource condition must still fold away.
		if runtime := u.byPath[llssa.PkgRuntime]; runtime != nil && !u.pathDup[llssa.PkgRuntime] {
			target := u.canonicalAlias(runtime.ssa.Func(coroRuntimeHelperTargetName(coroManagedFrameSlotAllocZCall)))
			if _, included := u.required[target]; target != nil && included {
				recipe.allocate = coroManagedFrameSlotAllocZCall + identity
				if err := builder.freezeDeliveryHelperCall(cleanupOwner, recipe.allocate, call, target); err != nil {
					return nil, err
				}
				extra = append(extra, coroPlannedRuntimeHelper{name: recipe.allocate, placement: coroRuntimeHelperAtCleanup})
			}
		}
	}
	if err := builder.freezeDeliveryHelperPlacements(ir, call, recipe.initialize, recipe.release, extra...); err != nil {
		return nil, err
	}
	return recipe, nil
}

// deliveryHelperActive is shared by lowering facts and all physical helper
// placements. A pruned helper must have an exact source recipe and a final
// negative CallPlan proof; absence from LoweredCalls alone is not permission
// to omit an expected managed invocation.
func (ir *coroProgramIR) deliveryHelperActive(plan *coro.SSAPlan, instruction ssa.Instruction, name string) (bool, error) {
	call, callable := instruction.(ssa.CallInstruction)
	if !callable {
		return true, nil
	}
	frozen, present, err := ir.callSitePlan(call)
	if err != nil {
		return false, err
	}
	if !present || frozen.dynamicDelivery == nil ||
		name != frozen.dynamicDelivery.initialize && name != frozen.dynamicDelivery.release && name != frozen.dynamicDelivery.allocate {
		return true, nil
	}
	if plan == nil {
		return false, fmt.Errorf("conditional delivery helper requires the frozen whole-program plan")
	}
	if plan.ElidesCall(call) {
		return false, nil
	}
	result, present := plan.CallPlan(call)
	if !present {
		return false, fmt.Errorf("conditional delivery helper lost its exact source CallPlan")
	}
	return result.ResultDelivery, nil
}

// freezeDeliveryHelperCall retains an ordinary exact or descriptor invocation
// with no fabricated SSA target. The existing value-flow pass resolves its exact
// source condition before computing effects; physical emission must consume
// that active lowered record, never the mutable builder map.
func (builder coroProgramIRBuilder) freezeDeliveryHelperCall(owner *ssa.Function, name string, source ssa.CallInstruction, target *ssa.Function) error {
	u := builder.canonical.universe
	if u == nil || owner == nil || u.canonicalAlias(owner) != owner ||
		source == nil || coro.SSAResultDeliveryOwner(source) != owner || source.Block() == nil || source.Common() == nil ||
		source.Common().StaticCallee() != nil ||
		source.Common().Signature() == nil || source.Common().Signature().Results().Len() == 0 ||
		name == "" || !utf8.ValidString(name) || strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("conditional delivery helper requires an exact owner-local typed dynamic result call")
	}
	if _, present := u.required[owner]; !present {
		return fmt.Errorf("conditional delivery helper owner is outside the emission universe")
	}
	if _, builtin := source.Common().Value.(*ssa.Builtin); builtin {
		return fmt.Errorf("conditional delivery helper cannot own a builtin invocation")
	}
	switch source.(type) {
	case *ssa.Call:
	case *ssa.Defer:
	default:
		return fmt.Errorf("conditional delivery helper cannot own a spawned invocation")
	}
	if target != nil {
		if _, present := u.required[target]; !present || u.canonicalAlias(target) != target {
			return fmt.Errorf("conditional exact delivery helper requires its real canonical target in the frozen inventory")
		}
	}
	return builder.recordLoweredCallTarget(owner, name, coroLoweredCallTarget{target: target, whenResultDeliveryOf: source})
}

func (p *context) emitCoroDeliveryHelper(b llssa.Builder, target *ssa.Function, signature *types.Signature) coroManagedCallable {
	if target == nil || signature == nil || signature.Recv() != nil || signature.Variadic() || signature.Results().Len() != 0 {
		panic("resource delivery descriptor requires one exact typed void helper")
	}
	entry := p.mustFunctionSymbol(target)
	if entry.ftype != goFunc || len(entry.function.FreeVars) != 0 ||
		entry.plan.External != coro.Defined && !(entry.importedLibrary && entry.plan.External == coro.ExternalKnown) || entry.plan.Demand == coro.NoDemand ||
		entry.plan.ManagedEntry == coro.ManagedEntryNone || entry.plan.RawPlainOnly {
		panic("resource delivery descriptor requires a demanded context-free managed primary")
	}
	if err := validateCoroManagedDescriptorPrimary(entry.function, entry.plan, p.immutableEmissionUniverse()); err != nil {
		panic(fmt.Errorf("resource delivery helper descriptor: %w", err))
	}
	return p.emitCoroManagedPrimary(b, entry, target, nil, signature)
}

func (p *context) coroDynamicDeliveryGuard(call ssa.CallInstruction) *coroDeliveryGuardState {
	cleanup := p.coroCleanup()
	if cleanup == nil || cleanup.delivery == nil {
		return nil
	}
	guard := cleanup.delivery.calls[call]
	if guard == nil || guard.plan.dynamic == nil {
		return nil
	}
	return guard
}

func (p *context) beginCoroDynamicDeliveryCall(
	b llssa.Builder, guard *coroDeliveryGuardState, fn llssa.Expr, args []llssa.Expr,
	selection llssa.CoroDispatchSelection, invocation coroCleanupInvocation,
) {
	if guard == nil || guard.plan.dynamic == nil || guard.result.IsNil() {
		panic("dynamic delivery requires the exact bound output guard")
	}
	present, ready := p.fn.MakeBlock(), p.fn.MakeBlock()
	b.If(selection.HasDelivery(), present, ready)
	b.SetBlockEx(present, llssa.AtEnd, false)
	recipe := guard.plan.dynamic
	initializerABI, err := newCoroPlainDispatchEffectiveABI(p, recipe.initializer)
	if err != nil {
		panic(err)
	}
	dropABI, err := newCoroPlainDispatchEffectiveABI(p, recipe.drop)
	if err != nil {
		panic(err)
	}
	initializer, drop := b.PrepareCoroDispatchDelivery(selection, recipe.initializer, recipe.drop,
		llssa.CoroDispatchDeliveryABI{Initializer: initializerABI.hash, Drop: dropABI.hash})
	// The nil environment of the helper is not another retained frame word.
	// Only its descriptor, the producer environment and exact arguments survive
	// a child suspension in the existing owner frame.
	p.armCoroDynamicDeliveryCall(b, guard, b.Field(fn, 1), args, initializer, drop, invocation)
	b.Jump(ready)
	b.SetBlockContinuation(ready)
}

func (p *context) armCoroDynamicDeliveryCall(
	b llssa.Builder, guard *coroDeliveryGuardState, env llssa.Expr, args []llssa.Expr,
	initializer, drop llssa.Expr, invocation coroCleanupInvocation,
) {
	if len(args) != len(guard.args) || guard.plan.dynamic == nil || guard.dropDescriptor.IsNil() {
		panic("dynamic delivery lost its exact typed captures")
	}
	if helper := guard.plan.dynamic.allocate; helper != "" {
		// Deferred results are discarded. Allocate large custody storage
		// only on the positive delivery-capability lane, never in the ramp
		// or for a base-only descriptor returning an ordinary large value.
		guard.result = b.AllocZAs(p.prog.Type(guard.plan.resultType(), llssa.InGo), helper)
	}
	if env.IsNil() {
		env = p.prog.Nil(p.prog.VoidPtr())
	}
	b.StoreKnownNonNil(guard.env, b.Convert(p.prog.VoidPtr(), env))
	b.StoreKnownNonNil(guard.dropDescriptor, b.Field(drop, 0))
	if !guard.resultAddress.IsNil() {
		b.StoreKnownNonNil(guard.resultAddress, guard.result)
	}
	for index, argument := range args {
		b.StoreKnownNonNil(guard.args[index], argument)
	}
	b.StoreKnownNonNil(guard.result, p.prog.Zero(p.prog.Elem(guard.result.Type)))
	p.compileCoroDynamicDeliveryHelper(b, guard.plan.dynamic.initialize, initializer, guard.plan.dynamic.initializer,
		[]llssa.Expr{guard.result}, invocation)
	// Initialization must complete before acquiring custody. A failed
	// initializer cannot run a drop on uninitialized output.
	b.StoreKnownNonNil(p.coroCleanup().delivery.active, p.prog.IntVal(uint64(guard.plan.tag), p.prog.Uint32()))
}

func (p *context) compileCoroDynamicDeliveryHelper(
	b llssa.Builder, name string, fn llssa.Expr, signature *types.Signature, args []llssa.Expr,
	invocation coroCleanupInvocation,
) {
	site := p.coroEmissionSite()
	if site == nil || signature == nil || signature.Results().Len() != 0 {
		panic("dynamic delivery helper requires an exact typed emission site")
	}
	call, callable := site.instruction.(ssa.CallInstruction)
	if !callable {
		panic("dynamic delivery helper requires its source call occurrence")
	}
	source, present, err := p.emissionUniverse.coroProgramIR.callSitePlan(call)
	if err != nil || !present || source.dynamicDelivery == nil {
		panic(fmt.Errorf("dynamic delivery helper %q lost its typed source recipe: %v", name, err))
	}
	recipe := source.dynamicDelivery
	if name != recipe.initialize && name != recipe.release ||
		name == recipe.initialize && !types.Identical(signature, recipe.initializer) ||
		name == recipe.release && !types.Identical(signature, recipe.drop) {
		panic(fmt.Errorf("dynamic delivery helper %q disagrees with its frozen source signature", name))
	}
	frozen, plainOnly := p.resolveCoroPhysicalHelperRecord(name)
	if plainOnly || !frozen.ManagedDispatch || frozen.Target != nil || frozen.ImportedTarget != "" || frozen.WhenResultDeliveryOf != site.instruction || frozen.NoUnwind {
		panic(fmt.Errorf("dynamic delivery helper %q lost its frozen conditional managed edge", name))
	}
	activeAtSite, err := p.emissionUniverse.coroProgramIR.deliveryHelperActive(p.immutablePlan(), site.instruction, name)
	if err != nil || !activeAtSite {
		panic(fmt.Errorf("dynamic delivery helper %q has no active exact source proof: %v", name, err))
	}
	// Helpers are ordinary managed dispatch calls, but never recursively
	// initialize the source producer's guard or grant source-defer recover.
	invocation.recoverParent = false
	p.compileCoroManagedDispatchAwaitValueResultWithRecovery(b, fn, args, signature, coroManagedDispatchAwaitOptions{
		cleanup: invocation, descriptorNonNil: true,
	})
}

func (p *context) coroDeliveryHelperInvocation() coroCleanupInvocation {
	if site := p.coroEmissionSite(); site != nil && site.placement != coroRuntimeHelperAtSource {
		return coroCleanupInvocation{state: p.coroCleanup()}
	}
	return coroCleanupInvocation{}
}
