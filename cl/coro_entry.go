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

const coroPrimarySuffix = "$coro"

// plannedFunctionSymbol is the single symbol selected for an SSA function.
// Emission selects whether this compilation materializes a body/declaration;
// FuncRep only describes escaped function values and never authorizes a
// second body.
type plannedFunctionSymbol struct {
	function      *ssa.Function
	pkgTypes      *types.Package
	name          string
	ftype         int
	plan          coro.FunctionPlan
	planned       bool
	physical      bool
	childAwait    bool
	programRun    bool
	plainDispatch bool
	staticSpawn   bool
	coroPlan      *coro.SSAPlan
	emission      *EmissionUniverse
}

// resolveFunctionSymbol is shared by function definitions and declarations so
// they cannot independently choose different primary symbols. The physical
// descriptor derives the signature from this exact entry. The zero-value
// compilation and report-only plans deliberately preserve the legacy symbol.
func (p *context) resolveFunctionSymbol(fn *ssa.Function) (plannedFunctionSymbol, error) {
	if p.compilation != nil && p.compilation.EnableCoroEntryResolution && p.compilation.EmissionUniverse != nil {
		canonical, ok := p.compilation.EmissionUniverse.Resolve(fn)
		if !ok {
			_, unresolvedName, _ := p.funcName(fn)
			return plannedFunctionSymbol{}, fmt.Errorf("coroutine entry resolution: function %q is absent from the prepared emission universe", unresolvedName)
		}
		fn = canonical
	}
	pkgTypes, name, ftype := p.funcName(fn)
	if p.compilation != nil && p.compilation.EnableCoroEntryResolution && p.compilation.EmissionUniverse != nil {
		var err error
		name, err = p.compilation.EmissionUniverse.physicalName(p.goPkg, fn, name)
		if err != nil {
			return plannedFunctionSymbol{}, err
		}
	}
	entry := plannedFunctionSymbol{
		function: fn,
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
	entry.physical = p.compilation.EnableCoroPhysicalABI
	entry.childAwait = p.compilation.EnableCoroChildAwait
	entry.programRun = p.compilation.EnableCoroProgramBootstrapRun
	entry.plainDispatch = p.compilation.EnableCoroPlainDispatch
	entry.staticSpawn = p.compilation.EnableCoroClosedStaticSpawn
	entry.coroPlan = p.compilation.CoroPlan
	entry.emission = p.compilation.EmissionUniverse
	if p.compilation.CoroPlan.IgnoresBody(fn) {
		return entry, fmt.Errorf("coroutine entry resolution: Go-emitted function %q has an ignored SSA body", plan.ID)
	}
	if err := validatePlannedFunction(fn, plan, len(fn.Blocks) != 0); err != nil {
		return entry, err
	}
	if plan.Emission == coro.EmitCoroutine {
		entry.name += coroPrimarySuffix
	}
	return entry, nil
}

func validatePlannedFunction(fn *ssa.Function, plan coro.FunctionPlan, hasEmittedBody bool) error {
	if fn == nil {
		return fmt.Errorf("coroutine entry resolution: function plan %q has no SSA function", plan.ID)
	}
	switch plan.Emission {
	case coro.EmitNone:
		if plan.Demand != coro.NoDemand {
			return fmt.Errorf("coroutine entry resolution: non-emitted function %q has demand %s", plan.ID, plan.Demand)
		}
		return nil
	case coro.EmitPlain:
		if plan.External != coro.Defined || !hasEmittedBody {
			return fmt.Errorf("coroutine entry resolution: plain emission %q has external kind %s and emitted-body=%t", plan.ID, plan.External, hasEmittedBody)
		}
	case coro.EmitCoroutine:
		if plan.External != coro.Defined || !hasEmittedBody {
			return fmt.Errorf("coroutine entry resolution: coroutine emission %q has external kind %s and emitted-body=%t", plan.ID, plan.External, hasEmittedBody)
		}
	case coro.EmitExternal:
		if plan.External == coro.Defined || hasEmittedBody {
			return fmt.Errorf("coroutine entry resolution: external emission %q has external kind %s and emitted-body=%t", plan.ID, plan.External, hasEmittedBody)
		}
	default:
		return fmt.Errorf("coroutine entry resolution: function %q has invalid emission kind %d", plan.ID, uint8(plan.Emission))
	}
	return nil
}

func (c *Compilation) plannedFunctionEmittedBody(fn *ssa.Function) (bool, error) {
	if c == nil || c.CoroPlan == nil || c.EmissionUniverse == nil || fn == nil {
		return false, fmt.Errorf("coroutine entry resolution: cannot classify a nil or unprepared planned function")
	}
	background, classified, err := c.EmissionUniverse.FunctionBackground(fn)
	if err != nil {
		return false, fmt.Errorf("coroutine entry resolution: classify frozen frontend ABI for %q: %w", fn.Name(), err)
	}
	ignored := c.CoroPlan.IgnoresBody(fn)
	frozenIgnored := classified && background == llssa.InC
	if ignored != frozenIgnored {
		return false, fmt.Errorf("coroutine entry resolution: function %q ignored-body=%t conflicts with frozen frontend background classified=%t kind=%d", fn.Name(), ignored, classified, background)
	}
	return classified && background == llssa.InGo && len(fn.Blocks) != 0, nil
}

// omitUnemittedFunction is used only by eager package/type/closure
// enumeration. A real body reference must go through mustFunctionSymbol and
// fail closed instead of silently turning an EmitNone decision into an LLVM
// declaration.
func (p *context) omitUnemittedFunction(fn *ssa.Function) bool {
	entry, err := p.resolveFunctionSymbol(fn)
	if err != nil {
		panic(err)
	}
	return entry.planned && entry.plan.Emission == coro.EmitNone
}

// checkSupported rejects plan decisions whose physical ABI is not implemented
// yet. Callers must run this before looking up or creating an LLVM symbol.
func (e plannedFunctionSymbol) checkSupported() error {
	if !e.planned {
		return nil
	}
	if e.plan.Emission == coro.EmitNone {
		return fmt.Errorf("coroutine entry resolution: function %q has no emitted entry", e.plan.ID)
	}
	if e.plan.FuncRep == coro.Dispatch {
		if !e.plainDispatch {
			return fmt.Errorf("coroutine entry resolution: function %q requires an unimplemented dispatch descriptor", e.plan.ID)
		}
		return validateCoroPlainDispatchTarget(e.function, e.plan)
	}
	if e.plan.Emission == coro.EmitCoroutine {
		if !e.physical {
			return fmt.Errorf("coroutine emission %q requires coroutine physical ABI lowering", e.plan.ID)
		}
		if err := validateCoroPhysicalFunctionValueABI(e.plan, e.function.Signature, e.plainDispatch); err != nil {
			return err
		}
		return validateCoroPhysicalABIWithUniverseCapabilities(e.function, e.plan, e.coroPlan, e.emission, e.childAwait, e.programRun, e.staticSpawn)
	}
	if e.plan.Emission == coro.EmitExternal && e.plan.FuncRep == coro.DirectCoro {
		return fmt.Errorf("external coroutine emission %q requires coroutine physical ABI lowering", e.plan.ID)
	}
	return nil
}

// preflightCoroPlan rejects every unsupported or inconsistent entry before cl
// creates an LLVM package. This includes non-Go/intrinsic functions present in
// the plan: active entry resolution may not silently route an unsupported plan
// through a legacy ABI merely because funcName classifies it specially.
func (c *Compilation) preflightCoroPlan() error {
	if c == nil {
		return nil
	}
	if c.EnableCoroPhysicalABI && !c.EnableCoroEntryResolution {
		return fmt.Errorf("coroutine physical ABI requires coroutine entry resolution")
	}
	if c.EnableCoroChildAwait && !c.EnableCoroPhysicalABI {
		return fmt.Errorf("coroutine child await requires coroutine physical ABI")
	}
	if c.EnableCoroPlainDispatch && !c.EnableCoroEntryResolution {
		return fmt.Errorf("coroutine plain dispatch requires coroutine entry resolution")
	}
	if c.EnableCoroExplicitStatusPanicABI && !c.EnableCoroEntryResolution {
		return fmt.Errorf("coroutine explicit-status panic ABI requires coroutine entry resolution")
	}
	if c.EnableCoroClosedStaticSpawn {
		if !c.EnableCoroChildAwait {
			return fmt.Errorf("coroutine closed static spawn requires coroutine child await")
		}
		if !c.EnableCoroProgramBootstrapRun {
			return fmt.Errorf("coroutine closed static spawn requires runnable program bootstrap v2")
		}
	}
	if !c.EnableCoroEntryResolution {
		return nil
	}
	c.coroPreflight.Do(func() {
		if err := c.validateCoroABIIdentity(false); err != nil {
			c.coroPreflightErr = err
			return
		}
		if c.EnableCoroExplicitStatusPanicABI {
			c.coroPreflightErr = fmt.Errorf("coroutine explicit-status panic ABI %q is identity-only: lowering and runtime semantics are not implemented", coro.PanicExplicitStatusABIV0)
			return
		}
		if c.CoroPlan == nil {
			c.coroPreflightErr = fmt.Errorf("coroutine entry resolution requires a compilation CoroPlan")
			return
		}
		if c.EmissionUniverse == nil {
			c.coroPreflightErr = fmt.Errorf("coroutine entry resolution requires a prepared emission universe")
			return
		}
		if err := c.EmissionUniverse.ValidateCoroPlan(c.CoroPlan); err != nil {
			c.coroPreflightErr = err
			return
		}
		if c.EnableCoroChildAwait {
			if err := validateCoroRootEntries(c.CoroPlan); err != nil {
				c.coroPreflightErr = err
				return
			}
		}
		for _, function := range c.CoroPlan.Functions() {
			hasEmittedBody, err := c.plannedFunctionEmittedBody(function.Function)
			if err != nil {
				c.coroPreflightErr = err
				return
			}
			if err := validatePlannedFunction(function.Function, function.Plan, hasEmittedBody); err != nil {
				c.coroPreflightErr = err
				return
			}
			if function.Plan.Emission == coro.EmitNone {
				continue
			}
			entry := plannedFunctionSymbol{
				function:      function.Function,
				plan:          function.Plan,
				planned:       true,
				physical:      c.EnableCoroPhysicalABI,
				childAwait:    c.EnableCoroChildAwait,
				programRun:    c.EnableCoroProgramBootstrapRun,
				plainDispatch: c.EnableCoroPlainDispatch,
				staticSpawn:   c.EnableCoroClosedStaticSpawn,
				coroPlan:      c.CoroPlan,
				emission:      c.EmissionUniverse,
			}
			if err := entry.checkSupported(); err != nil {
				c.coroPreflightErr = err
				return
			}
			if c.EnableCoroPhysicalABI && function.Plan.Emission == coro.EmitCoroutine {
				sig, err := c.EmissionUniverse.coroPhysicalSourceSignature(function.Function)
				if err == nil {
					err = validateCoroLeafPhysicalSignature(function.Plan, sig)
				}
				if err == nil {
					err = validateCoroPhysicalFunctionValueABI(function.Plan, sig, c.EnableCoroPlainDispatch)
				}
				if err != nil {
					c.coroPreflightErr = err
					return
				}
			}
		}
		if c.EnableCoroPhysicalABI {
			c.coroPreflightErr = validateCoroPhysicalConsumersCapabilities(c.CoroPlan, c.EnableCoroChildAwait, c.EnableCoroClosedStaticSpawn)
			if c.coroPreflightErr != nil {
				return
			}
		}
		if c.EnableCoroPlainDispatch {
			c.coroPreflightErr = validateCoroPlainDispatchConsumers(c.CoroPlan)
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
