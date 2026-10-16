/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"fmt"

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
