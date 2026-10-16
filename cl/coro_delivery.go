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
	"sort"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroDeliveryGuardABI       = "llgo.coro.borrowed-result-guards.v1"
	coroDeliveryInitializeCall = "coro.delivery.initialize"
	coroDeliveryDropCall       = "coro.delivery.drop"
)

// coroSourceDeliveryRecipe is the source half of the existing delivery fact,
// not a second effect graph or a runtime callback record. Only ProgramIR's
// source builder writes it; physical binding/publication remain fail-closed.
type coroSourceDeliveryRecipe struct {
	producer           *ssa.Function
	borrow             *ssa.Call
	initializer        *ssa.Function
	drop               *ssa.Function
	signature          *types.Signature
	result             *types.Struct
	consumerInitialize string
	consumerDrop       string
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
	if err := validateCoroDeliveryResultProjection(recipe); err != nil {
		return err
	}
	ir.deliveryProducers[recipe.producer] = recipe
	return nil
}

// A borrowed producer publishes its already protected result cells, not cached
// source return values. Keep this finite boundary structural: ordinary Go
// wrappers do not acquire resource ownership merely by calling the producer.
// The physical consumer must still bind both parent and discarded-entry guards
// before this source fact can authorize exposing the hidden result address.
func validateCoroDeliveryResultProjection(recipe coroSourceDeliveryRecipe) error {
	fail := func(reason string) error {
		return fmt.Errorf("llgo.coroBorrowResult: %s", reason)
	}
	entry := recipe.producer.Blocks[0]
	for _, instruction := range entry.Instrs {
		if instruction == recipe.borrow {
			break
		}
		if _, call := instruction.(ssa.CallInstruction); call {
			return fail("borrow must precede source calls, go and defer registrations")
		}
	}
	for _, block := range recipe.producer.Blocks {
		for _, instruction := range block.Instrs {
			ret, returning := instruction.(*ssa.Return)
			if !returning {
				continue
			}
			if len(ret.Results) != recipe.result.NumFields() {
				return fail("return does not project the exact borrowed result tuple")
			}
			projections := make(map[ssa.Instruction]bool, 2*len(ret.Results))
			for index, result := range ret.Results {
				load, ok := result.(*ssa.UnOp)
				if !ok || load.Op != token.MUL || load.Block() != block {
					return fail("return must load each exact borrowed result field in its return block")
				}
				field, ok := load.X.(*ssa.FieldAddr)
				if !ok || field.X != recipe.borrow || field.Field != index {
					return fail("return must project each exact borrowed result field in order")
				}
				projections[load], projections[field] = true, true
			}
			loaded := false
			for _, candidate := range block.Instrs {
				if candidate == ret {
					break
				}
				if projections[candidate] {
					if _, load := candidate.(*ssa.UnOp); load {
						loaded = true
					}
					continue
				}
				if _, debug := candidate.(*ssa.DebugRef); !debug && loaded {
					return fail("borrowed result loads must be followed only by the remaining projections and return")
				}
			}
		}
	}
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

// freezeDeliveryConsumers binds exact static consumers only after every
// producer source fact exists. Source/package traversal order cannot decide
// whether a call owns temporary cleanup. Dynamic descriptor publication is a
// separate physical consumer of the same producer fact, not inferred here.
func (builder coroProgramIRBuilder) freezeDeliveryConsumers(ir *coroProgramIR) error {
	u := builder.canonical.universe
	if ir == nil || u == nil || u.coroProgramIR != ir || ir.callsFrozen {
		return fmt.Errorf("delivery consumers require the active ProgramIR builder")
	}
	if len(ir.deliveryProducers) == 0 {
		return nil
	}
	for producer, recipe := range ir.deliveryProducers {
		identity := emissionDigest(u.finalIdentity(producer))
		recipe.consumerInitialize = coroDeliveryInitializeCall + ":" + identity
		recipe.consumerDrop = coroDeliveryDropCall + ":" + identity
		ir.deliveryProducers[producer] = recipe
		if err := builder.freezeDeliveryHelperPlacements(ir, recipe.borrow, coroDeliveryInitializeCall, coroDeliveryDropCall); err != nil {
			return err
		}
	}
	requireCleanup := func(function *ssa.Function, producer bool) error {
		facts, present := ir.localBodyFacts[function]
		if !present {
			return fmt.Errorf("delivery cleanup owner %q has no frozen local body", function.Name())
		}
		// This is local ownership, not a propagating resource effect. Even a
		// non-suspending initializer/drop needs the existing cleanup drainer
		// for panic, cancellation and discarded-entry fallback.
		facts.Exec = facts.Exec.Join(coro.NeedsCleanupFrame)
		facts.ResultDelivery = facts.ResultDelivery || producer
		facts.OutcomePlainLeaf = false
		facts.OutcomePlainDAG = false
		facts.StaticOutcomeLocal = false
		ir.localBodyFacts[function] = facts
		return nil
	}
	for _, function := range u.functions {
		if function == nil {
			continue
		}
		if _, producer := ir.deliveryProducers[function]; producer {
			if err := requireCleanup(function, true); err != nil {
				return err
			}
		}
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil || call.Common().IsInvoke() {
					continue
				}
				frozen, present := ir.callPlans[call]
				if !present || frozen.failure != "" || frozen.plan.ElidesCall() || frozen.plan.RawPlain {
					continue
				}
				target := u.canonicalAlias(call.Common().StaticCallee())
				if frozen.plan.ManagedStaticTarget != nil {
					target = frozen.plan.ManagedStaticTarget
				}
				recipe, producer := ir.deliveryProducers[target]
				if !producer {
					continue
				}
				frozen.deliveryProducer = recipe.producer
				ir.callPlans[call] = frozen
				for _, owner := range u.sortedUseOwners(function) {
					key := emissionFunctionOwnerKey{function: function, owner: owner}
					site, present := ir.sitePlans[key][instruction]
					if !present || !site.hasCallPlan {
						return fmt.Errorf("delivery call %q lacks its frozen owner SitePlan", call.String())
					}
					site.callPlan = frozen
					ir.sitePlans[key][instruction] = site
				}
				if _, spawned := call.(*ssa.Go); spawned {
					// A go statement does not execute the child's initializer/drop
					// on its parent. Its independently owned entry uses fallback.
					continue
				}
				if err := requireCleanup(function, false); err != nil {
					return err
				}
				for _, helper := range []struct {
					name   string
					target *ssa.Function
				}{
					{recipe.consumerInitialize, recipe.initializer},
					{recipe.consumerDrop, recipe.drop},
				} {
					if err := u.recordCoroLoweredCall(function, helper.name, helper.target); err != nil {
						return err
					}
				}
				var extra []coroPlannedRuntimeHelper
				if _, deferred := call.(*ssa.Defer); deferred && u.prog != nil && u.prog.LocalGoTypeExceedsNativeStack(recipe.result) {
					// The producer's nil-output fallback already materialized this
					// exact helper. Reuse its edge for the deferred consumer's slot.
					allocator := u.loweredCalls[recipe.producer][coroManagedFrameSlotAllocZCall]
					if err := u.recordCoroLoweredCall(function, coroManagedFrameSlotAllocZCall, allocator.target); err != nil {
						return err
					}
					extra = append(extra, coroPlannedRuntimeHelper{name: coroManagedFrameSlotAllocZCall, placement: coroRuntimeHelperAtCleanup})
				}
				if err := builder.freezeDeliveryHelperPlacements(ir, call, recipe.consumerInitialize, recipe.consumerDrop, extra...); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Resource helpers are ordinary frozen managed edges with exact source and
// cleanup placements, not an emitter-time exception to the SitePlan verifier.
func (builder coroProgramIRBuilder) freezeDeliveryHelperPlacements(
	ir *coroProgramIR, call ssa.CallInstruction, initializer, drop string, extra ...coroPlannedRuntimeHelper,
) error {
	u := builder.canonical.universe
	if ir == nil || ir.callsFrozen || u == nil || call == nil || initializer == "" || drop == "" {
		return fmt.Errorf("delivery helper placement requires the active ProgramIR builder")
	}
	placement := coroRuntimeHelperAtSource
	if _, deferred := call.(*ssa.Defer); deferred {
		placement = coroRuntimeHelperAtCleanup
	}
	for _, owner := range u.sortedUseOwners(call.Parent()) {
		key := emissionFunctionOwnerKey{function: call.Parent(), owner: owner}
		site, present := ir.sitePlans[key][call]
		if !present {
			return fmt.Errorf("delivery helper call %q lacks its frozen owner SitePlan", call.String())
		}
		site.managedRuntimeHelpers = append(site.managedRuntimeHelpers,
			coroPlannedRuntimeHelper{name: initializer, placement: placement},
			coroPlannedRuntimeHelper{name: drop, placement: coroRuntimeHelperAtResultDrop},
		)
		site.managedRuntimeHelpers = append(site.managedRuntimeHelpers, extra...)
		sort.Slice(site.managedRuntimeHelpers, func(i, j int) bool {
			first, second := site.managedRuntimeHelpers[i], site.managedRuntimeHelpers[j]
			if first.name != second.name {
				return first.name < second.name
			}
			return first.placement < second.placement
		})
		ir.sitePlans[key][call] = site
	}
	return nil
}

func (ir *coroProgramIR) deliverySiteHelpers(instruction ssa.Instruction) (initializer, drop string, present bool) {
	call, callable := instruction.(ssa.CallInstruction)
	if !callable {
		return "", "", false
	}
	if _, spawned := call.(*ssa.Go); spawned {
		return "", "", false
	}
	frozen := ir.callPlans[call]
	if recipe, found := ir.deliveryProducers[frozen.deliveryProducer]; found {
		return recipe.consumerInitialize, recipe.consumerDrop, true
	}
	if recipe, found := ir.deliveryProducers[call.Parent()]; found && recipe.borrow == instruction {
		return coroDeliveryInitializeCall, coroDeliveryDropCall, true
	}
	return "", "", false
}

func (p *context) compileCoroDeliveryHelper(
	b llssa.Builder, name string, target *ssa.Function, args []llssa.Expr,
) {
	if target == nil {
		panic("resource delivery helper requires an exact frozen target")
	}
	if _, emitted := p.resolveCoroLoweredHelperCall(b, name, llssa.Nil, args, target); !emitted {
		panic(fmt.Sprintf("resource delivery helper %q lost its frozen managed edge", name))
	}
}
