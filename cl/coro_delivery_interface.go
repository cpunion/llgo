/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"go/types"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// A canonical value wrapper may evaluate a pointer before calling a resource
// producer. Without custody for that evaluated value, publishing a descriptor
// without the producer's capability would silently lose cancellation cleanup.
// Reject this still-unsupported boundary, using only frozen source facts.
func (ir *coroProgramIR) validateInterfaceDeliveryReceiver(target *ssa.Function) error {
	return ir.validateDeliveryReceiver(target)
}

// Interface helpers use the same receiver-free ABI as the source invoke. An
// unused receiver disappears; a required pointer receiver retains its pointer
// identity. Never reread a pointee to reconstruct an already evaluated value.
func (p *context) coroInterfaceDeliveryDescriptorOptions(entry plannedFunctionSymbol, signature *types.Signature, receiver types.Type) *llssa.CoroDispatchDeliveryOptions {
	recipe, present, err := p.emissionUniverse.coroProgramIR.sourceDelivery(entry.function)
	if err != nil {
		panic(err)
	}
	if !present {
		return nil
	}
	if recipe.signature.Params().Len() != signature.Params().Len()+1 ||
		!types.Identical(recipe.signature.Params().At(0).Type(), receiver) ||
		!types.Identical(recipe.signature.Results(), signature.Results()) ||
		entry.plan.FuncRep != coro.Dispatch ||
		entry.plan.ManagedEntry != coro.ManagedEntryCoroutine && entry.plan.ManagedEntry != coro.ManagedEntryOutcomePlain ||
		p.immutablePlan().HasRawPlainVariant(entry.function) {
		panic("interface delivery requires its exact receiver-normalized guarded primary")
	}
	for index := 0; index < signature.Params().Len(); index++ {
		if !types.Identical(recipe.signature.Params().At(index+1).Type(), signature.Params().At(index).Type()) {
			panic("interface delivery lost its exact source argument types")
		}
	}
	_, initialize, drop, err := coro.DeliveryHelperSignatures(signature)
	if err != nil {
		panic(err)
	}
	base := recipe
	if recipe.forward != nil {
		base = p.emissionUniverse.coroProgramIR.deliveryProducers[recipe.forwardBase]
	}
	primary := p.coroDeliveryRecipeHelperPrimary(base, 1)
	literals := p.pkg.FuncOf(primary.physical.Name()).NewBuilder()
	defer literals.Dispose()
	arguments := []llssa.CoroHelperViewArgument{{Parameter: 0}, {Parameter: -1, Constant: p.prog.Nil(p.prog.VoidPtr())}}
	mapParameter := func(index int, fields []int) llssa.CoroHelperViewArgument {
		if index != 0 {
			return llssa.CoroHelperViewArgument{Parameter: index + 1, Fields: fields}
		}
		argument := llssa.CoroHelperViewArgument{Parameter: 1, Fields: fields}
		if _, pointer := types.Unalias(receiver).Underlying().(*types.Pointer); !pointer || len(fields) != 0 {
			panic("interface delivery requires its evaluated receiver binding")
		}
		return argument
	}
	if recipe.forward != nil {
		for _, argument := range recipe.forwardArgs {
			switch {
			case argument.sourceParameter >= 0:
				arguments = append(arguments, mapParameter(argument.sourceParameter, argument.fieldPath))
			case argument.constant != nil:
				arguments = append(arguments, llssa.CoroHelperViewArgument{Parameter: -1,
					Constant: literals.Const(argument.constant.Value, p.prog.Type(argument.valueType, llssa.InGo))})
			default:
				panic("interface method cannot borrow a separate closure capture environment")
			}
		}
	} else {
		for index := 0; index < recipe.signature.Params().Len(); index++ {
			arguments = append(arguments, mapParameter(index, nil))
		}
	}
	symbol := "__llgo_coro_delivery_interface_view." + emissionDigest(p.emissionUniverse.finalIdentity(recipe.producer)) + ".drop"
	primary = p.coroDeliveryTypedHelperView(primary, drop, symbol, arguments)
	dropABI, err := newCoroPlainDispatchEffectiveABI(p, drop)
	if err != nil {
		panic(err)
	}
	dropCallable := p.emitCoroManagedPrimaryDescriptorBinding(literals, primary.plan, dropABI,
		primary.physical, primary.physical, llssa.Nil, nil, false, llssa.Nil, nil, symbol)
	initializer := p.emitCoroRecipeDeliveryHelper(literals, base, 0, initialize)
	initializeABI, err := newCoroPlainDispatchEffectiveABI(p, initialize)
	if err != nil {
		panic(err)
	}
	return &llssa.CoroDispatchDeliveryOptions{Version: llssa.CoroDispatchDeliveryVersionV2,
		Initializer: llssa.CoroDispatchDeliveryHelper{Descriptor: initializer.descriptor, Signature: initialize, ABIHash: initializeABI.hash},
		Drop:        llssa.CoroDispatchDeliveryHelper{Descriptor: dropCallable.descriptor, Signature: drop, ABIHash: dropABI.hash},
	}
}
