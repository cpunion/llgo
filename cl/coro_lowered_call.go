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
	llssa "github.com/goplus/llgo/ssa"
)

// resolveCoroLoweredRuntimeCall replaces one rtFunc call with the exact
// physical entry frozen for the current SSA owner. Missing or divergent input
// is a compiler-plan error: falling back to the legacy symbol would recreate a
// hidden call edge after the whole-program fixed point was sealed.
func (p *context) resolveCoroLoweredRuntimeCall(b llssa.Builder, helper string, marker llssa.Expr, args []llssa.Expr) (llssa.Expr, bool) {
	if p.compilation == nil || !p.compilation.EnableCoroEntryResolution {
		return llssa.Nil, false
	}
	if p.emissionUniverse == nil || !p.emissionUniverse.CompleteRuntimeABI() {
		// Isolated package/report tests do not carry the production runtime ABI
		// and must keep the legacy rtFunc marker. internal/build always prepares
		// a complete universe for active entry resolution, where every missing
		// owner-scoped mapping remains a hard compiler-plan error below.
		return llssa.Nil, false
	}
	if p.goFn == nil || p.emissionUniverse == nil || p.compilation.CoroPlan == nil {
		panic("coroutine lowered runtime call requires an exact owner, emission universe, and SSA plan")
	}
	if b.Func != p.fn {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q escaped into another LLVM function", helper, p.goFn.Name()))
	}

	target, ok, err := p.emissionUniverse.ResolveCoroLoweredCall(p.goFn, helper)
	if err != nil {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q: %w", helper, p.goFn.Name(), err))
	}
	if !ok || target == nil {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q is absent from the frozen emission universe", helper, p.goFn.Name()))
	}
	plannedTarget, planned := p.compilation.CoroPlan.ResolveLoweredCall(p.goFn, helper)
	if !planned || plannedTarget != target {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q disagrees between the frozen emission universe and SSA plan", helper, p.goFn.Name()))
	}
	targetPlan, planned := p.compilation.CoroPlan.FunctionPlan(target)
	if !planned {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q targets an unplanned function", helper, p.goFn.Name()))
	}
	sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(target)
	if err != nil {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q: derive target %q signature: %w", helper, p.goFn.Name(), targetPlan.ID, err))
	}
	markerSig, ok := types.Unalias(marker.RawType()).(*types.Signature)
	if !ok || !types.Identical(markerSig, sourceSig) {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q target %q has a different effective source signature", helper, p.goFn.Name(), targetPlan.ID))
	}

	switch targetPlan.Emission {
	case coro.EmitPlain:
		if targetPlan.External != coro.Defined || targetPlan.Demand == coro.NoDemand || targetPlan.Effect.MaySuspend() || targetPlan.FuncRep == coro.DirectCoro {
			panic(fmt.Errorf("coroutine lowered runtime call %q in %q cannot call suspending target %q through a plain entry", helper, p.goFn.Name(), targetPlan.ID))
		}
		fn, _, kind := p.compileFunction(target)
		if fn == nil || kind != goFunc {
			panic(fmt.Errorf("coroutine lowered runtime call %q in %q target %q did not resolve to a Go entry", helper, p.goFn.Name(), targetPlan.ID))
		}
		return b.Call(fn.Expr, args...), true
	case coro.EmitCoroutine:
		if targetPlan.Exec&coro.MayUnwind != 0 {
			panic(fmt.Errorf("coroutine lowered runtime call %q in %q target %q may unwind, but child-frame panic propagation is not implemented", helper, p.goFn.Name(), targetPlan.ID))
		}
		return p.compileCoroTargetAwait(b, target, args), true
	case coro.EmitNone:
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q targets non-emitted function %q", helper, p.goFn.Name(), targetPlan.ID))
	case coro.EmitExternal:
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q requires an unimplemented external helper adapter for %q", helper, p.goFn.Name(), targetPlan.ID))
	default:
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q targets function %q with invalid emission %d", helper, p.goFn.Name(), targetPlan.ID, uint8(targetPlan.Emission)))
	}
}
