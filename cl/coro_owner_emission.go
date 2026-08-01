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
	"sort"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

type coroFrozenOwnerBody struct {
	function *ssa.Function
	state    emissionFunctionState
}

// coroFrozenOwnerBodies is the sole complete-runtime inventory of Go bodies
// that one package module must define. Source members and method-set walks are
// useful discovery inputs while ProgramIR is built, but are not an emission
// authority: closures, instantiated generics, and promoted wrappers need not
// occur in either enumeration.
func (p *context) coroFrozenOwnerBodies() ([]coroFrozenOwnerBody, error) {
	universe := p.immutableEmissionUniverse()
	plan := p.compilation.immutablePlan()
	if universe == nil || !universe.CompleteRuntimeABI() {
		return nil, nil
	}
	if p.emissionOwner == nil || universe.coroProgramIR == nil ||
		plan == nil {
		return nil, fmt.Errorf("coroutine owner emission requires one complete ProgramIR owner and function plan")
	}

	bodies := make([]coroFrozenOwnerBody, 0)
	for key := range universe.coroProgramIR.siteOwners {
		if key.owner != p.emissionOwner || key.function == nil {
			continue
		}
		function := key.function
		canonical, required := universe.Resolve(function)
		if !required || canonical != function {
			return nil, fmt.Errorf(
				"coroutine owner emission: frozen function %q is not one canonical required function",
				function.String(),
			)
		}
		kind, kindFrozen := universe.functionKinds[key]
		state, stateFrozen := universe.ownerStates[function][p.emissionOwner]
		if !kindFrozen || !stateFrozen {
			return nil, fmt.Errorf(
				"coroutine owner emission: frozen function %q has incomplete frontend provenance for owner %q",
				function.String(), p.emissionOwner.identity,
			)
		}
		if kind != goFunc || len(function.Blocks) == 0 {
			continue
		}
		functionPlan, planned := plan.FunctionPlan(function)
		if !planned {
			return nil, fmt.Errorf(
				"coroutine owner emission: frozen Go body %q is absent from the function plan",
				function.String(),
			)
		}
		switch functionPlan.Emission {
		case coro.EmitNone:
			continue
		case coro.EmitPlain, coro.EmitCoroutine, coro.EmitRawPlain, coro.EmitOutcomePlain:
			bodies = append(bodies, coroFrozenOwnerBody{function: function, state: state})
		case coro.EmitExternal:
			return nil, fmt.Errorf(
				"coroutine owner emission: defined Go body %q selected external emission",
				function.String(),
			)
		default:
			return nil, fmt.Errorf(
				"coroutine owner emission: defined Go body %q has invalid emission %d",
				function.String(), functionPlan.Emission,
			)
		}
	}
	sort.SliceStable(bodies, func(i, j int) bool {
		return universe.functionSortKey(bodies[i].function) <
			universe.functionSortKey(bodies[j].function)
	})
	return bodies, nil
}

// emitCoroFrozenOwnerBodies makes ProgramIR ownership sufficient for emission.
// Re-visiting a source-enumerated body is harmless: compileFuncDecl promotes an
// existing declaration only once and returns an existing definition unchanged.
func (p *context) emitCoroFrozenOwnerBodies(pkg llssa.Package) error {
	bodies, err := p.coroFrozenOwnerBodies()
	if err != nil || len(bodies) == 0 {
		return err
	}
	plan := p.compilation.immutablePlan()
	if plan == nil {
		return fmt.Errorf("coroutine owner emission lost its immutable function plan")
	}
	if p.coroOwnerBodySymbols == nil {
		p.coroOwnerBodySymbols = make(map[string]none)
	}
	oldState := p.state
	defer func() {
		p.state = oldState
	}()
	for _, body := range bodies {
		p.state = body.state.state
		function, _, kind := p.compileFuncDecl(pkg, body.function)
		if kind != goFunc || function == nil || !function.HasBody() {
			return fmt.Errorf(
				"coroutine owner emission: frozen Go body %q did not materialize a primary definition",
				body.function.String(),
			)
		}
		p.coroOwnerBodySymbols[function.Name()] = none{}

		functionPlan, _ := plan.FunctionPlan(body.function)
		if functionPlan.Emission != coro.EmitCoroutine ||
			!plan.HasRawPlainVariant(body.function) {
			continue
		}
		raw := p.rawPlainFuncs[body.function]
		if raw == nil || !raw.HasBody() {
			return fmt.Errorf(
				"coroutine owner emission: frozen Go body %q did not materialize its raw/plain twin",
				body.function.String(),
			)
		}
		p.coroOwnerBodySymbols[raw.Name()] = none{}
	}
	return nil
}

// validateCoroFrozenOwnerBodies is the package boundary gate. It converts a
// missed synthetic definition into a compiler error in its declaring package
// instead of an undefined symbol much later in the final program link.
func (p *context) validateCoroFrozenOwnerBodies(pkg llssa.Package) error {
	for symbol := range p.coroOwnerBodySymbols {
		function := pkg.FuncOf(symbol)
		if function == nil || !function.HasBody() {
			return fmt.Errorf(
				"coroutine owner emission: frozen symbol %q has no definition in owner %q",
				symbol, p.emissionOwner.identity,
			)
		}
	}
	return nil
}
