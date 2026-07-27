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
)

const (
	coroLibraryFunctionABIDigestDomain = "llgo.coro.library-function-abi.v1"
	coroLibrarySummarySymbolPrefix     = "__llgo_coro_library_effect_v1."
)

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
	seen := make(map[coro.FunctionID]struct{}, len(p.emissionOwner.selected))
	for unresolved := range p.emissionOwner.selected {
		function, ok := universe.Resolve(unresolved)
		if !ok {
			return fmt.Errorf("coroutine library summary: selected function %q is outside the frozen emission universe", unresolved.Name())
		}
		if universe.ownerOf(function) != p.emissionOwner {
			continue
		}
		functionPlan, ok := plan.FunctionPlan(function)
		if !ok {
			return fmt.Errorf("coroutine library summary: selected function %q has no plan", function.Name())
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
		signature, err := universe.coroPhysicalEntrySourceSignature(function)
		if err != nil {
			return fmt.Errorf("coroutine library summary: effective ABI for %q: %w", functionPlan.ID, err)
		}
		abiHash := emissionDigest(framedEmissionKey(
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
		))
		rawPlainSymbol := ""
		if functionPlan.RawPlainEntry {
			if entry.baseName == "" {
				return fmt.Errorf("coroutine library summary: function %q has no raw-plain base symbol", functionPlan.ID)
			}
			rawPlainSymbol = entry.baseName
		}
		functions = append(functions, coro.LibraryEffectFunction{
			ID:             functionPlan.ID,
			ABIHash:        abiHash,
			Effect:         functionPlan.Effect,
			Exec:           functionPlan.Exec,
			FuncRep:        functionPlan.FuncRep,
			Primary:        functionPlan.Primary,
			PrimarySymbol:  entry.name,
			RawPlainSymbol: rawPlainSymbol,
		})
		seen[functionPlan.ID] = struct{}{}
	}
	summary := coro.LibraryEffectSummary{
		Schema:    coro.LibraryEffectSummarySchema,
		Package:   p.emissionOwner.identity,
		Metadata:  metadata,
		Functions: functions,
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
