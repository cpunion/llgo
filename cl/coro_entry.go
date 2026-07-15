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

const coroPrimarySuffix = "$coro"

// plannedFunctionSymbol is the single symbol selected for an SSA function.
// Primary selects the source body; FuncRep only describes escaped function
// values and never authorizes a second body.
type plannedFunctionSymbol struct {
	pkgTypes *types.Package
	name     string
	ftype    int
	plan     coro.FunctionPlan
	planned  bool
}

// resolveFunctionSymbol is shared by function definitions and declarations so
// they cannot independently choose different primary symbols. Physical
// signatures remain unchanged in this slice and will be added to a later ABI
// descriptor. The zero-value compilation and report-only plans deliberately
// preserve the legacy symbol.
func (p *context) resolveFunctionSymbol(fn *ssa.Function) (plannedFunctionSymbol, error) {
	pkgTypes, name, ftype := p.funcName(fn)
	entry := plannedFunctionSymbol{
		pkgTypes: pkgTypes,
		name:     name,
		ftype:    ftype,
	}
	if ftype != goFunc || p.compilation == nil || !p.compilation.EnableCoroEntryResolution {
		return entry, nil
	}
	if p.compilation.CoroPlan == nil {
		return entry, fmt.Errorf("coroutine entry resolution requires a compilation CoroPlan")
	}
	plan, ok := p.compilation.CoroPlan.FunctionPlan(fn)
	if !ok {
		return entry, fmt.Errorf("coroutine entry resolution: function %q is absent from the compilation CoroPlan", name)
	}
	entry.plan = plan
	entry.planned = true
	if err := validatePlannedFunction(fn, plan); err != nil {
		return entry, err
	}
	if plan.Primary == coro.PrimaryCoroutine {
		entry.name += coroPrimarySuffix
	}
	return entry, nil
}

func validatePlannedFunction(fn *ssa.Function, plan coro.FunctionPlan) error {
	if fn == nil {
		return fmt.Errorf("coroutine entry resolution: function plan %q has no SSA function", plan.ID)
	}
	hasBody := len(fn.Blocks) != 0
	switch plan.Primary {
	case coro.PrimaryPlain:
		if plan.External != coro.Defined || !hasBody {
			return fmt.Errorf("coroutine entry resolution: plain primary %q has external kind %s and body=%t", plan.ID, plan.External, hasBody)
		}
	case coro.PrimaryCoroutine:
		if plan.External != coro.Defined || !hasBody {
			return fmt.Errorf("coroutine entry resolution: coroutine primary %q has external kind %s and body=%t", plan.ID, plan.External, hasBody)
		}
	case coro.PrimaryExternal:
		if plan.External == coro.Defined || hasBody {
			return fmt.Errorf("coroutine entry resolution: external primary %q has external kind %s and body=%t", plan.ID, plan.External, hasBody)
		}
	default:
		return fmt.Errorf("coroutine entry resolution: function %q has invalid primary kind %d", plan.ID, uint8(plan.Primary))
	}
	return nil
}

// checkSupported rejects plan decisions whose physical ABI is not implemented
// yet. Callers must run this before looking up or creating an LLVM symbol.
func (e plannedFunctionSymbol) checkSupported() error {
	if !e.planned {
		return nil
	}
	if e.plan.FuncRep == coro.Dispatch {
		return fmt.Errorf("coroutine entry resolution: function %q requires an unimplemented dispatch descriptor", e.plan.ID)
	}
	if e.plan.Primary == coro.PrimaryCoroutine {
		return fmt.Errorf("coroutine primary %q requires coroutine physical ABI lowering", e.plan.ID)
	}
	if e.plan.Primary == coro.PrimaryExternal && e.plan.FuncRep == coro.DirectCoro {
		return fmt.Errorf("external coroutine primary %q requires coroutine physical ABI lowering", e.plan.ID)
	}
	return nil
}

// preflightCoroPlan rejects every unsupported or inconsistent entry before cl
// creates an LLVM package. This includes non-Go/intrinsic functions present in
// the plan: active entry resolution may not silently route an unsupported plan
// through a legacy ABI merely because funcName classifies it specially.
func (c *Compilation) preflightCoroPlan() error {
	if c == nil || !c.EnableCoroEntryResolution {
		return nil
	}
	c.coroPreflight.Do(func() {
		if c.CoroPlan == nil {
			c.coroPreflightErr = fmt.Errorf("coroutine entry resolution requires a compilation CoroPlan")
			return
		}
		for _, function := range c.CoroPlan.Functions() {
			if err := validatePlannedFunction(function.Function, function.Plan); err != nil {
				c.coroPreflightErr = err
				return
			}
			entry := plannedFunctionSymbol{plan: function.Plan, planned: true}
			if err := entry.checkSupported(); err != nil {
				c.coroPreflightErr = err
				return
			}
		}
	})
	return c.coroPreflightErr
}

func (p *context) mustFunctionSymbol(fn *ssa.Function) plannedFunctionSymbol {
	entry, err := p.resolveFunctionSymbol(fn)
	if err == nil {
		err = entry.checkSupported()
	}
	if err != nil {
		panic(err)
	}
	return entry
}
