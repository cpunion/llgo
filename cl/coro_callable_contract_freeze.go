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
	"strconv"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

type CoroCallableContractScope = coro.CallableContractScope

const (
	CoroCallableContractScopeDeclaration = coro.CallableContractScopeDeclaration
	CoroCallableContractScopeWrapper     = coro.CallableContractScopeWrapper
)

const (
	coroCallableContractCertificateDomain = "llgo-coro-callable-certificate-v1"
	coroCallableTypedABIDomain            = "llgo-coro-callable-typed-abi-v1"
)

// CoroCallableContractCertificate is the immutable, production-side binding
// between one exact canonical SSA function and its target-neutral callable
// contract. CallableABI is deliberately independent from PhysicalABISignature:
// an annotation may name an abstract transport ABI, while an ordinary typed
// declaration or wrapper gets a stable ABI derived from TypedABISignature.
//
// CanonicalFunctionIdentity and LinkIdentity are diagnostic/audit fields. The
// certificate ID binds them, the contract digest, scope, callable ABI and (for
// declarations) exact physical C symbol/ABI. Consumers must compare ID rather
// than recreating a certificate from these display fields.
type CoroCallableContractCertificate = coro.CallableContractCertificate

type coroCallableFrozenShape struct {
	kind              int
	physicalSymbol    string
	typedABISignature string
}

// CoroCallableContractCertificate returns the construction-time certificate
// for fn. Alias lookup is exact and resolves only through the frozen alias map;
// package/name or physical-address guesses are never accepted. The returned
// value is a copy and cannot mutate the universe.
func (u *EmissionUniverse) CoroCallableContractCertificate(fn *ssa.Function) (certificate CoroCallableContractCertificate, certified bool, err error) {
	if u == nil {
		return CoroCallableContractCertificate{}, false, fmt.Errorf("coroutine callable contract certificate: nil emission universe")
	}
	if fn == nil {
		return CoroCallableContractCertificate{}, false, fmt.Errorf("coroutine callable contract certificate: nil function")
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return CoroCallableContractCertificate{}, false, fmt.Errorf("coroutine callable contract certificate: function has cyclic canonical aliases")
	}
	if _, required := u.required[canonical]; !required {
		return CoroCallableContractCertificate{}, false, fmt.Errorf("coroutine callable contract certificate: function %q is absent from the frozen emission universe", canonical.Name())
	}
	certificate, certified = u.callableContracts[canonical]
	return certificate, certified, nil
}

// freezeCoroCallableContractCertificates converts exact source annotations
// into production certificates only after aliases, final managed symbols and
// link identities have all been frozen. Nothing downstream is permitted to
// reread comments or infer metadata from a code address.
func (u *EmissionUniverse) freezeCoroCallableContractCertificates() error {
	if u == nil {
		return fmt.Errorf("prepare emission universe: cannot freeze callable contracts in a nil universe")
	}

	if u.callableContracts == nil {
		u.callableContracts = make(map[*ssa.Function]CoroCallableContractCertificate)
	}
	shapes := make(map[*ssa.Function]coroCallableFrozenShape, len(u.functions))
	shapeErrors := make(map[*ssa.Function]error)
	for _, function := range u.functions {
		canonical := u.canonicalAlias(function)
		if canonical == nil {
			return fmt.Errorf("prepare emission universe: callable contract inventory contains cyclic aliases")
		}
		if canonical != function {
			continue
		}
		shape, err := u.freezeCoroCallableShape(canonical)
		if err != nil {
			// A contract-free function must not make the new metadata layer
			// observable. Preserve the error and reject it only if an exact
			// annotation later claims this callable.
			shapeErrors[canonical] = err
			continue
		}
		shapes[canonical] = shape
	}

	declarations := append([]*ssa.Function(nil), u.functions...)
	for alias := range u.aliases {
		declarations = append(declarations, alias)
	}
	declarations = stableUniqueFunctions(declarations)
	sort.SliceStable(declarations, func(i, j int) bool {
		return u.functionSortKey(declarations[i]) < u.functionSortKey(declarations[j])
	})

	type exactAnnotation struct {
		declaration *ssa.Function
		canonical   *ssa.Function
		parsed      coroCallableContractCertificate
	}
	annotations := make([]exactAnnotation, 0)
	annotatedCanonical := make(map[*ssa.Function]*ssa.Function)
	legacyCanonical := make(map[*ssa.Function]coroForeignCallDirective)
	for _, declaration := range declarations {
		legacy, err := coroForeignCallDirectiveFor(declaration)
		if err != nil {
			return fmt.Errorf("prepare emission universe: callable contract legacy policy on %q: %w", declaration.Name(), err)
		}
		if legacy != coroForeignCallNone {
			canonical := u.canonicalAlias(declaration)
			if canonical == nil {
				return fmt.Errorf("prepare emission universe: callable contract legacy policy on %q has cyclic canonical aliases", declaration.Name())
			}
			legacyCanonical[canonical] = legacy
		}
		parsed, present, err := coroCallableContractCertificateFor(declaration)
		if err != nil {
			return fmt.Errorf("prepare emission universe: callable contract on %q: %w", declaration.Name(), err)
		}
		if !present {
			continue
		}
		canonical := u.canonicalAlias(declaration)
		if canonical == nil {
			return fmt.Errorf("prepare emission universe: callable contract on %q has cyclic canonical aliases", declaration.Name())
		}
		if _, required := u.required[canonical]; !required {
			return fmt.Errorf("prepare emission universe: callable contract on %q resolves outside the frozen emission universe", declaration.Name())
		}
		if previous := annotatedCanonical[canonical]; previous != nil && previous != declaration {
			return fmt.Errorf(
				"prepare emission universe: callable contract aliases %q and %q resolve to the same exact canonical function",
				previous.Name(), declaration.Name(),
			)
		}
		annotatedCanonical[canonical] = declaration
		annotations = append(annotations, exactAnnotation{
			declaration: declaration,
			canonical:   canonical,
			parsed:      parsed,
		})
	}
	// An exact C declaration without an explicit policy uses LLGo's conservative
	// foreign-call default: it may block, may run on any worker thread, does not
	// reenter Go, and borrows arguments until completion. The frozen function
	// kind is authoritative here: legacy GoPlus C/C++ declarations may retain a
	// dummy Go body that code generation never emits.
	//
	// This is a frozen frontend policy, not a backend inference from a symbol or
	// code address. Explicit target-neutral contracts and legacy noblock/sync/
	// worker policies remain authoritative and mutually
	// exclusive with the default.
	for _, canonical := range u.functions {
		if canonical == nil || u.canonicalAlias(canonical) != canonical ||
			annotatedCanonical[canonical] != nil || legacyCanonical[canonical] != coroForeignCallNone {
			continue
		}
		addressOnly, err := u.coroWorkerAddressOnlyDeclaration(canonical)
		if err != nil {
			return fmt.Errorf(
				"prepare emission universe: classify default callable contract target %q: %w",
				canonical.Name(), err,
			)
		}
		if addressOnly {
			// Its typed zero-argument declaration is only a vehicle for
			// FuncPCABI0. The exact llgo.syscall occurrence derives the real
			// word-call ABI; freezing a typed default here would create a
			// competing, fictitious calling convention.
			continue
		}
		shape, ok := shapes[canonical]
		if !ok || shape.kind != cFunc || shape.physicalSymbol == "" || shape.typedABISignature == "" {
			continue
		}
		parsed := defaultCoroForeignDeclarationContract()
		annotatedCanonical[canonical] = canonical
		annotations = append(annotations, exactAnnotation{
			declaration: canonical,
			canonical:   canonical,
			parsed:      parsed,
		})
	}

	for _, annotation := range annotations {
		declaration, canonical, parsed := annotation.declaration, annotation.canonical, annotation.parsed
		if err := shapeErrors[canonical]; err != nil {
			return fmt.Errorf("prepare emission universe: callable contract on %q: %w", declaration.Name(), err)
		}
		shape, ok := shapes[canonical]
		if !ok {
			return fmt.Errorf("prepare emission universe: callable contract on %q has no frozen typed callable ABI", declaration.Name())
		}
		scope := CoroCallableContractScope(parsed.Scope)
		identity := CoroCallableIdentityCertificate{}
		switch scope {
		case CoroCallableContractScopeDeclaration:
			if shape.kind != cFunc || shape.physicalSymbol == "" || shape.typedABISignature == "" {
				return fmt.Errorf("prepare emission universe: callable declaration contract on %q requires an exact frozen C declaration and physical ABI", declaration.Name())
			}
			var identityOK bool
			identity, identityOK = u.callableIdentities[canonical]
			if !identityOK {
				return fmt.Errorf("prepare emission universe: callable declaration contract on %q has no total callable identity", declaration.Name())
			}
			if err := identity.Validate(); err != nil {
				return fmt.Errorf("prepare emission universe: callable declaration contract on %q has an invalid callable identity: %w", declaration.Name(), err)
			}
		case CoroCallableContractScopeWrapper:
			if shape.kind != goFunc || len(canonical.Blocks) == 0 || shape.typedABISignature == "" {
				return fmt.Errorf("prepare emission universe: callable wrapper contract on %q requires an exact bodyful Go wrapper and typed ABI", declaration.Name())
			}
		default:
			return fmt.Errorf("prepare emission universe: callable contract on %q has invalid frozen scope %q", declaration.Name(), scope)
		}

		functionIdentity, linkIdentity := u.finalIdentity(canonical), u.linkIdentities[canonical]
		callableABI, explicit := parsed.ABI, parsed.ABI != ""
		if scope == CoroCallableContractScopeDeclaration {
			functionIdentity, linkIdentity = identity.CanonicalFunctionIdentity, identity.LinkIdentity
			callableABI, explicit = identity.CallableABI, identity.CallableABIExplicit
			if parsed.ABI != "" && parsed.ABI != callableABI || parsed.ABI == "" && explicit {
				return fmt.Errorf("prepare emission universe: callable contract on %q disagrees with its total callable identity ABI", declaration.Name())
			}
		} else {
			if linkIdentity == "" {
				return fmt.Errorf("prepare emission universe: callable contract on %q has no frozen link identity", declaration.Name())
			}
			if functionIdentity == "" || functionIdentity == "<nil>" || functionIdentity == "<cyclic-alias>" {
				return fmt.Errorf("prepare emission universe: callable contract on %q has no exact canonical function identity", declaration.Name())
			}
			if !explicit {
				if shape.typedABISignature == "" {
					return fmt.Errorf("prepare emission universe: callable contract on %q requires an explicit ABI because its typed ABI is unavailable", declaration.Name())
				}
				callableABI = derivedCoroCallableTypedABI(shape.typedABISignature)
			}
		}
		contractDigest, err := coro.CallableContractBehaviorDigest(parsed.Contract.ID, parsed.Contract)
		if err != nil {
			return fmt.Errorf("prepare emission universe: callable contract on %q has no canonical behavior digest: %w", declaration.Name(), err)
		}
		// foreign.v1 is the source/schema version, not the identity of one
		// behavior.  Two declarations may legitimately use the same schema with
		// different progress, affinity, reentry, or lifetime promises.  Give the
		// frozen behavior its own content-addressed ContractID before it can enter
		// a compilation-wide CallableContractFacts catalog; otherwise the catalog
		// would either reject the second contract as a duplicate or, worse, let a
		// consumer confuse two different behaviors under the shared schema name.
		frozenContract := parsed.Contract
		frozenContract.ID = coro.ContractID(string(parsed.Contract.ID) + "/" + contractDigest)
		frozenTrustedInline := coro.CallableContract{}
		trustedInlineDigest := ""
		if parsed.HasTrustedInlineContract {
			if err := coro.ValidateTrustedInlineCallableContractRefinement(parsed.TrustedInlineContract, parsed.Contract); err != nil {
				return fmt.Errorf("prepare emission universe: callable contract on %q has an invalid trusted-inline refinement: %w", declaration.Name(), err)
			}
			trustedInlineDigest, err = coro.CallableContractBehaviorDigest(
				parsed.TrustedInlineContract.ID, parsed.TrustedInlineContract,
			)
			if err != nil {
				return fmt.Errorf("prepare emission universe: callable contract on %q has no canonical trusted-inline behavior digest: %w", declaration.Name(), err)
			}
			frozenTrustedInline = parsed.TrustedInlineContract
			frozenTrustedInline.ID = coro.ContractID(string(parsed.TrustedInlineContract.ID) + "/" + trustedInlineDigest)
		}
		physicalSymbol, physicalABI := "", ""
		if scope == CoroCallableContractScopeDeclaration {
			physicalSymbol, physicalABI = shape.physicalSymbol, shape.typedABISignature
		}
		id := emissionDigest(framedEmissionKey(
			coroCallableContractCertificateDomain,
			functionIdentity,
			linkIdentity,
			string(scope),
			callableABI,
			strconv.FormatBool(explicit),
			shape.typedABISignature,
			physicalSymbol,
			physicalABI,
			contractDigest,
			strconv.FormatBool(parsed.HasTrustedInlineContract),
			trustedInlineDigest,
		))
		if previous, exists := u.callableContracts[canonical]; exists {
			return fmt.Errorf("prepare emission universe: duplicate frozen callable contract for %q (existing %q, replacement %q)", declaration.Name(), previous.ID, id)
		}
		frozen := CoroCallableContractCertificate{
			ID:                          id,
			CanonicalFunctionIdentity:   functionIdentity,
			LinkIdentity:                linkIdentity,
			Contract:                    frozenContract,
			ContractDigest:              contractDigest,
			TrustedInlineContract:       frozenTrustedInline,
			TrustedInlineContractDigest: trustedInlineDigest,
			HasTrustedInlineContract:    parsed.HasTrustedInlineContract,
			Scope:                       scope,
			CallableABI:                 callableABI,
			CallableABIExplicit:         explicit,
			TypedABISignature:           shape.typedABISignature,
			PhysicalSymbol:              physicalSymbol,
			PhysicalABISignature:        physicalABI,
		}
		if err := frozen.Validate(); err != nil {
			return fmt.Errorf("prepare emission universe: callable contract on %q produced an invalid frozen certificate: %w", declaration.Name(), err)
		}
		if scope == CoroCallableContractScopeDeclaration {
			if err := coro.ValidateCallableContractIdentity(identity, frozen); err != nil {
				return fmt.Errorf("prepare emission universe: callable contract on %q: %w", declaration.Name(), err)
			}
		}
		u.callableContracts[canonical] = frozen
	}
	return nil
}

func defaultCoroForeignDeclarationContract() coroCallableContractCertificate {
	return coroCallableContractCertificate{
		Contract: coro.CallableContract{
			ID:       coroCallableContractIDForeignV1,
			Progress: coro.ProgressMayBlock,
			Affinity: coro.AffinityAnyThread,
			Reentry:  coro.ReentryNone,
			Memory:   coro.MemoryBorrowUntilComplete,
		},
		Scope:     coroCallableContractScopeDeclaration,
		Canonical: "llgo:coro default foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete",
	}
}

func (u *EmissionUniverse) freezeCoroCallableShape(fn *ssa.Function) (coroCallableFrozenShape, error) {
	if u == nil || fn == nil || u.canonicalAlias(fn) != fn {
		return coroCallableFrozenShape{}, fmt.Errorf("prepare emission universe: callable shape requires an exact canonical function")
	}
	owners := u.sortedUseOwners(fn)
	if len(owners) == 0 {
		return coroCallableFrozenShape{}, fmt.Errorf("prepare emission universe: callable shape for %q has no frozen owner", fn.Name())
	}
	shape := coroCallableFrozenShape{kind: ignoredFunc}
	have := false
	firstOwner := ""
	firstKey := ""
	for _, owner := range owners {
		key := u.finalKeys[emissionFunctionOwnerKey{function: fn, owner: owner}]
		kind, symbol, signature, ok := splitManagedSymbolKey(key)
		if !ok || kind == ignoredFunc || signature == "" {
			continue
		}
		if !have {
			shape.kind = kind
			shape.physicalSymbol = symbol
			shape.typedABISignature = signature
			firstOwner = owner.identity
			firstKey = key
			have = true
		} else if shape.kind != kind || shape.physicalSymbol != symbol || shape.typedABISignature != signature {
			return coroCallableFrozenShape{}, fmt.Errorf(
				"prepare emission universe: function %q has owner-dependent typed callable ABI: owner %q key %q conflicts with owner %q key %q",
				fn.Name(), firstOwner, firstKey, owner.identity, key,
			)
		}
	}
	if !have {
		return coroCallableFrozenShape{}, nil
	}
	return shape, nil
}
