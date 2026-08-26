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
	"strings"
	"unicode/utf8"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// CoroPlanningMetadataView is the immutable analysis-facing projection of a
// prepared emission universe. Compiler planning consumes exact frontend-added
// references through this view so each new metadata family does not create a
// second direct plan-authority surface.
type CoroPlanningMetadataView struct {
	index emissionCanonicalIndex
}

// ManagedGenerationEntry reports whether ModeGen may publish fn as an
// independently callable managed Go entry. It is narrower than InGo: cmd/cgo
// worker adapters also have frontend-emitted Go bodies, but their only valid
// entry is the exact raw/native-stack target carried by a frozen worker call
// certificate. The answer is frozen per emission owner in ProgramIR; build
// must not reconstruct it from a function name or source directive.
func (view CoroPlanningMetadataView) ManagedGenerationEntry(fn *ssa.Function) (bool, error) {
	u := view.index.universe
	if u == nil || u.coroProgramIR == nil {
		return false, fmt.Errorf("coroutine managed generation entry: nil emission universe or ProgramIR")
	}
	if fn == nil {
		return false, fmt.Errorf("coroutine managed generation entry: nil function")
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return false, fmt.Errorf("coroutine managed generation entry: function has cyclic canonical aliases")
	}
	if _, required := u.required[canonical]; !required {
		return false, fmt.Errorf(
			"coroutine managed generation entry: function %q is absent from the frozen emission universe",
			canonical.Name(),
		)
	}
	owners := u.sortedUseOwners(canonical)
	if len(owners) == 0 {
		return false, fmt.Errorf(
			"coroutine managed generation entry: function %q has no frozen owner",
			canonical.Name(),
		)
	}
	eligible := false
	for index, owner := range owners {
		preamble, err := u.coroProgramIR.functionPreambleForOwner(canonical, owner)
		if err != nil {
			return false, fmt.Errorf(
				"coroutine managed generation entry: function %q owner %q: %w",
				canonical.Name(), owner.identity, err,
			)
		}
		if index != 0 && eligible != preamble.managedGenerationEntry {
			return false, fmt.Errorf(
				"coroutine managed generation entry: function %q has owner-dependent eligibility",
				canonical.Name(),
			)
		}
		eligible = preamble.managedGenerationEntry
	}
	return eligible, nil
}

// ExportIngressCertificate returns the immutable candidate binding for an
// exact bodyful //export definition. Target selection still decides whether
// the current platform has a physical ingress implementation; a certificate
// alone never authorizes code generation.
func (view CoroPlanningMetadataView) ExportIngressCertificate(
	fn *ssa.Function,
) (certificateID string, certified bool, err error) {
	u := view.index.universe
	if u == nil || fn == nil {
		return "", false, fmt.Errorf("coroutine export ingress certificate requires one prepared function")
	}
	canonical, ok := u.Resolve(fn)
	if !ok || canonical == nil || canonical != fn {
		return "", false, fmt.Errorf(
			"coroutine export ingress function %q is not one canonical emitted definition",
			fn.Name(),
		)
	}
	certificate, certified := u.exportIngressBindings[canonical]
	return certificate.ID, certified, nil
}

func (view CoroPlanningMetadataView) exportIngressCertificate(
	fn *ssa.Function,
) (coroExportIngressCertificate, bool) {
	u := view.index.universe
	if u == nil || fn == nil {
		return coroExportIngressCertificate{}, false
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil || canonical != fn {
		return coroExportIngressCertificate{}, false
	}
	certificate, ok := u.exportIngressBindings[canonical]
	return certificate, ok
}

// ManagedValueReferences returns exact managed function values introduced by
// compiler lowering without a source SSA operand. They use descriptor
// transport and are disjoint from raw ABI method/code-address references.
func (view CoroPlanningMetadataView) ManagedValueReferences(owner *ssa.Function) ([]*ssa.Function, error) {
	u := view.index.universe
	if u == nil {
		return nil, fmt.Errorf("coroutine managed value references require a prepared emission universe")
	}
	return u.coroFrozenABIReferences(owner, u.managedValueReferences, "managed value")
}

// PlainLoweredCalls returns exact compiler-inserted helper occurrences in an
// owner's ordinary Go ABI/legacy-stack representation. They contribute raw
// plain demand but never become managed call edges.
func (view CoroPlanningMetadataView) PlainLoweredCalls(owner *ssa.Function) ([]coro.SSALoweredCall, error) {
	u := view.index.universe
	if u == nil || owner == nil {
		return nil, fmt.Errorf("coroutine plain lowered calls require a prepared universe and exact owner")
	}
	canonical := u.canonicalAlias(owner)
	if canonical == nil || canonical != owner {
		return nil, fmt.Errorf("coroutine plain lowered-call owner %q is not exact canonical", owner.Name())
	}
	if _, frozen := u.required[owner]; !frozen {
		return nil, fmt.Errorf("coroutine plain lowered-call owner %q is outside the frozen emission universe", owner.Name())
	}
	byName := u.plainLoweredCalls[owner]
	calls := make([]coro.SSALoweredCall, 0, len(byName))
	for logicalName, target := range byName {
		if logicalName == "" || !utf8.ValidString(logicalName) || strings.IndexByte(logicalName, 0) >= 0 || target == nil {
			return nil, fmt.Errorf("coroutine plain lowered call %q in %q has invalid frozen metadata", logicalName, owner.Name())
		}
		if canonicalTarget := u.canonicalAlias(target); canonicalTarget == nil || canonicalTarget != target {
			return nil, fmt.Errorf("coroutine plain lowered call %q in %q has a non-canonical target", logicalName, owner.Name())
		}
		if _, frozen := u.required[target]; !frozen {
			return nil, fmt.Errorf("coroutine plain lowered call %q in %q targets a helper outside the frozen emission universe", logicalName, owner.Name())
		}
		calls = append(calls, coro.SSALoweredCall{
			LogicalName: logicalName,
			Target:      target,
			RawPlain:    true,
		})
	}
	sort.Slice(calls, func(i, j int) bool {
		return calls[i].LogicalName < calls[j].LogicalName
	})
	return calls, nil
}
