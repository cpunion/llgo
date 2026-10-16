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

type coroDeliveryGuardPlan struct {
	call    ssa.CallInstruction
	recipe  coroSourceDeliveryRecipe
	dynamic *coroDynamicDeliveryRecipe
	tag     uint32
	hasEnv  bool
}

func (plan coroDeliveryGuardPlan) signature() *types.Signature {
	if plan.dynamic != nil {
		return plan.dynamic.signature
	}
	return plan.recipe.signature
}

func (plan coroDeliveryGuardPlan) resultType() *types.Struct {
	if plan.dynamic != nil {
		return plan.dynamic.result
	}
	return plan.recipe.result
}

type coroDeliveryFunctionPlan struct {
	fallback *coroSourceDeliveryRecipe
	calls    []coroDeliveryGuardPlan
}

func prepareCoroDeliveryFunctionPlan(fn *ssa.Function, whole *coro.SSAPlan, ir *coroProgramIR, source coroDeliverySourceUniverse) (*coroDeliveryFunctionPlan, error) {
	if ir == nil || source == nil || len(ir.deliveryProducers) == 0 {
		return nil, nil
	}
	plan := new(coroDeliveryFunctionPlan)
	if recipe, present := ir.deliveryProducers[fn]; present {
		fact, found := whole.FunctionPlan(fn)
		if !found || fact.FuncRep == coro.Dispatch {
			return nil, fmt.Errorf("llgo.coroBorrowResult requires a bound physical resource-delivery guard for descriptor publication")
		}
		if recipe.archive == nil {
			if recipe.loan.Value != recipe.borrow || recipe.loan.FunctionsVisited == 0 {
				return nil, fmt.Errorf("llgo.coroBorrowResult requires its frozen borrowed output lifetime proof")
			}
			for ordinal, helper := range []*ssa.Function{recipe.initializer, recipe.drop} {
				proof := recipe.helperLoans[ordinal]
				if proof.Function != helper || proof.Index != 0 || proof.FunctionsVisited == 0 {
					return nil, fmt.Errorf("llgo.coroBorrowResult requires its exact helper output lifetime proof")
				}
			}
			plan.fallback = &recipe
		}
	}
	reachable := ir.coroReachableBlocks(fn)
	for _, block := range fn.Blocks {
		if !reachable[block] {
			continue
		}
		for _, instruction := range block.Instrs {
			call, callable := instruction.(ssa.CallInstruction)
			if !callable {
				continue
			}
			frozen := ir.callPlans[call]
			if frozen.failure != "" || frozen.plan.ElidesCall() || frozen.plan.RawPlain {
				continue
			}
			recipe, present := ir.deliveryProducers[frozen.deliveryProducer]
			if !present {
				if dynamic := frozen.dynamicDelivery; dynamic != nil {
					fact, planned := whole.CallPlan(call)
					if !planned || !fact.ResultDelivery {
						continue
					}
					if dynamic.largeResult && dynamic.allocate == "" {
						return nil, fmt.Errorf("large dynamic delivery requires its frozen conditional result allocator")
					}
					if len(fact.Targets) == 1 {
						target, _ := whole.Function(fact.Targets[0])
						recipe = ir.deliveryProducers[target]
					}
					if uint64(len(plan.calls)) == uint64(^uint32(0)) {
						return nil, fmt.Errorf("too many resource delivery call sites")
					}
					plan.calls = append(plan.calls, coroDeliveryGuardPlan{
						call: call, recipe: recipe, dynamic: dynamic, tag: uint32(len(plan.calls)) + 1, hasEnv: true,
					})
					continue
				}
				// A dynamic/closed-value occurrence must not receive an ordinary
				// result slot until its descriptor carries the same contract.
				if fact, planned := whole.CallPlan(call); planned {
					for _, id := range fact.Targets {
						target, _ := whole.Function(id)
						if _, resource := ir.deliveryProducers[target]; resource {
							return nil, fmt.Errorf("llgo.coroBorrowResult requires a bound physical resource-delivery guard for dynamic call %q", call.String())
						}
					}
				}
				continue
			}
			if _, spawned := call.(*ssa.Go); spawned {
				continue // The independently owned entry uses its nil-output fallback.
			}
			if uint64(len(plan.calls)) == uint64(^uint32(0)) {
				return nil, fmt.Errorf("too many resource delivery call sites")
			}
			entry, err := source.coroPhysicalEntrySourceSignature(recipe.producer)
			if err != nil || entry == nil || entry.Params().Len() < recipe.signature.Params().Len() || entry.Params().Len() > recipe.signature.Params().Len()+1 {
				return nil, fmt.Errorf("delivery producer lacks its exact environment layout")
			}
			plan.calls = append(plan.calls, coroDeliveryGuardPlan{
				call: call, recipe: recipe, tag: uint32(len(plan.calls)) + 1,
				hasEnv: entry.Params().Len() != recipe.signature.Params().Len(),
			})
		}
	}
	if plan.fallback == nil && len(plan.calls) == 0 {
		return nil, nil
	}
	return plan, nil
}

// These are emission-time bindings of existing typed frame storage. There is
// no runtime guard object, independent cancellation decision, or per-call bool.
type coroDeliveryGuardState struct {
	plan           coroDeliveryGuardPlan
	result         llssa.Expr
	args           []llssa.Expr
	env            llssa.Expr
	dropDescriptor llssa.Expr
	resultAddress  llssa.Expr
}

type coroDeliveryFunctionState struct {
	plan            *coroDeliveryFunctionPlan
	active          llssa.Expr
	calls           map[ssa.CallInstruction]*coroDeliveryGuardState
	fallbackActive  llssa.Expr
	fallback        llssa.Expr
	fallbackManaged bool
}

func (s *coroStaticCleanupState) bindDelivery(p *context, plan *coroDeliveryFunctionPlan) {
	if plan == nil {
		return
	}
	if s == nil || s.external || s.delivery != nil {
		panic("resource delivery storage requires one owner-local cleanup drainer")
	}
	delivery := &coroDeliveryFunctionState{plan: plan, calls: make(map[ssa.CallInstruction]*coroDeliveryGuardState, len(plan.calls))}
	if len(plan.calls) != 0 {
		delivery.active = p.coroFrameAlloc(p.prog.Uint32())
	}
	for _, site := range plan.calls {
		guard := &coroDeliveryGuardState{plan: site}
		if site.hasEnv {
			guard.env = p.coroFrameAlloc(p.prog.VoidPtr())
		}
		if site.dynamic != nil {
			guard.dropDescriptor = p.coroFrameAlloc(p.prog.VoidPtr())
		}
		if _, deferred := site.call.(*ssa.Defer); deferred && (s.dynamic || site.dynamic != nil && site.dynamic.largeResult) {
			// A heterogeneous drainer emits its prefix before its site bodies.
			// Keep the active site's result address in the existing owner frame;
			// never depend on code-generation order or a later LLVM definition.
			guard.resultAddress = p.coroFrameAlloc(p.prog.Pointer(p.prog.Type(site.resultType(), llssa.InGo)))
		}
		for index := 0; index < site.signature().Params().Len(); index++ {
			guard.args = append(guard.args, p.coroFrameAlloc(p.type_(site.signature().Params().At(index).Type(), llssa.InGo)))
		}
		delivery.calls[site.call] = guard
	}
	if plan.fallback != nil {
		delivery.fallbackActive = p.coroFrameAlloc(p.prog.Bool())
		typ := p.prog.Type(plan.fallback.result, llssa.InGo)
		delivery.fallbackManaged = p.prog.LocalAllocExceedsNativeStack(typ)
		if delivery.fallbackManaged {
			// A borrowed parent sink needs no second aggregate. Retain only
			// a zeroed pointer, allocating the fallback on the nil-output lane.
			delivery.fallback = p.coroFrameAlloc(p.prog.Pointer(typ))
		} else {
			delivery.fallback = p.coroResultSlot(typ)
		}
	}
	s.delivery = delivery
}

func (p *context) beginCoroDeliveryCall(b llssa.Builder, target *ssa.Function, env llssa.Expr, args []llssa.Expr) *coroDeliveryGuardState {
	cleanup, site := p.coroCleanup(), p.coroEmissionSite()
	if cleanup == nil || cleanup.delivery == nil || site == nil {
		return nil
	}
	call, callable := site.instruction.(ssa.CallInstruction)
	if !callable {
		return nil
	}
	guard := cleanup.delivery.calls[call]
	if guard == nil || guard.plan.recipe.producer != target {
		return nil // An initializer/drop at the same source site is not the producer.
	}
	if guard.plan.dynamic != nil {
		// A closed singleton may use an exact ramp without materializing the
		// producer's public descriptor. Its conditional helper edges still
		// invoke the same typed primary helper descriptors.
		_, initializer, drop, err := coro.DeliveryHelperSignatures(guard.plan.signature())
		if err != nil {
			panic(err)
		}
		if guard.plan.dynamic.allocate == "" {
			guard.result = p.coroResultSlot(p.prog.Type(guard.plan.resultType(), llssa.InGo))
		}
		p.armCoroDynamicDeliveryCall(b, guard, env, args,
			p.emitCoroRecipeDeliveryHelperValue(b, guard.plan.recipe, 0, initializer),
			p.emitCoroRecipeDeliveryHelperValue(b, guard.plan.recipe, 1, drop), p.coroDeliveryHelperInvocation())
		return guard
	}
	if len(args) != len(guard.args) {
		panic("resource delivery call lost its exact typed captures")
	}
	// Bind through the source occurrence so the existing managed-slot helper
	// and its exact SitePlan are observed together. coroResultSlot still places
	// physical storage in the dominating ramp, once per static call site.
	guard.result = p.coroResultSlot(p.prog.Type(guard.plan.recipe.result, llssa.InGo))
	if !guard.resultAddress.IsNil() {
		b.StoreKnownNonNil(guard.resultAddress, guard.result)
	}
	if guard.plan.hasEnv {
		if env.IsNil() {
			panic("resource delivery call lost its frozen closure environment")
		}
		b.StoreKnownNonNil(guard.env, b.Convert(p.prog.VoidPtr(), env))
	}
	for index, argument := range args {
		b.StoreKnownNonNil(guard.args[index], argument)
	}
	b.StoreKnownNonNil(guard.result, p.prog.Zero(p.prog.Elem(guard.result.Type)))
	p.compileCoroRecipeDeliveryHelper(b, guard.plan.recipe.consumerInitialize, guard.plan.recipe, 0, []llssa.Expr{guard.result})
	b.StoreKnownNonNil(cleanup.delivery.active, p.prog.IntVal(uint64(guard.plan.tag), p.prog.Uint32()))
	return guard
}

func (s *coroDeliveryFunctionState) claim(b llssa.Builder, guard *coroDeliveryGuardState) {
	b.StoreKnownNonNil(s.active, b.Prog.IntVal(0, b.Prog.Uint32()))
	guard.clearCaptures(b)
}

func (guard *coroDeliveryGuardState) clearCaptures(b llssa.Builder) {
	if !guard.dropDescriptor.IsNil() {
		b.StoreVolatile(guard.dropDescriptor, b.Prog.Nil(b.Prog.VoidPtr()))
	}
	if !guard.resultAddress.IsNil() {
		b.StoreVolatile(guard.resultAddress, b.Prog.Nil(b.Prog.Elem(guard.resultAddress.Type)))
	}
	if !guard.env.IsNil() {
		b.StoreVolatile(guard.env, b.Prog.Nil(b.Prog.VoidPtr()))
	}
	for _, argument := range guard.args {
		b.StoreVolatile(argument, b.Prog.Zero(b.Prog.Elem(argument.Type)))
	}
}

// emitDeliveryPrefix is part of the existing cleanup drainer. Clear the tag
// before calling a possibly suspending/panicking drop so re-entry cannot
// execute the same ownership record twice.
func (s *coroStaticCleanupState) emitDeliveryPrefix(p *context, b llssa.Builder, next llssa.BasicBlock) llssa.BasicBlock {
	if s.delivery == nil || len(s.delivery.plan.calls) == 0 {
		return next
	}
	delivery := s.delivery
	entry, invalid := p.fn.MakeBlock(), p.fn.MakeBlock()
	b.SetBlock(entry)
	dispatch := b.Switch(b.LoadKnownNonNil(delivery.active), invalid)
	dispatch.Case(p.prog.IntVal(0, p.prog.Uint32()), next)
	for _, site := range delivery.plan.calls {
		guard := delivery.calls[site.call]
		call := p.fn.MakeBlock()
		dispatch.Case(p.prog.IntVal(uint64(site.tag), p.prog.Uint32()), call)
		b.SetBlock(call)
		env := p.prog.Nil(p.prog.VoidPtr())
		if !guard.env.IsNil() {
			env = b.LoadKnownNonNil(guard.env)
		}
		result := guard.result
		if !guard.resultAddress.IsNil() {
			result = b.LoadKnownNonNil(guard.resultAddress)
		}
		args := []llssa.Expr{result, env}
		for _, capture := range guard.args {
			args = append(args, b.LoadKnownNonNil(capture))
		}
		var drop llssa.Expr
		if site.dynamic != nil {
			drop = b.Aggregate(p.prog.Closure(site.dynamic.drop),
				b.LoadKnownNonNil(guard.dropDescriptor), p.prog.Nil(p.prog.VoidPtr()))
		}
		delivery.claim(b, guard)
		finish := s.beginRelocatedSiteEmission(p, site.call, coroRuntimeHelperAtResultDrop)
		if site.dynamic != nil {
			p.compileCoroDynamicDeliveryHelper(b, site.dynamic.release, drop, site.dynamic.drop, args, coroCleanupInvocation{state: s})
		} else {
			p.compileCoroRecipeDeliveryHelper(b, site.recipe.consumerDrop, site.recipe, 1, args)
		}
		finish()
		b.Jump(next)
	}
	b.SetBlock(entry)
	dispatch.End(b)
	b.SetBlock(invalid)
	b.Unreachable()
	return entry
}

func (p *context) compileCoroBorrowedResult(b llssa.Builder) llssa.Expr {
	cleanup := p.coroCleanup()
	if cleanup == nil || cleanup.delivery == nil || cleanup.delivery.plan.fallback == nil {
		panic("llgo.coroBorrowResult requires a bound physical resource-delivery guard")
	}
	delivery := cleanup.delivery
	// The incoming result sink is an ABI word, not a reconstructed function
	// pointer or a second result container. The parent initialized it before
	// acquisition; only a discarded-entry fallback initializes its own slot.
	incoming := p.coroIncomingResultSlot()
	provided, fallback, initialized, joined := p.fn.MakeBlock(), p.fn.MakeBlock(), p.fn.MakeBlock(), p.fn.MakeBlock()
	b.If(b.BinOp(token.NEQ, incoming, p.prog.Nil(p.prog.VoidPtr())), provided, fallback)
	b.SetBlockEx(fallback, llssa.AtEnd, false)
	storage := delivery.fallback
	if delivery.fallbackManaged {
		storage = b.AllocZAs(p.prog.Elem(p.prog.Elem(storage.Type)), coroManagedFrameSlotAllocZCall)
		b.StoreKnownNonNil(delivery.fallback, storage)
	} else {
		b.StoreKnownNonNil(storage, p.prog.Zero(p.prog.Elem(storage.Type)))
	}
	recipe := delivery.plan.fallback
	p.compileCoroDeliveryHelper(b, coroDeliveryInitializeCall, recipe.initializer, []llssa.Expr{storage})
	b.Jump(initialized)
	b.SetBlockEx(initialized, llssa.AtEnd, false)
	b.StoreKnownNonNil(delivery.fallbackActive, p.prog.BoolVal(true))
	b.Jump(joined)
	b.SetBlockEx(provided, llssa.AtEnd, false)
	parent := b.Convert(storage.Type, incoming)
	b.Jump(joined)
	b.SetBlockContinuation(joined)
	result := b.Phi(storage.Type)
	result.AddIncoming(b, []llssa.BasicBlock{initialized, provided}, func(index int, _ llssa.BasicBlock) llssa.Expr {
		if index == 0 {
			return storage
		}
		return parent
	})
	return result.Expr
}

func (s *coroStaticCleanupState) emitDeliveryFallback(p *context, b llssa.Builder) {
	if s.delivery == nil || s.delivery.plan.fallback == nil {
		return
	}
	delivery := s.delivery
	inspect, call, normal, committed, done := p.fn.MakeBlock(), p.fn.MakeBlock(), p.fn.MakeBlock(), p.fn.MakeBlock(), p.fn.MakeBlock()
	b.If(b.LoadKnownNonNil(delivery.fallbackActive), inspect, done)
	b.SetBlock(inspect)
	returns := b.BinOp(token.AND, p.coroCleanupReturnsNormally(b, s), b.UnOp(token.NOT, b.LoadKnownNonNil(s.panicActive)))
	b.If(returns, normal, call)
	b.SetBlock(normal)
	// RunDefers can resume source result reloads before terminal Return. Keep
	// fallback custody through that interval. Only the completion pass follows
	// the actual source Return commit. A normal discarded result then follows
	// Go semantics: no implicit physical close merely because no caller kept it.
	b.If(b.BinOp(token.EQL, b.LoadKnownNonNil(s.continuation), p.prog.IntVal(uint64(coroStaticCleanupContinueComplete), p.prog.Uint32())), committed, done)
	b.SetBlock(committed)
	b.StoreKnownNonNil(delivery.fallbackActive, p.prog.BoolVal(false))
	if delivery.fallbackManaged {
		b.StoreVolatile(delivery.fallback, p.prog.Zero(p.prog.Elem(delivery.fallback.Type)))
	}
	b.Jump(done)
	b.SetBlock(call)
	b.StoreKnownNonNil(delivery.fallbackActive, p.prog.BoolVal(false))
	base := p.coroEmissionSourceParamBase()
	if base != 2 && base != 3 {
		panic("resource fallback escaped its normalized physical parameter layout")
	}
	env := p.prog.Nil(p.prog.VoidPtr())
	if base == 3 {
		env = b.Convert(p.prog.VoidPtr(), p.fn.PhysicalParam(2))
	}
	storage := delivery.fallback
	if delivery.fallbackManaged {
		storage = b.LoadKnownNonNil(delivery.fallback)
		b.StoreVolatile(delivery.fallback, p.prog.Zero(p.prog.Elem(delivery.fallback.Type)))
	}
	args := []llssa.Expr{storage, env}
	recipe := delivery.plan.fallback
	for index := 0; index < recipe.signature.Params().Len(); index++ {
		args = append(args, p.fn.PhysicalParam(base+index))
	}
	finish := s.beginRelocatedSiteEmission(p, recipe.borrow, coroRuntimeHelperAtResultDrop)
	p.compileCoroDeliveryHelper(b, coroDeliveryDropCall, recipe.drop, args)
	finish()
	b.Jump(done)
	b.SetBlockContinuation(done)
}
