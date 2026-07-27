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
	"strconv"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroLibraryFunctionABIDigestDomain = "llgo.coro.library-function-abi.v1"
	coroLibraryExportABIDigestDomain   = "llgo.coro.library-export-abi.v1"
	coroLibrarySummarySymbolPrefix     = "__llgo_coro_library_effect_v2."
)

// CoroLibraryEffectView is the immutable archive-facing projection of a
// prepared emission universe. It deliberately exposes only stable function
// ABI and symbol proofs, not plan demand or mutable frontend state.
type CoroLibraryEffectView struct {
	index emissionCanonicalIndex
}

// CoroLibraryEffectSummaryRecords returns the byte-exact producer records
// emitted into pkg. Package archiving consumes these bytes directly so Full
// and Thin LTO never need to be reopened merely to recover compiler metadata.
func CoroLibraryEffectSummaryRecords(pkg llssa.Package) ([]byte, error) {
	if pkg == nil {
		return nil, nil
	}
	var records []byte
	for _, blob := range pkg.CompilerMetadataBlobs() {
		if !strings.HasPrefix(blob.Name, coroLibrarySummarySymbolPrefix) {
			continue
		}
		records = append(records, blob.Data...)
	}
	if len(records) == 0 {
		return nil, nil
	}
	if _, err := coro.ParseLibraryEffectSummaryRecords(records); err != nil {
		return nil, fmt.Errorf("read emitted coroutine library effect records: %w", err)
	}
	return records, nil
}

// FunctionABIHash returns the structural physical-entry ABI hash
// shared by producer summaries and consumers. It derives the effective
// receiver/variadic/closure-context signature from the frozen emission
// universe; callers must never rebuild this hash from a symbol or source name.
func (view CoroLibraryEffectView) FunctionABIHash(
	function *ssa.Function,
	metadata coro.LibraryEffectMetadata,
) (string, error) {
	u := view.index.universe
	if u == nil || function == nil {
		return "", fmt.Errorf("coroutine library ABI requires an exact prepared function")
	}
	canonical, ok := u.Resolve(function)
	if !ok || canonical == nil {
		return "", fmt.Errorf("function %q is outside the frozen emission universe", function.Name())
	}
	signature, err := u.coroPhysicalEntrySourceSignature(canonical)
	if err != nil {
		return "", err
	}
	return emissionDigest(framedEmissionKey(
		coroLibraryFunctionABIDigestDomain,
		metadata.CoroABI,
		metadata.SchedulerABI,
		metadata.PanicABI,
		metadata.FuncRepABI,
		metadata.TargetTriple,
		strconv.Itoa(metadata.PointerBits),
		metadata.Endianness,
		metadata.DataLayout,
		structuralEmissionABITypeKey(signature),
	)), nil
}

// ExportABIHash binds one source export's effective raw C signature without
// granting a raw physical entry. The later ingress-adapter gate consumes the
// same hash, so neither archiving nor lowering has to reconstruct an ABI from
// a symbol name or code address.
func (view CoroLibraryEffectView) ExportABIHash(
	function *ssa.Function,
	metadata coro.LibraryEffectMetadata,
) (string, error) {
	u := view.index.universe
	if u == nil || function == nil {
		return "", fmt.Errorf("coroutine library export ABI requires an exact prepared function")
	}
	canonical, ok := u.Resolve(function)
	if !ok || canonical == nil {
		return "", fmt.Errorf("function %q is outside the frozen emission universe", function.Name())
	}
	signature, err := u.coroPhysicalEntrySourceSignature(canonical)
	if err != nil {
		return "", err
	}
	return emissionDigest(framedEmissionKey(
		coroLibraryExportABIDigestDomain,
		metadata.CoroABI,
		metadata.SchedulerABI,
		metadata.PanicABI,
		metadata.FuncRepABI,
		metadata.TargetTriple,
		strconv.Itoa(metadata.PointerBits),
		metadata.Endianness,
		metadata.DataLayout,
		structuralEmissionABITypeKey(signature),
	)), nil
}

// ValidateFunction binds one imported producer fact to the
// exact bodyless declaration selected by this emission universe. FunctionID is
// checked by the build-owned index before this call; this gate independently
// proves the structural ABI and every physical symbol spelling that lowering
// will use.
func (view CoroLibraryEffectView) ValidateFunction(
	function *ssa.Function,
	metadata coro.LibraryEffectMetadata,
	fact coro.LibraryEffectFunction,
) error {
	if _, err := fact.ImportedPolicy(); err != nil {
		return err
	}
	haveABI, err := view.FunctionABIHash(function, metadata)
	if err != nil {
		return fmt.Errorf("coroutine library ABI for %q: %w", fact.ID, err)
	}
	if haveABI != fact.ABIHash {
		return fmt.Errorf(
			"coroutine library ABI hash for %q is %q, producer published %q",
			fact.ID, haveABI, fact.ABIHash,
		)
	}
	base, err := view.FunctionBaseSymbol(function)
	if err != nil {
		return fmt.Errorf("coroutine library symbol for %q: %w", fact.ID, err)
	}
	primary := base
	if fact.Primary == coro.PrimaryCoroutine {
		primary += coroPrimarySuffix
	}
	if fact.PrimarySymbol != primary {
		return fmt.Errorf(
			"coroutine library primary symbol for %q is %q, producer published %q",
			fact.ID, primary, fact.PrimarySymbol,
		)
	}
	if fact.RawPlainSymbol != "" && fact.RawPlainSymbol != base {
		return fmt.Errorf(
			"coroutine library raw-plain symbol for %q is %q, producer published %q",
			fact.ID, base, fact.RawPlainSymbol,
		)
	}
	return nil
}

// validateCoroLibraryEffects repeats the consumer-side boundary proof at cl's
// immutable preflight gate. The build driver owns archive discovery, but code
// generation must still reject a manually assembled Compilation that supplies
// a stale fact, attaches it to another SSA pointer, or lets consumer analysis
// change a producer-owned physical capability.
func (c *Compilation) validateCoroLibraryEffects() error {
	if c == nil || len(c.CoroLibraryEffects) == 0 {
		return nil
	}
	plan := c.immutablePlan()
	universe := c.immutableEmissionUniverse()
	if plan == nil || universe == nil {
		return fmt.Errorf("coroutine library effects require one frozen plan and emission universe")
	}
	consumer := coro.LibraryEffectMetadata{
		FunctionIDSchema:   coro.FunctionIDSchema,
		CoroABI:            c.CoroABI,
		SchedulerABI:       c.SchedulerABI,
		PanicABI:           c.PanicABI,
		FuncRepABI:         c.FuncRepABI,
		TargetTriple:       c.CoroPlanMetadata.TargetTriple,
		TargetCPU:          c.CoroPlanMetadata.TargetCPU,
		TargetFeatures:     c.CoroPlanMetadata.TargetFeatures,
		TargetABI:          c.CoroPlanMetadata.TargetABI,
		PointerBits:        c.CoroPlanMetadata.PointerBits,
		Endianness:         c.CoroPlanMetadata.Endianness,
		DataLayout:         c.CoroPlanMetadata.DataLayout,
		TargetCapabilities: c.CoroTargetCapabilities,
	}
	if err := coro.ValidateLibraryEffectCompatibility(c.CoroLibraryEffectMetadata, consumer); err != nil {
		return fmt.Errorf("coroutine library effect compilation metadata: %w", err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = consumer.CoroABI
	functionIDs.SchedulerABI = consumer.SchedulerABI
	functionIDs.ArchiveReady = true
	for function, fact := range c.CoroLibraryEffects {
		if function == nil {
			return fmt.Errorf("coroutine library effect map contains a nil function")
		}
		canonical, found := universe.Resolve(function)
		if !found || canonical != function {
			return fmt.Errorf(
				"coroutine library effect %q is not attached to one exact canonical emission function",
				fact.ID,
			)
		}
		background, classified, err := universe.FunctionBackground(function)
		if err != nil {
			return fmt.Errorf("coroutine library effect %q frontend classification: %w", fact.ID, err)
		}
		if !classified || background != llssa.InGo || len(function.Blocks) != 0 {
			return fmt.Errorf(
				"coroutine library effect %q requires one bodyless managed-Go declaration",
				fact.ID,
			)
		}
		id, err := coro.StableFunctionID(function, functionIDs)
		if err != nil {
			return fmt.Errorf("coroutine library effect function identity: %w", err)
		}
		if id != fact.ID {
			return fmt.Errorf(
				"coroutine library effect function identity is %q, producer published %q",
				id, fact.ID,
			)
		}
		if err := universe.CoroLibraryEffects().ValidateFunction(function, consumer, fact); err != nil {
			return err
		}
		functionPlan, planned := plan.FunctionPlan(function)
		if !planned {
			return fmt.Errorf("coroutine library effect %q is absent from the final plan", fact.ID)
		}
		if !plan.IgnoresBody(function) ||
			functionPlan.External != coro.ExternalKnown ||
			functionPlan.Primary != coro.PrimaryExternal ||
			functionPlan.DeclaredEffect != fact.Effect ||
			functionPlan.LocalEffect != fact.Effect ||
			functionPlan.Effect != fact.Effect ||
			functionPlan.DeclaredExec != fact.Exec ||
			functionPlan.LocalExec != fact.Exec ||
			functionPlan.Exec != fact.Exec ||
			functionPlan.FuncRep != fact.FuncRep ||
			functionPlan.Emission != coro.EmitNone && functionPlan.Emission != coro.EmitExternal {
			return fmt.Errorf(
				"coroutine library effect %q disagrees with final consumer plan: plan=%+v producer=%+v ignored=%t",
				fact.ID, functionPlan, fact, plan.IgnoresBody(function),
			)
		}
		// RawPlainSymbol is retained in the producer record so a later lowering
		// can bind exact legacy crossings without rediscovering symbols. The v2
		// managed-function consumer does not yet own an external raw-body capability,
		// however: mustRawPlainFunctionSymbol deliberately accepts only a
		// locally defined variant. Reject every imported raw demand here even
		// when the producer named a symbol, rather than accepting metadata that
		// a later emitter cannot honor.
		if functionPlan.RawPlainDemand {
			return fmt.Errorf(
				"coroutine library effect %q has consumer raw-plain demand, which library summary v2 does not lower",
				fact.ID,
			)
		}
		// The v2 managed-function record publishes the primary entry only.
		// Descriptor construction is an
		// independently versioned ABI and cannot be inferred from FuncRep width.
		// An undemanded declaration emits nothing and therefore needs no
		// descriptor in this consumer; reject only an active crossing.
		if fact.FuncRep == coro.Dispatch && functionPlan.Emission != coro.EmitNone {
			return fmt.Errorf(
				"coroutine library effect %q requires an external Dispatch producer, which library summary v2 does not publish",
				fact.ID,
			)
		}
	}
	return nil
}

// FunctionBaseSymbol returns the exact frozen ordinary Go ABI symbol used to
// derive published plain, coroutine, and optional raw entry capabilities.
func (view CoroLibraryEffectView) FunctionBaseSymbol(function *ssa.Function) (string, error) {
	u := view.index.universe
	if u == nil || function == nil {
		return "", fmt.Errorf("missing exact prepared function")
	}
	function, ok := u.Resolve(function)
	if !ok || function == nil {
		return "", fmt.Errorf("function is outside the frozen emission universe")
	}
	var base string
	for _, owner := range u.sortedUseOwners(function) {
		key := emissionFunctionOwnerKey{function: function, owner: owner}
		ftype, legacy, _, valid := splitManagedSymbolKey(u.finalKeys[key])
		if !valid || ftype != goFunc || legacy == "" {
			continue
		}
		// An original initializer in a patched package has a private
		// init$hasPatch role for the compiler-inserted patch edge, but ordinary
		// package imports (and therefore a library summary) name its public init
		// role. That distinction belongs to the frozen owner state; the function
		// pointer alone cannot select the private role.
		if function.Name() == "init" && function.Signature != nil &&
			function.Signature.Recv() == nil {
			if state, frozen := u.ownerStates[function][owner]; frozen &&
				state.state == pkgHasPatch && !state.fromPatch &&
				strings.HasSuffix(legacy, "$hasPatch") {
				legacy = strings.TrimSuffix(legacy, "$hasPatch")
			}
		}
		candidate, err := u.physicalName(owner.ssa, function, legacy)
		if err != nil {
			return "", err
		}
		if base == "" {
			base = candidate
			continue
		}
		if candidate != base {
			return "", fmt.Errorf(
				"function %q has owner-dependent physical symbols %q and %q",
				function.Name(), base, candidate,
			)
		}
	}
	if base == "" {
		return "", fmt.Errorf("function %q has no frozen managed physical symbol", function.Name())
	}
	return base, nil
}

func (p *context) emitCoroLibraryEffectSummary() error {
	if p == nil || p.cacheRegistration || p.compilation == nil ||
		p.emissionOwner == nil || p.compilation.CoroPlanDigest == "" {
		return nil
	}
	plan := p.compilation.immutablePlan()
	universe := p.immutableEmissionUniverse()
	if plan == nil || universe == nil {
		return nil
	}
	metadata, emit, err := p.coroLibraryEffectMetadata()
	if err != nil || !emit {
		return err
	}
	functions := make([]coro.LibraryEffectFunction, 0, len(p.emissionOwner.selected))
	foreignCallables := make([]coro.LibraryEffectForeignCallable, 0)
	exportBindings := make([]coro.LibraryEffectExportBinding, 0)
	seen := make(map[coro.FunctionID]struct{}, len(p.emissionOwner.selected))
	seenForeign := make(map[coro.FunctionID]struct{})
	managedFacts := make(map[coro.FunctionID]coro.LibraryEffectFunction)
	selectedFunctions := make(map[string]*ssa.Function)
	for unresolved := range p.emissionOwner.selected {
		function, ok := universe.Resolve(unresolved)
		if !ok {
			return fmt.Errorf("coroutine library summary: selected function %q is outside the frozen emission universe", unresolved.Name())
		}
		if universe.ownerOf(function) != p.emissionOwner {
			continue
		}
		if function.Pkg != nil && function.Pkg.Pkg != nil {
			selectedFunctions[funcName(function.Pkg.Pkg, function, false)] = function
		}
		functionPlan, ok := plan.FunctionPlan(function)
		if !ok {
			return fmt.Errorf("coroutine library summary: selected function %q has no plan", function.Name())
		}
		identity, hasIdentity, err := universe.CoroCallableIdentityCertificate(function)
		if err != nil {
			return fmt.Errorf("coroutine library summary: callable identity for %q: %w", functionPlan.ID, err)
		}
		if hasIdentity {
			if _, duplicate := seenForeign[functionPlan.ID]; duplicate {
				continue
			}
			contract, hasContract, err := universe.CoroCallableContractCertificate(function)
			if err != nil {
				return fmt.Errorf("coroutine library summary: callable contract for %q: %w", functionPlan.ID, err)
			}
			callable := coro.LibraryEffectForeignCallable{
				Function:    functionPlan.ID,
				Identity:    identity,
				Contract:    contract,
				HasContract: hasContract,
			}
			if err := callable.Validate(); err != nil {
				return fmt.Errorf("coroutine library summary: foreign callable %q: %w", functionPlan.ID, err)
			}
			foreignCallables = append(foreignCallables, callable)
			seenForeign[functionPlan.ID] = struct{}{}
		}
		// Raw-only closure variants are private implementation details. Their
		// effects still contribute to the summarized public callers, but no
		// downstream managed call may name this legacy body as a producer entry.
		if functionPlan.External != coro.Defined ||
			functionPlan.Emission == coro.EmitNone ||
			functionPlan.Emission == coro.EmitExternal ||
			functionPlan.RawPlainOnly {
			continue
		}
		if _, duplicate := seen[functionPlan.ID]; duplicate {
			continue
		}
		entry, err := p.resolveFunctionSymbol(function)
		if err != nil {
			return fmt.Errorf("coroutine library summary: resolve %q: %w", functionPlan.ID, err)
		}
		if !entry.planned || entry.name == "" {
			return fmt.Errorf("coroutine library summary: function %q has no planned managed symbol", functionPlan.ID)
		}
		abiHash, err := universe.CoroLibraryEffects().FunctionABIHash(function, metadata)
		if err != nil {
			return fmt.Errorf("coroutine library summary: effective ABI for %q: %w", functionPlan.ID, err)
		}
		rawPlainSymbol := ""
		if functionPlan.RawPlainEntry {
			if entry.baseName == "" {
				return fmt.Errorf("coroutine library summary: function %q has no raw-plain base symbol", functionPlan.ID)
			}
			rawPlainSymbol = entry.baseName
		}
		fact := coro.LibraryEffectFunction{
			ID:             functionPlan.ID,
			ABIHash:        abiHash,
			Effect:         functionPlan.Effect,
			Exec:           functionPlan.Exec,
			FuncRep:        functionPlan.FuncRep,
			Primary:        functionPlan.Primary,
			PrimarySymbol:  entry.name,
			RawPlainSymbol: rawPlainSymbol,
		}
		if err := universe.CoroLibraryEffects().ValidateFunction(function, metadata, fact); err != nil {
			return fmt.Errorf("coroutine library summary: preflight %q: %w", functionPlan.ID, err)
		}
		functions = append(functions, fact)
		seen[functionPlan.ID] = struct{}{}
		managedFacts[functionPlan.ID] = fact
	}
	for sourceName, symbol := range p.pkg.ExportFuncs() {
		function := selectedFunctions[sourceName]
		if function == nil || symbol == "" {
			continue
		}
		functionPlan, planned := plan.FunctionPlan(function)
		if !planned {
			return fmt.Errorf("coroutine library summary: export %q target %q has no plan", symbol, sourceName)
		}
		fact, managed := managedFacts[functionPlan.ID]
		if !managed {
			// A current raw-only export has no producer-owned managed primary.
			// Do not publish a misleading ingress capability. The export-adapter
			// cutover removes RawPlainOnly and makes this binding mandatory.
			continue
		}
		abiHash, err := universe.CoroLibraryEffects().ExportABIHash(function, metadata)
		if err != nil {
			return fmt.Errorf("coroutine library summary: export ABI for %q: %w", symbol, err)
		}
		exportBindings = append(exportBindings, coro.LibraryEffectExportBinding{
			Symbol:               symbol,
			ABIHash:              abiHash,
			Function:             functionPlan.ID,
			ManagedPrimary:       fact.Primary,
			ManagedPrimarySymbol: fact.PrimarySymbol,
		})
	}
	summary := coro.LibraryEffectSummary{
		Schema:           coro.LibraryEffectSummarySchema,
		Package:          p.emissionOwner.identity,
		Metadata:         metadata,
		Functions:        functions,
		ForeignCallables: foreignCallables,
		ExportBindings:   exportBindings,
	}
	record, err := summary.MarshalRecord()
	if err != nil {
		return fmt.Errorf("coroutine library summary for %q: %w", p.emissionOwner.identity, err)
	}
	digest, err := summary.Digest()
	if err != nil {
		return fmt.Errorf("coroutine library summary digest for %q: %w", p.emissionOwner.identity, err)
	}
	section := coro.LibraryEffectSummarySection
	triple := strings.ToLower(p.prog.TargetSpec().Triple)
	if strings.Contains(triple, "darwin") || strings.Contains(triple, "apple") {
		section = "__LLVM,__llgo_coro"
	}
	if err := p.pkg.AddCompilerMetadataBlob(
		coroLibrarySummarySymbolPrefix+digest[:32], section, record,
	); err != nil {
		return fmt.Errorf("emit coroutine library summary for %q: %w", p.emissionOwner.identity, err)
	}
	return nil
}

func (p *context) coroLibraryEffectMetadata() (coro.LibraryEffectMetadata, bool, error) {
	compilation := p.compilation
	plan := compilation.CoroPlanMetadata
	if plan == (coro.PlanDigestMetadata{}) {
		return coro.LibraryEffectMetadata{}, false, nil
	}
	if plan.CoroABI != compilation.CoroABI ||
		plan.SchedulerABI != compilation.SchedulerABI ||
		plan.PanicABI != compilation.PanicABI ||
		plan.FuncRepABI != compilation.FuncRepABI {
		return coro.LibraryEffectMetadata{}, false, fmt.Errorf(
			"coroutine library summary metadata disagrees with compilation ABI identities",
		)
	}
	return coro.LibraryEffectMetadata{
		FunctionIDSchema:   coro.FunctionIDSchema,
		CoroABI:            plan.CoroABI,
		SchedulerABI:       plan.SchedulerABI,
		PanicABI:           plan.PanicABI,
		FuncRepABI:         plan.FuncRepABI,
		TargetTriple:       plan.TargetTriple,
		TargetCPU:          plan.TargetCPU,
		TargetFeatures:     plan.TargetFeatures,
		TargetABI:          plan.TargetABI,
		PointerBits:        plan.PointerBits,
		Endianness:         plan.Endianness,
		DataLayout:         plan.DataLayout,
		TargetCapabilities: compilation.CoroTargetCapabilities,
	}, true, nil
}
