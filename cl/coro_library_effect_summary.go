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
	coroLibrarySummarySymbolPrefix     = "__llgo_coro_library_effect_v6."
)

// CoroLibraryEffectView is the immutable archive-facing projection of a
// prepared emission universe. It deliberately exposes only stable function
// ABI and symbol proofs, not plan demand or mutable frontend state.
type CoroLibraryEffectView struct {
	index emissionCanonicalIndex
}

// CallableContractDefault reports whether fn's frozen callable contract came
// from the conservative frontend default rather than an explicit source
// declaration. Cross-archive producer metadata may replace only this default;
// an explicit local contract remains authoritative and a disagreement must
// fail closed.
func (view CoroLibraryEffectView) CallableContractDefault(
	fn *ssa.Function,
) (defaulted bool, err error) {
	u := view.index.universe
	if u == nil {
		return false, fmt.Errorf("coroutine callable contract provenance: nil emission universe")
	}
	if fn == nil {
		return false, fmt.Errorf("coroutine callable contract provenance: nil function")
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return false, fmt.Errorf("coroutine callable contract provenance: function has cyclic canonical aliases")
	}
	if _, required := u.required[canonical]; !required {
		return false, fmt.Errorf(
			"coroutine callable contract provenance: function %q is absent from the frozen emission universe",
			canonical.Name(),
		)
	}
	_, defaulted = u.callableDefaults[canonical]
	return defaulted, nil
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
	switch fact.ManagedEntry {
	case coro.ManagedEntryPlain:
	case coro.ManagedEntryCoroutine:
		primary += coroPrimarySuffix
	case coro.ManagedEntryOutcomePlain:
		primary += coroOutcomePlainPrimarySuffix
	default:
		return fmt.Errorf("coroutine library managed entry for %q is %s", fact.ID, fact.ManagedEntry)
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

// ValidateForeignCallable binds one archive producer record to the exact
// frontend C declaration selected by this emission universe. The consumer
// derives the declaration identity independently from its frozen symbol and
// typed ABI; producer metadata may refine only the abstract CallableABI.
// Nothing here chooses a worker, same-M, event, or raw-host operation.
func (view CoroLibraryEffectView) ValidateForeignCallable(
	function *ssa.Function,
	metadata coro.LibraryEffectMetadata,
	fact coro.LibraryEffectForeignCallable,
) error {
	u := view.index.universe
	if u == nil || function == nil {
		return fmt.Errorf("coroutine library foreign callable requires an exact prepared function")
	}
	canonical, ok := u.Resolve(function)
	if !ok || canonical == nil || canonical != function {
		return fmt.Errorf("foreign callable function %q is not one exact canonical emission function", function.Name())
	}
	background, classified, err := u.FunctionBackground(function)
	if err != nil {
		return fmt.Errorf("coroutine library foreign callable %q frontend classification: %w", fact.Function, err)
	}
	if !classified || background != llssa.InC {
		return fmt.Errorf("coroutine library foreign callable %q does not name one frontend C declaration", fact.Function)
	}
	if err := fact.Validate(); err != nil {
		return fmt.Errorf("coroutine library foreign callable %q: %w", fact.Function, err)
	}
	functionIDs := u.FunctionIDConfig()
	functionIDs.CoroABI = metadata.CoroABI
	functionIDs.SchedulerABI = metadata.SchedulerABI
	functionIDs.ArchiveReady = true
	id, err := coro.StableFunctionID(function, functionIDs)
	if err != nil {
		return fmt.Errorf("coroutine library foreign callable identity: %w", err)
	}
	if id != fact.Function {
		return fmt.Errorf(
			"coroutine library foreign callable function identity is %q, producer published %q",
			id, fact.Function,
		)
	}
	local, certified, err := u.CoroCallableIdentityCertificate(function)
	if err != nil {
		return fmt.Errorf("coroutine library foreign callable %q local identity: %w", fact.Function, err)
	}
	if !certified || local.IsZero() {
		return fmt.Errorf("coroutine library foreign callable %q has no local C declaration identity", fact.Function)
	}
	if local.CanonicalFunctionIdentity != fact.Identity.CanonicalFunctionIdentity ||
		local.LinkIdentity != fact.Identity.LinkIdentity ||
		local.TypedABISignature != fact.Identity.TypedABISignature ||
		local.PhysicalSymbol != fact.Identity.PhysicalSymbol ||
		local.PhysicalABISignature != fact.Identity.PhysicalABISignature ||
		local.Origin != fact.Identity.Origin ||
		local.Evidence != fact.Identity.Evidence {
		return fmt.Errorf(
			"coroutine library foreign callable %q producer identity does not match the exact local declaration shape",
			fact.Function,
		)
	}
	// An explicit local CallableABI is source authority and cannot be replaced
	// by an archive. Without one, the producer may publish an exact abstract ABI
	// which the consumer could not reconstruct from the C signature alone.
	if local.CallableABIExplicit {
		if local.CallableABI != fact.Identity.CallableABI ||
			!fact.Identity.CallableABIExplicit ||
			local.ID != fact.Identity.ID {
			return fmt.Errorf(
				"coroutine library foreign callable %q conflicts with the explicit local callable ABI",
				fact.Function,
			)
		}
	} else if !fact.Identity.CallableABIExplicit &&
		local.CallableABI != fact.Identity.CallableABI {
		return fmt.Errorf(
			"coroutine library foreign callable %q derived callable ABI differs from the local declaration",
			fact.Function,
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
	if c == nil ||
		len(c.CoroLibraryEffects) == 0 &&
			len(c.CoroLibraryForeignCallables) == 0 {
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
			functionPlan.ManagedEntry != fact.ManagedEntry ||
			functionPlan.AtomicCost != fact.AtomicCost ||
			functionPlan.AtomicCostProof != fact.AtomicCostProof ||
			functionPlan.AtomicCostCertificate != fact.AtomicCostCertificate ||
			functionPlan.Emission != coro.EmitNone && functionPlan.Emission != coro.EmitExternal {
			return fmt.Errorf(
				"coroutine library effect %q disagrees with final consumer plan: plan=%+v producer=%+v ignored=%t",
				fact.ID, functionPlan, fact, plan.IgnoresBody(function),
			)
		}
		// RawPlainSymbol is retained in the producer record so a later lowering
		// can bind exact legacy crossings without rediscovering symbols. The v5
		// managed-function consumer does not yet own an external raw-body capability,
		// however: mustRawPlainFunctionSymbol deliberately accepts only a
		// locally defined variant. Reject every imported raw demand here even
		// when the producer named a symbol, rather than accepting metadata that
		// a later emitter cannot honor.
		if functionPlan.RawPlainDemand {
			return fmt.Errorf(
				"coroutine library effect %q has consumer raw-plain demand, which library summary v5 does not lower",
				fact.ID,
			)
		}
		// The v5 managed-function record publishes the primary entry only.
		// Descriptor construction is an
		// independently versioned ABI and cannot be inferred from FuncRep width.
		// An undemanded declaration emits nothing and therefore needs no
		// descriptor in this consumer; reject only an active crossing.
		if fact.FuncRep == coro.Dispatch && functionPlan.Emission != coro.EmitNone {
			return fmt.Errorf(
				"coroutine library effect %q requires an external Dispatch producer, which library summary v5 does not publish",
				fact.ID,
			)
		}
	}
	for function, fact := range c.CoroLibraryForeignCallables {
		if function == nil {
			return fmt.Errorf("coroutine library foreign callable map contains a nil function")
		}
		canonical, found := universe.Resolve(function)
		if !found || canonical != function {
			return fmt.Errorf(
				"coroutine library foreign callable %q is not attached to one exact canonical emission function",
				fact.Function,
			)
		}
		if err := universe.CoroLibraryEffects().ValidateForeignCallable(
			function, consumer, fact,
		); err != nil {
			return err
		}
		planIdentity, identityPlanned := plan.CallableIdentityCertificate(function)
		if !identityPlanned || planIdentity != fact.Identity {
			return fmt.Errorf(
				"coroutine library foreign callable %q identity disagrees with the final consumer plan",
				fact.Function,
			)
		}
		planContract, contractPlanned := plan.CallableContractCertificate(function)
		if fact.HasContract {
			if !contractPlanned || planContract != fact.Contract {
				return fmt.Errorf(
					"coroutine library foreign callable %q contract disagrees with the final consumer plan",
					fact.Function,
				)
			}
		} else if contractPlanned || !planContract.IsZero() {
			return fmt.Errorf(
				"coroutine library identity-only foreign callable %q gained a consumer contract",
				fact.Function,
			)
		}
		noBlock, noBlockOK := plan.ForeignNoBlockCertificate(function)
		synchronous, syncOK := plan.ForeignSyncCertificate(function)
		worker, workerOK := plan.ForeignWorkerCertificate(function)
		for _, certificate := range []struct {
			kind  string
			value string
			ok    bool
		}{
			{kind: "noblock", value: noBlock, ok: noBlockOK},
			{kind: "sync", value: synchronous, ok: syncOK},
			{kind: "worker", value: worker, ok: workerOK},
		} {
			if certificate.ok || certificate.value != "" {
				return fmt.Errorf(
					"coroutine library foreign callable %q gained a legacy %s certificate",
					fact.Function, certificate.kind,
				)
			}
		}
		functionPlan, planned := plan.FunctionPlan(function)
		if !planned {
			return fmt.Errorf(
				"coroutine library foreign callable %q is absent from the final plan",
				fact.Function,
			)
		}
		expected, err := fact.ImportedPolicy()
		if err != nil {
			return fmt.Errorf(
				"coroutine library foreign callable %q policy: %w",
				fact.Function, err,
			)
		}
		if !plan.IgnoresBody(function) ||
			functionPlan.External != expected.External ||
			functionPlan.DeclaredEffect != expected.Effect ||
			functionPlan.LocalEffect != expected.Effect ||
			functionPlan.Effect != expected.Effect ||
			functionPlan.DeclaredExec != expected.Exec ||
			functionPlan.LocalExec != expected.Exec ||
			functionPlan.Exec != expected.Exec ||
			functionPlan.FuncRep != coro.DirectPlain ||
			functionPlan.Emission != coro.EmitNone &&
				functionPlan.Emission != coro.EmitExternal {
			return fmt.Errorf(
				"coroutine library foreign callable %q disagrees with final consumer plan: plan=%+v expected=%+v ignored=%t",
				fact.Function, functionPlan, expected, plan.IgnoresBody(function),
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
		contract, hasContract, err := universe.CoroCallableContractCertificate(function)
		if err != nil {
			return fmt.Errorf("coroutine library summary: callable contract for %q: %w", functionPlan.ID, err)
		}
		if imported, ok := p.compilation.importedCoroLibraryForeignCallable(function); ok {
			if imported.Function != functionPlan.ID {
				return fmt.Errorf(
					"coroutine library summary: imported foreign callable function is %q, plan uses %q",
					imported.Function, functionPlan.ID,
				)
			}
			// Re-publish the exact producer fact. The source universe may carry
			// only the consumer's reconstructed conservative default, which is
			// deliberately replaceable and must not leak into a transitive
			// archive.
			identity, hasIdentity = imported.Identity, true
			contract, hasContract = imported.Contract, imported.HasContract
		}
		if hasIdentity {
			if _, duplicate := seenForeign[functionPlan.ID]; duplicate {
				continue
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
			ID:                    functionPlan.ID,
			ABIHash:               abiHash,
			Effect:                functionPlan.Effect,
			Exec:                  functionPlan.Exec,
			FuncRep:               functionPlan.FuncRep,
			Primary:               functionPlan.Primary,
			ManagedEntry:          functionPlan.ManagedEntry,
			AtomicCost:            functionPlan.AtomicCost,
			AtomicCostProof:       functionPlan.AtomicCostProof,
			AtomicCostCertificate: functionPlan.AtomicCostCertificate,
			PrimarySymbol:         entry.name,
			RawPlainSymbol:        rawPlainSymbol,
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
