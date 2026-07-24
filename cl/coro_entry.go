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
	function          *ssa.Function
	pkgTypes          *types.Package
	name              string
	baseName          string
	ftype             int
	plan              coro.FunctionPlan
	planned           bool
	physical          bool
	frameRetentionABI string
	coroPlan          *coro.SSAPlan
	emission          *EmissionUniverse
	physicalOwner     *preparedEmissionPackage
	interfacePlain    *coroClosedInterfacePlainPlan
	managedInterface  *coroManagedInterfaceDispatchPlan
	patchOriginalInit bool
}

// resolveFunctionSymbol is shared by function definitions and declarations so
// they cannot independently choose different primary symbols. The physical
// descriptor derives the signature from this exact entry. A nil Compilation is
// accepted only by isolated low-level frontend helpers; production always
// supplies the compilation-scoped stackless plan.
func (p *context) resolveFunctionSymbol(fn *ssa.Function) (plannedFunctionSymbol, error) {
	universe := p.immutableEmissionUniverse()
	if universe != nil {
		canonical, ok := universe.Resolve(fn)
		if !ok {
			_, unresolvedName, _ := p.funcName(fn)
			return plannedFunctionSymbol{}, fmt.Errorf("coroutine entry resolution: function %q is absent from the prepared emission universe", unresolvedName)
		}
		fn = canonical
	}
	pkgTypes, name, ftype := p.funcName(fn)
	if universe != nil {
		var err error
		name, err = universe.physicalName(p.goPkg, fn, name)
		if err != nil {
			return plannedFunctionSymbol{}, err
		}
	}
	entry := plannedFunctionSymbol{
		function: fn,
		pkgTypes: pkgTypes,
		name:     name,
		baseName: name,
		ftype:    ftype,
	}
	if ftype != goFunc || p.compilation == nil {
		return entry, nil
	}
	whole := p.compilation.immutablePlan()
	if whole == nil {
		return entry, fmt.Errorf("coroutine entry resolution requires a compilation CoroPlan")
	}
	plan, ok := whole.FunctionPlan(fn)
	if !ok {
		return entry, fmt.Errorf("coroutine entry resolution: function %q is absent from the compilation CoroPlan", name)
	}
	entry.plan = plan
	entry.planned = true
	entry.physical = true
	entry.frameRetentionABI = p.compilation.CoroFrameRetentionABI
	entry.coroPlan = whole
	entry.emission = universe
	// Symbol resolution can happen in a caller package that merely references
	// fn. Its physical proof belongs to fn's body owner; actual multi-owner body
	// emission performs its own exact lookup in compileCoroPhysicalBody.
	if universe != nil {
		entry.physicalOwner = universe.ownerOf(fn)
	}
	entry.interfacePlain = p.compilation.coroClosedInterfacePlain
	entry.managedInterface = p.compilation.coroManagedInterface
	ignored := whole.IgnoresBody(fn)
	hasEmittedBody := len(fn.Blocks) != 0
	if universe != nil {
		var err error
		hasEmittedBody, err = p.compilation.plannedFunctionEmittedBody(fn)
		if err != nil {
			return entry, err
		}
	} else if ignored {
		return entry, fmt.Errorf("coroutine entry resolution: Go-emitted function %q has an ignored SSA body", plan.ID)
	}
	if err := validatePlannedFunction(fn, plan, hasEmittedBody); err != nil {
		return entry, err
	}
	if plan.Emission == coro.EmitCoroutine {
		entry.name += coroPrimarySuffix
	}
	return entry, nil
}

// resolvePatchOriginalInitSymbol selects the private physical role of the
// exact original initializer reached by a compiler-owned patch-init edge.
// Generic function resolution must never infer this role from the function
// pointer alone because source dependency calls to that pointer target the
// public patch initializer instead.
func (p *context) resolvePatchOriginalInitSymbol(fn *ssa.Function) (plannedFunctionSymbol, error) {
	entry, err := p.resolveFunctionSymbol(fn)
	if err != nil {
		return plannedFunctionSymbol{}, err
	}
	universe := p.immutableEmissionUniverse()
	if p.compilation == nil || universe == nil {
		return plannedFunctionSymbol{}, fmt.Errorf("coroutine patch original initializer role requires active entry resolution")
	}
	hidden, err := universe.patchOriginalInitPhysicalName(entry.function)
	if err != nil {
		return plannedFunctionSymbol{}, err
	}
	entry.baseName = hidden
	entry.name = hidden
	if entry.planned && entry.plan.Emission == coro.EmitCoroutine {
		entry.name += coroPrimarySuffix
	}
	entry.patchOriginalInit = true
	return entry, nil
}

func (p *context) mustPatchOriginalInitFunctionSymbol(fn *ssa.Function) plannedFunctionSymbol {
	entry, err := p.resolvePatchOriginalInitSymbol(fn)
	if err == nil {
		err = entry.checkSupported()
	}
	if err != nil {
		panic(err)
	}
	return entry
}

func coroEntryFunctionDiagnostic(fn *ssa.Function) string {
	if fn == nil {
		return "<nil>"
	}
	origin := "<nil>"
	if value := fn.Origin(); value != nil {
		origin = value.String()
	}
	return fmt.Sprintf(
		"%s [name=%q origin=%q synthetic=%q blocks=%d]",
		fn.String(), fn.Name(), origin, fn.Synthetic, len(fn.Blocks),
	)
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
			return fmt.Errorf("coroutine entry resolution: plain emission %q (%s) has external kind %s and emitted-body=%t", plan.ID, coroEntryFunctionDiagnostic(fn), plan.External, hasEmittedBody)
		}
	case coro.EmitRawPlain:
		if plan.External != coro.Defined || !hasEmittedBody || !plan.RawPlainOnly ||
			plan.ManagedDemand != coro.NoDemand || !plan.RawPlainDemand ||
			plan.Primary != coro.PrimaryPlain || plan.FuncRep != coro.DirectPlain {
			return fmt.Errorf(
				"coroutine entry resolution: raw-only emission %q (%s) has external=%s emitted-body=%t raw-only=%t managed=%s raw=%t primary=%s representation=%s",
				plan.ID, coroEntryFunctionDiagnostic(fn), plan.External, hasEmittedBody, plan.RawPlainOnly, plan.ManagedDemand,
				plan.RawPlainDemand, plan.Primary, plan.FuncRep,
			)
		}
	case coro.EmitCoroutine:
		if plan.External != coro.Defined || !hasEmittedBody {
			return fmt.Errorf("coroutine entry resolution: coroutine emission %q (%s) has external kind %s and emitted-body=%t", plan.ID, coroEntryFunctionDiagnostic(fn), plan.External, hasEmittedBody)
		}
	case coro.EmitExternal:
		if plan.External == coro.Defined || hasEmittedBody {
			return fmt.Errorf("coroutine entry resolution: external emission %q (%s) has external kind %s and emitted-body=%t", plan.ID, coroEntryFunctionDiagnostic(fn), plan.External, hasEmittedBody)
		}
	default:
		return fmt.Errorf("coroutine entry resolution: function %q has invalid emission kind %d", plan.ID, uint8(plan.Emission))
	}
	return nil
}

func (c *Compilation) plannedFunctionEmittedBody(fn *ssa.Function) (bool, error) {
	plan := c.immutablePlan()
	universe := c.immutableEmissionUniverse()
	if plan == nil || universe == nil || fn == nil {
		return false, fmt.Errorf("coroutine entry resolution: cannot classify a nil or unprepared planned function")
	}
	background, classified, err := universe.FunctionBackground(fn)
	if err != nil {
		return false, fmt.Errorf("coroutine entry resolution: classify frozen frontend ABI for %q: %w", fn.Name(), err)
	}
	ignored := plan.IgnoresBody(fn)
	_, assemblyCertified, assemblyErr := universe.CoroAssemblyNoSuspendCertificate(fn)
	if assemblyErr != nil {
		return false, fmt.Errorf("coroutine entry resolution: classify frozen assembly ABI for %q: %w", fn.Name(), assemblyErr)
	}
	if assemblyCertified && (!classified || background != llssa.InGo || len(fn.Blocks) != 0) {
		return false, fmt.Errorf("coroutine entry resolution: assembly-certified function %q has frontend classified=%t kind=%d body=%t", fn.Name(), classified, background, len(fn.Blocks) != 0)
	}
	managedBodylessNoBlock := false
	if classified && background == llssa.InGo && len(fn.Blocks) == 0 {
		certificate, certified, certificateErr := universe.CoroForeignNoBlockCertificate(fn)
		if certificateErr != nil {
			return false, fmt.Errorf("coroutine entry resolution: classify managed bodyless noblock declaration %q: %w", fn.Name(), certificateErr)
		}
		managedBodylessNoBlock = certified && certificate.ID != ""
	}
	frozenIgnored := classified && background == llssa.InC || assemblyCertified || managedBodylessNoBlock
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
	if universe := p.immutableEmissionUniverse(); universe != nil {
		canonical, ok := universe.Resolve(fn)
		if !ok || canonical == nil {
			panic(fmt.Errorf("coroutine eager emission: function %q is absent from the prepared emission universe", fn.Name()))
		}
		if canonical != fn {
			// Bodyless go:linkname declarations and replaced package members are
			// aliases, not additional definition owners. Lazy references still
			// resolve them to the canonical symbol, but eager enumeration must wait
			// for the canonical owner's package to emit the one physical body.
			return true
		}
	}
	entry, err := p.resolveFunctionSymbol(fn)
	if err != nil {
		panic(err)
	}
	return entry.planned && entry.plan.Emission == coro.EmitNone
}

// checkSupported rejects plan decisions whose physical ABI is not implemented
// yet. Callers must run this before looking up or creating an LLVM symbol.
func (e plannedFunctionSymbol) checkSupported() error {
	return e.checkSupportedWithPhysicalPlan(nil)
}

func (e plannedFunctionSymbol) checkSupportedWithPhysicalPlan(accept func(*coroPhysicalFunctionPlan) error) error {
	if !e.planned {
		return nil
	}
	if e.plan.Emission == coro.EmitNone {
		return fmt.Errorf("coroutine entry resolution: function %q has no emitted entry", e.plan.ID)
	}
	if e.plan.Emission == coro.EmitRawPlain {
		if e.plan.FuncRep != coro.DirectPlain || e.plan.Primary != coro.PrimaryPlain ||
			!e.plan.RawPlainOnly || e.plan.ManagedDemand != coro.NoDemand || !e.plan.RawPlainDemand {
			return fmt.Errorf(
				"coroutine entry resolution: raw-only function %q has invalid selection (representation=%s primary=%s raw-only=%t managed=%s raw=%t)",
				e.plan.ID, e.plan.FuncRep, e.plan.Primary, e.plan.RawPlainOnly,
				e.plan.ManagedDemand, e.plan.RawPlainDemand,
			)
		}
		variant := e.coroPlan != nil && e.coroPlan.HasRawPlainVariant(e.function)
		return validatePlannedRawPlainVariant(e.function, e.plan, variant)
	}
	// EmitPlain remains a legal single native-stack body under the target-wide
	// ExplicitStatus identity. The physical-coroutine call-site verifier is the
	// authority that forbids entering a MayUnwind plain body from a stackless
	// activation; rejecting unrelated synchronous-only bodies here would force
	// unnecessary dual versions across the standard library.
	if e.plan.FuncRep == coro.Dispatch {
		receiverDispatchTarget := e.interfacePlain.acceptsTarget(e.function, e.plan) ||
			e.managedInterface.acceptsTarget(e.function, e.plan)
		if receiverDispatchTarget {
			// A plain target keeps the legacy callable itab entry. An async
			// receiver method instead uses that word only as a closed dispatch
			// discriminator and must still pass the ordinary physical-coroutine
			// checks below.
			if e.plan.Emission == coro.EmitPlain {
				return nil
			}
			if e.plan.Emission != coro.EmitCoroutine {
				return fmt.Errorf("coroutine entry resolution: raw/interface target %q has unsupported emission %s", e.plan.ID, e.plan.Emission)
			}
		} else {
			if err := validateCoroDynamicDispatchTarget(e.function, e.plan, e.emission); err != nil {
				return err
			}
			if e.plan.Emission == coro.EmitPlain {
				return nil
			}
			// A coroutine descriptor publishes only a thin HasCoro entry thunk;
			// the single source primary must still pass the complete physical-body
			// validation below.
		}
	}
	if e.plan.Emission == coro.EmitCoroutine {
		if !e.physical {
			return fmt.Errorf("coroutine emission %q requires coroutine physical ABI lowering", e.plan.ID)
		}
		sourceSig := coroPhysicalNormalizeSourceSignature(e.function.Signature)
		if e.emission != nil {
			var err error
			sourceSig, err = e.emission.coroPhysicalEntrySourceSignature(e.function)
			if err != nil {
				return err
			}
		}
		if err := validateCoroPhysicalFunctionValueABI(e.plan, sourceSig, true); err != nil {
			return err
		}
		rawMethodToken := e.interfacePlain.acceptsTarget(e.function, e.plan) ||
			e.managedInterface.acceptsTarget(e.function, e.plan)
		if accept == nil && e.emission != nil && e.emission.coroProgramIR != nil &&
			e.emission.coroProgramIR.physicalPlansSealed {
			_, err := e.emission.coroProgramIR.physicalFunctionPlan(e.function, e.physicalOwner)
			return err
		}
		return validateCoroPhysicalABIForOwner(
			e.function, e.plan, e.coroPlan, e.emission, e.physicalOwner, true, true,
			true, true, e.frameRetentionABI, true, true, rawMethodToken,
			e.interfacePlain, e.managedInterface, accept,
		)
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
	if err := c.validateCoroTargetCapabilities(); err != nil {
		return err
	}
	if err := c.validateCoroWorkerUniverseTarget(); err != nil {
		return err
	}
	c.coroPreflight.Do(func() {
		if err := c.validateCoroABIIdentity(false); err != nil {
			c.coroPreflightErr = err
			return
		}
		if c.CoroLoweringFacts.Schema != "" || c.CoroLoweringFactsDigest != "" {
			if err := c.validateCoroLoweringFactsIdentity(); err != nil {
				c.coroPreflightErr = err
				return
			}
		}
		plan := c.immutablePlan()
		if plan == nil {
			c.coroPreflightErr = fmt.Errorf("coroutine entry resolution requires a compilation CoroPlan")
			return
		}
		universe := c.immutableEmissionUniverse()
		if universe == nil {
			c.coroPreflightErr = fmt.Errorf("coroutine entry resolution requires a prepared emission universe")
			return
		}
		if universe.CoroWorkerSupported() != c.CoroWorkerSupported() {
			c.coroPreflightErr = fmt.Errorf("coroutine worker lowering disagrees with the prepared emission universe")
			return
		}
		if err := universe.ValidateCoroPlan(plan); err != nil {
			c.coroPreflightErr = err
			return
		}
		managedInterface, err := analyzeCoroManagedInterfaceDispatchPlan(
			plan, universe, true,
		)
		if err != nil {
			c.coroPreflightErr = err
			return
		}
		c.coroManagedInterface = managedInterface
		interfacePlain, err := analyzeCoroClosedInterfacePlainPlan(
			plan, universe, true, true, managedInterface,
		)
		if err != nil {
			c.coroPreflightErr = err
			return
		}
		c.coroClosedInterfacePlain = interfacePlain
		if err := validateCoroRootEntries(plan); err != nil {
			c.coroPreflightErr = err
			return
		}
		physicalExpected := make(map[emissionFunctionOwnerKey]none)
		physicalStage := newCoroPhysicalPlanStage()
		for _, function := range plan.Functions() {
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
				function:          function.Function,
				plan:              function.Plan,
				planned:           true,
				physical:          true,
				frameRetentionABI: c.CoroFrameRetentionABI,
				coroPlan:          plan,
				emission:          universe,
				interfacePlain:    c.coroClosedInterfacePlain,
				managedInterface:  c.coroManagedInterface,
			}
			if function.Plan.Emission == coro.EmitCoroutine {
				owners := universe.sortedUseOwners(function.Function)
				if len(owners) == 0 {
					c.coroPreflightErr = fmt.Errorf("coroutine physical preflight: function %q has no exact emission owner", function.Plan.ID)
					return
				}
				for _, owner := range owners {
					ownerEntry := entry
					ownerEntry.physicalOwner = owner
					key := emissionFunctionOwnerKey{function: function.Function, owner: owner}
					physicalExpected[key] = none{}
					if err := ownerEntry.checkSupportedWithPhysicalPlan(func(plan *coroPhysicalFunctionPlan) error {
						return physicalStage.freezePhysicalFunctionPlan(plan)
					}); err != nil {
						c.coroPreflightErr = err
						return
					}
				}
			} else if err := entry.checkSupported(); err != nil {
				c.coroPreflightErr = err
				return
			}
			if function.Plan.Emission == coro.EmitCoroutine {
				sig, err := universe.coroPhysicalEntrySourceSignature(function.Function)
				if err == nil {
					err = validateCoroLeafPhysicalSignature(function.Plan, sig)
				}
				if err == nil {
					err = validateCoroPhysicalFunctionValueABI(function.Plan, sig, true)
				}
				if err != nil {
					c.coroPreflightErr = err
					return
				}
			}
		}
		if err := validateCoroRawCFunctionAdapters(plan, universe); err != nil {
			c.coroPreflightErr = err
			return
		}
		if err := validateCoroRawPlainConsumers(plan, universe, true); err != nil {
			c.coroPreflightErr = err
			return
		}
		c.coroPreflightErr = validateCoroPhysicalConsumersCapabilities(plan, universe, true, true, true)
		if c.coroPreflightErr != nil {
			return
		}
		c.coroPreflightErr = validateCoroPlainDispatchConsumers(
			plan, universe, c.coroClosedInterfacePlain, c.coroManagedInterface,
		)
		if c.coroPreflightErr != nil {
			return
		}
		if err := universe.coroProgramIR.commitPhysicalFunctionPlans(physicalStage, physicalExpected); err != nil {
			c.coroPreflightErr = fmt.Errorf("coroutine physical preflight: commit ProgramIR: %w", err)
			return
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

// mustRawPlainFunctionSymbol selects the separately planned legacy Go-ABI body
// for one member of an exactly validated raw synchronous closure. It never
// changes the managed primary selected by mustFunctionSymbol. Captured
// functions are admitted only as internal closure-context variants; publishing
// their address still requires validatePlannedRawPlainEntry and is rejected.
func (p *context) mustRawPlainFunctionSymbol(fn *ssa.Function) plannedFunctionSymbol {
	entry, err := p.resolveFunctionSymbol(fn)
	if err == nil {
		err = entry.checkSupported()
	}
	return p.mustRawPlainFunctionSymbolFromEntry(entry, err)
}

// mustRawPlainFunctionSymbolFromEntry preserves an already-selected symbol
// role while choosing its native-stack twin. This matters for the private
// patch-original role: its raw twin is init$hasPatch, never the public init.
func (p *context) mustRawPlainFunctionSymbolFromEntry(entry plannedFunctionSymbol, err error) plannedFunctionSymbol {
	if err == nil {
		plan := p.compilation.immutablePlan()
		variant := plan != nil && plan.HasRawPlainVariant(entry.function)
		err = validatePlannedRawPlainVariant(entry.function, entry.plan, variant)
	}
	if err == nil && (entry.plan.Emission == coro.EmitCoroutine || entry.plan.Emission == coro.EmitRawPlain) {
		if entry.baseName == "" {
			err = fmt.Errorf("raw plain entry %q has no frozen base symbol", entry.plan.ID)
		} else {
			entry.name = entry.baseName
			entry.physical = false
		}
	}
	if err != nil {
		panic(err)
	}
	return entry
}

func validatePlannedRawPlainVariant(fn *ssa.Function, plan coro.FunctionPlan, variant bool) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("raw plain variant %q: %s", plan.ID, fmt.Sprintf(format, args...))
	}
	if fn == nil || fn.Signature == nil || len(fn.Blocks) == 0 {
		return fail("requires one owned Go body")
	}
	if !variant || plan.External != coro.Defined || !plan.RawPlainDemand || plan.Demand == coro.NoDemand {
		return fail(
			"requires a raw-demanded defined RawPlainVariant plan (variant=%t external=%s demand=%s managed=%s raw=%t)",
			variant, plan.External, plan.Demand, plan.ManagedDemand, plan.RawPlainDemand,
		)
	}
	switch plan.Emission {
	case coro.EmitPlain:
		if plan.RawPlainOnly || plan.ManagedDemand == coro.NoDemand || plan.Primary != coro.PrimaryPlain ||
			plan.FuncRep == coro.DirectCoro || plan.Effect.MaySuspend() {
			return fail(
				"plain alias has raw-only=%t managed=%s primary=%s representation=%s effect=%s",
				plan.RawPlainOnly, plan.ManagedDemand, plan.Primary, plan.FuncRep, plan.Effect,
			)
		}
	case coro.EmitCoroutine:
		if plan.RawPlainOnly || plan.ManagedDemand == coro.NoDemand ||
			plan.Primary != coro.PrimaryCoroutine || !plan.Effect.MaySuspend() {
			return fail(
				"dual body has raw-only=%t managed=%s primary=%s effect=%s, want managed coroutine primary",
				plan.RawPlainOnly, plan.ManagedDemand, plan.Primary, plan.Effect,
			)
		}
	case coro.EmitRawPlain:
		if !plan.RawPlainOnly || plan.ManagedDemand != coro.NoDemand ||
			plan.Primary != coro.PrimaryPlain || plan.FuncRep != coro.DirectPlain {
			return fail(
				"raw-only body has raw-only=%t managed=%s primary=%s representation=%s",
				plan.RawPlainOnly, plan.ManagedDemand, plan.Primary, plan.FuncRep,
			)
		}
	default:
		return fail("unsupported emission %s", plan.Emission)
	}
	return nil
}

func validatePlannedRawPlainEntry(fn *ssa.Function, plan coro.FunctionPlan) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("raw plain entry %q: %s", plan.ID, fmt.Sprintf(format, args...))
	}
	if fn == nil || fn.Signature == nil || len(fn.FreeVars) != 0 || len(fn.Blocks) == 0 {
		return fail("requires one owned non-capturing Go body")
	}
	if !plan.RawPlainEntry || plan.External != coro.Defined || !plan.RawPlainDemand || plan.Demand == coro.NoDemand {
		return fail(
			"requires a raw-demanded defined RawPlainEntry plan (entry=%t external=%s demand=%s managed=%s raw=%t)",
			plan.RawPlainEntry, plan.External, plan.Demand, plan.ManagedDemand, plan.RawPlainDemand,
		)
	}
	if err := validatePlannedRawPlainVariant(fn, plan, true); err != nil {
		return fail("invalid legacy body: %v", err)
	}
	return nil
}
