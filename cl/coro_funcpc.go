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
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

const (
	coroFuncPCABI0PackagePath      = "internal/abi"
	coroFuncPCABI0LocalName        = "FuncPCABI0"
	coroFuncPCABIInternalLocalName = "FuncPCABIInternal"
	coroFuncPCABIInternalIntrinsic = "funcPCABIInternal"
)

func coroFuncPCIntrinsicName(localName string) string {
	if localName == coroFuncPCABIInternalLocalName {
		return coroFuncPCABIInternalIntrinsic
	}
	return "funcPCABI0"
}

// aliasPatchedFuncPCABI0Declarations records the one intentional cross-kind
// patch replacement used by Go's internal/abi package. The upstream package
// owns a bodyless Go declaration while LLGo's alternate package owns the
// compiler intrinsic that implements it. Their managed keys cannot collide:
// one is a Go symbol and the other is llgo.funcPCABI0.
//
// This bridge is deliberately narrower than managed-symbol canonicalization.
// It considers only the exact original and alternate packages already paired
// by one prepared Patch, requires the same package-scope source name and exact
// structural ABI signature, and consumes only frozen frontend classification.
// It never searches another package or guesses from Function.String/Name.
func (u *EmissionUniverse) aliasPatchedFuncPCABI0Declarations() error {
	if u == nil {
		return fmt.Errorf("prepare emission universe: cannot alias patched FuncPCABI0 in a nil universe")
	}
	packages := make([]*preparedEmissionPackage, 0, len(u.packages))
	for _, prepared := range u.packages {
		if prepared != nil && prepared.hasPatch && !prepared.metadataOnly && prepared.pkgPath == coroFuncPCABI0PackagePath {
			packages = append(packages, prepared)
		}
	}
	sort.SliceStable(packages, func(i, j int) bool {
		if packages[i].order != packages[j].order {
			return packages[i].order < packages[j].order
		}
		return packages[i].identity < packages[j].identity
	})

	type operation struct {
		owner       *preparedEmissionPackage
		original    *ssa.Function
		intrinsic   *ssa.Function
		originalKey string
	}
	operations := make([]operation, 0, len(packages))
	for _, prepared := range packages {
		for _, localName := range []string{coroFuncPCABI0LocalName, coroFuncPCABIInternalLocalName} {
			original, _ := prepared.ssa.Members[localName].(*ssa.Function)
			if !coroFuncPCBodylessDeclaration(original, localName) {
				continue
			}
			intrinsic, _ := prepared.patch.Alt.Members[localName].(*ssa.Function)
			if intrinsic == nil || intrinsic.Parent() != nil || intrinsic.Signature == nil || intrinsic.Signature.Recv() != nil ||
				intrinsic.TypeParams() != nil || intrinsic.TypeArgs() != nil {
				continue
			}

			originalOwnerKey := emissionFunctionOwnerKey{function: original, owner: prepared}
			originalKind, originalKindOK := u.functionKinds[originalOwnerKey]
			originalKey, originalKeyOK := u.finalKeys[originalOwnerKey]
			originalKeyKind, originalSymbol, originalSignature, originalKeyValid := splitManagedSymbolKey(originalKey)
			if !originalKindOK || originalKind != goFunc || !originalKeyOK || !originalKeyValid || originalKeyKind != goFunc ||
				originalSymbol != coroFuncPCABI0PackagePath+"."+localName {
				continue
			}

			intrinsicOwnerKey := emissionFunctionOwnerKey{function: intrinsic, owner: prepared}
			intrinsicKind, intrinsicKindOK := u.functionKinds[intrinsicOwnerKey]
			intrinsicOpcode, intrinsicOpcodeOK := u.intrinsicOps[intrinsicOwnerKey]
			if !intrinsicKindOK || intrinsicKind != llgoInstr || !intrinsicOpcodeOK || intrinsicOpcode != llgoFuncPCABI0 ||
				intrinsic.Signature == nil {
				continue
			}
			intrinsicSignature := u.emissionTypeKeys.strictABI(u.effectiveType(prepared, intrinsic, intrinsic.Signature, false))
			if originalSignature != intrinsicSignature {
				return fmt.Errorf(
					"prepare emission universe: patched internal/abi.%s declaration and alternate intrinsic have different structural ABI signatures", localName,
				)
			}

			// selectFunction normally canonicalizes duplicate intrinsic declarations
			// by managed key. That is correct for ordinary intrinsic calls, but using
			// such a winner here would make the patch bridge depend on an unrelated
			// alternate source name. Require one exact same-signature declaration.
			matches := make([]*ssa.Function, 0, 2)
			for _, member := range prepared.patch.Alt.Members {
				candidate, ok := member.(*ssa.Function)
				if !ok || candidate.Parent() != nil {
					continue
				}
				if candidate.Name() != localName &&
					(candidate.Name() == coroFuncPCABI0LocalName || candidate.Name() == coroFuncPCABIInternalLocalName) {
					// The two sanctioned source intrinsics intentionally share one
					// opcode but own different frozen intrinsic symbols.
					continue
				}
				candidateOwnerKey := emissionFunctionOwnerKey{function: candidate, owner: prepared}
				candidateKind, kindOK := u.functionKinds[candidateOwnerKey]
				candidateOpcode, opcodeOK := u.intrinsicOps[candidateOwnerKey]
				candidateSignature := ""
				if candidate.Signature != nil {
					candidateSignature = u.emissionTypeKeys.strictABI(u.effectiveType(prepared, candidate, candidate.Signature, false))
				}
				if kindOK && candidateKind == llgoInstr && opcodeOK && candidateOpcode == llgoFuncPCABI0 &&
					candidateSignature == originalSignature {
					matches = append(matches, candidate)
				}
			}
			if len(matches) != 1 || matches[0] != intrinsic {
				diagnostics := make([]string, len(matches))
				for index, candidate := range matches {
					diagnostics[index] = emissionFunctionDiagnostic(candidate)
				}
				sort.Strings(diagnostics)
				return fmt.Errorf(
					"prepare emission universe: patched internal/abi.%s has ambiguous alternate intrinsic replacements: %s",
					localName, strings.Join(diagnostics, ", "),
				)
			}
			if canonical := u.canonicalAlias(intrinsic); canonical == nil || canonical != intrinsic {
				return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 alternate intrinsic is not canonical")
			}
			intrinsicKey, intrinsicKeyOK := u.finalKeys[intrinsicOwnerKey]
			intrinsicKeyKind, intrinsicSymbol, frozenIntrinsicSignature, intrinsicKeyValid := splitManagedSymbolKey(intrinsicKey)
			if !intrinsicKeyOK || !intrinsicKeyValid || intrinsicKeyKind != llgoInstr || intrinsicSymbol != coroFuncPCIntrinsicName(localName) ||
				frozenIntrinsicSignature != intrinsicSignature {
				return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 alternate intrinsic has inconsistent frozen managed-symbol metadata")
			}
			if canonical := u.canonicalAlias(original); canonical == nil || canonical != original {
				return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 original declaration is not canonical before patch aliasing")
			}
			if _, required := u.required[original]; !required {
				return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 original declaration is not selected")
			}
			if _, required := u.required[intrinsic]; !required {
				return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 alternate intrinsic is not selected")
			}
			if winner := prepared.winners[originalKey]; winner != original {
				return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 original declaration is not its exact managed winner")
			}
			if !prepared.fromPatch[intrinsic] || prepared.fromPatch[original] {
				return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 has inconsistent original/alternate provenance")
			}
			if err := u.validatePatchedFuncPCABI0AliasLifecycle(prepared, original, intrinsic); err != nil {
				return err
			}
			operations = append(operations, operation{owner: prepared, original: original, intrinsic: intrinsic, originalKey: originalKey})
		}
	}

	for _, operation := range operations {
		prepared, original, intrinsic := operation.owner, operation.original, operation.intrinsic
		u.aliases[original] = intrinsic
		for alias, canonical := range u.aliases {
			if canonical == original {
				u.aliases[alias] = intrinsic
			}
		}
		if prepared.winners[operation.originalKey] == original {
			delete(prepared.winners, operation.originalKey)
		}
		delete(prepared.fromPatch, original)
		for owner := range u.useOwners[original] {
			ownerKey := emissionFunctionOwnerKey{function: original, owner: owner}
			delete(u.functionKinds, ownerKey)
			delete(u.intrinsicOps, ownerKey)
			delete(u.finalKeys, ownerKey)
			delete(u.physicalNames, ownerKey)
		}
		delete(u.required, original)
		delete(u.useOwners, original)
		delete(u.ownerStates, original)
		delete(u.fnOwners, original)
		delete(u.fnStates, original)
		delete(u.excluded, original)
		delete(u.foreignNoBlock, original)
		delete(u.foreignSync, original)
		delete(u.foreignWorker, original)
		delete(u.linkIdentities, original)
		delete(u.linkOnceNames, original)
	}
	return nil
}

func coroFuncPCBodylessDeclaration(function *ssa.Function, localName string) bool {
	if function == nil || function.Pkg == nil || function.Parent() != nil || function.Signature == nil || function.Signature.Recv() != nil ||
		function.TypeParams() != nil || function.TypeArgs() != nil || functionNeedsLinkOnce(function) || len(function.Blocks) != 0 {
		return false
	}
	declaration, _ := function.Syntax().(*ast.FuncDecl)
	return declaration != nil && declaration.Body == nil && declaration.Recv == nil && declaration.Name != nil &&
		declaration.Name.Name == localName
}

func (u *EmissionUniverse) validatePatchedFuncPCABI0AliasLifecycle(owner *preparedEmissionPackage, original, intrinsic *ssa.Function) error {
	if _, materialized := u.materialized[original]; materialized || len(u.materializedOwners[original]) != 0 ||
		len(u.abiMethodReferences[original]) != 0 || len(u.abiSyncReferences[original]) != 0 ||
		len(u.managedValueReferences[original]) != 0 || len(u.loweredCalls[original]) != 0 ||
		len(u.plainLoweredCalls[original]) != 0 || len(u.normalReturnBlocks[original]) != 0 {
		return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 original declaration was materialized before exact aliasing")
	}
	owners := u.useOwners[original]
	if len(owners) != 1 {
		return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 original declaration has %d frozen use owners; want exact patch owner", len(owners))
	}
	if _, ok := owners[owner]; !ok {
		return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 original declaration is not owned by its exact patch")
	}
	state, stateOK := u.ownerStates[original][owner]
	if !stateOK || state.fromPatch || state.state != pkgHasPatch || u.fnOwners[original] != owner {
		return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 original declaration has incomplete frozen provenance")
	}
	intrinsicOwners := u.useOwners[intrinsic]
	if len(intrinsicOwners) != 1 {
		return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 alternate intrinsic has %d frozen use owners; want exact patch owner", len(intrinsicOwners))
	}
	if _, ok := intrinsicOwners[owner]; !ok {
		return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 alternate intrinsic is not owned by its exact patch")
	}
	intrinsicState, stateOK := u.ownerStates[intrinsic][owner]
	if !stateOK || !intrinsicState.fromPatch || intrinsicState.state != pkgInPatch || u.fnOwners[intrinsic] != owner {
		return fmt.Errorf("prepare emission universe: patched internal/abi.FuncPCABI0 alternate intrinsic has incomplete frozen provenance")
	}
	return nil
}

// validateCoroFuncPCABI0CallSite freezes the same operand shapes consumed by
// funcPCABI0Value. The intrinsic emits only address selection/load operations;
// a structurally exposed Go function is still required to belong to the exact
// emission universe so its selected entry representation cannot drift later.
func (u *EmissionUniverse) validateCoroFuncPCABI0CallSite(direct *ssa.Call) error {
	if direct == nil || direct.Common() == nil || direct.Common().IsInvoke() {
		return fmt.Errorf("emission universe intrinsic call semantics: llgo.funcPCABI0 must be an exact direct call")
	}
	args := direct.Common().Args
	signature := direct.Common().Signature()
	if len(args) != 1 || signature == nil || signature.Recv() != nil || signature.Variadic() ||
		signature.Params() == nil || signature.Params().Len() != 1 ||
		signature.Results() == nil || signature.Results().Len() != 1 {
		return fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcPCABI0 call %q requires the exact func(any) uintptr shape", direct.String(),
		)
	}
	parameter, ok := types.Unalias(signature.Params().At(0).Type()).Underlying().(*types.Interface)
	if !ok || !parameter.Empty() {
		return fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcPCABI0 call %q requires the exact func(any) uintptr shape", direct.String(),
		)
	}
	result, ok := types.Unalias(signature.Results().At(0).Type()).Underlying().(*types.Basic)
	if !ok || result.Kind() != types.Uintptr {
		return fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcPCABI0 call %q requires the exact func(any) uintptr shape", direct.String(),
		)
	}
	if err := u.validateCoroFuncPCABI0Value(args[0]); err != nil {
		return fmt.Errorf("emission universe intrinsic call semantics: llgo.funcPCABI0 call %q: %w", direct.String(), err)
	}
	return nil
}

func (u *EmissionUniverse) validateCoroFuncPCABI0Value(value ssa.Value) error {
	switch value := value.(type) {
	case *ssa.MakeInterface:
		return u.validateCoroFuncPCABI0Value(value.X)
	case *ssa.Function:
		if extractTrampolineCName(value.Name()) != "" {
			return nil
		}
		if canonical, resolved := u.Resolve(value); !resolved || canonical == nil {
			return fmt.Errorf("target function %q is outside the frozen emission universe", value.Name())
		}
		return nil
	case *ssa.MakeClosure:
		return u.validateCoroFuncPCABI0Value(value.Fn)
	case *ssa.Const:
		if value.IsNil() {
			return fmt.Errorf("argument is statically nil")
		}
		return fmt.Errorf("argument has unsupported SSA type %T", value)
	default:
		if value != nil && value.Type() != nil {
			if _, ok := types.Unalias(value.Type()).Underlying().(*types.Interface); ok {
				return nil
			}
		}
		return fmt.Errorf("argument has unsupported SSA type %T", value)
	}
}

// coroFuncPCABI0RawStaticOperand reports the structural function-address form
// whose transient MakeInterface must not by itself demand a dispatch wrapper.
// Dynamic interface values remain ordinary ABI roots and return false.
func coroFuncPCABI0RawStaticOperand(direct *ssa.Call) bool {
	target, exact := coroFuncPCABI0ExactStaticOperand(direct)
	if !exact {
		return false
	}
	// funcPCABI0Value does not compile a Go function value for C trampolines;
	// it synthesizes the foreign declaration and takes that address directly.
	// Do not advertise such an operand as a managed raw-function singleton to
	// the coroutine analyzer, whose raw-address proof intentionally requires a
	// canonical target in the emission universe.
	return extractTrampolineCName(target.Name()) == ""
}

func coroFuncPCABI0ExactStaticOperand(direct *ssa.Call) (*ssa.Function, bool) {
	if direct == nil || direct.Common() == nil || len(direct.Common().Args) != 1 {
		return nil, false
	}
	boxed, ok := direct.Common().Args[0].(*ssa.MakeInterface)
	if !ok {
		return nil, false
	}
	refs := boxed.Referrers()
	if refs == nil || len(*refs) != 1 || (*refs)[0] != direct {
		return nil, false
	}
	target, ok := boxed.X.(*ssa.Function)
	if !ok || target == nil || len(target.FreeVars) != 0 {
		return nil, false
	}
	return target, true
}
