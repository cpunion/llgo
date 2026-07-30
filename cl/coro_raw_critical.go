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

const (
	coroRawCriticalCertificateDomain     = "llgo-coro-raw-critical-certificate-v1"
	coroRawCriticalCallCertificateDomain = "llgo-coro-raw-critical-call-certificate-v1"
)

func validateCoroRawPlainSourceCall(
	plan *coro.SSAPlan,
	resolve func(*ssa.Function) (*ssa.Function, bool),
	call *ssa.Call,
) (*ssa.Function, coro.FunctionPlan, error) {
	if plan == nil || resolve == nil || call == nil || call.Common() == nil ||
		call.Common().IsInvoke() || call.Common().Method != nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("raw/plain invocation requires an exact plan, resolver, and ordinary static call")
	}
	callPlan, planned := plan.CallPlan(call)
	if !planned || !callPlan.RawPlain || callPlan.RawPlainCertificate == "" ||
		callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.DirectPlain ||
		callPlan.Transport != coro.ManagedTransport || callPlan.Open || callPlan.MayBeNil ||
		callPlan.SyncDispatch || len(callPlan.Targets) != 1 {
		return nil, coro.FunctionPlan{}, fmt.Errorf("raw/plain invocation has no exact closed DirectPlain CallPlan")
	}
	static := call.Common().StaticCallee()
	target, resolved := resolve(static)
	if !resolved || target == nil || target != static {
		return nil, coro.FunctionPlan{}, fmt.Errorf("raw/plain invocation target is not one exact canonical function")
	}
	targetPlan, targetPlanned := plan.FunctionPlan(target)
	if !targetPlanned || targetPlan.ID != callPlan.Targets[0] ||
		!plan.HasRawPlainVariant(target) {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"raw/plain invocation target %q has no exact raw/plain variant",
			callPlan.Targets[0],
		)
	}
	if err := validatePlannedRawPlainVariant(target, targetPlan, true); err != nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"raw/plain invocation target %q has an invalid raw/plain variant: %w",
			callPlan.Targets[0], err,
		)
	}
	return target, targetPlan, nil
}

func (p *context) tryCompileCoroRawPlainCall(b llssa.Builder, call *ssa.Call) (llssa.Expr, bool) {
	if p == nil || p.compilation == nil || p.emissionUniverse == nil || call == nil {
		return llssa.Expr{}, false
	}
	// A physical coroutine consumes this capability through its frozen control
	// recipe so every source call remains owned by the physical dispatcher.
	if p.hasCoroPhysicalBody() {
		return llssa.Expr{}, false
	}
	plan := p.compilation.immutablePlan()
	if plan == nil {
		return llssa.Expr{}, false
	}
	callPlan, planned := plan.CallPlan(call)
	if !planned || !callPlan.RawPlain {
		return llssa.Expr{}, false
	}
	target, _, err := validateCoroRawPlainSourceCall(plan, p.emissionUniverse.Resolve, call)
	if err != nil {
		panic(err)
	}
	return p.compileCoroRawPlainTargetCall(b, call, target), true
}

func (p *context) compileCoroRawPlainTargetCall(
	b llssa.Builder,
	call *ssa.Call,
	target *ssa.Function,
) llssa.Expr {
	if p == nil || call == nil || target == nil {
		panic("raw/plain invocation emission requires one exact source call and frozen target")
	}
	function, _, kind := p.compileRawPlainFunction(target)
	if function == nil || kind != goFunc {
		panic(fmt.Errorf("raw/plain invocation target %q did not resolve to a Go raw body", target.Name()))
	}
	p.emitPCLineLabel(b, call.Pos())
	args := p.compileValues(b, call.Call.Args, fnNormal)
	return b.Call(function.Expr, args...)
}
