/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"fmt"
	"go/types"
	"sort"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

// SourceDeliveryRecord publishes the same exact source recipe as static
// lowering. Bodyless archive consumption remains fail-closed until its helper
// declarations and physical guard are bound; absence is never a downgrade.
func (view CoroLibraryEffectView) SourceDeliveryRecord(
	function *ssa.Function, metadata coro.LibraryEffectMetadata,
) (coro.DeliveryRecipeRecord, bool, error) {
	u := view.index.universe
	if u == nil || u.coroProgramIR == nil || function == nil {
		return coro.DeliveryRecipeRecord{}, false, fmt.Errorf("delivery record requires one prepared source function")
	}
	recipe, present, err := u.coroProgramIR.sourceDelivery(function)
	if err != nil || !present {
		return coro.DeliveryRecipeRecord{}, present, err
	}
	if recipe.archive != nil && recipe.forward == nil {
		record := recipe.archive.recipe.Record()
		hash, err := view.FunctionABIHash(function, metadata)
		if err != nil || hash != record.ProducerABI {
			return coro.DeliveryRecipeRecord{}, true, fmt.Errorf("archive delivery producer ABI changed: %v", err)
		}
		for _, helper := range recipe.archive.helpers {
			if view.signatureABIHash(helper.signature, metadata) != helper.callableABI() {
				return coro.DeliveryRecipeRecord{}, true, fmt.Errorf("archive delivery helper ABI changed")
			}
		}
		return record, true, nil
	}
	ids := u.FunctionIDConfig()
	ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = metadata.CoroABI, metadata.SchedulerABI, true
	functions := [3]*ssa.Function{recipe.producer, recipe.initializer, recipe.drop}
	base := recipe
	if base.forward != nil {
		base = u.coroProgramIR.deliveryProducers[base.forwardBase]
	}
	var callables [3]coro.DeliveryCallable
	for index, target := range functions {
		if index != 0 && target == nil && base.archive != nil {
			helper := base.archive.helpers[index-1]
			callables[index] = coro.DeliveryCallable{ID: helper.fact.ID, ABIHash: helper.callableABI(), Signature: helper.signature}
			continue
		}
		id, err := coro.StableFunctionID(target, ids)
		if err != nil {
			return coro.DeliveryRecipeRecord{}, true, err
		}
		hash, err := view.FunctionABIHash(target, metadata)
		if err != nil {
			return coro.DeliveryRecipeRecord{}, true, err
		}
		signature, err := u.coroPhysicalSourceSignature(target)
		if err != nil {
			return coro.DeliveryRecipeRecord{}, true, err
		}
		callables[index] = coro.DeliveryCallable{ID: id, ABIHash: hash, Signature: signature}
	}
	result := u.prog.Type(recipe.result, llssa.InGo)
	callables[0].ResultSize, callables[0].ResultAlign = u.prog.SizeOf(result), u.prog.AlignOf(result)
	record := coro.DeliveryRecipeRecord{
		Version:  coro.DeliveryRecipeVersionV1,
		Producer: callables[0].ID, ProducerABI: callables[0].ABIHash,
		Initializer: callables[1].ID, InitializerABI: callables[1].ABIHash,
		Drop: callables[2].ID, DropABI: callables[2].ABIHash,
		ResultSize: callables[0].ResultSize, ResultAlign: callables[0].ResultAlign,
	}
	if base.archive != nil {
		record.InitializerABI = base.archive.helpers[0].fact.ABIHash
		record.DropABI = base.archive.helpers[1].fact.ABIHash
	}
	if recipe.forward != nil {
		_, _, signature, err := coro.DeliveryHelperSignatures(recipe.signature)
		if err != nil {
			return coro.DeliveryRecipeRecord{}, true, err
		}
		record.DropView = coro.DeliveryHelperViewRecord{Symbol: coroDeliveryViewSymbol(u.finalIdentity(function)), ABIHash: view.signatureABIHash(signature, metadata)}
		callables[2].Signature, callables[2].ABIHash = signature, record.DropView.ABIHash
	}
	if _, err := coro.FreezeDeliveryRecipe(record, callables[0], callables[1], callables[2]); err != nil {
		return coro.DeliveryRecipeRecord{}, true, err
	}
	return record, true, nil
}

// A private archive helper has a real typed declaration, but no source SSA
// object. Its producer-owned fact is joined to the same Graph before analysis;
// this binding alone never authorizes a physical result-custody guard.
type coroArchiveDeliveryHelper struct {
	fact      coro.LibraryEffectFunction
	signature *types.Signature
	view      coro.DeliveryHelperViewRecord
	source    *ssa.Function
}

func (helper coroArchiveDeliveryHelper) callableABI() string {
	if !helper.view.IsZero() {
		return helper.view.ABIHash
	}
	return helper.fact.ABIHash
}

func (helper coroArchiveDeliveryHelper) symbol() string {
	if !helper.view.IsZero() {
		return helper.view.Symbol
	}
	return helper.fact.PrimarySymbol
}

type coroArchiveDeliveryRecipe struct {
	recipe  coro.DeliveryRecipe
	helpers [2]coroArchiveDeliveryHelper
}

func (view CoroLibraryEffectView) bindArchiveDeliveryRecipe(
	function *ssa.Function, metadata coro.LibraryEffectMetadata,
	fact coro.LibraryEffectFunction, index *coro.LibraryEffectIndex,
) (coroArchiveDeliveryRecipe, error) {
	fail := func(format string, args ...any) (coroArchiveDeliveryRecipe, error) {
		return coroArchiveDeliveryRecipe{}, fmt.Errorf("bind archive delivery: "+format, args...)
	}
	u := view.index.universe
	if u == nil || function == nil || len(function.Blocks) != 0 || index == nil || fact.Delivery.IsZero() {
		return fail("requires a bodyless producer and complete archive index")
	}
	if err := coro.ValidateLibraryEffectCompatibility(index.Metadata(), metadata); err != nil {
		return fail("%v", err)
	}
	canonical, present := u.Resolve(function)
	if !present || canonical != function || len(function.FreeVars) != 0 {
		return fail("producer has no exact context-free source declaration")
	}
	background, classified, err := u.FunctionBackground(function)
	if err != nil || !classified || background != llssa.InGo {
		return fail("producer is not one managed Go declaration: %v", err)
	}
	ids := u.FunctionIDConfig()
	ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = metadata.CoroABI, metadata.SchedulerABI, true
	id, err := coro.StableFunctionID(function, ids)
	if err != nil || id != fact.ID {
		return fail("producer identity disagrees with its source declaration: %v", err)
	}
	if indexed, present := index.Lookup(id); !present || indexed != fact {
		return fail("producer is absent or differs from the checked archive index")
	}
	if err := view.validateFunctionEntry(function, metadata, fact); err != nil {
		return fail("%v", err)
	}
	signature, err := u.coroPhysicalSourceSignature(function)
	if err != nil {
		return fail("producer signature: %v", err)
	}
	result, initialize, drop, err := coro.DeliveryHelperSignatures(signature)
	if err != nil {
		return fail("%v", err)
	}
	output := u.prog.Type(result, llssa.InGo)
	producer := coro.DeliveryCallable{ID: id, ABIHash: fact.ABIHash, Signature: signature,
		ResultSize: u.prog.SizeOf(output), ResultAlign: u.prog.AlignOf(output)}
	record := fact.Delivery
	if !record.DropView.IsZero() && record.DropView.Symbol != coroDeliveryViewSymbol(u.finalIdentity(function)) {
		return fail("drop view is not the producer-owned physical symbol")
	}
	var bound coroArchiveDeliveryRecipe
	var callables [2]coro.DeliveryCallable
	for ordinal, helperID := range [2]coro.FunctionID{record.Initializer, record.Drop} {
		helper, present := index.Lookup(helperID)
		if !present || !helper.Delivery.IsZero() || helper.DeliveryUnbound {
			return fail("helper %q has no ordinary exact archive entry", helperID)
		}
		helperSignature := initialize
		if ordinal == 1 {
			helperSignature = drop
		}
		// This exact structural hash also proves the absence of a hidden
		// closure environment: the expected helper signature has none.
		hash := view.signatureABIHash(helperSignature, metadata)
		physicalView := coro.DeliveryHelperViewRecord{}
		if ordinal == 1 {
			physicalView = record.DropView
		}
		wantHash := helper.ABIHash
		if !physicalView.IsZero() {
			wantHash = physicalView.ABIHash
		}
		if hash != wantHash {
			return fail("helper %q ABI does not match its complete typed no-capture signature", helperID)
		}
		bound.helpers[ordinal] = coroArchiveDeliveryHelper{fact: helper, signature: helperSignature, view: physicalView}
		callables[ordinal] = coro.DeliveryCallable{ID: helperID, ABIHash: hash, Signature: helperSignature}
	}
	bound.recipe, err = coro.FreezeDeliveryRecipe(record, producer, callables[0], callables[1])
	if err != nil {
		return fail("%v", err)
	}
	return bound, nil
}

func (builder coroProgramIRBuilder) freezeArchiveDeliveryProducers(ir *coroProgramIR) error {
	u := builder.canonical.universe
	if u.libraryImports == nil {
		return nil
	}
	metadata := u.libraryImports.Metadata()
	local := metadata
	target := u.prog.TargetSpec()
	local.TargetTriple, local.TargetCPU, local.TargetFeatures, local.TargetABI = target.Triple, target.CPU, target.Features, target.TargetABI
	local.PointerBits, local.DataLayout, local.TargetCapabilities = u.prog.PointerSize()*8, u.prog.DataLayout(), u.coroCapabilities
	local.Endianness = "little"
	if u.prog.TargetData().ByteOrder() == llvm.BigEndian {
		local.Endianness = "big"
	}
	if err := coro.ValidateLibraryEffectCompatibility(metadata, local); err != nil {
		return err
	}
	ids := u.FunctionIDConfig()
	ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = metadata.CoroABI, metadata.SchedulerABI, true
	sources := make(map[coro.FunctionID]*ssa.Function, len(u.functions))
	for _, function := range u.functions {
		id, err := coro.StableFunctionID(function, ids)
		if err != nil {
			return err
		}
		if previous := sources[id]; previous != nil && previous != function {
			return fmt.Errorf("archive binding found conflicting source functions for %q", id)
		}
		sources[id] = function
	}
	for id, function := range sources {
		if len(function.Blocks) != 0 {
			continue
		} // A source definition is authoritative.
		fact, found := u.libraryImports.Lookup(id)
		if found && fact.DeliveryUnbound {
			if err := u.libraryEffects.validateFunctionEntry(function, metadata, fact); err != nil {
				return err
			}
			facts := ir.localBodyFacts[function]
			facts.deliveryReceiver, facts.ResultDelivery = function, true
			ir.localBodyFacts[function] = facts
			continue
		}
		if !found || fact.Delivery.IsZero() {
			continue
		}
		bound, err := u.libraryEffects.bindArchiveDeliveryRecipe(function, metadata, fact, u.libraryImports)
		if err != nil {
			return err
		}
		signature, err := u.coroPhysicalSourceSignature(function)
		if err != nil {
			return err
		}
		recipe := coroSourceDeliveryRecipe{producer: function, signature: signature, result: bound.recipe.ResultType(), archive: &bound}
		for ordinal, helper := range bound.helpers {
			if source := sources[helper.fact.ID]; source != nil {
				if len(source.Blocks) != 0 {
					return fmt.Errorf("archive helper %q collides with a source definition; recompile the library", helper.fact.ID)
				}
				if err := u.libraryEffects.validateFunctionEntry(source, metadata, helper.fact); err != nil {
					return err
				}
				if helper.view.IsZero() {
					if ordinal == 0 {
						recipe.initializer = source
					} else {
						recipe.drop = source
					}
					continue
				}
				helper.source = source
				bound.helpers[ordinal] = helper
			}
			if previous, found := ir.archiveDeliveryHelpers[helper.fact.ID]; found && previous.fact != helper.fact {
				return fmt.Errorf("archive helper %q has conflicting typed bindings", helper.fact.ID)
			}
			ir.archiveDeliveryHelpers[helper.fact.ID] = helper
		}
		ir.deliveryProducers[function] = recipe
	}
	return nil
}

// ImportedFunctions is the immutable, typed archive-only inventory projected to
// the same effect planner. Source helper declarations stay in Functions instead.
func (view CoroPlanningMetadataView) ImportedFunctions() ([]coro.LibraryEffectFunction, error) {
	u := view.index.universe
	if u == nil || u.coroProgramIR == nil || !u.coroProgramIR.callsFrozen {
		return nil, fmt.Errorf("archive helper inventory is not frozen")
	}
	ret := make([]coro.LibraryEffectFunction, 0, len(u.coroProgramIR.archiveDeliveryHelpers))
	for _, helper := range u.coroProgramIR.archiveDeliveryHelpers {
		if helper.source != nil {
			continue
		} // The real declaration already owns its graph node.
		ret = append(ret, helper.fact)
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].ID < ret[j].ID })
	return ret, nil
}

func (p *context) emitCoroArchiveDeliveryHelper(b llssa.Builder, helper coroArchiveDeliveryHelper) coroManagedCallable {
	whole := p.immutablePlan()
	if whole == nil || helper.signature == nil {
		panic("archive helper requires one frozen typed callable")
	}
	plan, found := whole.BasePlan().Lookup(helper.fact.ID)
	bound, present := p.emissionUniverse.coroProgramIR.archiveDeliveryHelpers[helper.fact.ID]
	plain, structured := coroExternalManagedDescriptorEntry(plan)
	if !found || !present || bound.fact != helper.fact ||
		!plain && !structured || plan.Demand == coro.NoDemand {
		panic("archive helper lost its typed binding or exact managed demand")
	}
	abi, err := newCoroPlainDispatchEffectiveABI(p, helper.signature)
	if err != nil {
		panic(err)
	}
	signature := helper.signature
	switch plan.ManagedEntry {
	case coro.ManagedEntryPlain:
	case coro.ManagedEntryOutcomePlain:
		signature = newOutcomePlainPhysicalABI(signature).physicalSig
	case coro.ManagedEntryCoroutine:
		signature = coroPhysicalEntrySignature(signature)
	default:
		panic("archive helper has no exact managed primary")
	}
	physical := p.pkg.NewFuncEx(helper.symbol(), signature, llssa.InGo, false, false)
	if !types.Identical(physical.RawType(), signature) {
		panic("archive helper conflicts with an existing physical declaration")
	}
	return p.emitCoroManagedPrimaryDescriptorBinding(b, plan, abi, physical.Expr, physical.Expr, llssa.Nil, nil, false, llssa.Nil, nil, helper.view.Symbol)
}

func (p *context) emitCoroRecipeDeliveryHelper(b llssa.Builder, recipe coroSourceDeliveryRecipe, ordinal int, signature *types.Signature) coroManagedCallable {
	if recipe.forward != nil {
		return p.emitCoroForwardedDeliveryHelper(b, recipe, ordinal, signature)
	}
	target := recipe.initializer
	if ordinal == 1 {
		target = recipe.drop
	}
	if target != nil {
		return p.emitCoroDeliveryHelper(b, target, signature)
	}
	if recipe.archive == nil || !types.Identical(recipe.archive.helpers[ordinal].signature, signature) {
		panic("resource helper has no exact typed source/archive binding")
	}
	return p.emitCoroArchiveDeliveryHelper(b, recipe.archive.helpers[ordinal])
}

func (p *context) compileCoroRecipeDeliveryHelper(b llssa.Builder, name string, recipe coroSourceDeliveryRecipe, ordinal int, args []llssa.Expr) {
	if recipe.forward != nil {
		if ordinal == 1 {
			recipe, args = p.coroForwardedDeliveryArguments(b, recipe, args)
		} else {
			recipe = p.emissionUniverse.coroProgramIR.deliveryProducers[recipe.forwardBase]
		}
	}
	target := recipe.initializer
	if ordinal == 1 {
		target = recipe.drop
	}
	if target != nil {
		p.compileCoroDeliveryHelper(b, name, target, args)
		return
	}
	if recipe.archive == nil || !p.hasStructuredOutcomePhysicalBody() {
		panic("archive helper requires its frozen resource guard owner")
	}
	helper := recipe.archive.helpers[ordinal]
	frozen, plainOnly := p.resolveCoroPhysicalHelperRecord(name)
	if plainOnly || frozen.ImportedTarget != helper.fact.ID || frozen.Target != nil || frozen.ManagedDispatch ||
		frozen.WhenResultDeliveryOf != nil || frozen.RawPlain || frozen.NoUnwind || frozen.UnwindOnly || frozen.ExplicitStatusElided {
		panic("archive helper lost its exact frozen invocation edge")
	}
	// Reuse the existing typed invocation and cleanup drainer. Constant
	// descriptors can be folded by LLVM; there is no new scheduler or call ABI.
	invocation := p.coroDeliveryHelperInvocation()
	invocation.recoverParent = false
	p.compileCoroManagedDispatchAwaitValueResultWithRecovery(b, p.emitCoroArchiveDeliveryHelper(b, helper).value, args, helper.signature,
		coroManagedDispatchAwaitOptions{cleanup: invocation, descriptorNonNil: true})
}
