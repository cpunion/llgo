/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"fmt"
	"go/token"
	"go/types"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// This is a structural custody projection inside ProgramIR, not a second
// effect fixed point. The existing call graph still owns effect and demand.
// Only exact return forwarding of an already proven producer is eligible.
func (builder coroProgramIRBuilder) freezeDeliveryForwarders(ir *coroProgramIR) error {
	u := builder.canonical.universe
	visiting := make(map[*ssa.Function]bool)
	visited := make(map[*ssa.Function]bool)
	var visit func(*ssa.Function) error
	visit = func(function *ssa.Function) error {
		if _, present := ir.deliveryProducers[function]; present || visited[function] || visiting[function] {
			return nil
		}
		visiting[function] = true
		defer func() { delete(visiting, function); visited[function] = true }()
		facts, present := ir.localBodyFacts[function]
		shape := facts.forwarding
		if !present || shape == nil || function.Signature.Variadic() || len(shape.target.FreeVars) != 0 {
			return nil
		}
		background, classified, err := u.FunctionBackground(function)
		if err != nil {
			return err
		}
		if !classified || background != llssa.InGo || coro.TypeContainsUnresolvedTypeParam(function.Signature, make(map[types.Type]bool)) {
			return nil
		}
		frozen, present := ir.callPlans[shape.call]
		if !present || frozen.failure != "" || frozen.plan.ElidesCall() || frozen.plan.RawPlain ||
			frozen.plan.ManagedStaticTarget != nil && frozen.plan.ManagedStaticTarget != shape.target ||
			u.canonicalAlias(shape.target) != shape.target {
			return nil
		}
		if err := visit(shape.target); err != nil {
			return err
		}
		if receiver := ir.localBodyFacts[shape.target].deliveryReceiver; receiver != nil {
			facts.deliveryReceiver = receiver
			facts.ResultDelivery = true
			ir.localBodyFacts[function] = facts
			return nil
		}
		target, present := ir.deliveryProducers[shape.target]
		if !present {
			return nil
		}
		snapshot := target.snapshot
		stateOnly := target.requiresState()
		if !stateOnly && coroDeliveryNeedsReceiverSnapshot(shape, target) {
			// The primary retains the original loads and nil checks. Capture
			// their SSA value once, before the real producer acquires anything.
			value := shape.receiverEvaluation
			snapshot = &coroDeliveryReceiverSnapshot{owner: function, value: value}
			copy := *shape
			copy.args = append([]coroForwardingSourceArgument(nil), shape.args...)
			copy.args[0].evaluation, copy.args[0].projectedType = value, value.Type()
			shape = &copy
		}
		var sourceArguments []coroForwardingSourceArgument
		base, composed := target.producer, true
		if stateOnly {
			// A state-bound helper ignores every original producer argument.
			// Its deepest owner captured the complete cleanup tuple, so an
			// outer wrapper must neither reread nor replace that state.
			if target.forward != nil {
				base = target.forwardBase
			}
		} else {
			sourceArguments, base, composed = composeCoroDeliveryForwardingArguments(shape, target)
		}
		if !composed {
			return nil
		}
		if _, hasEnv, err := u.closureEnvironments.entryEnvironment(base); err != nil {
			return err
		} else if hasEnv {
			return nil // This projection does not invent a callee environment.
		}
		arguments, projected, err := builder.freezeDeliveryForwardingArguments(function, sourceArguments)
		if err != nil {
			return err
		} else if !projected {
			return nil
		}
		signature, err := u.coroPhysicalSourceSignature(function)
		if err != nil {
			return err
		}
		result, _, _, err := coro.DeliveryHelperSignatures(signature)
		if err != nil || !types.Identical(result, target.result) {
			return nil
		}
		if snapshot != nil && snapshot.owner == function {
			fields := make([]*types.Var, 0, len(arguments)+1)
			for index, argument := range arguments {
				fields = append(fields, types.NewField(token.NoPos, nil, fmt.Sprintf("Arg%d", index), argument.valueType, false))
			}
			fields = append(fields, types.NewField(token.NoPos, nil, "padding", types.Typ[types.Byte], false))
			snapshot.box = types.NewStruct(fields, nil)
		}
		// Numeric conversions may have source semantics even if LLVM happens
		// to use equal-width words. Only the existing exact transport proof
		// grants the zero-allocation retag used by helper ABI views.
		for _, argument := range arguments {
			if (argument.sourceParameter >= 0 || argument.capture != nil || argument.evaluation != nil) && coroPhysicalTransportTypeKey(u, argument.projectedType) != coroPhysicalTransportTypeKey(u, argument.valueType) {
				return nil
			}
		}
		recipe := coroSourceDeliveryRecipe{producer: function, signature: signature, result: result,
			initializer: target.initializer, drop: target.drop, forward: shape, forwardBase: base, forwardArgs: arguments, snapshot: snapshot, stateBind: target.stateBind}
		// An archive's real private helpers remain checked opaque facts. They
		// are not converted to made-up source function bodies.
		if target.archive != nil {
			copy := *target.archive
			recipe.archive = &copy
		}
		ir.deliveryProducers[function] = recipe
		if snapshot != nil && snapshot.owner == function {
			if err := builder.freezeDeliverySnapshotAllocator(function); err != nil {
				return err
			}
		}
		for ordinal, name := range []string{coroDeliveryInitializeCall, coroDeliveryDropCall} {
			helper := recipe.initializer
			if ordinal == 1 {
				helper = recipe.drop
			}
			var err error
			if helper != nil {
				err = u.recordCoroLoweredCall(function, name, helper)
			} else {
				err = builder.recordLoweredCallTarget(function, name, coroLoweredCallTarget{importedTarget: recipe.archive.helpers[ordinal].fact.ID})
			}
			if err != nil {
				return err
			}
		}
		if u.prog != nil && u.prog.LocalGoTypeExceedsNativeStack(llssa.CoroDeliveryStorageGoType(result)) {
			runtime := u.byPath[llssa.PkgRuntime]
			if runtime == nil || u.pathDup[llssa.PkgRuntime] {
				return fmt.Errorf("large forwarded result requires its exact runtime allocator")
			}
			allocator := u.canonicalAlias(runtime.ssa.Func(coroRuntimeHelperTargetName(coroManagedFrameSlotAllocZCall)))
			if allocator == nil {
				return fmt.Errorf("large forwarded result has no runtime allocator")
			}
			if err := u.recordCoroLoweredCall(function, coroManagedFrameSlotAllocZCall, allocator); err != nil {
				return err
			}
		}
		return nil
	}
	for _, function := range u.functions {
		if err := visit(function); err != nil {
			return err
		}
	}
	return nil
}

// This is a projection of the same frozen forwarding proof, not a body walk
// or an ownership effect propagated through arbitrary calls. An unused
// receiver remains eligible; only a helper input that needs the evaluated
// receiver makes its custody binding necessary.
func coroDeliveryNeedsReceiverSnapshot(shape *coroForwardingSourceShape, target coroSourceDeliveryRecipe) bool {
	if !shape.methodProjection || shape.receiverEvaluation == nil || len(shape.args) == 0 {
		return false
	}
	receiver := shape.args[0]
	if receiver.sourceParameter >= 0 || receiver.capture != nil || receiver.constant != nil {
		return false
	}
	if target.forward == nil {
		return true
	}
	for _, argument := range target.forwardArgs {
		if argument.sourceParameter == 0 {
			return true
		}
	}
	return false
}

func composeCoroDeliveryForwardingArguments(shape *coroForwardingSourceShape, target coroSourceDeliveryRecipe) ([]coroForwardingSourceArgument, *ssa.Function, bool) {
	if target.forward == nil {
		return shape.args, target.producer, true
	}
	if target.forwardBase == nil {
		return nil, nil, false
	}
	arguments := make([]coroForwardingSourceArgument, len(target.forwardArgs))
	for index, projection := range target.forwardArgs {
		argument := projection.coroForwardingSourceArgument
		if projection.sourceParameter >= 0 {
			if projection.sourceParameter >= len(shape.args) {
				return nil, nil, false
			}
			argument = shape.args[projection.sourceParameter]
			if len(projection.fieldPath) != 0 {
				projected, ok := coroForwardingProjectedType(argument.projectedType, projection.fieldPath)
				if !ok || argument.constant != nil {
					return nil, nil, false
				}
				argument.fieldPath = append(append([]int(nil), argument.fieldPath...), projection.fieldPath...)
				argument.projectedType = projected
			}
			argument.valueType = projection.valueType
		} else if projection.constant == nil {
			// A callee's capture belongs to a different environment. Only the
			// source function's own immutable captures can later be projected.
			return nil, nil, false
		}
		arguments[index] = argument
	}
	return arguments, target.forwardBase, true
}

func (builder coroProgramIRBuilder) freezeDeliveryForwardingArguments(function *ssa.Function, source []coroForwardingSourceArgument) ([]coroDeliveryForwardArgument, bool, error) {
	u := builder.canonical.universe
	arguments := make([]coroDeliveryForwardArgument, len(source))
	for index, projection := range source {
		argument := coroDeliveryForwardArgument{coroForwardingSourceArgument: projection}
		switch {
		case projection.sourceParameter >= 0, projection.constant != nil:
		case projection.evaluation != nil:
			instruction, ok := projection.evaluation.(ssa.Instruction)
			shape := u.coroProgramIR.localBodyFacts[function].forwarding
			if !ok || instruction.Parent() != function || shape == nil || !shape.methodProjection || shape.receiverEvaluation != projection.evaluation {
				return nil, false, fmt.Errorf("delivery snapshot lost its exact primary receiver evaluation")
			}
		case projection.capture != nil:
			environment, hasEnv, err := u.closureEnvironments.entryEnvironment(function)
			if err != nil {
				return nil, false, err
			}
			if !hasEnv {
				if !u.closureEnvironments.elidesZeroSizedFreeVar(function, projection.capture) || u.prog == nil ||
					u.prog.SizeOf(u.prog.Type(projection.capture.Type(), llssa.InGo)) != 0 {
					return nil, false, nil // Never replace an elided pointer with nil.
				}
				break
			}
			pointer, ok := environment.Type().(*types.Pointer)
			if !ok {
				return nil, false, fmt.Errorf("captured delivery value has no typed environment")
			}
			structure, ok := pointer.Elem().Underlying().(*types.Struct)
			if !ok || structure.NumFields() != len(function.FreeVars) {
				return nil, false, fmt.Errorf("captured delivery value has a conflicting frozen environment")
			}
			found := false
			for field, free := range function.FreeVars {
				if free == projection.capture {
					argument.environment, argument.environmentField, found = environment, field, true
					break
				}
			}
			if !found {
				return nil, false, fmt.Errorf("captured delivery value belongs to a different function")
			}
			effective := u.effectiveType(u.ownerOf(function), function, projection.capture.Type())
			if !types.Identical(structure.Field(argument.environmentField).Type(), effective) {
				return nil, false, fmt.Errorf("captured delivery field does not match its frozen value type")
			}
		default:
			return nil, false, nil // A required receiver evaluation needs a snapshot.
		}
		arguments[index] = argument
	}
	return arguments, true, nil
}

// Apply the frozen source mapping to the producer arguments of a drop call.
// The result address is unchanged. The terminal producer is context-free, so
// its helper receives nil, never a forwarding wrapper's unrelated funcval env.
func (p *context) coroProjectDeliveryArguments(b llssa.Builder, recipe coroSourceDeliveryRecipe, args []llssa.Expr) (coroSourceDeliveryRecipe, []llssa.Expr) {
	if recipe.forward != nil {
		if len(args) != recipe.signature.Params().Len()+2 {
			panic("forwarded delivery lost its typed arguments")
		}
		forwarded := []llssa.Expr{args[0], p.prog.Nil(p.prog.VoidPtr())}
		for _, argument := range recipe.forwardArgs {
			var value llssa.Expr
			if argument.sourceParameter >= 0 {
				value = args[2+argument.sourceParameter]
				for _, field := range argument.fieldPath {
					value = b.Field(value, field)
				}
			} else if argument.capture != nil {
				if argument.environment == nil {
					value = p.prog.Zero(p.prog.Type(argument.valueType, llssa.InGo))
				} else {
					address := b.Convert(p.prog.Type(argument.environment.Type(), llssa.InGo), args[1])
					address = b.FieldAddrKnownNonNil(address, argument.environmentField)
					for _, field := range argument.fieldPath {
						address = b.FieldAddrKnownNonNil(address, field)
					}
					value = b.LoadKnownNonNil(address)
				}
			} else if argument.evaluation != nil {
				value = p.compileValue(b, argument.evaluation)
				for _, field := range argument.fieldPath {
					value = b.Field(value, field)
				}
			} else {
				value = p.compileValueAs(b, argument.constant, argument.valueType)
			}
			if !types.Identical(value.RawType(), argument.valueType) {
				value = b.ChangeType(p.prog.Type(argument.valueType, llssa.InGo), value)
			}
			forwarded = append(forwarded, value)
		}
		var found bool
		recipe, found = p.emissionUniverse.coroProgramIR.deliveryProducers[recipe.forwardBase]
		if !found {
			panic("forwarded delivery lost its proven producer")
		}
		args = forwarded
	}
	return recipe, args
}

func coroDeliveryViewSymbol(identity string) string {
	return "__llgo_coro_delivery_view." + emissionDigest(identity) + ".drop"
}

// Physical views have no source identity, effect node, or independent demand.
// Their single real helper's final plan selects the physical prefix. Archives
// publish the public typed view separately from that logical helper's ABI.
type coroDeliveryHelperPrimary struct {
	plan      coro.FunctionPlan
	signature *types.Signature
	physical  llssa.Expr
	view      string
}

func (p *context) coroDeliveryRecipeHelperPrimary(recipe coroSourceDeliveryRecipe, ordinal int) coroDeliveryHelperPrimary {
	if recipe.forward != nil {
		target := p.emissionUniverse.coroProgramIR.deliveryProducers[recipe.forwardBase]
		primary := p.coroDeliveryRecipeHelperPrimary(target, ordinal)
		if ordinal == 0 {
			return primary
		}
		_, _, signature, err := coro.DeliveryHelperSignatures(recipe.signature)
		if err != nil {
			panic(err)
		}
		arguments := []llssa.CoroHelperViewArgument{{Parameter: 0}, {Parameter: -1, Constant: p.prog.Nil(p.prog.VoidPtr())}}
		literals := p.pkg.FuncOf(primary.physical.Name()).NewBuilder()
		defer literals.Dispose()
		if recipe.requiresState() {
			for index := 0; index < target.signature.Params().Len(); index++ {
				if recipe.snapshot != nil {
					arguments = append(arguments, llssa.CoroHelperViewArgument{Parameter: 0,
						ResultState: p.prog.Pointer(p.prog.Type(recipe.snapshot.box, llssa.InGo)), Fields: []int{index}})
				} else {
					arguments = append(arguments, llssa.CoroHelperViewArgument{Parameter: -1,
						Constant: p.prog.Zero(p.prog.Type(target.signature.Params().At(index).Type(), llssa.InGo))})
				}
			}
		} else {
			for _, argument := range recipe.forwardArgs {
				mapped := llssa.CoroHelperViewArgument{Parameter: -1}
				if argument.sourceParameter >= 0 {
					mapped.Parameter = argument.sourceParameter + 2
					mapped.Fields = argument.fieldPath
				} else if argument.capture != nil {
					if argument.environment == nil {
						mapped.Constant = p.prog.Zero(p.prog.Type(argument.valueType, llssa.InGo))
					} else {
						mapped.Parameter, mapped.Environment = 1, p.prog.Type(argument.environment.Type(), llssa.InGo)
						mapped.Fields = append([]int{argument.environmentField}, argument.fieldPath...)
					}
				} else {
					mapped.Constant = literals.Const(argument.constant.Value, p.prog.Type(argument.valueType, llssa.InGo))
				}
				arguments = append(arguments, mapped)
			}
		}
		symbol := coroDeliveryViewSymbol(p.emissionUniverse.finalIdentity(recipe.producer))
		return p.coroDeliveryTypedHelperView(primary, signature, symbol, arguments)
	}
	target := recipe.initializer
	if ordinal == 1 {
		target = recipe.drop
	}
	if target != nil {
		return p.coroDeliverySourceHelperPrimary(target)
	}
	return p.coroDeliveryArchiveHelperPrimary(recipe, ordinal)
}

func (p *context) coroDeliverySourceHelperPrimary(target *ssa.Function) coroDeliveryHelperPrimary {
	entry := p.mustFunctionSymbol(target)
	if entry.ftype != goFunc || len(entry.function.FreeVars) != 0 || entry.plan.RawPlainOnly || entry.plan.Demand == coro.NoDemand {
		panic("delivery view requires its demanded real context-free helper")
	}
	if err := validateCoroManagedDescriptorPrimary(entry.function, entry.plan, p.immutableEmissionUniverse()); err != nil {
		panic(fmt.Errorf("delivery view real helper primary: %w", err))
	}
	signature, err := p.emissionUniverse.coroPhysicalEntrySourceSignature(target)
	if err != nil {
		panic(err)
	}
	// RTTI may request this helper while a raw libffi callback is being
	// emitted. Its immutable managed plan selects the primary independently
	// of the requesting body's native-stack variant.
	physical, _, kind := p.compileManagedFunction(entry.function)
	if kind != goFunc || physical == nil {
		panic("delivery view lost its real typed helper primary")
	}
	return coroDeliveryHelperPrimary{plan: entry.plan, signature: signature, physical: physical.Expr}
}

func (p *context) coroDeliveryArchiveHelperPrimary(recipe coroSourceDeliveryRecipe, ordinal int) coroDeliveryHelperPrimary {
	if recipe.archive == nil {
		panic("delivery view has no source/archive helper")
	}
	helper := recipe.archive.helpers[ordinal]
	plan, found := p.immutablePlan().BasePlan().Lookup(helper.fact.ID)
	plain, structured := coroExternalManagedDescriptorEntry(plan)
	bound, present := p.emissionUniverse.coroProgramIR.archiveDeliveryHelpers[helper.fact.ID]
	if !found || plan.Demand == coro.NoDemand || !plain && !structured || !present || bound.fact != helper.fact {
		panic("delivery view lost its imported helper plan")
	}
	signature := helper.signature
	physicalSignature := signature
	switch plan.ManagedEntry {
	case coro.ManagedEntryPlain:
	case coro.ManagedEntryOutcomePlain:
		physicalSignature = newOutcomePlainPhysicalABI(signature).physicalSig
	case coro.ManagedEntryCoroutine:
		physicalSignature = coroPhysicalEntrySignature(signature)
	default:
		panic("delivery view has no imported managed primary")
	}
	physical := p.pkg.NewFuncEx(helper.symbol(), physicalSignature, llssa.InGo, false, false)
	return coroDeliveryHelperPrimary{plan: plan, signature: signature, physical: physical.Expr, view: helper.view.Symbol}
}

func (p *context) coroDeliveryTypedHelperView(primary coroDeliveryHelperPrimary, signature *types.Signature, symbol string, arguments []llssa.CoroHelperViewArgument) coroDeliveryHelperPrimary {
	prefix, physicalSignature := 0, signature
	switch primary.plan.ManagedEntry {
	case coro.ManagedEntryPlain:
	case coro.ManagedEntryOutcomePlain:
		prefix, physicalSignature = 3, newOutcomePlainPhysicalABI(signature).physicalSig
	case coro.ManagedEntryCoroutine:
		prefix, physicalSignature = 2, coroPhysicalEntrySignature(signature)
	default:
		panic("delivery view lost its managed primary")
	}
	physical := p.pkg.NewCoroHelperView(symbol, physicalSignature, primary.physical, prefix, arguments)
	return coroDeliveryHelperPrimary{plan: primary.plan, signature: signature, physical: physical.Expr, view: symbol}
}

func (p *context) emitCoroForwardedDeliveryHelper(b llssa.Builder, recipe coroSourceDeliveryRecipe, ordinal int, signature *types.Signature) coroManagedCallable {
	primary := p.coroDeliveryRecipeHelperPrimary(recipe, ordinal)
	if !types.Identical(primary.signature, signature) {
		panic("delivery view does not match its exact public signature")
	}
	abi, err := newCoroPlainDispatchEffectiveABI(p, signature)
	if err != nil {
		panic(err)
	}
	return p.emitCoroManagedPrimaryDescriptorBinding(b, primary.plan, abi, primary.physical, primary.physical, llssa.Nil, nil, false, llssa.Nil, nil, primary.view)
}
