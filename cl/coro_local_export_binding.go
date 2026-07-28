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

const coroLocalExportBindingCertificateDomain = "llgo-coro-local-export-binding-certificate-v1"

// coroLocalExportBindingCertificate binds one exact managed C declaration to
// one exact bodyful Go definition which publishes the same local //export
// symbol and structural C ABI. It is immutable builder evidence, not another
// public plan authority: the frozen CallSitePlan publishes only its digest and
// exact target to analysis, validation, and lowering.
type coroLocalExportBindingCertificate struct {
	ID                     string
	DeclarationIdentity    string
	TargetFunctionIdentity string
	TargetLinkIdentity     string
	PhysicalSymbol         string
	PhysicalABISignature   string
}

type coroLocalExportBinding struct {
	target      *ssa.Function
	certificate coroLocalExportBindingCertificate
}

type coroLocalExportTargetGroup struct {
	targets []*ssa.Function
}

// coroLocalExportManagedCallTarget selects the local Go implementation only
// for one exact managed call, defer, or spawn occurrence. It deliberately does
// not alias the declaration: raw code-address transport and a raw/plain
// variant of the same SSA body continue to call the exported C symbol.
func coroLocalExportManagedCallTarget(
	bindings map[*ssa.Function]coroLocalExportBinding,
	required map[*ssa.Function]none,
	canonicalAlias func(*ssa.Function) *ssa.Function,
	call ssa.CallInstruction,
) (*ssa.Function, coroLocalExportBindingCertificate, bool, error) {
	if call == nil || call.Common() == nil {
		return nil, coroLocalExportBindingCertificate{}, false, nil
	}
	switch call.(type) {
	case *ssa.Call, *ssa.Defer, *ssa.Go:
	default:
		return nil, coroLocalExportBindingCertificate{}, false, nil
	}
	common := call.Common()
	declaration, exact := common.Value.(*ssa.Function)
	if !exact || declaration == nil || common.StaticCallee() != declaration ||
		common.IsInvoke() || common.Method != nil {
		return nil, coroLocalExportBindingCertificate{}, false, nil
	}
	canonical := canonicalAlias(declaration)
	if canonical == nil {
		return nil, coroLocalExportBindingCertificate{}, false, fmt.Errorf(
			"local export managed call has cyclic declaration aliases",
		)
	}
	if canonical != declaration {
		return nil, coroLocalExportBindingCertificate{}, false, nil
	}
	binding, bound := bindings[declaration]
	if !bound {
		return nil, coroLocalExportBindingCertificate{}, false, nil
	}
	target, certificate := binding.target, binding.certificate
	if target == nil || target == declaration || canonicalAlias(target) != target ||
		len(target.Blocks) == 0 || len(target.FreeVars) != 0 {
		return nil, coroLocalExportBindingCertificate{}, false, fmt.Errorf(
			"local export declaration %q has a stale managed target",
			declaration.Name(),
		)
	}
	if _, retained := required[declaration]; !retained {
		return nil, coroLocalExportBindingCertificate{}, false, fmt.Errorf(
			"local export declaration %q is outside the frozen emission universe",
			declaration.Name(),
		)
	}
	if _, retained := required[target]; !retained {
		return nil, coroLocalExportBindingCertificate{}, false, fmt.Errorf(
			"local export managed target %q is outside the frozen emission universe",
			target.Name(),
		)
	}
	if declaration.Signature == nil || target.Signature == nil ||
		!types.Identical(declaration.Signature, target.Signature) ||
		!types.Identical(common.Signature(), target.Signature) {
		// A structural C ABI match is sufficient to bind link identities, but
		// an SSA edge substitution also has to preserve exact Go operand types.
		// Leave representational adapters on the conservative foreign path.
		return nil, coroLocalExportBindingCertificate{}, false, nil
	}
	if len(common.Args) != target.Signature.Params().Len() {
		return nil, coroLocalExportBindingCertificate{}, false, fmt.Errorf(
			"local export managed call to %q has %d arguments, want %d",
			declaration.Name(), len(common.Args), target.Signature.Params().Len(),
		)
	}
	for index, argument := range common.Args {
		if argument == nil ||
			!types.Identical(argument.Type(), target.Signature.Params().At(index).Type()) {
			return nil, coroLocalExportBindingCertificate{}, false, fmt.Errorf(
				"local export managed call to %q has an incompatible argument %d",
				declaration.Name(), index,
			)
		}
	}
	if certificate.ID == "" ||
		certificate.PhysicalSymbol == "" ||
		certificate.DeclarationIdentity == "" ||
		certificate.TargetFunctionIdentity == "" {
		return nil, coroLocalExportBindingCertificate{}, false, fmt.Errorf(
			"local export declaration %q has an incomplete binding certificate",
			declaration.Name(),
		)
	}
	return target, certificate, true, nil
}

type coroLocalExportBindingFreezeInput struct {
	functions             []*ssa.Function
	callableIdentities    map[*ssa.Function]CoroCallableIdentityCertificate
	linkIdentities        map[*ssa.Function]string
	canonicalAlias        func(*ssa.Function) *ssa.Function
	functionSortKey       func(*ssa.Function) string
	freezeCallableShape   func(*ssa.Function) (coroCallableFrozenShape, error)
	entrySourceSignature  func(*ssa.Function) (*types.Signature, error)
	finalFunctionIdentity func(*ssa.Function) string
}

// freezeCoroLocalExportBindings joins only compiler-visible source facts:
//
//   - an unannotated exact managed C declaration;
//   - one bodyful local Go function with a single, unambiguous //export;
//   - the same frozen final physical symbol; and
//   - the same structural C ABI after patch/type normalization.
//
// Multiple export targets for one symbol+ABI deliberately leave the binding
// absent. This phase never chooses a definition by source name, package name,
// address, or link order.
func freezeCoroLocalExportBindings(
	input coroLocalExportBindingFreezeInput,
) (map[*ssa.Function]coroLocalExportBinding, error) {
	bindings := make(map[*ssa.Function]coroLocalExportBinding)
	if input.canonicalAlias == nil || input.functionSortKey == nil ||
		input.freezeCallableShape == nil || input.entrySourceSignature == nil ||
		input.finalFunctionIdentity == nil {
		return nil, fmt.Errorf("prepare emission universe: local export binding freezer has incomplete builder inputs")
	}

	functions := append([]*ssa.Function(nil), input.functions...)
	functions = stableUniqueFunctions(functions)
	sort.SliceStable(functions, func(i, j int) bool {
		return input.functionSortKey(functions[i]) < input.functionSortKey(functions[j])
	})

	targets := make(map[string]*coroLocalExportTargetGroup)
	targetABI := make(map[*ssa.Function]string)
	for _, function := range functions {
		canonical := input.canonicalAlias(function)
		if canonical == nil {
			return nil, fmt.Errorf("prepare emission universe: local export target inventory contains cyclic aliases")
		}
		if canonical != function || len(canonical.Blocks) == 0 || len(canonical.FreeVars) != 0 {
			continue
		}
		symbol, exported := exactLocalCExportSymbol(canonical)
		if !exported || symbol == "" {
			continue
		}
		// Parsing the rare //export candidate before deriving its complete
		// callable shape keeps this phase linear but cheap for ordinary Go
		// functions in a large runtime/standard-library emission universe.
		shape, err := input.freezeCallableShape(canonical)
		if err != nil || shape.kind != goFunc || shape.physicalSymbol != symbol {
			continue
		}
		signature := canonical.Signature
		if signature == nil || signature.Recv() != nil || signature.Variadic() ||
			typeParamCount(signature.TypeParams()) != 0 || typeParamCount(signature.RecvTypeParams()) != 0 {
			continue
		}
		effective, err := input.entrySourceSignature(canonical)
		if err != nil || effective == nil {
			continue
		}
		abi := structuralCFunctionABITypeKey(effective)
		if abi == "" {
			continue
		}
		key := framedEmissionKey(coroLocalExportBindingCertificateDomain, symbol, abi)
		group := targets[key]
		if group == nil {
			group = &coroLocalExportTargetGroup{}
			targets[key] = group
		}
		group.targets = append(group.targets, canonical)
		targetABI[canonical] = abi
	}

	declarations := make([]*ssa.Function, 0, len(input.callableIdentities))
	for declaration := range input.callableIdentities {
		declarations = append(declarations, declaration)
	}
	sort.SliceStable(declarations, func(i, j int) bool {
		return input.functionSortKey(declarations[i]) < input.functionSortKey(declarations[j])
	})
	for _, declaration := range declarations {
		canonical := input.canonicalAlias(declaration)
		if canonical == nil {
			return nil, fmt.Errorf("prepare emission universe: local export declaration inventory contains cyclic aliases")
		}
		if canonical != declaration {
			continue
		}
		identity, identityOK := input.callableIdentities[canonical]
		if !identityOK || identity.PhysicalSymbol == "" || identity.PhysicalABISignature == "" {
			continue
		}
		legacy, err := coroForeignCallDirectiveFor(canonical)
		if err != nil {
			return nil, fmt.Errorf(
				"prepare emission universe: local export declaration policy on %q: %w",
				canonical.Name(), err,
			)
		}
		_, explicitContract, err := coroCallableContractCertificateFor(canonical)
		if err != nil {
			return nil, fmt.Errorf(
				"prepare emission universe: local export declaration contract on %q: %w",
				canonical.Name(), err,
			)
		}
		if legacy != coroForeignCallNone || explicitContract {
			// Source policy remains authoritative until it is deliberately
			// removed. Automatic proof never competes with an annotation.
			continue
		}
		key := framedEmissionKey(
			coroLocalExportBindingCertificateDomain,
			identity.PhysicalSymbol,
			identity.PhysicalABISignature,
		)
		group := targets[key]
		if group == nil || len(group.targets) != 1 {
			continue
		}
		target := group.targets[0]
		if target == nil || target == canonical || targetABI[target] != identity.PhysicalABISignature {
			continue
		}
		targetFunctionIdentity := input.finalFunctionIdentity(target)
		targetLinkIdentity := input.linkIdentities[target]
		if targetFunctionIdentity == "" || targetFunctionIdentity == "<nil>" ||
			targetFunctionIdentity == "<cyclic-alias>" || targetLinkIdentity == "" {
			return nil, fmt.Errorf(
				"prepare emission universe: local export target %q has no exact frozen identity",
				target.Name(),
			)
		}
		certificate := coroLocalExportBindingCertificate{
			DeclarationIdentity:    identity.ID,
			TargetFunctionIdentity: targetFunctionIdentity,
			TargetLinkIdentity:     targetLinkIdentity,
			PhysicalSymbol:         identity.PhysicalSymbol,
			PhysicalABISignature:   identity.PhysicalABISignature,
		}
		certificate.ID = emissionDigest(framedEmissionKey(
			coroLocalExportBindingCertificateDomain,
			certificate.DeclarationIdentity,
			certificate.TargetFunctionIdentity,
			certificate.TargetLinkIdentity,
			certificate.PhysicalSymbol,
			certificate.PhysicalABISignature,
		))
		bindings[canonical] = coroLocalExportBinding{
			target:      target,
			certificate: certificate,
		}
	}
	return bindings, nil
}

// exactLocalCExportSymbol accepts only one direct, bodyful //export source
// publication with no competing physical-ABI directive. The frozen physical
// symbol is checked separately; this parser does not interpret or repair
// malformed export text.
func exactLocalCExportSymbol(function *ssa.Function) (string, bool) {
	declaration, _ := function.Syntax().(*ast.FuncDecl)
	if declaration == nil || declaration.Body == nil || declaration.Doc == nil {
		return "", false
	}
	symbol := ""
	for _, comment := range declaration.Doc.List {
		if comment == nil {
			continue
		}
		text := strings.TrimSpace(comment.Text)
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "//export":
			if symbol != "" || len(fields) != 2 || fields[1] == "" {
				return "", false
			}
			symbol = fields[1]
		case "//go:linkname", "//llgo:link", "//go:wasmexport", "//go:wasmimport":
			return "", false
		case "//":
			if len(fields) > 1 && fields[1] == "llgo:link" {
				return "", false
			}
		default:
			if strings.HasPrefix(text, "//go:cgo_") {
				return "", false
			}
		}
	}
	return symbol, symbol != ""
}
