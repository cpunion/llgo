/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"encoding/hex"
	"go/types"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// The bridge is real demanded reflect source. Its typed views borrow the
// caller-owned result's private record and introduce no source activation,
// ownership analysis, or invocation engine.
func (p *context) coroMethodResultPrototype(signature *types.Signature) llssa.Expr {
	if signature.Results().Len() == 0 {
		return llssa.Nil
	}
	var initialize, drop *ssa.Function
	u := p.emissionUniverse
	for _, path := range []string{"github.com/xgo-dev/llgo/runtime/internal/lib/reflect", "reflect"} {
		pkg := u.byPath[path]
		if pkg == nil || u.pathDup[path] {
			continue
		}
		functions := pkg.ssa
		if pkg.hasPatch {
			functions = pkg.patch.Alt
		}
		if target := functions.Func("initializeBoundMethodNative"); target != nil {
			initialize = u.canonicalAlias(target)
			drop = u.canonicalAlias(functions.Func("dropBoundMethodResult"))
			break
		}
	}
	if initialize == nil || drop == nil {
		return llssa.Nil // No reflect bridge in freestanding/runtime islands.
	}
	parameters := make([]*types.Var, signature.Params().Len()-1)
	for i := range parameters {
		parameters[i] = signature.Params().At(i + 1)
	}
	bound := types.NewSignatureType(nil, nil, nil, types.NewTuple(parameters...), signature.Results(), signature.Variadic())
	abi, err := newCoroPlainDispatchEffectiveABI(p, bound)
	if err != nil {
		panic(err)
	}
	if p.prog.SizeOf(p.prog.Type(abi.resultSlotType, llssa.InC)) == 0 {
		return llssa.Nil // A zero-sized result cannot carry resource custody.
	}
	symbol := "__llgo_coro_method_result." + hex.EncodeToString(abi.hash[:])
	// Helpers receive the packed final slice, while the published bound ABI
	// retains its source variadic identity.
	helperSource := types.NewSignatureType(nil, nil, nil, bound.Params(), bound.Results(), false)
	_, initSignature, dropSignature, err := coro.DeliveryHelperSignatures(helperSource)
	if err != nil {
		panic(err)
	}
	var helpers [2]llssa.CoroDispatchDeliveryHelper
	for i, target := range []*ssa.Function{initialize, drop} {
		primary := p.coroDeliverySourceHelperPrimary(target)
		viewSignature := initSignature
		suffix := ".initialize"
		if i == 1 {
			viewSignature, suffix = dropSignature, ".drop"
		}
		record := p.prog.Type(primary.signature.Params().At(0).Type(), llssa.InGo)
		primary = p.coroDeliveryTypedHelperView(primary, viewSignature, symbol+suffix,
			[]llssa.CoroHelperViewArgument{{Parameter: 0, ResultState: record, StatePointer: true}})
		viewABI, err := newCoroPlainDispatchEffectiveABI(p, viewSignature)
		if err != nil {
			panic(err)
		}
		literals := p.pkg.FuncOf(primary.physical.Name()).NewBuilder()
		callable := p.emitCoroManagedPrimaryDescriptorBinding(literals, primary.plan, viewABI,
			primary.physical, primary.physical, llssa.Nil, nil, false, llssa.Nil, nil, primary.view)
		literals.Dispose()
		helpers[i] = llssa.CoroDispatchDeliveryHelper{Descriptor: callable.descriptor, Signature: viewSignature, ABIHash: viewABI.hash}
	}
	return p.pkg.NewCoroMethodResultPrototype(symbol, helperSource, abi.hash, p.prog.Type(abi.resultSlotType, llssa.InC),
		llssa.CoroDispatchDeliveryOptions{Version: llssa.CoroDispatchDeliveryVersionV3, RequiresState: true,
			Initializer: helpers[0], Drop: helpers[1]})
}
