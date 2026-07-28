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
	"golang.org/x/tools/go/ssa"
)

type CoroCallableIdentityCertificate = coro.CallableIdentityCertificate

// CoroCallableIdentityCertificate returns the immutable identity of one exact
// managed C declaration. It grants no execution policy and is never recovered
// from a physical address or symbol-name lookup.
func (u *EmissionUniverse) CoroCallableIdentityCertificate(fn *ssa.Function) (certificate CoroCallableIdentityCertificate, certified bool, err error) {
	if u == nil {
		return CoroCallableIdentityCertificate{}, false, fmt.Errorf("coroutine callable identity certificate: nil emission universe")
	}
	if fn == nil {
		return CoroCallableIdentityCertificate{}, false, fmt.Errorf("coroutine callable identity certificate: nil function")
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return CoroCallableIdentityCertificate{}, false, fmt.Errorf("coroutine callable identity certificate: function has cyclic canonical aliases")
	}
	if _, required := u.required[canonical]; !required {
		return CoroCallableIdentityCertificate{}, false, fmt.Errorf(
			"coroutine callable identity certificate: function %q is absent from the frozen managed emission universe", canonical.Name(),
		)
	}
	certificate, certified = u.callableIdentities[canonical]
	return certificate, certified, nil
}

// freezeCoroCallableIdentityCertificates inventories every exact C
// declaration already retained by the managed emission universe. Repeated
// physical (symbol, ABI) pairs remain distinct DeclarationRefs because the
// certificate digest also binds canonical and link identities. This scan does
// not globally reject ABI conflicts between different declarations.
func (u *EmissionUniverse) freezeCoroCallableIdentityCertificates() error {
	if u == nil {
		return fmt.Errorf("prepare emission universe: cannot freeze callable identities in a nil universe")
	}
	if u.callableIdentities == nil {
		u.callableIdentities = make(map[*ssa.Function]CoroCallableIdentityCertificate)
	}

	declarations := append([]*ssa.Function(nil), u.functions...)
	for alias := range u.aliases {
		declarations = append(declarations, alias)
	}
	declarations = stableUniqueFunctions(declarations)
	sort.SliceStable(declarations, func(i, j int) bool {
		return u.functionSortKey(declarations[i]) < u.functionSortKey(declarations[j])
	})
	annotations := make(map[*ssa.Function]coroCallableContractCertificate)
	annotationOwners := make(map[*ssa.Function]*ssa.Function)
	for _, declaration := range declarations {
		parsed, present, err := coroCallableContractCertificateFor(declaration)
		if err != nil {
			return fmt.Errorf("prepare emission universe: callable identity annotation on %q: %w", declaration.Name(), err)
		}
		if !present || parsed.Scope != coroCallableContractScopeDeclaration {
			continue
		}
		canonical := u.canonicalAlias(declaration)
		if canonical == nil {
			return fmt.Errorf("prepare emission universe: callable identity annotation on %q has cyclic canonical aliases", declaration.Name())
		}
		if previous := annotationOwners[canonical]; previous != nil && previous != declaration {
			return fmt.Errorf(
				"prepare emission universe: callable contract aliases %q and %q resolve to the same exact canonical function",
				previous.Name(), declaration.Name(),
			)
		}
		annotationOwners[canonical] = declaration
		annotations[canonical] = parsed
	}

	for _, function := range u.functions {
		canonical := u.canonicalAlias(function)
		if canonical == nil {
			return fmt.Errorf("prepare emission universe: callable identity inventory contains cyclic aliases")
		}
		if canonical != function {
			continue
		}
		shape, err := u.freezeCoroCallableShape(canonical)
		if err != nil {
			// Total callable identity is frozen only for managed C declarations.
			// A Pkg-nil Go wrapper may intentionally have one owner-scoped symbol
			// per consuming module; that is not a C declaration ambiguity and must
			// remain invisible unless the wrapper carries an explicit callable
			// contract (the contract freezer retains and diagnoses its shape error).
			managedC := false
			for _, owner := range u.sortedUseOwners(canonical) {
				kind, _, _, ok := splitManagedSymbolKey(u.finalKeys[emissionFunctionOwnerKey{function: canonical, owner: owner}])
				managedC = managedC || ok && kind == cFunc
			}
			if managedC {
				return fmt.Errorf("prepare emission universe: callable identity on %q: %w", canonical.Name(), err)
			}
			continue
		}
		if shape.kind != cFunc {
			continue
		}
		if shape.physicalSymbol == "" || shape.typedABISignature == "" {
			return fmt.Errorf("prepare emission universe: managed C declaration %q has no frozen physical symbol or ABI", canonical.Name())
		}
		baseFunctionIdentity := u.finalIdentity(canonical)
		if baseFunctionIdentity == "" || baseFunctionIdentity == "<nil>" || baseFunctionIdentity == "<cyclic-alias>" {
			return fmt.Errorf("prepare emission universe: managed C declaration %q has no exact canonical function identity", canonical.Name())
		}
		// finalIdentity intentionally models the managed physical key and may be
		// shared by two Go declarations naming the same C symbol+ABI. Bind the
		// stable exact SSA declaration key as well so each one gets a distinct
		// DeclarationRef without changing or disambiguating the physical symbol.
		functionIdentity := framedEmissionKey("cl-callable-exact-declaration-v1", u.functionSortKey(canonical))
		linkIdentity := u.linkIdentities[canonical]
		if linkIdentity == "" {
			return fmt.Errorf("prepare emission universe: managed C declaration %q has no frozen link identity", canonical.Name())
		}

		callableABI := ""
		explicit := false
		if annotation, ok := annotations[canonical]; ok && annotation.ABI != "" {
			callableABI, explicit = annotation.ABI, true
		}
		if callableABI == "" {
			callableABI = derivedCoroCallableTypedABI(shape.typedABISignature)
		}
		certificate, err := coro.FreezeCallableIdentityCertificate(coro.CallableIdentityCertificate{
			CanonicalFunctionIdentity: functionIdentity,
			LinkIdentity:              linkIdentity,
			CallableABI:               callableABI,
			CallableABIExplicit:       explicit,
			TypedABISignature:         shape.typedABISignature,
			PhysicalSymbol:            shape.physicalSymbol,
			PhysicalABISignature:      shape.typedABISignature,
			Origin:                    coro.CallableIdentityOriginManagedCDeclaration,
			Evidence:                  coro.CallableIdentityEvidenceManagedFinalShape,
		})
		if err != nil {
			return fmt.Errorf("prepare emission universe: freeze callable identity on %q: %w", canonical.Name(), err)
		}
		u.callableIdentities[canonical] = certificate
	}
	bindings, err := freezeCoroLocalExportBindings(coroLocalExportBindingFreezeInput{
		functions:             u.functions,
		callableIdentities:    u.callableIdentities,
		linkIdentities:        u.linkIdentities,
		canonicalAlias:        u.canonicalAlias,
		functionSortKey:       u.functionSortKey,
		freezeCallableShape:   u.freezeCoroCallableShape,
		entrySourceSignature:  u.coroPhysicalEntrySourceSignature,
		finalFunctionIdentity: u.finalIdentity,
	})
	if err != nil {
		return err
	}
	u.localExportBindings = bindings
	return nil
}

func derivedCoroCallableTypedABI(signature string) string {
	return "typed.v1/" + emissionDigest(framedEmissionKey(coroCallableTypedABIDomain, signature))
}
