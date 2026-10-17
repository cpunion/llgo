/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
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

// coroManagedDispatchPublishedEntry returns the one ABI
// actually stored in every descriptor reachable at a closed managed call.
// HasStaticOutcome alone is insufficient: a coroutine primary may also emit an
// exact-call outcome twin while its shared function/interface descriptor still
// publishes the coroutine ABI. Mixed target families retain universal dispatch.
func coroManagedDispatchPublishedEntry(
	plan *coro.SSAPlan,
	callPlan coro.SSACallPlan,
) coro.ManagedEntryKind {
	if plan == nil || (callPlan.Kind != coro.CallDirect && callPlan.Kind != coro.CallDefer) ||
		callPlan.Rep != coro.Dispatch ||
		callPlan.Transport != coro.ManagedTransport || callPlan.Open ||
		callPlan.SyncDispatch || callPlan.RawPlain || len(callPlan.Targets) == 0 {
		return coro.ManagedEntryNone
	}
	if callPlan.ResultDelivery {
		return coro.ManagedEntryNone // Structured-only dispatch has no custody probe.
	}
	published := coro.ManagedEntryNone
	for _, targetID := range callPlan.Targets {
		target, found := plan.Function(targetID)
		if !found || target == nil {
			return coro.ManagedEntryNone
		}
		targetPlan, found := plan.FunctionPlan(target)
		if !found || targetPlan.ID != targetID {
			return coro.ManagedEntryNone
		}
		entry := targetPlan.ManagedEntry
		switch entry {
		case coro.ManagedEntryPlain:
			if targetPlan.Effect != coro.NoSuspend || targetPlan.Exec.Contains(coro.MayUnwind) {
				return coro.ManagedEntryNone
			}
		case coro.ManagedEntryCoroutine:
		case coro.ManagedEntryOutcomePlain:
			if !targetPlan.HasStaticOutcome() {
				return coro.ManagedEntryNone
			}
		default:
			return coro.ManagedEntryNone
		}
		if published == coro.ManagedEntryNone {
			published = entry
		} else if published != entry {
			return coro.ManagedEntryNone
		}
	}
	return published
}

func coroManagedDispatchPublishedOutcomeOnly(plan *coro.SSAPlan, callPlan coro.SSACallPlan) bool {
	return coroManagedDispatchPublishedEntry(plan, callPlan) == coro.ManagedEntryOutcomePlain
}

func coroManagedDispatchPublishedCoroutineOnly(plan *coro.SSAPlan, callPlan coro.SSACallPlan) bool {
	return coroManagedDispatchPublishedEntry(plan, callPlan) == coro.ManagedEntryCoroutine
}

func coroManagedEntryIsSynchronous(entry coro.ManagedEntryKind) bool {
	return entry == coro.ManagedEntryPlain || entry == coro.ManagedEntryOutcomePlain
}

// compileCoroManagedDispatchAwait lowers one managed Go function-value call
// carried by the universal {descriptor, environment} representation. The
// descriptor publishes exactly the capability of its one primary body:
// bounded plain targets execute inline, explicit-status targets complete a
// synchronous outcome transaction, and coroutine targets enter the same
// scheduler-owned child transaction as an exact static await.
func (p *context) compileCoroManagedDispatchAwait(
	b llssa.Builder, call *ssa.Call, instructionPlan coroPhysicalInstructionPlan,
) llssa.Expr {
	if !p.hasStructuredOutcomePhysicalBody() || call == nil ||
		instructionPlan.control != coroPhysicalControlDispatchAwait ||
		p.hasOutcomePlainPhysicalBody() && !coroManagedEntryIsSynchronous(instructionPlan.dispatchEntry) {
		panic("coroutine managed dispatch await escaped its frozen physical control recipe")
	}

	p.emitPCLineLabel(b, call.Pos())
	// Evaluate the callee before arguments and every argument left-to-right,
	// before probing capabilities or publishing scheduler state.
	fn := p.compileValue(b, call.Call.Value)
	closure, ok := types.Unalias(fn.RawType()).Underlying().(*types.Struct)
	if !ok || !llssa.IsClosure(closure) {
		owner := "<unknown>"
		if p.goFn != nil {
			owner = p.goFn.String()
		}
		panic(fmt.Errorf(
			"coroutine managed dispatch await: function %q call %q lowered callee %T %q as %s; want the canonical descriptor closure",
			owner, call.String(), call.Call.Value, call.Call.Value.String(), fn.RawType(),
		))
	}
	args := p.compileValues(b, call.Call.Args, fnNormal)
	if instructionPlan.recoverAlias {
		p.observeCoroPhysicalRecoverAlias(call)
	}
	var keepaliveSlots []llssa.Expr
	if !coroManagedEntryIsSynchronous(instructionPlan.dispatchEntry) {
		keepaliveSlots = p.compileCoroCallKeepaliveSlots(b, call)
	}
	result := p.compileCoroManagedDispatchAwaitValueResultWithRecovery(
		b, fn, args, call.Call.Signature(), coroManagedDispatchAwaitOptions{
			keepaliveSlots:          keepaliveSlots,
			transparentRecoverAlias: instructionPlan.recoverAlias,
			trustedDescriptor:       instructionPlan.dispatchEntry != coro.ManagedEntryNone,
			entry:                   instructionPlan.dispatchEntry,
			nativeResult:            instructionPlan.directOutcomeNativeResult,
			delivery:                p.coroDynamicDeliveryGuard(call),
		},
	)
	p.recordCoroValueAddress(call, result.address)
	return result.value
}

type coroManagedDispatchAwaitOptions struct {
	cleanup                 coroCleanupInvocation
	keepaliveSlots          []llssa.Expr
	recoverAliasChild       bool
	transparentRecoverAlias bool
	trustedDescriptor       bool
	descriptorNonNil        bool
	entry                   coro.ManagedEntryKind
	nativeResult            bool
	delivery                *coroDeliveryGuardState
}

// compileCoroManagedDispatchAwaitValue is the one capability probe and child
// transaction shared by ordinary function descriptors and interface-method
// descriptors. The caller owns source evaluation order and exact transport
// validation before entering this helper.
func (p *context) compileCoroManagedDispatchAwaitValue(
	b llssa.Builder, fn llssa.Expr, args []llssa.Expr, signature *types.Signature, keepaliveSlots []llssa.Expr,
) llssa.Expr {
	return p.compileCoroManagedDispatchAwaitValueResultWithRecovery(
		b, fn, args, signature, coroManagedDispatchAwaitOptions{keepaliveSlots: keepaliveSlots},
	).value
}

func (p *context) compileCoroManagedDispatchAwaitValueResultWithRecovery(
	b llssa.Builder, fn llssa.Expr, args []llssa.Expr, signature *types.Signature,
	options coroManagedDispatchAwaitOptions,
) coroAwaitedValue {
	options.cleanup.validate()
	cleanup := options.cleanup.state
	if cleanup != nil && options.transparentRecoverAlias {
		panic("cleanup descriptor cannot also be a transparent recovery wrapper")
	}
	if !p.hasStructuredOutcomePhysicalBody() || b == nil || b.Func != p.fn {
		panic("managed dispatch requires an active structured physical body")
	}
	switch options.entry {
	case coro.ManagedEntryNone, coro.ManagedEntryPlain, coro.ManagedEntryOutcomePlain, coro.ManagedEntryCoroutine:
	default:
		panic("managed dispatch has an invalid frozen entry kind")
	}
	if options.entry != coro.ManagedEntryNone && !options.trustedDescriptor {
		panic("single-entry dispatch requires a frozen trusted publication proof")
	}
	if options.delivery != nil && options.entry != coro.ManagedEntryNone {
		panic("resource delivery requires the capability-aware child transaction")
	}
	abi, err := newCoroPlainDispatchABI(p, signature)
	if err != nil {
		panic(fmt.Errorf("coroutine managed dispatch await: %w", err))
	}
	resultLayout := p.prog.Type(abi.resultSlotType, llssa.InC)
	dispatchOptions := llssa.CoroDispatchCallOptions{
		Version:           coroPlainDispatchVersion,
		ABIHash:           abi.hash,
		Result:            resultLayout,
		TrustedDescriptor: options.trustedDescriptor,
		DeliveryGuard:     options.delivery != nil,
	}
	// Descriptor validation would otherwise introduce a hidden
	// runtime.AssertNilDeref call after the whole-program helper closure was
	// frozen. A physical coroutine owns nil-call semantics directly: route nil
	// through its explicit-status fault edge once, then let every descriptor
	// operation reuse the proven non-nil word.
	descriptorWord := b.Field(fn, 0)
	// The descriptor value is deliberately checked here, after a defer record
	// has been popped, rather than when the defer statement registers it. This
	// preserves Go's rule that invoking a nil deferred function panics while
	// running the deferred call. A cleanup-internal nil replaces the current
	// panic overlay without replacing its normal/RunDefers/cancellation base.
	if options.descriptorNonNil {
		// Interface method lookup has already proved both the itab and its
		// compiler-owned method descriptor non-nil.
	} else if cleanup == nil {
		p.compileCoroImplicitNilAccessGuard(b, descriptorWord)
	} else {
		fault := p.fn.MakeBlock()
		nonNil := p.fn.MakeBlock()
		b.If(b.BinOp(token.EQL, descriptorWord, p.prog.Nil(descriptorWord.Type)), fault, nonNil)
		b.SetBlockEx(fault, llssa.AtEnd, false)
		cleanup.replaceFault(p, b, coroFaultNilV1)
		b.SetBlockContinuation(nonNil)
	}
	dispatchOptions.DescriptorNonNil = true
	if options.entry == coro.ManagedEntryPlain {
		callPlain := func() llssa.Expr { return b.CallCoroDispatchPlain(fn, args, dispatchOptions) }
		var value llssa.Expr
		if options.transparentRecoverAlias {
			value = p.callCoroTransparentRecoverAlias(b, b.CoroDispatchCodeEntry(fn, dispatchOptions), callPlain)
		} else {
			value = callPlain()
		}
		return coroAwaitedValue{value: value, physicalCalls: []coroPhysicalCall{{call: value, argCount: len(args) + 1, resultArg: -1}}}
	}
	resultSlot := p.prog.Nil(p.prog.VoidPtr())
	retainResult := !options.cleanup.discardResult || options.delivery != nil
	deferredAllocation := options.delivery != nil && options.delivery.plan.dynamic.allocate != ""
	results := abi.signature.Results()
	if options.cleanup.discardResult {
		results = types.NewTuple()
	}
	if retainResult && !deferredAllocation && abi.signature.Results().Len() != 0 {
		resultType := p.prog.Type(abi.resultSlotType, llssa.InGo)
		if p.hasCoroPhysicalBody() {
			if options.delivery != nil {
				resultSlot = p.coroDeliveryResultSlot(resultType)
			} else {
				resultSlot = p.coroResultSlot(resultType)
			}
		} else {
			if options.entry != coro.ManagedEntryOutcomePlain || !options.nativeResult {
				panic("synchronous descriptor result escaped its native-stack proof")
			}
			resultSlot = p.structuredOutcomeAlloca(resultType, false)
		}
	}
	if options.entry == coro.ManagedEntryOutcomePlain {
		if p.hasOutcomePlainPhysicalBody() && (!options.nativeResult || options.transparentRecoverAlias) {
			panic("outcome-only descriptor call escaped its native-stack or recover proof")
		}
		selection := b.PrepareCoroDispatchStructuredOnly(fn, dispatchOptions, options.transparentRecoverAlias)
		completion := p.structuredOutcomeScratch()
		callOutcome := func() llssa.Expr {
			return b.CallPreparedCoroDispatchOutcomeOnly(selection, p.managedPhysicalTask(),
				b.Convert(p.prog.VoidPtr(), resultSlot), b.Convert(p.prog.VoidPtr(), completion), args)
		}
		var value llssa.Expr
		if options.transparentRecoverAlias {
			value = p.callCoroTransparentRecoverAlias(b, selection.CodeEntry(), callOutcome)
		} else {
			value = callOutcome()
		}
		p.dispatchOutcomePlainCompletionWithRecovery(b, completion, cleanup)
		return coroAwaitedValue{
			value:         p.loadCoroAwaitResult(b, resultSlot, results),
			address:       p.coroAwaitResultAddress(b, resultSlot, results),
			physicalCalls: []coroPhysicalCall{{call: value, argCount: len(args) + 4, resultArg: 1}},
		}
	}
	body := p.coroBody()
	if body == nil {
		panic("managed dispatch await requires an active physical coroutine body")
	}
	awaitChild := func(child llssa.Expr, endShield func(llssa.Builder)) {
		var afterConsume func(llssa.Builder)
		if options.cleanup.recoverParent {
			token := descriptorWord
			if options.recoverAliasChild {
				token = child
			}
			afterConsume = p.beginCoroRecoverAliasScope(
				b, llssa.Nil, token, b.Load(cleanup.panicActive),
			)
		} else if options.transparentRecoverAlias {
			afterConsume = p.beginCoroTransparentRecoverAliasScope(b, child)
		}
		afterConsume = chainCoroReconciliation(afterConsume, endShield)
		p.awaitCoroChildCompletionWithRecovery(
			b, child, options.cleanup,
			options.keepaliveSlots, afterConsume,
		)
	}
	if options.entry == coro.ManagedEntryCoroutine {
		if !dispatchOptions.TrustedDescriptor {
			panic("coroutine-only descriptor await requires a trusted publication proof")
		}
		selection := b.PrepareCoroDispatchStructuredOnly(fn, dispatchOptions, false)
		endShield := p.beginCoroCleanupShield(b, options.cleanup)
		child := b.CallPreparedCoroDispatchCoroOnly(
			selection,
			body.task,
			b.Convert(p.prog.VoidPtr(), resultSlot),
			args,
		)
		awaitChild(child, endShield)
		return coroAwaitedValue{
			value:   p.loadCoroAwaitResult(b, resultSlot, results),
			address: p.coroAwaitResultAddress(b, resultSlot, results),
			physicalCalls: []coroPhysicalCall{{
				call: child, argCount: len(args) + 3, resultArg: 1,
			}},
		}
	}

	coroutineBlock := p.fn.MakeBlock()
	nonCoroutineBlock := p.fn.MakeBlock()
	outcomeBlock := p.fn.MakeBlock()
	plainBlock := p.fn.MakeBlock()
	join := p.fn.MakeBlock()
	selection := b.PrepareCoroDispatchCall(fn, dispatchOptions)
	if guard := options.delivery; guard != nil {
		guard.result = resultSlot
		p.beginCoroDynamicDeliveryCall(b, guard, fn, args, selection, options.cleanup)
		if deferredAllocation {
			resultSlot = b.LoadKnownNonNil(guard.resultAddress)
		}
	}
	hasOutcome, hasCoro, codeEntry := selection.HasOutcome(), selection.HasCoro(), selection.CodeEntry()
	b.If(hasCoro, coroutineBlock, nonCoroutineBlock)

	b.SetBlockEx(nonCoroutineBlock, llssa.AtEnd, false)
	b.If(hasOutcome, outcomeBlock, plainBlock)

	b.SetBlockEx(coroutineBlock, llssa.AtEnd, false)
	endShield := p.beginCoroCleanupShield(b, options.cleanup)
	child := b.CallPreparedCoroDispatchCoro(
		selection,
		body.task,
		b.Convert(p.prog.VoidPtr(), resultSlot),
		args,
	)
	awaitChild(child, endShield)
	b.Jump(join)

	b.SetBlockEx(outcomeBlock, llssa.AtEnd, false)
	completion := p.structuredOutcomeScratch()
	callOutcome := func() llssa.Expr {
		return b.CallPreparedCoroDispatchOutcome(
			selection,
			body.task,
			b.Convert(p.prog.VoidPtr(), resultSlot),
			b.Convert(p.prog.VoidPtr(), completion),
			args,
		)
	}
	var outcomeCall llssa.Expr
	if options.transparentRecoverAlias {
		outcomeCall = p.callCoroTransparentRecoverAlias(b, codeEntry, callOutcome)
	} else {
		outcomeCall = callOutcome()
	}
	p.dispatchOutcomePlainCompletionWithRecovery(b, completion, cleanup)
	b.Jump(join)

	b.SetBlockEx(plainBlock, llssa.AtEnd, false)
	var plainResult llssa.Expr
	if options.transparentRecoverAlias {
		plainResult = p.callCoroTransparentRecoverAlias(b, codeEntry, func() llssa.Expr {
			return b.CallPreparedCoroDispatchPlain(selection, args)
		})
	} else {
		plainResult = b.CallPreparedCoroDispatchPlain(selection, args)
	}
	if retainResult {
		p.storeCoroDynamicDispatchResult(b, resultSlot, plainResult, abi.signature.Results())
	}
	b.Jump(join)

	b.SetBlockContinuation(join)
	value := p.loadCoroAwaitResult(b, resultSlot, results)
	if guard := options.delivery; guard != nil {
		body.cleanup.delivery.claim(b, guard)
	}
	return coroAwaitedValue{
		value:   value,
		address: p.coroAwaitResultAddress(b, resultSlot, results),
		physicalCalls: []coroPhysicalCall{
			{call: child, argCount: len(args) + 3, resultArg: 1},
			{call: outcomeCall, argCount: len(args) + 4, resultArg: 1},
			{call: plainResult, argCount: len(args) + 1, resultArg: -1},
		},
	}
}

func validateCoroManagedDispatchAwaitShape(
	plan *coro.SSAPlan, owner *ssa.Function, call *ssa.Call, callPlan coro.SSACallPlan,
) error {
	fail := func(format string, args ...any) error {
		name := "<unknown>"
		if owner != nil {
			name = owner.Name()
		}
		return fmt.Errorf("coroutine managed dispatch await: function %q: %s", name, fmt.Sprintf(format, args...))
	}
	if plan == nil || owner == nil || call == nil || call.Common() == nil || call.Parent() != owner {
		return fail("requires one exact ordinary call in the compilation plan")
	}
	ownerPlan, ok := plan.FunctionPlan(owner)
	coroutineOwner := ok && ownerPlan.Emission == coro.EmitCoroutine &&
		ownerPlan.Primary == coro.PrimaryCoroutine
	outcomeOwner := ok && ownerPlan.Emission == coro.EmitOutcomePlain &&
		ownerPlan.ManagedEntry == coro.ManagedEntryOutcomePlain &&
		ownerPlan.Primary == coro.PrimaryCoroutine && ownerPlan.HasStaticOutcome() &&
		coroManagedEntryIsSynchronous(coroManagedDispatchPublishedEntry(plan, callPlan))
	if !coroutineOwner && !outcomeOwner {
		return fail("owner is neither a coroutine primary nor a synchronous descriptor parent")
	}
	common := call.Common()
	if callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.Dispatch || callPlan.Transport != coro.ManagedTransport ||
		callPlan.SyncDispatch || callPlan.Open && callPlan.Unresolved != coro.UnknownManagedDispatch || common.StaticCallee() != nil ||
		common.IsInvoke() || common.Method != nil {
		return fail(
			"requires an ordinary managed descriptor call (and UnknownManagedDispatch when open), got kind=%v representation=%s open=%t unresolved=%v",
			callPlan.Kind, callPlan.Rep, callPlan.Open, callPlan.Unresolved,
		)
	}
	if _, err := coroConcreteManagedCallableSignature(common.Signature()); err != nil {
		return fail("call signature is not one concrete receiver-free callable: %v", err)
	}
	valuePlan, ok := plan.ValuePlan(common.Value)
	if !ok || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 ||
		valuePlan.Funcs[0].Rep != coro.Dispatch || valuePlan.Funcs[0].Transport != coro.ManagedTransport {
		return fail("callee has no exact scalar Dispatch ValuePlan")
	}
	return nil
}

func (p *context) storeCoroDynamicDispatchResult(
	b llssa.Builder, resultSlot, result llssa.Expr, results *types.Tuple,
) {
	count := 0
	if results != nil {
		count = results.Len()
	}
	switch count {
	case 0:
		return
	case 1:
		b.Store(b.FieldAddr(resultSlot, 0), result)
	default:
		for index := 0; index < count; index++ {
			b.Store(b.FieldAddr(resultSlot, index), b.Extract(result, index))
		}
	}
}

func typeParamCount(list *types.TypeParamList) int {
	if list == nil {
		return 0
	}
	return list.Len()
}
