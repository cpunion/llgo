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

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// resolveCoroLoweredRuntimeCall replaces one rtFunc call with the exact
// physical entry frozen for the current SSA owner. Missing or divergent input
// is a compiler-plan error: falling back to the legacy symbol would recreate a
// hidden call edge after the whole-program fixed point was sealed.
func (p *context) resolveCoroLoweredRuntimeCall(b llssa.Builder, helper string, marker llssa.Expr, args []llssa.Expr) (llssa.Expr, bool) {
	return p.resolveCoroLoweredHelperCall(b, helper, marker, args, nil)
}

// expectedTarget is non-nil only for a typed compiler-owned helper whose exact
// source signature and identity were already frozen in ProgramIR. This shares
// edge validation, SitePlan observation and physical selection with rtFunc;
// it does not create another emitter-time call authority.
func (p *context) resolveCoroLoweredHelperCall(b llssa.Builder, helper string, marker llssa.Expr, args []llssa.Expr, expectedTarget *ssa.Function) (llssa.Expr, bool) {
	if expectedTarget != nil && !p.hasStructuredOutcomePhysicalBody() {
		panic("typed compiler helper requires its exact managed physical owner")
	}
	if p.compilation == nil {
		return llssa.Nil, false
	}
	if p.emissionUniverse == nil || expectedTarget == nil && !p.emissionUniverse.CompleteRuntimeABI() {
		// Isolated package/report tests do not carry the production runtime ABI
		// and must keep the legacy rtFunc marker. internal/build always prepares
		// a complete universe for active entry resolution, where every missing
		// owner-scoped mapping remains a hard compiler-plan error below.
		return llssa.Nil, false
	}
	if p.goFn == nil || p.emissionUniverse == nil || p.immutablePlan() == nil {
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
	var printKeepaliveSlots []llssa.Expr
	if helper == "PrintBatchV1" {
		// Print batching replaces several per-operand managed calls with one
		// child. Preserve the source builtin's exact pointer-derived scalar
		// roots across that child just as the old individual Print* awaits did.
		// Relocated deferred builtins consume already-captured cleanup fields and
		// deliberately do not recompile their original SSA operands here.
		if source := p.coroCurrentSourceCall(); source != nil {
			printKeepaliveSlots = p.compileCoroCallKeepaliveSlots(b, source)
		}
	}

	// ProgramIR freezes both occurrence domains before representation and
	// demand reach their fixed point. Select by the body being emitted, not by
	// the target's aggregate FunctionPlan: an ordinary/raw Go ABI body must use
	// the independently proven legacy-stack entry even when its physical twin
	// awaits the same logical helper as a managed coroutine.
	plainDomain := !p.hasCoroPhysicalEmission()
	frozenCall := coro.SSALoweredCall{}
	target := (*ssa.Function)(nil)
	ok := false
	var err error
	plainOnly := false
	if plainDomain {
		target, ok, err = p.emissionUniverse.ResolveCoroPlainLoweredCall(p.goFn, helper)
		if err != nil {
			panic(fmt.Errorf("coroutine plain lowered runtime call %q in %q: %w", helper, p.goFn.Name(), err))
		}
		plainOnly = ok
	}
	if !ok {
		frozenCall, ok, err = p.emissionUniverse.ResolveCoroLoweredCallRecord(p.goFn, helper)
		if err != nil {
			panic(fmt.Errorf("coroutine lowered runtime call %q in %q: %w", helper, p.goFn.Name(), err))
		}
		target = frozenCall.Target
	}
	rawPlainOccurrence := ok && !plainOnly && frozenCall.RawPlain
	if !ok || target == nil {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q is absent from the frozen emission universe", helper, p.goFn.Name()))
	}
	if !plainOnly {
		plannedCall, planned := p.immutablePlan().ResolveLoweredCallRecord(p.goFn, helper)
		if !planned || plannedCall.Target != target || plannedCall.RawPlain != rawPlainOccurrence ||
			plannedCall.NoUnwind != frozenCall.NoUnwind ||
			plannedCall.UnwindOnly != frozenCall.UnwindOnly ||
			plannedCall.ExplicitStatusElided != frozenCall.ExplicitStatusElided {
			panic(fmt.Errorf("coroutine lowered runtime call %q in %q disagrees between the frozen emission universe and SSA plan", helper, p.goFn.Name()))
		}
	}
	explicitStatusLegacyPlain, err := coroExplicitStatusElidedUsesRawPlainEntry(
		p.hasStructuredOutcomePhysicalBody(), p.rawPlainBody, frozenCall.ExplicitStatusElided,
	)
	if err != nil {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q: %w", helper, p.goFn.Name(), err))
	}
	targetPlan, planned := p.immutablePlan().FunctionPlan(target)
	if !planned {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q targets an unplanned function", helper, p.goFn.Name()))
	}
	sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(target)
	if err != nil {
		panic(fmt.Errorf("coroutine lowered runtime call %q in %q: derive target %q signature: %w", helper, p.goFn.Name(), targetPlan.ID, err))
	}
	// The compiler-created rtFunc marker already carries LLGo's physical Go ABI:
	// function values are {fn,env}, interfaces use their runtime headers, and
	// other source types have crossed the same target-specific conversion.
	// Convert the frozen logical source signature through that one canonical
	// boundary before comparing. x/tools SSA has also packed a variadic
	// invocation into the final slice argument, so normalize both sides.
	if expectedTarget != nil {
		if target != expectedTarget || plainOnly || rawPlainOccurrence || explicitStatusLegacyPlain ||
			frozenCall.ExplicitStatusElided || sourceSig.Results().Len() != 0 || len(args) != sourceSig.Params().Len() {
			panic(fmt.Errorf("typed compiler helper %q has no exact non-elided managed call", helper))
		}
	} else {
		markerSig, ok := types.Unalias(marker.RawType()).(*types.Signature)
		if !ok {
			panic(fmt.Errorf(
				"coroutine lowered runtime call %q in %q target %q marker has non-signature type %T (%v)",
				helper, p.goFn.Name(), targetPlan.ID, types.Unalias(marker.RawType()), marker.RawType(),
			))
		}
		markerSig = coroPhysicalNormalizeSourceSignature(markerSig)
		physicalSourceSig := coroPhysicalNormalizeSourceSignature(p.prog.PhysicalFuncDecl(sourceSig, llssa.InGo))
		if !types.Identical(markerSig, physicalSourceSig) {
			panic(fmt.Errorf(
				"coroutine lowered runtime call %q in %q target %q has a different effective source signature: marker=%s target=%s",
				helper, p.goFn.Name(), targetPlan.ID,
				types.TypeString(markerSig, types.RelativeTo(nil)),
				types.TypeString(physicalSourceSig, types.RelativeTo(nil)),
			))
		}
	}
	if plainOnly || rawPlainOccurrence || explicitStatusLegacyPlain {
		if !targetPlan.RawPlainDemand || !p.immutablePlan().HasRawPlainVariant(target) {
			panic(fmt.Errorf("coroutine raw/plain lowered runtime call %q in %q targets %q without an exact raw-plain variant", helper, p.goFn.Name(), targetPlan.ID))
		}
		fn, _, kind := p.compileRawPlainFunction(target)
		if fn == nil || kind != goFunc && !isNativeFuncKind(kind) {
			panic(fmt.Errorf("coroutine raw/plain lowered runtime call %q in %q target %q did not resolve to a raw-callable Go/C entry", helper, p.goFn.Name(), targetPlan.ID))
		}
		return b.Call(fn.Expr, args...), true
	}
	if p.rawPlainBody {
		if targetPlan.Emission == coro.EmitCoroutine && !p.immutablePlan().HasRawPlainVariant(target) {
			panic(fmt.Errorf("coroutine lowered runtime call %q in raw plain body %q targets managed coroutine %q without a raw plain variant", helper, p.goFn.Name(), targetPlan.ID))
		}
		fn, _, kind := p.compileRawPlainFunction(target)
		if fn == nil || kind != goFunc && !isNativeFuncKind(kind) {
			panic(fmt.Errorf("coroutine lowered runtime call %q in raw plain body %q target %q did not resolve to a raw-callable Go/C entry", helper, p.goFn.Name(), targetPlan.ID))
		}
		return b.Call(fn.Expr, args...), true
	}
	return p.emitCoroResolvedManagedHelper(b, helper, target, targetPlan, sourceSig, args, printKeepaliveSlots), true
}

// emitCoroResolvedManagedHelper is the common physical entry selector after
// a caller has consumed its exact frozen lowered-call edge. Resource adapters
// use the same plain/outcome/coroutine paths as ordinary runtime helpers.
func (p *context) emitCoroResolvedManagedHelper(
	b llssa.Builder, helper string, target *ssa.Function, targetPlan coro.FunctionPlan,
	sourceSig *types.Signature, args, keepaliveSlots []llssa.Expr,
) llssa.Expr {
	if p.hasStructuredOutcomePhysicalBody() && targetPlan.HasStaticOutcome() {
		nativeResult := !p.prog.LocalGoTypeExceedsNativeStack(
			newOutcomePlainPhysicalABI(sourceSig).resultSlotType,
		)
		var cleanup *coroStaticCleanupState
		if site := p.coroEmissionSite(); site != nil && site.placement.cleanup() {
			cleanup = p.coroCleanup()
			if cleanup == nil {
				panic("lowered outcome cleanup has no active drainer")
			}
		}
		return p.compileCoroStaticOutcomeTargetCallAliasResult(
			b, target, args, nativeResult, false, cleanup,
		).value
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
		return b.Call(fn.Expr, args...)
	case coro.EmitCoroutine:
		if !p.hasStructuredOutcomePhysicalBody() {
			ownerPlan, _ := p.immutablePlan().FunctionPlan(p.goFn)
			panic(fmt.Errorf(
				"coroutine lowered runtime call %q in non-physical owner %q (emission=%s primary=%s representation=%s demand=%s effect=%s) targets managed coroutine %q (%s; demand=%s effect=%s exec=%s; trace=%s)",
				helper, p.goFn.Name(), ownerPlan.Emission, ownerPlan.Primary,
				ownerPlan.FuncRep, ownerPlan.Demand, ownerPlan.Effect, targetPlan.ID,
				target.String(), targetPlan.Demand, targetPlan.Effect, targetPlan.Exec,
				p.immutablePlan().SuspensionEffectTrace(target, targetPlan.Effect),
			))
		}
		if site := p.coroEmissionSite(); site != nil && site.placement.cleanup() {
			return p.compileCoroCleanupTargetAwait(b, target, args, keepaliveSlots)
		}
		if len(keepaliveSlots) != 0 {
			return p.compileCoroTargetAwaitWithKeepalive(b, target, args, keepaliveSlots)
		}
		return p.compileCoroTargetAwait(b, target, args)
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
// only raw demand for the legacy runtime.Panic call: a structured physical body
// publishes its outcome and emits no call, while a managed plain primary (or a
// raw variant) still executes the legacy native-stack helper. Keeping this
// decision beside entry resolution prevents a plain primary from accidentally
// requesting a managed helper entry and widening the whole-program coloring.
func coroExplicitStatusElidedUsesRawPlainEntry(
	structuredPhysicalBody bool,
	rawPlainBody bool,
	explicitStatusElided bool,
) (bool, error) {
	if !explicitStatusElided {
		return false, nil
	}
	if structuredPhysicalBody {
		if rawPlainBody {
			return false, fmt.Errorf("ExplicitStatus-elided helper observed incompatible structured-physical and raw-plain body domains")
		}
		return false, fmt.Errorf("ExplicitStatus-elided helper escaped into its structured physical body")
	}
	if rawPlainBody {
		return true, nil
	}
	// There is no third executable physical domain: an emitted body which is
	// neither the stackless coroutine body nor its separately frozen raw
	// variant is the ordinary managed/plain primary.
	return true, nil
}
