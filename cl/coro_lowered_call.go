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
	if p.compilation == nil {
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
	// Every production compiler-inserted runtime call reaches this resolver
	// through Package.RuntimeFunc's private expression marker, including a
	// declaration which was already materialized in the package. Keep the
	// exact SitePlan observation at this one shared boundary so individual
	// instrumentation and lowering helpers cannot become new authorities.
	p.observeCoroSiteRuntimeHelper(helper)

	frozenCall, ok, err := p.emissionUniverse.ResolveCoroLoweredCallRecord(p.goFn, helper)
	if err != nil {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q: %w", helper, p.goFn.Name(), err))
	}
	target := frozenCall.Target
	rawPlainOccurrence := ok && frozenCall.RawPlain
	plainOnly := false
	if !ok && p.coroBody() == nil {
		target, ok, err = p.emissionUniverse.ResolveCoroPlainLoweredCall(p.goFn, helper)
		if err != nil {
			panic(fmt.Errorf("coroutine plain lowered runtime call %q in %q: %w", helper, p.goFn.Name(), err))
		}
		plainOnly = ok
	}
	if !ok || target == nil {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q is absent from the frozen emission universe", helper, p.goFn.Name()))
	}
	if !plainOnly {
		plannedCall, planned := p.compilation.CoroPlan.ResolveLoweredCallRecord(p.goFn, helper)
		if !planned || plannedCall.Target != target || plannedCall.RawPlain != rawPlainOccurrence ||
			plannedCall.NoUnwind != frozenCall.NoUnwind ||
			plannedCall.UnwindOnly != frozenCall.UnwindOnly ||
			plannedCall.ExplicitStatusElided != frozenCall.ExplicitStatusElided {
			panic(fmt.Errorf("coroutine lowered runtime call %q in %q disagrees between the frozen emission universe and SSA plan", helper, p.goFn.Name()))
		}
	}
	explicitStatusLegacyPlain, err := coroExplicitStatusElidedUsesRawPlainEntry(
		p.hasCoroPhysicalBody(), p.rawPlainBody, frozenCall.ExplicitStatusElided,
	)
	if err != nil {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q: %w", helper, p.goFn.Name(), err))
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
	if !ok {
		panic(fmt.Errorf(
			"coroutine lowered runtime call %q in %q target %q marker has non-signature type %T (%v)",
			helper, p.goFn.Name(), targetPlan.ID, types.Unalias(marker.RawType()), marker.RawType(),
		))
	}
	// The compiler-created rtFunc marker already carries LLGo's physical Go ABI:
	// function values are {fn,env}, interfaces use their runtime headers, and
	// other source types have crossed the same target-specific conversion.
	// Convert the frozen logical source signature through that one canonical
	// boundary before comparing. x/tools SSA has also packed a variadic
	// invocation into the final slice argument, so normalize both sides.
	markerSig = coroPhysicalNormalizeSourceSignature(markerSig)
	physicalSourceSig := coroPhysicalNormalizeSourceSignature(
		p.prog.PhysicalFuncDecl(sourceSig, llssa.InGo),
	)
	if !types.Identical(markerSig, physicalSourceSig) {
		panic(fmt.Errorf(
			"coroutine lowered runtime call %q in %q target %q has a different effective source signature: marker=%s target=%s",
			helper, p.goFn.Name(), targetPlan.ID,
			types.TypeString(markerSig, types.RelativeTo(nil)),
			types.TypeString(physicalSourceSig, types.RelativeTo(nil)),
		))
	}
	if plainOnly || rawPlainOccurrence || explicitStatusLegacyPlain {
		if !targetPlan.RawPlainDemand || !p.compilation.CoroPlan.HasRawPlainVariant(target) {
			panic(fmt.Errorf("coroutine raw/plain lowered runtime call %q in %q targets %q without an exact raw-plain variant", helper, p.goFn.Name(), targetPlan.ID))
		}
		fn, _, kind := p.compileRawPlainFunction(target)
		if fn == nil || kind != goFunc && kind != cFunc {
			panic(fmt.Errorf("coroutine raw/plain lowered runtime call %q in %q target %q did not resolve to a raw-callable Go/C entry", helper, p.goFn.Name(), targetPlan.ID))
		}
		return b.Call(fn.Expr, args...), true
	}
	if p.rawPlainBody {
		if targetPlan.Emission == coro.EmitCoroutine && !p.compilation.CoroPlan.HasRawPlainVariant(target) {
			panic(fmt.Errorf("coroutine lowered runtime call %q in raw plain body %q targets managed coroutine %q without a raw plain variant", helper, p.goFn.Name(), targetPlan.ID))
		}
		fn, _, kind := p.compileRawPlainFunction(target)
		if fn == nil || kind != goFunc && kind != cFunc {
			panic(fmt.Errorf("coroutine lowered runtime call %q in raw plain body %q target %q did not resolve to a raw-callable Go/C entry", helper, p.goFn.Name(), targetPlan.ID))
		}
		return b.Call(fn.Expr, args...), true
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
		if site := p.coroEmissionSite(); site != nil && site.placement == coroRuntimeHelperAtCleanup {
			return p.compileCoroCleanupTargetAwait(b, target, args), true
		}
		return p.compileCoroTargetAwait(b, target, args), true
	case coro.EmitRawPlain:
		panic(fmt.Errorf(
			"coroutine lowered runtime call %q in managed body %q targets raw-plain-only function %q without a managed entry",
			helper, p.goFn.Name(), targetPlan.ID,
		))
	case coro.EmitNone:
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q targets non-emitted function %q", helper, p.goFn.Name(), targetPlan.ID))
	case coro.EmitExternal:
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q requires an unimplemented external helper adapter for %q", helper, p.goFn.Name(), targetPlan.ID))
	default:
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q targets function %q with invalid emission %d", helper, p.goFn.Name(), targetPlan.ID, uint8(targetPlan.Emission)))
	}
}

// coroExplicitStatusElidedUsesRawPlainEntry projects one immutable source-panic
// fact into the body currently being emitted. The graph deliberately retains
// only raw demand for the legacy runtime.Panic call: a physical coroutine
// publishes its outcome and emits no call, while a managed plain primary (or a
// raw variant) still executes the legacy native-stack helper. Keeping this
// decision beside entry resolution prevents a plain primary from accidentally
// requesting a managed helper entry and widening the whole-program coloring.
func coroExplicitStatusElidedUsesRawPlainEntry(
	physicalCoroutineBody bool,
	rawPlainBody bool,
	explicitStatusElided bool,
) (bool, error) {
	if !explicitStatusElided {
		return false, nil
	}
	if physicalCoroutineBody {
		if rawPlainBody {
			return false, fmt.Errorf("ExplicitStatus-elided helper observed incompatible physical-coroutine and raw-plain body domains")
		}
		return false, fmt.Errorf("ExplicitStatus-elided helper escaped into its physical coroutine body")
	}
	if rawPlainBody {
		return true, nil
	}
	// There is no third executable physical domain: an emitted body which is
	// neither the stackless coroutine body nor its separately frozen raw
	// variant is the ordinary managed/plain primary.
	return true, nil
}
