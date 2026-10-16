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

const (
	coroDeliveryInitializeCall = "coro.delivery.initialize"
	coroDeliveryDropCall       = "coro.delivery.drop"
)

// coroSourceDeliveryRecipe is the source half of the existing delivery fact,
// not a second effect graph or a runtime callback record. Only ProgramIR's
// source builder writes it; physical binding/publication remain fail-closed.
type coroSourceDeliveryRecipe struct {
	producer    *ssa.Function
	borrow      *ssa.Call
	initializer *ssa.Function
	drop        *ssa.Function
	signature   *types.Signature
	result      *types.Struct
}

type coroDeliverySourceUniverse interface {
	Resolve(*ssa.Function) (*ssa.Function, bool)
	FunctionBackground(*ssa.Function) (llssa.Background, bool, error)
	coroPhysicalSourceSignature(*ssa.Function) (*types.Signature, error)
	coroPhysicalEntrySourceSignature(*ssa.Function) (*types.Signature, error)
}

func planCoroDeliveryBorrow(u coroDeliverySourceUniverse, ctx *context, call *ssa.Call) (coroSourceDeliveryRecipe, error) {
	fail := func(format string, args ...any) (coroSourceDeliveryRecipe, error) {
		return coroSourceDeliveryRecipe{}, fmt.Errorf("llgo.coroBorrowResult: "+format, args...)
	}
	if u == nil || ctx == nil || call == nil || call.Parent() == nil ||
		ctx.goFn != call.Parent() || call.Common() == nil || call.Common().IsInvoke() ||
		call.Common().StaticCallee() == nil || len(call.Common().Args) != 2 {
		return fail("requires an exact owned call with initializer and drop functions")
	}
	producer := call.Parent()
	if canonical, found := u.Resolve(producer); !found || canonical != producer ||
		len(producer.Blocks) == 0 || call.Block() != producer.Blocks[0] {
		return fail("producer must be canonical and borrow in its source entry block")
	}
	if background, classified, err := u.FunctionBackground(producer); err != nil || !classified || background != llssa.InGo {
		return fail("producer must be a managed Go function")
	}
	signature, err := u.coroPhysicalSourceSignature(producer)
	if err != nil {
		return fail("producer signature: %v", err)
	}
	result, wantInit, wantDrop, err := coro.DeliveryHelperSignatures(signature)
	if err != nil {
		return fail("%v", err)
	}
	callSignature, ok := ctx.patchType(call.Common().Signature()).(*types.Signature)
	if !ok || callSignature.Recv() != nil || callSignature.Variadic() ||
		callSignature.Params().Len() != 2 || callSignature.Results().Len() != 1 ||
		!types.Identical(callSignature.Params().At(0).Type(), wantInit) ||
		!types.Identical(callSignature.Params().At(1).Type(), wantDrop) ||
		!types.Identical(callSignature.Results().At(0).Type(), types.NewPointer(result)) {
		return fail("declaration must use the producer's exact typed result and helper signatures")
	}
	helpers := [2]*ssa.Function{}
	for index, argument := range call.Common().Args {
		helper, exact := argument.(*ssa.Function)
		if !exact || len(helper.FreeVars) != 0 {
			return fail("helper %d is not an exact context-free function", index)
		}
		canonical, found := u.Resolve(helper)
		if !found || canonical == nil || canonical == producer || len(canonical.FreeVars) != 0 {
			return fail("helper %d has no distinct frozen identity", index)
		}
		if background, classified, err := u.FunctionBackground(canonical); err != nil || !classified || background != llssa.InGo {
			return fail("helper %d must be a managed Go helper", index)
		}
		actual, err := u.coroPhysicalEntrySourceSignature(canonical)
		if err != nil {
			return fail("helper %d signature: %v", index, err)
		}
		want := wantInit
		if index == 1 {
			want = wantDrop
		}
		if !types.Identical(actual, want) {
			return fail("helper %d signature %s, want %s", index, actual, want)
		}
		helpers[index] = canonical
	}
	if helpers[0] == helpers[1] {
		return fail("initializer and drop must have distinct frozen identities")
	}
	return coroSourceDeliveryRecipe{
		producer: producer, borrow: call, initializer: helpers[0], drop: helpers[1],
		signature: signature, result: result,
	}, nil
}

func (ir *coroProgramIR) freezeDeliveryProducer(recipe coroSourceDeliveryRecipe) error {
	if ir == nil || ir.callsFrozen || ir.deliveryProducers == nil || recipe.producer == nil || recipe.borrow == nil ||
		recipe.initializer == nil || recipe.drop == nil || recipe.signature == nil || recipe.result == nil {
		return fmt.Errorf("source delivery recipe requires the active ProgramIR source builder")
	}
	if previous, exists := ir.deliveryProducers[recipe.producer]; exists {
		if previous.borrow != recipe.borrow || previous.initializer != recipe.initializer || previous.drop != recipe.drop ||
			!types.Identical(previous.signature, recipe.signature) || !types.Identical(previous.result, recipe.result) {
			return fmt.Errorf("function %q has conflicting or repeated borrowed-result declarations", recipe.producer.Name())
		}
		return nil // Same source site projected through another exact type owner.
	}
	ir.deliveryProducers[recipe.producer] = recipe
	return nil
}

func (ir *coroProgramIR) sourceDelivery(producer *ssa.Function) (coroSourceDeliveryRecipe, bool, error) {
	if ir == nil || !ir.callsFrozen {
		return coroSourceDeliveryRecipe{}, false, fmt.Errorf("source delivery facts are not frozen")
	}
	if producer == nil {
		return coroSourceDeliveryRecipe{}, false, fmt.Errorf("source delivery lookup requires an exact producer")
	}
	// Call/FunctionPlan consumers already name canonical SSA functions. This
	// read-only projection does not acquire another canonicalization authority.
	recipe, present := ir.deliveryProducers[producer]
	return recipe, present, nil
}
