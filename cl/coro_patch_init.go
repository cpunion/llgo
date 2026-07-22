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

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// tryCompileCoroPatchInitRedirect replaces one x/tools dependency call to an
// original package initializer with the exact public initializer selected by
// package patching. Analysis sees the same physical edge through the owner's
// frozen lowered-call occurrence.
func (p *context) tryCompileCoroPatchInitRedirect(b llssa.Builder, call *ssa.Call) (result llssa.Expr, handled bool) {
	if p.compilation == nil || p.emissionUniverse == nil || call == nil {
		return llssa.Nil, false
	}
	logicalName, target, redirected, err := p.emissionUniverse.CoroPatchInitRedirect(call)
	if err != nil {
		panic(fmt.Errorf("coroutine patch initializer replacement: %w", err))
	}
	if !redirected {
		return llssa.Nil, false
	}
	defer func() {
		if handled {
			p.observeCoroCallElision(CoroCallElidedPatchRedirect)
		}
	}()
	if p.goFn == nil || call.Parent() != p.goFn || p.compilation.CoroPlan == nil || b.Func != p.fn {
		panic("coroutine patch initializer replacement requires its exact active owner and SSA plan")
	}
	if !p.compilation.CoroPlan.ElidesCall(call) {
		panic("coroutine patch initializer replacement source occurrence is not frontend-elided in the SSA plan")
	}
	frozen, planned := p.compilation.CoroPlan.ResolveLoweredCallRecord(p.goFn, logicalName)
	if !planned || frozen.Target != target || frozen.RawPlain || frozen.UnwindOnly || frozen.ExplicitStatusElided {
		panic("coroutine patch initializer replacement disagrees between the emission universe and SSA plan")
	}
	targetPlan, planned := p.compilation.CoroPlan.FunctionPlan(target)
	if !planned || targetPlan.External != coro.Defined || targetPlan.Demand == coro.NoDemand {
		panic("coroutine patch initializer replacement targets an unavailable function")
	}
	if target.Signature == nil || target.Signature.Recv() != nil || target.Signature.Params().Len() != 0 ||
		target.Signature.Results().Len() != 0 || len(target.FreeVars) != 0 {
		panic("coroutine patch initializer replacement target does not have exact func() shape")
	}

	if p.rawPlainBody {
		var fn llssa.Function
		var kind int
		switch targetPlan.Emission {
		case coro.EmitPlain:
			fn, _, kind = p.compileManagedFunction(target)
		case coro.EmitCoroutine:
			if !p.compilation.CoroPlan.HasRawPlainVariant(target) {
				panic("raw plain patch initializer replacement has no exact raw target variant")
			}
			fn, _, kind = p.compileRawPlainFunction(target)
		default:
			panic(fmt.Sprintf("raw plain patch initializer replacement has unsupported target emission %s", targetPlan.Emission))
		}
		if fn == nil || kind != goFunc {
			panic("raw plain patch initializer replacement did not resolve to a Go entry")
		}
		b.Call(fn.Expr)
		return llssa.Nil, true
	}

	switch targetPlan.Emission {
	case coro.EmitPlain:
		if targetPlan.Effect.MaySuspend() || targetPlan.FuncRep == coro.DirectCoro {
			panic("plain patch initializer replacement target has coroutine-only semantics")
		}
		fn, _, kind := p.compileFunction(target)
		if fn == nil || kind != goFunc {
			panic("plain patch initializer replacement did not resolve to a Go entry")
		}
		b.Call(fn.Expr)
	case coro.EmitCoroutine:
		if p.coroBody() == nil {
			panic("coroutine patch initializer replacement escaped into a plain owner")
		}
		if result := p.compileCoroTargetAwait(b, target, nil); !result.IsNil() {
			panic("coroutine patch initializer replacement returned a value")
		}
	default:
		panic(fmt.Sprintf("managed patch initializer replacement has unsupported target emission %s", targetPlan.Emission))
	}
	return llssa.Nil, true
}
