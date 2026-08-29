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

// coroManagedDispatchPublishedStructuredEntry returns the one structured ABI
// actually stored in every descriptor reachable at a closed managed call.
// HasStaticOutcome alone is insufficient: a coroutine primary may also emit an
// exact-call outcome twin while its shared function/interface descriptor still
// publishes the coroutine ABI. Mixed target families retain universal dispatch.
func coroManagedDispatchPublishedStructuredEntry(
	plan *coro.SSAPlan,
	callPlan coro.SSACallPlan,
) coro.ManagedEntryKind {
	if plan == nil || (callPlan.Kind != coro.CallDirect && callPlan.Kind != coro.CallDefer) ||
		callPlan.Rep != coro.Dispatch ||
		callPlan.Transport != coro.ManagedTransport || callPlan.Open ||
		callPlan.SyncDispatch || callPlan.RawPlain || len(callPlan.Targets) == 0 {
		return coro.ManagedEntryNone
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
	return coroManagedDispatchPublishedStructuredEntry(plan, callPlan) == coro.ManagedEntryOutcomePlain
}

func coroManagedDispatchPublishedCoroutineOnly(plan *coro.SSAPlan, callPlan coro.SSACallPlan) bool {
	return coroManagedDispatchPublishedStructuredEntry(plan, callPlan) == coro.ManagedEntryCoroutine
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
		p.hasOutcomePlainPhysicalBody() && !instructionPlan.structuredOutcomeOnly {
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
	var result coroAwaitedValue
	if instructionPlan.structuredOutcomeOnly {
		result = p.compileCoroManagedDispatchOutcomeOnlyValueResult(
			b, fn, args, call.Call.Signature(), instructionPlan.recoverAlias,
			false, instructionPlan.directOutcomeNativeResult,
		)
	} else {
		keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
		result = p.compileCoroManagedDispatchAwaitValueResultWithRecovery(
			b, fn, args, call.Call.Signature(), coroManagedDispatchAwaitOptions{
				keepaliveSlots:          keepaliveSlots,
				transparentRecoverAlias: instructionPlan.recoverAlias,
				trustedDescriptor:       instructionPlan.structuredCoroutineOnly,
				structuredCoroutineOnly: instructionPlan.structuredCoroutineOnly,
			},
		)
	}
	p.recordCoroValueAddress(call, result.address)
	return result.value
}

// compileCoroManagedDispatchOutcomeOnlyValueResult lowers one closed
// descriptor occurrence whose complete target set publishes only synchronous
// outcome entries. Unlike the universal three-way dispatcher it emits no
// impossible plain/coroutine branches and allocates no keepalive/suspend state.
func (p *context) compileCoroManagedDispatchOutcomeOnlyValueResult(
	b llssa.Builder,
	fn llssa.Expr,
	args []llssa.Expr,
	signature *types.Signature,
	transparentRecoverAlias bool,
	descriptorNonNil bool,
	nativeResult bool,
) coroAwaitedValue {
	if !p.hasStructuredOutcomePhysicalBody() || b == nil || b.Func != p.fn {
		panic("outcome-only descriptor call requires one structured physical parent")
	}
	if p.hasOutcomePlainPhysicalBody() && (!nativeResult || transparentRecoverAlias) {
		panic("outcome-only descriptor call escaped its native-stack or recover proof")
	}
	abi, err := newCoroPlainDispatchABI(p, signature)
	if err != nil {
		panic(fmt.Errorf("outcome-only managed dispatch: %w", err))
	}
	resultSlot := p.prog.Nil(p.prog.VoidPtr())
	if abi.signature.Results().Len() != 0 {
		resultType := p.prog.Type(abi.resultSlotType, llssa.InGo)
		if p.hasCoroPhysicalBody() {
			resultSlot = p.coroResultSlot(resultType)
		} else {
			resultSlot = p.structuredOutcomeAlloca(resultType, false)
		}
	}
	if !descriptorNonNil {
		descriptorWord := b.Field(fn, 0)
		p.compileCoroImplicitNilAccessGuard(b, descriptorWord)
	}
	selection := b.PrepareCoroDispatchStructuredOnly(fn, llssa.CoroDispatchCallOptions{
		Version:           coroPlainDispatchVersion,
		ABIHash:           abi.hash,
		Result:            p.prog.Type(abi.resultSlotType, llssa.InC),
		DescriptorNonNil:  true,
		TrustedDescriptor: true,
	}, transparentRecoverAlias)
	completion := p.structuredOutcomeScratch()
	callOutcome := func() llssa.Expr {
		return b.CallPreparedCoroDispatchOutcomeOnly(
			selection,
			p.managedPhysicalTask(),
			b.Convert(p.prog.VoidPtr(), resultSlot),
			b.Convert(p.prog.VoidPtr(), completion),
			args,
		)
	}
	var physicalCall llssa.Expr
	if transparentRecoverAlias {
		physicalCall = p.callCoroTransparentRecoverAlias(b, selection.CodeEntry(), callOutcome)
	} else {
		physicalCall = callOutcome()
	}
	p.dispatchOutcomePlainCompletion(b, completion)
	return coroAwaitedValue{
		value:   p.loadCoroAwaitResult(b, resultSlot, abi.signature.Results()),
		address: p.coroAwaitResultAddress(b, resultSlot, abi.signature.Results()),
		physicalCalls: []coroPhysicalCall{{
			call: physicalCall, argCount: len(args) + 4, resultArg: 1,
		}},
	}
}

type coroManagedDispatchAwaitOptions struct {
	cleanup                 *coroStaticCleanupState
	keepaliveSlots          []llssa.Expr
	recoverAliasChild       bool
	transparentRecoverAlias bool
	trustedDescriptor       bool
	descriptorNonNil        bool
	structuredCoroutineOnly bool
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
	body := p.coroBody()
	if body == nil {
		panic("managed dispatch await requires an active physical coroutine body")
	}
	abi, err := newCoroPlainDispatchABI(p, signature)
	if err != nil {
		panic(fmt.Errorf("coroutine managed dispatch await: %w", err))
	}
	resultLayout := p.prog.Type(abi.resultSlotType, llssa.InC)
	resultSlot := p.coroResultSlot(p.prog.Type(abi.resultSlotType, llssa.InGo))
	dispatchOptions := llssa.CoroDispatchCallOptions{
		Version:           coroPlainDispatchVersion,
		ABIHash:           abi.hash,
		Result:            resultLayout,
		TrustedDescriptor: options.trustedDescriptor,
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
	} else if options.cleanup == nil {
		p.compileCoroImplicitNilAccessGuard(b, descriptorWord)
	} else {
		fault := p.fn.MakeBlock()
		nonNil := p.fn.MakeBlock()
		b.If(b.BinOp(token.EQL, descriptorWord, p.prog.Nil(descriptorWord.Type)), fault, nonNil)
		b.SetBlockEx(fault, llssa.AtEnd, false)
		options.cleanup.replaceFault(p, b, coroFaultNilV1)
		b.SetBlockContinuation(nonNil)
	}
	dispatchOptions.DescriptorNonNil = true
	awaitChild := func(child llssa.Expr) {
		var afterConsume func(llssa.Builder)
		if options.cleanup != nil {
			token := descriptorWord
			if options.recoverAliasChild {
				token = child
			}
			afterConsume = p.beginCoroRecoverAliasScope(
				b, llssa.Nil, token, b.Load(options.cleanup.panicActive),
			)
		} else if options.transparentRecoverAlias {
			afterConsume = p.beginCoroTransparentRecoverAliasScope(b, child)
		}
		p.awaitCoroChildWithRecoveryAndConsume(
			b, child, resultSlot, abi.signature.Results(), options.cleanup,
			options.keepaliveSlots, afterConsume,
		)
	}
	if options.structuredCoroutineOnly {
		if !dispatchOptions.TrustedDescriptor {
			panic("coroutine-only descriptor await requires a trusted publication proof")
		}
		selection := b.PrepareCoroDispatchStructuredOnly(fn, dispatchOptions, false)
		child := b.CallPreparedCoroDispatchCoroOnly(
			selection,
			body.task,
			b.Convert(p.prog.VoidPtr(), resultSlot),
			args,
		)
		awaitChild(child)
		return coroAwaitedValue{
			value:   p.loadCoroAwaitResult(b, resultSlot, abi.signature.Results()),
			address: p.coroAwaitResultAddress(b, resultSlot, abi.signature.Results()),
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
	hasOutcome, hasCoro, codeEntry := selection.HasOutcome(), selection.HasCoro(), selection.CodeEntry()
	b.If(hasCoro, coroutineBlock, nonCoroutineBlock)

	b.SetBlockEx(nonCoroutineBlock, llssa.AtEnd, false)
	b.If(hasOutcome, outcomeBlock, plainBlock)

	b.SetBlockEx(coroutineBlock, llssa.AtEnd, false)
	child := b.CallPreparedCoroDispatchCoro(
		selection,
		body.task,
		b.Convert(p.prog.VoidPtr(), resultSlot),
		args,
	)
	awaitChild(child)
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
	p.dispatchOutcomePlainCompletionWithRecovery(b, completion, options.cleanup)
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
	p.storeCoroDynamicDispatchResult(b, resultSlot, plainResult, abi.signature.Results())
	b.Jump(join)

	b.SetBlockContinuation(join)
	return coroAwaitedValue{
		value:   p.loadCoroAwaitResult(b, resultSlot, abi.signature.Results()),
		address: p.coroAwaitResultAddress(b, resultSlot, abi.signature.Results()),
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
		coroManagedDispatchPublishedOutcomeOnly(plan, callPlan)
	if !coroutineOwner && !outcomeOwner {
		return fail("owner is neither a coroutine primary nor an outcome-only descriptor parent")
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
