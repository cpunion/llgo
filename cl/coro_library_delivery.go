/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"fmt"
	"go/types"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
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
	ids := u.FunctionIDConfig()
	ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = metadata.CoroABI, metadata.SchedulerABI, true
	functions := [3]*ssa.Function{recipe.producer, recipe.initializer, recipe.drop}
	var callables [3]coro.DeliveryCallable
	for index, target := range functions {
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
	var bound coroArchiveDeliveryRecipe
	var callables [2]coro.DeliveryCallable
	for ordinal, helperID := range [2]coro.FunctionID{record.Initializer, record.Drop} {
		helper, present := index.Lookup(helperID)
		if !present || !helper.Delivery.IsZero() {
			return fail("helper %q has no ordinary exact archive entry", helperID)
		}
		helperSignature := initialize
		if ordinal == 1 {
			helperSignature = drop
		}
		// This exact structural hash also proves the absence of a hidden
		// closure environment: the expected helper signature has none.
		hash := view.signatureABIHash(helperSignature, metadata)
		if hash != helper.ABIHash {
			return fail("helper %q ABI does not match its complete typed no-capture signature", helperID)
		}
		bound.helpers[ordinal] = coroArchiveDeliveryHelper{fact: helper, signature: helperSignature}
		callables[ordinal] = coro.DeliveryCallable{ID: helperID, ABIHash: hash, Signature: helperSignature}
	}
	bound.recipe, err = coro.FreezeDeliveryRecipe(record, producer, callables[0], callables[1])
	if err != nil {
		return fail("%v", err)
	}
	return bound, nil
}
