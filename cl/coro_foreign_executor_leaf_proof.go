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
	"strings"
	"unicode/utf8"

	"github.com/goplus/llgo/internal/coro"
)

const (
	coroInferredExecutorLeafContractSchema = coro.ContractID(
		"llvm-executor-leaf.v1",
	)
	coroInferredExecutorLeafCertificateDomain = "llgo-coro-inferred-executor-leaf-certificate-v1"
)

// CoroForeignExecutorLeafProof is a build-owned structural proof over one exact
// target-selected C/LLVM definition and its complete direct-call closure. The
// frontend still binds it to a physical C symbol and one unambiguous typed Go
// declaration; neither a symbol name nor this record alone grants authority.
type CoroForeignExecutorLeafProof struct {
	ProducerIdentity string
	PhysicalSymbol   string
	LLVMABISignature string
	LLVMTargetTriple string
	LLVMDataLayout   string
	CallClosure      []string
	ClosureSHA256    string
}

func coroLLVMTargetArchitecture(triple string) string {
	architecture := strings.ToLower(strings.TrimSpace(triple))
	if index := strings.IndexByte(architecture, '-'); index >= 0 {
		architecture = architecture[:index]
	}
	switch architecture {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "loong64":
		return "loongarch64"
	case "ppc64":
		return "powerpc64"
	case "ppc64le":
		return "powerpc64le"
	default:
		return architecture
	}
}

func cloneCoroForeignExecutorLeafProofs(proofs []CoroForeignExecutorLeafProof) (map[string]CoroForeignExecutorLeafProof, error) {
	if len(proofs) == 0 {
		return nil, nil
	}
	result := make(map[string]CoroForeignExecutorLeafProof, len(proofs))
	for index, proof := range proofs {
		for _, field := range []struct {
			name  string
			value string
		}{
			{"producer identity", proof.ProducerIdentity},
			{"physical symbol", proof.PhysicalSymbol},
			{"LLVM ABI", proof.LLVMABISignature},
			{"LLVM target", proof.LLVMTargetTriple},
			{"LLVM data layout", proof.LLVMDataLayout},
		} {
			if field.value == "" || !utf8.ValidString(field.value) ||
				strings.IndexByte(field.value, 0) >= 0 {
				return nil, fmt.Errorf(
					"inferred executor-leaf proof %d has an invalid %s",
					index, field.name,
				)
			}
		}
		digest, err := hex.DecodeString(proof.ClosureSHA256)
		if err != nil || len(digest) != 32 ||
			proof.ClosureSHA256 != strings.ToLower(proof.ClosureSHA256) {
			return nil, fmt.Errorf(
				"inferred executor-leaf proof for %q has an invalid SHA-256 closure identity",
				proof.PhysicalSymbol,
			)
		}
		if len(proof.CallClosure) == 0 {
			return nil, fmt.Errorf(
				"inferred executor-leaf proof for %q has an empty call closure",
				proof.PhysicalSymbol,
			)
		}
		closure := append([]string(nil), proof.CallClosure...)
		containsRoot := false
		for closureIndex, symbol := range closure {
			if symbol == "" || !utf8.ValidString(symbol) ||
				strings.IndexByte(symbol, 0) >= 0 {
				return nil, fmt.Errorf(
					"inferred executor-leaf proof for %q has an invalid closure symbol at %d",
					proof.PhysicalSymbol, closureIndex,
				)
			}
			if closureIndex != 0 && closure[closureIndex-1] >= symbol {
				return nil, fmt.Errorf(
					"inferred executor-leaf proof for %q has a non-canonical call closure",
					proof.PhysicalSymbol,
				)
			}
			containsRoot = containsRoot || symbol == proof.PhysicalSymbol
		}
		if !containsRoot {
			return nil, fmt.Errorf(
				"inferred executor-leaf proof for %q omits its root from the call closure",
				proof.PhysicalSymbol,
			)
		}
		if _, duplicate := result[proof.PhysicalSymbol]; duplicate {
			return nil, fmt.Errorf(
				"duplicate inferred executor-leaf proof for physical symbol %q",
				proof.PhysicalSymbol,
			)
		}
		proof.CallClosure = closure
		result[proof.PhysicalSymbol] = proof
	}
	return result, nil
}

func inferredCoroExecutorLeafContract(
	base CoroCallableContractCertificate,
	proof CoroForeignExecutorLeafProof,
) (CoroCallableContractCertificate, error) {
	if err := base.Validate(); err != nil {
		return CoroCallableContractCertificate{}, fmt.Errorf(
			"inferred executor-leaf base contract: %w", err,
		)
	}
	if base.Scope != coro.CallableContractScopeDeclaration ||
		base.HasTrustedInlineContract {
		return CoroCallableContractCertificate{}, fmt.Errorf(
			"inferred executor leaf requires one unrefined declaration contract",
		)
	}
	contract := coro.CallableContract{
		ID:       coroInferredExecutorLeafContractSchema,
		Progress: coro.ProgressExecutorSafe,
		Affinity: coro.AffinityCallerThread,
		Reentry:  coro.ReentryNone,
		Memory:   coro.MemoryBorrowUntilReturn,
	}
	digest, err := coro.CallableContractBehaviorDigest(contract.ID, contract)
	if err != nil {
		return CoroCallableContractCertificate{}, fmt.Errorf(
			"content-address inferred executor-leaf behavior: %w", err,
		)
	}
	contract.ID = coro.ContractID(string(contract.ID) + "/" + digest)

	frozen := base
	frozen.Contract = contract
	frozen.ContractDigest = digest
	frozen.ID = emissionDigest(framedEmissionKey(
		coroInferredExecutorLeafCertificateDomain,
		base.CanonicalFunctionIdentity,
		base.LinkIdentity,
		base.CallableABI,
		base.TypedABISignature,
		base.PhysicalSymbol,
		base.PhysicalABISignature,
		proof.ProducerIdentity,
		proof.PhysicalSymbol,
		proof.LLVMABISignature,
		proof.LLVMTargetTriple,
		proof.LLVMDataLayout,
		proof.ClosureSHA256,
	))
	if err := frozen.Validate(); err != nil {
		return CoroCallableContractCertificate{}, fmt.Errorf(
			"inferred executor-leaf contract: %w", err,
		)
	}
	return frozen, nil
}
