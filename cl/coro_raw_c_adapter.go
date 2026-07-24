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
	"golang.org/x/tools/go/ssa"
)

type coroRawCChangeTypePlan struct {
	target     *ssa.Function
	resultType types.Type
	rawRetag   bool
}

// resolveCoroRawCChangeType freezes the only implicit cross-transport adapter
// currently implemented by LLGo: an exact, context-free Go function may be
// published as one //llgo:type C code pointer when the whole-program plan has
// independently selected and validated its raw/plain entry.  This proof is
// occurrence-local.  It neither changes another use of the Go function nor
// permits a dynamic Managed<->RawC reinterpretation.
func resolveCoroRawCChangeType(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	owner *ssa.Function,
	change *ssa.ChangeType,
) (coroRawCChangeTypePlan, bool, error) {
	if plan == nil || universe == nil || owner == nil || change == nil || change.X == nil {
		return coroRawCChangeTypePlan{}, false, nil
	}
	sourceType := coroCallableEffectiveType(universe, owner, change.X.Type())
	resultType := coroCallableEffectiveType(universe, owner, change.Type())
	sourceTransport, err := coroCallableLeafTransport(universe, sourceType)
	if err != nil {
		return coroRawCChangeTypePlan{}, false, fmt.Errorf("source transport: %w", err)
	}
	resultTransport, err := coroCallableLeafTransport(universe, resultType)
	if err != nil {
		return coroRawCChangeTypePlan{}, false, fmt.Errorf("result transport: %w", err)
	}
	if sourceTransport == coro.ManagedTransport && resultTransport == coro.ManagedTransport {
		return coroRawCChangeTypePlan{}, false, nil
	}
	fail := func(format string, args ...any) (coroRawCChangeTypePlan, bool, error) {
		return coroRawCChangeTypePlan{}, true, fmt.Errorf(
			"coroutine raw C function adapter in %q: %s", owner.Name(), fmt.Sprintf(format, args...),
		)
	}
	if sourceTransport == coro.RawCCodePointer && resultTransport == coro.ManagedTransport {
		return fail("RawC-to-Managed ChangeType has no descriptor construction recipe")
	}

	sourcePlan, sourceFound := plan.ValuePlan(change.X)
	resultPlan, resultFound := plan.ValuePlan(change)
	if !sourceFound || sourcePlan.Value != change.X || len(sourcePlan.Funcs) != 1 || len(sourcePlan.Funcs[0].Path) != 0 {
		return fail("source %q has no exact scalar ValuePlan", change.X.Name())
	}
	if !resultFound || resultPlan.Value != change || len(resultPlan.Funcs) != 1 || len(resultPlan.Funcs[0].Path) != 0 {
		return fail("result %q has no exact scalar ValuePlan", change.Name())
	}
	sourceLeaf, resultLeaf := sourcePlan.Funcs[0], resultPlan.Funcs[0]
	if sourceLeaf.Transport != sourceTransport || resultLeaf.Transport != resultTransport {
		return fail(
			"frozen ValuePlan transport disagrees with frontend metadata (source=%s/%s result=%s/%s)",
			sourceLeaf.Transport, sourceTransport, resultLeaf.Transport, resultTransport,
		)
	}
	if resultLeaf.Transport != coro.RawCCodePointer || resultLeaf.Rep != coro.DirectPlain {
		return fail("raw result requires RawCCodePointer/DirectPlain, got %s/%s", resultLeaf.Transport, resultLeaf.Rep)
	}
	if sourceLeaf.Transport == coro.RawCCodePointer {
		if sourceLeaf.Rep != coro.DirectPlain || sourceLeaf.MayBeNil != resultLeaf.MayBeNil ||
			!equalCoroFunctionTargets(sourceLeaf.Targets, resultLeaf.Targets) {
			return fail("RawC retag changes representation, nilability, or targets")
		}
		return coroRawCChangeTypePlan{resultType: resultType, rawRetag: true}, true, nil
	}
	// A bodyless frontend C declaration already denotes one physical C code
	// pointer even when its source-level Go signature is not itself named with
	// //llgo:type C. Converting that exact symbol to a compatible named C
	// callback is therefore a representation-preserving retag, not a request
	// for a Go raw/plain adapter.
	if static, exact := change.X.(*ssa.Function); exact && static != nil {
		canonical, resolved := universe.Resolve(static)
		background, classified, backgroundErr := universe.FunctionBackground(static)
		if backgroundErr != nil {
			return fail("classify static source background: %v", backgroundErr)
		}
		if resolved && canonical != nil && classified && background == llssa.InC {
			if sourceLeaf.Rep != coro.DirectPlain || sourceLeaf.MayBeNil || resultLeaf.MayBeNil ||
				len(sourceLeaf.Targets) != 1 || len(resultLeaf.Targets) != 1 ||
				sourceLeaf.Targets[0] != resultLeaf.Targets[0] {
				return fail("physical C retag requires one identical, statically non-nil direct target")
			}
			target, found := plan.Function(resultLeaf.Targets[0])
			if !found || target == nil || target != canonical {
				return fail("physical C retag target %q is absent or non-canonical", resultLeaf.Targets[0])
			}
			targetPlan, planned := plan.FunctionPlan(target)
			if !planned || targetPlan.ID != resultLeaf.Targets[0] ||
				targetPlan.External == coro.Defined || targetPlan.Emission != coro.EmitExternal {
				return fail("physical C retag target %q is not one emitted external declaration", resultLeaf.Targets[0])
			}
			if !types.Identical(types.Unalias(sourceType).Underlying(), types.Unalias(resultType).Underlying()) {
				return fail("physical C source and result signatures are not identical")
			}
			return coroRawCChangeTypePlan{resultType: resultType, rawRetag: true}, true, nil
		}
	}
	if sourceLeaf.Transport != coro.ManagedTransport ||
		(sourceLeaf.Rep != coro.DirectPlain && sourceLeaf.Rep != coro.DirectCoro) {
		return fail("Go-to-RawC source requires an exact managed direct entry, got %s/%s", sourceLeaf.Transport, sourceLeaf.Rep)
	}
	if sourceLeaf.MayBeNil || resultLeaf.MayBeNil || len(sourceLeaf.Targets) != 1 ||
		len(resultLeaf.Targets) != 1 || sourceLeaf.Targets[0] != resultLeaf.Targets[0] {
		return fail("Go-to-RawC adapter requires one identical, statically non-nil target")
	}
	target, found := plan.Function(resultLeaf.Targets[0])
	if !found || target == nil {
		return fail("target %q is absent from the compilation plan", resultLeaf.Targets[0])
	}
	static, exact := change.X.(*ssa.Function)
	if !exact || static == nil || len(static.FreeVars) != 0 {
		return fail("source is not one exact non-capturing SSA function")
	}
	canonical, resolved := universe.Resolve(static)
	if !resolved || canonical == nil || canonical != target {
		return fail("static source %q does not resolve to frozen target %q", static.Name(), resultLeaf.Targets[0])
	}
	targetPlan, planned := plan.FunctionPlan(target)
	if !planned || targetPlan.ID != resultLeaf.Targets[0] {
		return fail("target %q has no canonical FunctionPlan", resultLeaf.Targets[0])
	}
	if !plan.HasRawPlainVariant(target) {
		return fail("target %q has no frozen raw/plain variant", targetPlan.ID)
	}
	if err := validatePlannedRawPlainEntry(target, targetPlan); err != nil {
		return fail("target has no public raw/plain entry: %v", err)
	}
	if !types.Identical(types.Unalias(sourceType).Underlying(), types.Unalias(resultType).Underlying()) {
		return fail("source and result signatures are not identical")
	}
	return coroRawCChangeTypePlan{target: target, resultType: resultType}, true, nil
}

func equalCoroFunctionTargets(left, right []coro.FunctionID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateCoroRawCFunctionAdapters(plan *coro.SSAPlan, universe *EmissionUniverse) error {
	if plan == nil || universe == nil {
		return fmt.Errorf("coroutine raw C function adapters require a plan and emission universe")
	}
	for _, function := range plan.Functions() {
		if function.Function == nil || function.Plan.Emission == coro.EmitNone {
			continue
		}
		for _, block := range function.Function.Blocks {
			for _, instruction := range block.Instrs {
				change, ok := instruction.(*ssa.ChangeType)
				if !ok {
					continue
				}
				if _, _, err := resolveCoroRawCChangeType(plan, universe, function.Function, change); err != nil {
					return fmt.Errorf("%s: %w", change.String(), err)
				}
			}
		}
	}
	return nil
}

func (p *context) tryCompileCoroRawCChangeType(b llssa.Builder, change *ssa.ChangeType) (llssa.Expr, bool) {
	if p == nil || p.compilation == nil ||
		p.compilation.CoroPlan == nil || p.compilation.EmissionUniverse == nil || p.goFn == nil {
		return llssa.Expr{}, false
	}
	adapter, recognized, err := resolveCoroRawCChangeType(
		p.compilation.CoroPlan, p.compilation.EmissionUniverse, p.goFn, change,
	)
	if err != nil {
		panic(err)
	}
	if !recognized {
		return llssa.Expr{}, false
	}
	targetType := p.prog.Type(adapter.resultType, llssa.InC)
	if adapter.rawRetag {
		return b.ChangeType(targetType, p.compileValue(b, change.X)), true
	}
	function, py, kind := p.compileRawPlainFunction(adapter.target)
	if kind != goFunc || function == nil || py != nil {
		panic(fmt.Errorf("coroutine raw C function adapter target %q did not compile as one raw Go entry", adapter.target.Name()))
	}
	return b.ChangeType(targetType, function.Expr), true
}
