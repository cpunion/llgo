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
	"go/ast"
	"go/token"
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// coroClosureEnvironmentFact is the immutable physical-context projection for
// one canonical SSA function. It is frozen while the emission closure is open
// so ABI planning, helper inventory, preflight, and codegen never rediscover
// closure layout or directive state independently.
type coroClosureEnvironmentFact struct {
	environment    *types.Var
	explicit       bool
	elidesZeroSize bool
}

type coroClosureEnvironmentProjection struct {
	facts map[*ssa.Function]coroClosureEnvironmentFact
}

func newCoroClosureEnvironmentProjection() coroClosureEnvironmentProjection {
	return coroClosureEnvironmentProjection{
		facts: make(map[*ssa.Function]coroClosureEnvironmentFact),
	}
}

func (p *coroClosureEnvironmentProjection) freeze(
	prog llssa.Program,
	goProg *ssa.Program,
	owner *preparedEmissionPackage,
	fn *ssa.Function,
	effectiveType func(types.Type) types.Type,
) error {
	if p == nil || fn == nil || owner == nil || effectiveType == nil {
		return fmt.Errorf("coroutine closure environment requires one exact function and emission owner")
	}
	if _, frozen := p.facts[fn]; frozen {
		return nil
	}
	explicit := false
	if prog != nil && goProg != nil {
		if decl, ok := fn.Syntax().(*ast.FuncDecl); ok && decl != nil && owner.pkgTypes != nil {
			fullName, _ := astFuncName(llssa.PathOf(owner.pkgTypes), decl)
			explicit = prog.HasClosureEnvDirective(goProg.Fset, fullName, decl.Pos())
		}
	}
	// Lexical captures are pointers to source variables. A pointer to a
	// zero-sized variable may be recreated from LLGo's canonical non-nil
	// sentinel. Synthetic bound-method wrappers instead capture the receiver
	// value directly; that environment is elidable only when the value itself
	// has zero physical size. In particular, a pointer receiver remains
	// non-elidable and preserves its semantically significant nil state.
	sourceClosure := fn.Parent() != nil && fn.Synthetic == ""
	elidesZeroSize := len(fn.FreeVars) != 0
	for _, free := range fn.FreeVars {
		if !elidesZeroSize {
			break
		}
		if free == nil {
			elidesZeroSize = false
			break
		}
		physical := effectiveType(free.Type())
		candidate := physical
		if sourceClosure {
			pointer, ok := types.Unalias(physical).Underlying().(*types.Pointer)
			if !ok {
				elidesZeroSize = false
				break
			}
			candidate = pointer.Elem()
		}
		if prog != nil {
			elidesZeroSize = emissionZeroSizedType(candidate, prog.PointerSize())
		} else {
			elidesZeroSize = emissionUniversallyZeroSizedType(candidate)
		}
	}
	captured := len(fn.FreeVars) != 0 && !elidesZeroSize
	if captured && explicit {
		return fmt.Errorf("coroutine physical ABI: function %q has both lexical captures and //llgo:env", fn.Name())
	}
	fact := coroClosureEnvironmentFact{
		explicit:       explicit,
		elidesZeroSize: elidesZeroSize,
	}
	switch {
	case captured:
		if fn.Signature == nil || fn.Signature.Recv() != nil {
			return fmt.Errorf("coroutine physical ABI: captured function %q must be a receiver-free closure body", fn.Name())
		}
		if owner.pkgTypes == nil {
			return fmt.Errorf("coroutine physical ABI: captured function %q has no emission owner", fn.Name())
		}
		fact.environment = makeClosureCtx(owner.pkgTypes, fn.FreeVars)
	case explicit:
		fact.environment = types.NewParam(token.NoPos, nil, "$env", types.Typ[types.UnsafePointer])
	}
	p.facts[fn] = fact
	return nil
}

func (u *EmissionUniverse) freezeCoroClosureEnvironment(
	fn *ssa.Function,
	fallbackOwner *preparedEmissionPackage,
) error {
	if u == nil || fn == nil {
		return fmt.Errorf("coroutine closure environment requires one emission universe and function")
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return fmt.Errorf("coroutine closure environment target %q has cyclic aliases", fn.Name())
	}
	owner := u.ownerOf(canonical)
	if owner == nil {
		owner = fallbackOwner
	}
	if owner == nil {
		return fmt.Errorf("coroutine closure environment target %q has no emission owner", canonical.Name())
	}
	return u.closureEnvironments.freeze(
		u.prog,
		u.goProg,
		owner,
		canonical,
		func(typ types.Type) types.Type {
			return u.effectiveType(owner, canonical, typ, false)
		},
	)
}

func (p coroClosureEnvironmentProjection) entryEnvironment(fn *ssa.Function) (*types.Var, bool, error) {
	if fn == nil {
		return nil, false, nil
	}
	fact, frozen := p.facts[fn]
	if !frozen {
		return nil, false, fmt.Errorf("coroutine physical ABI: function %q has no frozen closure-environment fact", fn.Name())
	}
	return fact.environment, fact.environment != nil, nil
}

func (p coroClosureEnvironmentProjection) hasExplicitEnvironment(fn *ssa.Function) bool {
	return p.facts[fn].explicit
}

func (p coroClosureEnvironmentProjection) canElideZeroSizedEnvironment(fn *ssa.Function) bool {
	return p.facts[fn].elidesZeroSize
}

func (u *EmissionUniverse) canElideZeroSizedClosureEnvironment(fn *ssa.Function) bool {
	if u == nil || fn == nil {
		return false
	}
	canonical := u.canonicalAlias(fn)
	return canonical != nil && u.closureEnvironments.canElideZeroSizedEnvironment(canonical)
}

func (p coroClosureEnvironmentProjection) elidesZeroSizedFreeVar(fn *ssa.Function, value *ssa.FreeVar) bool {
	if value == nil || !p.canElideZeroSizedEnvironment(fn) {
		return false
	}
	for _, free := range fn.FreeVars {
		if free == value {
			return true
		}
	}
	return false
}
