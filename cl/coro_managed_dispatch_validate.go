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
	"go/types"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// validateCoroManagedDispatchCall proves the source and plan half of the v1
// universal {descriptor, environment} call contract. Capability ownership is
// intentionally checked by each physical consumer: this helper cannot turn a
// disabled frontend feature into an accepted lowering.
//
// UnknownManaged and UnknownForeign remain distinct fail-closed domains. Only
// UnknownManagedDispatch certifies that an open operand already has the
// universal descriptor representation. A closed Dispatch call needs no
// unknown-domain certificate: its exact descriptor targets were frozen by
// value flow, but it uses the same physical capability dispatch (notably when
// a callback parameter can carry both plain and coroutine producers).
func validateCoroManagedDispatchCall(
	plan *coro.SSAPlan,
	owner *ssa.Function,
	call ssa.CallInstruction,
	callPlan coro.SSACallPlan,
	universes ...*EmissionUniverse,
) error {
	return validateCoroManagedDispatchCallKind(plan, owner, call, callPlan, coro.CallDirect, universes...)
}

func validateCoroManagedDispatchDefer(
	plan *coro.SSAPlan,
	owner *ssa.Function,
	call *ssa.Defer,
	callPlan coro.SSACallPlan,
	universes ...*EmissionUniverse,
) error {
	return validateCoroManagedDispatchCallKind(plan, owner, call, callPlan, coro.CallDefer, universes...)
}

func validateCoroManagedDispatchCallKind(
	plan *coro.SSAPlan,
	owner *ssa.Function,
	call ssa.CallInstruction,
	callPlan coro.SSACallPlan,
	expectedKind coro.CallKind,
	universes ...*EmissionUniverse,
) error {
	var universe *EmissionUniverse
	if len(universes) != 0 {
		universe = universes[0]
	}
	fail := func(format string, args ...any) error {
		return coroPlainDispatchInstructionError(owner, call, fmt.Sprintf(format, args...))
	}
	if plan == nil {
		return fail("managed descriptor dispatch requires a compilation plan")
	}
	if call == nil {
		return fail("managed descriptor dispatch requires one exact call instruction")
	}
	switch expectedKind {
	case coro.CallDirect:
		if direct, ordinary := call.(*ssa.Call); !ordinary || direct == nil {
			return fail("managed descriptor dispatch is supported only for an ordinary direct call instruction")
		}
		if callPlan.Kind != coro.CallDirect {
			return fail("managed descriptor dispatch requires an ordinary direct call instruction with a matching CallDirect plan")
		}
	case coro.CallDefer:
		if deferred, ordinary := call.(*ssa.Defer); !ordinary || deferred == nil || deferred.DeferStack != nil {
			return fail("managed descriptor cleanup requires one owner-local defer instruction")
		}
		if callPlan.Kind != coro.CallDefer {
			return fail("managed descriptor cleanup requires one owner-local defer instruction with a matching CallDefer plan")
		}
	default:
		return fail("managed descriptor dispatch has unsupported call kind %v", expectedKind)
	}
	common := call.Common()
	if common == nil || common.StaticCallee() != nil || common.IsInvoke() || common.Method != nil {
		return fail("managed descriptor dispatch requires an ordinary dynamic function call")
	}
	if _, builtin := common.Value.(*ssa.Builtin); builtin {
		return fail("managed descriptor dispatch cannot target a builtin")
	}
	if callPlan.Rep != coro.Dispatch || callPlan.Transport != coro.ManagedTransport {
		return fail("requires a managed Dispatch CallPlan, got transport=%s representation=%s", callPlan.Transport, callPlan.Rep)
	}
	if callPlan.SyncDispatch && expectedKind != coro.CallDefer {
		return fail("synchronous descriptor CallPlan must use plain dispatch lowering")
	}
	if callPlan.Open && callPlan.Unresolved != coro.UnknownManagedDispatch {
		return fail(
			"open Dispatch CallPlan is not certified as UnknownManagedDispatch (unresolved=%v)",
			callPlan.Unresolved,
		)
	}
	sig := common.Signature()
	if sig == nil || sig.Recv() != nil || sig.Variadic() {
		return fail("v1 descriptor requires an ordinary non-variadic function signature")
	}
	if params := sig.TypeParams(); params != nil && params.Len() != 0 {
		return fail("v1 descriptor does not support generic signatures")
	}
	if params := sig.RecvTypeParams(); params != nil && params.Len() != 0 {
		return fail("v1 descriptor does not support generic receiver signatures")
	}
	if err := validateCoroManagedDispatchSignatureShape(sig); err != nil {
		return fail("v1 descriptor signature: %v", err)
	}

	valuePlan, found := plan.ValuePlan(common.Value)
	if !found || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 ||
		valuePlan.Funcs[0].Rep != coro.Dispatch || valuePlan.Funcs[0].Transport != coro.ManagedTransport {
		return fail("callee has no exact scalar Dispatch ValuePlan")
	}
	leaf := valuePlan.Funcs[0]
	// ValuePlan contains the targets established by structural value flow. A
	// field load or parameter can have an empty/strict-subset list while the
	// exact call occurrence is closed by whole-program dynamic CHA. CallPlan is
	// therefore authoritative for execution; every producer-known target must
	// be present, but additional call-site candidates are valid descriptors.
	if missing, ok := coroDispatchTargetsSubset(leaf.Targets, callPlan.Targets); !ok {
		return fail("callee ValuePlan target %q is absent from CallPlan", missing)
	}
	if leaf.MayBeNil != callPlan.MayBeNil {
		return fail("callee nilability %t conflicts with CallPlan nilability %t", leaf.MayBeNil, callPlan.MayBeNil)
	}

	for _, targetID := range callPlan.Targets {
		target, found := plan.Function(targetID)
		if !found || target == nil {
			return fail("target %q is absent from the compilation plan", targetID)
		}
		targetPlan, found := plan.FunctionPlan(target)
		if !found || targetPlan.ID != targetID {
			return fail("target %q has no canonical function plan", targetID)
		}
		if err := validateCoroDynamicDispatchTarget(target, targetPlan, universe); err != nil {
			return fail("target %q: %v", targetID, err)
		}
		if target.Signature == nil || !types.Identical(sig, target.Signature) {
			return fail("call signature %s does not match target %q signature %s", sig, targetID, target.Signature)
		}
	}
	return nil
}

func coroDispatchTargetsSubset(values, calls []coro.FunctionID) (coro.FunctionID, bool) {
	callSet := make(map[coro.FunctionID]struct{}, len(calls))
	for _, target := range calls {
		callSet[target] = struct{}{}
	}
	for _, target := range values {
		if _, ok := callSet[target]; !ok {
			return target, false
		}
	}
	return "", true
}
