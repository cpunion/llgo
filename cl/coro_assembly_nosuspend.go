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
	"encoding/hex"
	"fmt"
	"go/ast"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/go/ssa"
)

// CoroAssemblyNoSuspendProof is a build-owned proof over one target-selected,
// post-CABI translated Plan9 assembly definition and its complete direct-call
// closure. EmissionPackage accepts these records only as inputs; cl binds them
// again to an exact bodyless Go declaration and frozen physical symbol.
type CoroAssemblyNoSuspendProof struct {
	PhysicalSymbol string
	ABISignature   string
	CallClosure    []string
	ClosureSHA256  string
}

// CoroAssemblyNoSuspendCertificate is the immutable frontend certificate for
// one retained physical Go-ABI assembly call. The call is never elided and
// remains IRQUnsafe; the certificate proves only that it cannot suspend.
type CoroAssemblyNoSuspendCertificate struct {
	ID             string
	PhysicalSymbol string
	ABISignature   string
	ClosureSHA256  string
}

func cloneCoroAssemblyNoSuspendProofs(proofs []CoroAssemblyNoSuspendProof) (map[string]CoroAssemblyNoSuspendProof, error) {
	if len(proofs) == 0 {
		return nil, nil
	}
	result := make(map[string]CoroAssemblyNoSuspendProof, len(proofs))
	for index, proof := range proofs {
		if proof.PhysicalSymbol == "" || !utf8.ValidString(proof.PhysicalSymbol) || strings.IndexByte(proof.PhysicalSymbol, 0) >= 0 {
			return nil, fmt.Errorf("assembly no-suspend proof %d has an invalid physical symbol", index)
		}
		if proof.ABISignature == "" || !utf8.ValidString(proof.ABISignature) || strings.IndexByte(proof.ABISignature, 0) >= 0 {
			return nil, fmt.Errorf("assembly no-suspend proof for %q has an invalid ABI signature", proof.PhysicalSymbol)
		}
		digest, err := hex.DecodeString(proof.ClosureSHA256)
		if err != nil || len(digest) != 32 || proof.ClosureSHA256 != strings.ToLower(proof.ClosureSHA256) {
			return nil, fmt.Errorf("assembly no-suspend proof for %q has an invalid SHA-256 closure identity", proof.PhysicalSymbol)
		}
		if len(proof.CallClosure) == 0 {
			return nil, fmt.Errorf("assembly no-suspend proof for %q has an empty call closure", proof.PhysicalSymbol)
		}
		closure := append([]string(nil), proof.CallClosure...)
		containsRoot := false
		for closureIndex, name := range closure {
			if name == "" || !utf8.ValidString(name) || strings.IndexByte(name, 0) >= 0 {
				return nil, fmt.Errorf("assembly no-suspend proof for %q has an invalid closure symbol at %d", proof.PhysicalSymbol, closureIndex)
			}
			if closureIndex != 0 && closure[closureIndex-1] >= name {
				return nil, fmt.Errorf("assembly no-suspend proof for %q has a non-canonical call closure", proof.PhysicalSymbol)
			}
			containsRoot = containsRoot || name == proof.PhysicalSymbol
		}
		if !containsRoot {
			return nil, fmt.Errorf("assembly no-suspend proof for %q omits its root from the call closure", proof.PhysicalSymbol)
		}
		if _, duplicate := result[proof.PhysicalSymbol]; duplicate {
			return nil, fmt.Errorf("duplicate assembly no-suspend proof for physical symbol %q", proof.PhysicalSymbol)
		}
		proof.CallClosure = closure
		result[proof.PhysicalSymbol] = proof
	}
	return result, nil
}

func (u *EmissionUniverse) freezeCoroAssemblyNoSuspendCertificates() error {
	used := make(map[string]*ssa.Function)
	for _, fn := range u.functions {
		if fn == nil || fn.Pkg == nil || fn.Parent() != nil || functionNeedsLinkOnce(fn) || len(fn.Blocks) != 0 {
			continue
		}
		declaration, _ := fn.Syntax().(*ast.FuncDecl)
		if declaration == nil || declaration.Body != nil {
			continue
		}
		owners := u.sortedUseOwners(fn)
		if len(owners) != 1 {
			continue
		}
		owner := owners[0]
		ownerKey := emissionFunctionOwnerKey{function: fn, owner: owner}
		if u.functionKinds[ownerKey] != goFunc {
			continue
		}
		kind, symbol, managedSignature, ok := splitManagedSymbolKey(u.finalKeys[ownerKey])
		if !ok || kind != goFunc {
			continue
		}
		if physical := u.physicalNames[ownerKey]; physical != "" {
			symbol = physical
		}
		proof, proved := owner.assemblyNoSuspend[symbol]
		if !proved {
			continue
		}
		usageKey := owner.identity + "\x00" + symbol
		if previous := used[usageKey]; previous != nil && previous != fn {
			return fmt.Errorf("prepare emission universe: assembly no-suspend proof for %q matches multiple bodyless Go declarations", symbol)
		}
		used[usageKey] = fn
		linkIdentity := u.linkIdentities[fn]
		if linkIdentity == "" {
			return fmt.Errorf("prepare emission universe: assembly no-suspend declaration %q has no frozen link identity", fn.Name())
		}
		target := u.prog.TargetSpec()
		fields := []string{
			"llgo-coro-assembly-nosuspend-v0",
			owner.identity,
			owner.pkgPath,
			linkIdentity,
			symbol,
			managedSignature,
			proof.ABISignature,
			proof.ClosureSHA256,
			target.Triple,
			target.CPU,
			target.Features,
			target.TargetABI,
			u.prog.DataLayout(),
		}
		fields = append(fields, proof.CallClosure...)
		u.assemblyNoSuspend[fn] = CoroAssemblyNoSuspendCertificate{
			ID:             framedEmissionKey(fields...),
			PhysicalSymbol: symbol,
			ABISignature:   proof.ABISignature,
			ClosureSHA256:  proof.ClosureSHA256,
		}
	}
	return nil
}

// CoroAssemblyNoSuspendCertificate returns the exact frozen translated-
// assembly certificate for fn. Ordinary bodyless Go declarations remain
// uncertified and therefore retain the conservative opaque boundary.
func (u *EmissionUniverse) CoroAssemblyNoSuspendCertificate(fn *ssa.Function) (certificate CoroAssemblyNoSuspendCertificate, certified bool, err error) {
	if u == nil {
		return CoroAssemblyNoSuspendCertificate{}, false, fmt.Errorf("coroutine assembly no-suspend certificate: nil emission universe")
	}
	if fn == nil {
		return CoroAssemblyNoSuspendCertificate{}, false, fmt.Errorf("coroutine assembly no-suspend certificate: nil function")
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return CoroAssemblyNoSuspendCertificate{}, false, fmt.Errorf("coroutine assembly no-suspend certificate: function has cyclic canonical aliases")
	}
	if _, required := u.required[canonical]; !required {
		return CoroAssemblyNoSuspendCertificate{}, false, fmt.Errorf("coroutine assembly no-suspend certificate: function %q is absent from the frozen emission universe", canonical.Name())
	}
	certificate, certified = u.assemblyNoSuspend[canonical]
	return certificate, certified, nil
}

func sortedCoroAssemblyNoSuspendProofs(proofs map[string]CoroAssemblyNoSuspendProof) []CoroAssemblyNoSuspendProof {
	if len(proofs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(proofs))
	for symbol := range proofs {
		keys = append(keys, symbol)
	}
	sort.Strings(keys)
	result := make([]CoroAssemblyNoSuspendProof, 0, len(keys))
	for _, symbol := range keys {
		proof := proofs[symbol]
		proof.CallClosure = append([]string(nil), proof.CallClosure...)
		result = append(result, proof)
	}
	return result
}
