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

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// tryCompileCoroManagedDispatchAwait lowers an open Go function-value call
// carried by the universal {descriptor, environment} representation. The
// descriptor publishes exactly the capability of its one primary body:
// bounded plain targets execute inline, while coroutine targets enter the
// same scheduler-owned child transaction as an exact static await.
func (p *context) tryCompileCoroManagedDispatchAwait(b llssa.Builder, call *ssa.Call) (llssa.Expr, bool) {
	if p.currentCoro == nil || p.compilation == nil || p.compilation.CoroPlan == nil ||
		!p.compilation.EnableCoroChildAwait || !p.compilation.EnableCoroPlainDispatch || call == nil {
		return llssa.Nil, false
	}
	callPlan, found := p.compilation.CoroPlan.CallPlan(call)
	if !found || callPlan.Rep != coro.Dispatch || callPlan.Transport != coro.ManagedTransport || callPlan.SyncDispatch || call.Common() == nil ||
		call.Common().StaticCallee() != nil || call.Common().IsInvoke() {
		return llssa.Nil, false
	}
	if err := validateCoroManagedDispatchAwaitShape(p.compilation.CoroPlan, p.goFn, call, callPlan); err != nil {
		panic(err)
	}

	p.recordCallerLocationForCall(b, &call.Call)
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
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
	return p.compileCoroManagedDispatchAwaitValue(b, fn, args, call.Call.Signature(), keepaliveSlots), true
}

// compileCoroManagedDispatchAwaitValue is the one capability probe and child
// transaction shared by ordinary function descriptors and interface-method
// descriptors. The caller owns source evaluation order and exact transport
// validation before entering this helper.
func (p *context) compileCoroManagedDispatchAwaitValue(
	b llssa.Builder, fn llssa.Expr, args []llssa.Expr, signature *types.Signature, keepaliveSlots []llssa.Expr,
) llssa.Expr {
	return p.compileCoroManagedDispatchAwaitValueWithRecovery(b, fn, args, signature, nil, keepaliveSlots)
}

// compileCoroManagedDispatchAwaitValueWithRecovery is the cleanup-aware core
// of descriptor dispatch. A deferred coroutine target must be a direct child
// of the owner whose drainer supplied cleanup; introducing a wrapper child
// would break Go's direct-recover rule. Ordinary descriptor calls pass nil and
// retain their existing child-outcome behavior.
func (p *context) compileCoroManagedDispatchAwaitValueWithRecovery(
	b llssa.Builder, fn llssa.Expr, args []llssa.Expr, signature *types.Signature,
	cleanup *coroStaticCleanupState, keepaliveSlots []llssa.Expr,
) llssa.Expr {
	abi, err := newCoroPlainDispatchABI(p, signature)
	if err != nil {
		panic(fmt.Errorf("coroutine managed dispatch await: %w", err))
	}
	resultLayout := p.prog.Type(abi.resultSlotType, llssa.InC)
	resultSlot := p.coroFrameAlloca(p.prog.Type(abi.resultSlotType, llssa.InGo))
	opts := llssa.CoroDispatchCallOptions{
		Version: coroPlainDispatchVersion,
		ABIHash: abi.hash,
		Result:  resultLayout,
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
	if cleanup == nil {
		p.compileCoroImplicitNilAccessGuard(b, descriptorWord)
	} else {
		fault := p.fn.MakeBlock()
		nonNil := p.fn.MakeBlock()
		b.If(b.BinOp(token.EQL, descriptorWord, p.prog.Nil(descriptorWord.Type)), fault, nonNil)
		b.SetBlockEx(fault, llssa.AtEnd, false)
		cleanup.replaceFault(p, b, coroFaultNilV1)
		b.SetBlockContinuation(nonNil)
	}
	opts.DescriptorNonNil = true

	coroutineBlock := p.fn.MakeBlock()
	plainBlock := p.fn.MakeBlock()
	join := p.fn.MakeBlock()
	b.If(b.CoroDispatchHasCoro(fn, opts), coroutineBlock, plainBlock)

	b.SetBlockEx(coroutineBlock, llssa.AtEnd, false)
	child := b.CallCoroDispatchCoro(
		fn,
		p.currentCoro.task,
		b.Convert(p.prog.VoidPtr(), resultSlot),
		args,
		opts,
	)
	p.awaitCoroChildWithRecovery(b, child, resultSlot, abi.signature.Results(), cleanup, keepaliveSlots)
	b.Jump(join)

	b.SetBlockEx(plainBlock, llssa.AtEnd, false)
	plainResult := b.CallCoroDispatchPlain(fn, args, opts)
	p.storeCoroDynamicDispatchResult(b, resultSlot, plainResult, abi.signature.Results())
	b.Jump(join)

	b.SetBlockContinuation(join)
	return p.loadCoroAwaitResult(b, resultSlot, abi.signature.Results())
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
	if !ok || ownerPlan.Emission != coro.EmitCoroutine || ownerPlan.Primary != coro.PrimaryCoroutine {
		return fail("owner is not one coroutine primary")
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
	sig := common.Signature()
	if sig == nil || sig.Recv() != nil || sig.Variadic() ||
		typeParamCount(sig.TypeParams()) != 0 || typeParamCount(sig.RecvTypeParams()) != 0 {
		return fail("call signature must be receiver-free, non-variadic, and non-generic")
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
