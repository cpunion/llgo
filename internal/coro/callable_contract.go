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

package coro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const CallableContractFactsSchema = "llgo.coro.callable-contract-facts.v1"
const CallableContractFactsDigestDomain = "llgo.coro.callable-contract-facts.digest.v1"

type CallableRefID string

type ProgressClass string

const (
	ProgressUnknown         ProgressClass = "unknown"
	ProgressExecutorSafe    ProgressClass = "executor-safe"
	ProgressMayBlock        ProgressClass = "may-block"
	ProgressAsyncCompletion ProgressClass = "async-completion"
	ProgressNoReturn        ProgressClass = "no-return"
)

func (value ProgressClass) Validate() error {
	switch value {
	case ProgressUnknown, ProgressExecutorSafe, ProgressMayBlock, ProgressAsyncCompletion, ProgressNoReturn:
		return nil
	default:
		return fmt.Errorf("coro: invalid callable progress class %q", value)
	}
}

type AffinityClass string

const (
	AffinityUnknown      AffinityClass = "unknown"
	AffinityAnyThread    AffinityClass = "any-thread"
	AffinityCallerThread AffinityClass = "caller-thread"
	AffinityOwnerThread  AffinityClass = "owner-thread"
	AffinityHostMain     AffinityClass = "host-main"
)

func (value AffinityClass) Validate() error {
	switch value {
	case AffinityUnknown, AffinityAnyThread, AffinityCallerThread, AffinityOwnerThread, AffinityHostMain:
		return nil
	default:
		return fmt.Errorf("coro: invalid callable affinity class %q", value)
	}
}

type ReentryClass string

const (
	ReentryUnknown         ReentryClass = "unknown"
	ReentryNone            ReentryClass = "none"
	ReentryManagedCallback ReentryClass = "managed-callback"
)

func (value ReentryClass) Validate() error {
	switch value {
	case ReentryUnknown, ReentryNone, ReentryManagedCallback:
		return nil
	default:
		return fmt.Errorf("coro: invalid callable reentry class %q", value)
	}
}

type MemoryClass string

const (
	MemoryUnknown             MemoryClass = "unknown"
	MemoryByValue             MemoryClass = "by-value"
	MemoryBorrowUntilReturn   MemoryClass = "borrow-until-return"
	MemoryBorrowUntilComplete MemoryClass = "borrow-until-complete"
	MemoryRetained            MemoryClass = "retained"
)

func (value MemoryClass) Validate() error {
	switch value {
	case MemoryUnknown, MemoryByValue, MemoryBorrowUntilReturn, MemoryBorrowUntilComplete, MemoryRetained:
		return nil
	default:
		return fmt.Errorf("coro: invalid callable memory class %q", value)
	}
}

// CallableContract contains only target-neutral behavior. Structural ABI and
// backend execution recipes belong to CallableFact/InvocationFact and later
// lowering layers respectively.
type CallableContract struct {
	ID       ContractID    `json:"id"`
	Progress ProgressClass `json:"progress"`
	Affinity AffinityClass `json:"affinity"`
	Reentry  ReentryClass  `json:"reentry"`
	Memory   MemoryClass   `json:"memory"`
}

func (contract CallableContract) Validate() error {
	if err := validateStableToken("callable contract ID", string(contract.ID)); err != nil {
		return err
	}
	if err := contract.Progress.Validate(); err != nil {
		return err
	}
	if err := contract.Affinity.Validate(); err != nil {
		return err
	}
	if err := contract.Reentry.Validate(); err != nil {
		return err
	}
	if err := contract.Memory.Validate(); err != nil {
		return err
	}
	return nil
}

const CallableContractBehaviorDigestDomain = "llgo.coro.callable-contract.behavior-digest.v1"

// CallableContractBehaviorDigest gives one source schema plus its four
// target-neutral behavior dimensions a canonical, domain-separated identity.
// The schema is supplied separately because a frozen contract replaces the
// parser schema ID (for example foreign.v1) with schema/digest.
func CallableContractBehaviorDigest(schema ContractID, contract CallableContract) (string, error) {
	canonical := contract
	canonical.ID = schema
	if err := canonical.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("coro: marshal canonical callable contract behavior: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(CallableContractBehaviorDigestDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CallableContractScope identifies whether a contract summarizes one exact
// external declaration or one exact Go wrapper body. A wrapper contract is
// metadata only; it never suppresses analysis of the Go body.
type CallableContractScope string

const (
	CallableContractScopeDeclaration CallableContractScope = "declaration"
	CallableContractScopeWrapper     CallableContractScope = "wrapper"
)

// CallableContractCertificate is the pointer-free binding frozen after exact
// aliases, link identity and physical ABI are known. CallableABI is independent
// from PhysicalABISignature: it may be an explicit abstract ABI or a stable
// derivation from TypedABISignature.
type CallableContractCertificate struct {
	ID                        string           `json:"id"`
	CanonicalFunctionIdentity string           `json:"canonical_function_identity"`
	LinkIdentity              string           `json:"link_identity"`
	Contract                  CallableContract `json:"contract"`
	ContractDigest            string           `json:"contract_digest"`
	// TrustedInlineContract is an optional executor-safe behavior owned by
	// this exact callable. It shares CallableABI with the default contract: a
	// refinement can narrow behavior but cannot replace the callable ABI.
	// Value+bool representation keeps certificates comparable and makes absence
	// explicit in archive/plan digests without pointer identity.
	TrustedInlineContract       CallableContract      `json:"trusted_inline_contract"`
	TrustedInlineContractDigest string                `json:"trusted_inline_contract_digest"`
	HasTrustedInlineContract    bool                  `json:"has_trusted_inline_contract"`
	Scope                       CallableContractScope `json:"scope"`
	CallableABI                 string                `json:"callable_abi"`
	CallableABIExplicit         bool                  `json:"callable_abi_explicit"`
	TypedABISignature           string                `json:"typed_abi_signature"`
	PhysicalSymbol              string                `json:"physical_symbol,omitempty"`
	PhysicalABISignature        string                `json:"physical_abi_signature,omitempty"`
}

func (certificate CallableContractCertificate) IsZero() bool {
	return certificate == (CallableContractCertificate{})
}

func (certificate CallableContractCertificate) Validate() error {
	if err := validateSHA256Hex("callable contract certificate ID", certificate.ID); err != nil {
		return err
	}
	if certificate.CanonicalFunctionIdentity == "" || !utf8.ValidString(certificate.CanonicalFunctionIdentity) {
		return fmt.Errorf("coro: callable contract certificate has no valid canonical function identity")
	}
	if certificate.LinkIdentity == "" || !utf8.ValidString(certificate.LinkIdentity) {
		return fmt.Errorf("coro: callable contract certificate has no valid link identity")
	}
	if err := certificate.Contract.Validate(); err != nil {
		return fmt.Errorf("coro: callable contract certificate: %w", err)
	}
	if err := validateSHA256Hex("callable contract digest", certificate.ContractDigest); err != nil {
		return err
	}
	contractID := string(certificate.Contract.ID)
	separator := strings.LastIndexByte(contractID, '/')
	if separator <= 0 || separator == len(contractID)-1 || contractID[separator+1:] != certificate.ContractDigest {
		return fmt.Errorf("coro: callable contract ID %q is not content-addressed by digest %q", certificate.Contract.ID, certificate.ContractDigest)
	}
	schema := ContractID(contractID[:separator])
	wantDigest, err := CallableContractBehaviorDigest(schema, certificate.Contract)
	if err != nil {
		return fmt.Errorf("coro: callable contract certificate digest: %w", err)
	}
	if wantDigest != certificate.ContractDigest {
		return fmt.Errorf("coro: callable contract certificate digest does not match contract behavior")
	}
	if !certificate.HasTrustedInlineContract {
		if certificate.TrustedInlineContract != (CallableContract{}) || certificate.TrustedInlineContractDigest != "" {
			return fmt.Errorf("coro: callable contract certificate has trusted-inline data without presence")
		}
	} else {
		trusted := certificate.TrustedInlineContract
		if err := trusted.Validate(); err != nil {
			return fmt.Errorf("coro: trusted-inline callable contract certificate: %w", err)
		}
		if err := validateSHA256Hex("trusted-inline callable contract digest", certificate.TrustedInlineContractDigest); err != nil {
			return err
		}
		trustedID := string(trusted.ID)
		trustedSeparator := strings.LastIndexByte(trustedID, '/')
		if trustedSeparator <= 0 || trustedSeparator == len(trustedID)-1 ||
			trustedID[trustedSeparator+1:] != certificate.TrustedInlineContractDigest {
			return fmt.Errorf(
				"coro: trusted-inline callable contract ID %q is not content-addressed by digest %q",
				trusted.ID, certificate.TrustedInlineContractDigest,
			)
		}
		trustedSchema := ContractID(trustedID[:trustedSeparator])
		if trustedSchema != schema {
			return fmt.Errorf("coro: trusted-inline callable contract schema %q differs from default schema %q", trustedSchema, schema)
		}
		wantTrustedDigest, err := CallableContractBehaviorDigest(trustedSchema, trusted)
		if err != nil {
			return fmt.Errorf("coro: trusted-inline callable contract certificate digest: %w", err)
		}
		if wantTrustedDigest != certificate.TrustedInlineContractDigest {
			return fmt.Errorf("coro: trusted-inline callable contract certificate digest does not match contract behavior")
		}
		if err := ValidateTrustedInlineCallableContractRefinement(trusted, certificate.Contract); err != nil {
			return fmt.Errorf("coro: callable contract certificate: %w", err)
		}
	}
	switch certificate.Scope {
	case CallableContractScopeDeclaration, CallableContractScopeWrapper:
	default:
		return fmt.Errorf("coro: callable contract certificate has invalid scope %q", certificate.Scope)
	}
	if err := validateStableToken("callable contract ABI", certificate.CallableABI); err != nil {
		return err
	}
	if err := validateStableIdentityText("typed callable ABI signature", certificate.TypedABISignature); err != nil {
		return err
	}
	switch certificate.Scope {
	case CallableContractScopeDeclaration:
		if err := validateStableIdentityText("callable contract physical symbol", certificate.PhysicalSymbol); err != nil {
			return err
		}
		if err := validateStableIdentityText("callable contract physical ABI signature", certificate.PhysicalABISignature); err != nil {
			return err
		}
		if certificate.PhysicalABISignature != certificate.TypedABISignature {
			return fmt.Errorf("coro: callable declaration physical ABI differs from its typed ABI")
		}
	case CallableContractScopeWrapper:
		if certificate.PhysicalSymbol != "" || certificate.PhysicalABISignature != "" {
			return fmt.Errorf("coro: callable wrapper certificate must not claim a physical C ABI")
		}
	}
	return nil
}

func validateSHA256Hex(name, value string) error {
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("coro: %s must contain one SHA-256 digest", name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("coro: %s is not hexadecimal", name)
	}
	return nil
}

// CallableContractExecConstraints projects dimensions that the current
// physical lowering cannot otherwise enforce. Thread-affine affinities remain
// explicit. Unknown reentry and unknown/retained memory require a future
// adapter/lifetime recipe, so OpaqueExec keeps lowering fail-closed. Exact
// managed-callback declarations are instead consumed by the compiler-owned
// ForeignReentry recipe; the contract does not itself grant that recipe.
// This grants no worker, raw-plain, or trusted-inline capability.
func CallableContractExecConstraints(contract CallableContract) ExecFlags {
	var flags ExecFlags
	switch contract.Affinity {
	case AffinityUnknown, AffinityOwnerThread, AffinityHostMain:
		flags |= ThreadAffine
	}
	switch contract.Reentry {
	case ReentryUnknown:
		flags |= OpaqueExec
	}
	switch contract.Memory {
	case MemoryUnknown, MemoryRetained:
		flags |= OpaqueExec
	}
	return flags
}

type CallableFact struct {
	Ref                   CallableRefID `json:"ref"`
	Function              FunctionID    `json:"function"`
	ABI                   string        `json:"abi"`
	Contract              ContractID    `json:"contract"`
	TrustedInlineContract ContractID    `json:"trusted_inline_contract,omitempty"`
}

type InvocationPolicy string

const (
	InvocationAuto          InvocationPolicy = "auto"
	InvocationTrustedInline InvocationPolicy = "trusted-inline"
)

func (policy InvocationPolicy) Validate() error {
	switch policy {
	case InvocationAuto, InvocationTrustedInline:
		return nil
	default:
		return fmt.Errorf("coro: invalid callable invocation policy %q", policy)
	}
}

type InvocationFact struct {
	Site       SourceSiteID     `json:"site"`
	Candidates []CallableRefID  `json:"candidates"`
	Open       bool             `json:"open"`
	Policy     InvocationPolicy `json:"policy"`
	Contract   ContractID       `json:"contract"`
	ABI        string           `json:"abi"`
}

// CallableContractFacts is pointer-free archive metadata. Invocation policy
// selects a contract already owned by every candidate; it never manufactures
// a call-site capability from a raw address or backend choice.
type CallableContractFacts struct {
	Schema      string             `json:"schema"`
	Contracts   []CallableContract `json:"contracts"`
	Callables   []CallableFact     `json:"callables"`
	Invocations []InvocationFact   `json:"invocations"`
}

func (facts CallableContractFacts) Verify() error {
	if facts.Schema != CallableContractFactsSchema {
		return fmt.Errorf("coro: callable contract facts schema %q, want %q", facts.Schema, CallableContractFactsSchema)
	}
	contracts := make(map[ContractID]CallableContract, len(facts.Contracts))
	for index, contract := range facts.Contracts {
		if err := contract.Validate(); err != nil {
			return fmt.Errorf("coro: callable contract %d: %w", index, err)
		}
		if _, duplicate := contracts[contract.ID]; duplicate {
			return fmt.Errorf("coro: duplicate callable contract ID %q", contract.ID)
		}
		contracts[contract.ID] = contract
	}

	callables := make(map[CallableRefID]CallableFact, len(facts.Callables))
	for index, callable := range facts.Callables {
		if err := validateStableToken("callable reference", string(callable.Ref)); err != nil {
			return fmt.Errorf("coro: callable fact %d: %w", index, err)
		}
		if err := callable.Function.validate(); err != nil {
			return fmt.Errorf("coro: callable fact %d: %w", index, err)
		}
		if err := validateStableToken("callable ABI", callable.ABI); err != nil {
			return fmt.Errorf("coro: callable fact %d: %w", index, err)
		}
		base, ok := contracts[callable.Contract]
		if !ok {
			return fmt.Errorf("coro: callable %q references unknown contract %q", callable.Ref, callable.Contract)
		}
		if callable.TrustedInlineContract != "" {
			trusted, ok := contracts[callable.TrustedInlineContract]
			if !ok {
				return fmt.Errorf("coro: callable %q references unknown trusted-inline contract %q", callable.Ref, callable.TrustedInlineContract)
			}
			if err := ValidateTrustedInlineCallableContractRefinement(trusted, base); err != nil {
				return fmt.Errorf("coro: callable %q: %w", callable.Ref, err)
			}
		}
		if _, duplicate := callables[callable.Ref]; duplicate {
			return fmt.Errorf("coro: duplicate callable reference %q", callable.Ref)
		}
		callables[callable.Ref] = callable
	}

	sites := make(map[SourceSiteID]struct{}, len(facts.Invocations))
	for index, invocation := range facts.Invocations {
		if err := invocation.Site.Validate(); err != nil {
			return fmt.Errorf("coro: invocation fact %d: %w", index, err)
		}
		if _, duplicate := sites[invocation.Site]; duplicate {
			return fmt.Errorf("coro: duplicate callable invocation site %+v", invocation.Site)
		}
		sites[invocation.Site] = struct{}{}
		if err := invocation.Policy.Validate(); err != nil {
			return fmt.Errorf("coro: invocation fact %d: %w", index, err)
		}
		if invocation.Open && invocation.Policy != InvocationAuto {
			return fmt.Errorf("coro: open invocation fact %d must use auto policy", index)
		}
		if err := validateStableToken("invocation ABI", invocation.ABI); err != nil {
			return fmt.Errorf("coro: invocation fact %d: %w", index, err)
		}
		selected, ok := contracts[invocation.Contract]
		if !ok {
			return fmt.Errorf("coro: invocation fact %d references unknown contract %q", index, invocation.Contract)
		}
		if len(invocation.Candidates) == 0 {
			return fmt.Errorf("coro: invocation fact %d has no callable candidates", index)
		}
		candidateSet := make(map[CallableRefID]struct{}, len(invocation.Candidates))
		candidateContracts := make([]CallableContract, 0, len(invocation.Candidates))
		for _, reference := range invocation.Candidates {
			if _, duplicate := candidateSet[reference]; duplicate {
				return fmt.Errorf("coro: invocation fact %d has duplicate candidate %q", index, reference)
			}
			candidateSet[reference] = struct{}{}
			callable, ok := callables[reference]
			if !ok {
				return fmt.Errorf("coro: invocation fact %d references unknown callable %q", index, reference)
			}
			if callable.ABI != invocation.ABI {
				return fmt.Errorf("coro: invocation fact %d ABI %q differs from candidate %q ABI %q", index, invocation.ABI, reference, callable.ABI)
			}
			switch invocation.Policy {
			case InvocationAuto:
				candidateContracts = append(candidateContracts, contracts[callable.Contract])
			case InvocationTrustedInline:
				if callable.TrustedInlineContract == "" || callable.TrustedInlineContract != invocation.Contract {
					return fmt.Errorf("coro: trusted-inline invocation fact %d candidate %q does not own contract %q", index, reference, invocation.Contract)
				}
			}
		}
		switch invocation.Policy {
		case InvocationAuto:
			var want CallableContract
			if invocation.Open {
				want = unknownCallableContract()
			} else {
				want = joinCallableContracts(candidateContracts)
			}
			if !sameCallableContractBehavior(selected, want) {
				return fmt.Errorf("coro: auto invocation fact %d contract %q is not the conservative candidate join", index, selected.ID)
			}
		case InvocationTrustedInline:
			if invocation.Open {
				return fmt.Errorf("coro: trusted-inline invocation fact %d cannot be open", index)
			}
			if selected.Progress != ProgressExecutorSafe {
				return fmt.Errorf("coro: trusted-inline invocation fact %d contract %q is not executor-safe", index, selected.ID)
			}
		}
	}
	return nil
}

func (facts CallableContractFacts) CanonicalJSON() ([]byte, error) {
	canonical, err := facts.canonical()
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("coro: marshal canonical callable contract facts: %w", err)
	}
	return payload, nil
}

func (facts CallableContractFacts) Digest() (string, error) {
	payload, err := facts.CanonicalJSON()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(CallableContractFactsDigestDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (facts CallableContractFacts) canonical() (CallableContractFacts, error) {
	if err := facts.Verify(); err != nil {
		return CallableContractFacts{}, err
	}
	ret := CallableContractFacts{
		Schema:      CallableContractFactsSchema,
		Contracts:   append([]CallableContract(nil), facts.Contracts...),
		Callables:   append([]CallableFact(nil), facts.Callables...),
		Invocations: append([]InvocationFact(nil), facts.Invocations...),
	}
	if ret.Contracts == nil {
		ret.Contracts = make([]CallableContract, 0)
	}
	if ret.Callables == nil {
		ret.Callables = make([]CallableFact, 0)
	}
	if ret.Invocations == nil {
		ret.Invocations = make([]InvocationFact, 0)
	}
	sort.Slice(ret.Contracts, func(i, j int) bool { return ret.Contracts[i].ID < ret.Contracts[j].ID })
	sort.Slice(ret.Callables, func(i, j int) bool { return ret.Callables[i].Ref < ret.Callables[j].Ref })
	sort.Slice(ret.Invocations, func(i, j int) bool { return compareSourceSiteID(ret.Invocations[i].Site, ret.Invocations[j].Site) < 0 })
	for index := range ret.Invocations {
		invocation := &ret.Invocations[index]
		invocation.Candidates = append([]CallableRefID(nil), invocation.Candidates...)
		sort.Slice(invocation.Candidates, func(i, j int) bool { return invocation.Candidates[i] < invocation.Candidates[j] })
	}
	return ret, nil
}

func compareSourceSiteID(left, right SourceSiteID) int {
	if left.Function != right.Function {
		if left.Function < right.Function {
			return -1
		}
		return 1
	}
	if left.Kind != right.Kind {
		return strings.Compare(string(left.Kind), string(right.Kind))
	}
	for _, pair := range [][2]int{{left.Block, right.Block}, {left.Instruction, right.Instruction}, {left.Successor, right.Successor}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if left.Role != right.Role {
		return strings.Compare(string(left.Role), string(right.Role))
	}
	if left.Ordinal < right.Ordinal {
		return -1
	}
	if left.Ordinal > right.Ordinal {
		return 1
	}
	return 0
}

func unknownCallableContract() CallableContract {
	return CallableContract{
		Progress: ProgressUnknown,
		Affinity: AffinityUnknown,
		Reentry:  ReentryUnknown,
		Memory:   MemoryUnknown,
	}
}

func joinCallableContracts(contracts []CallableContract) CallableContract {
	if len(contracts) == 0 {
		return unknownCallableContract()
	}
	result := contracts[0]
	result.ID = ""
	for _, next := range contracts[1:] {
		result.Progress = joinProgress(result.Progress, next.Progress)
		result.Affinity = joinAffinity(result.Affinity, next.Affinity)
		result.Reentry = joinReentry(result.Reentry, next.Reentry)
		result.Memory = joinMemory(result.Memory, next.Memory)
	}
	return result
}

func joinProgress(left, right ProgressClass) ProgressClass {
	if left == right {
		return left
	}
	if left == ProgressUnknown || right == ProgressUnknown {
		return ProgressUnknown
	}
	if left == ProgressExecutorSafe && right == ProgressMayBlock || left == ProgressMayBlock && right == ProgressExecutorSafe {
		return ProgressMayBlock
	}
	return ProgressUnknown
}

func joinAffinity(left, right AffinityClass) AffinityClass {
	if left == right {
		return left
	}
	if left == AffinityUnknown || right == AffinityUnknown {
		return AffinityUnknown
	}
	if left == AffinityAnyThread {
		return right
	}
	if right == AffinityAnyThread {
		return left
	}
	return AffinityUnknown
}

func joinReentry(left, right ReentryClass) ReentryClass {
	if left == right {
		return left
	}
	if left == ReentryUnknown || right == ReentryUnknown {
		return ReentryUnknown
	}
	return ReentryManagedCallback
}

func joinMemory(left, right MemoryClass) MemoryClass {
	if memoryRank(left) >= memoryRank(right) {
		return left
	}
	return right
}

func memoryRank(value MemoryClass) int {
	switch value {
	case MemoryByValue:
		return 0
	case MemoryBorrowUntilReturn:
		return 1
	case MemoryBorrowUntilComplete:
		return 2
	case MemoryRetained:
		return 3
	default:
		return 4
	}
}

func callableContractRefines(refined, base CallableContract) bool {
	return progressRefines(refined.Progress, base.Progress) &&
		affinityRefines(refined.Affinity, base.Affinity) &&
		reentryRank(refined.Reentry) <= reentryRank(base.Reentry) &&
		memoryRank(refined.Memory) <= memoryRank(base.Memory)
}

// ValidateCallableContractRefinement verifies the target-neutral safety
// lattice shared by source parsing, frontend freezing, archive facts, and SSA
// trusted-inline consumption. IDs are identities, not behavior dimensions;
// callers bind them separately to content-addressed digests.
func ValidateCallableContractRefinement(refined, base CallableContract) error {
	if err := base.Validate(); err != nil {
		return fmt.Errorf("invalid default callable contract: %w", err)
	}
	if err := refined.Validate(); err != nil {
		return fmt.Errorf("invalid refined callable contract: %w", err)
	}
	if !callableContractRefines(refined, base) {
		return fmt.Errorf("trusted-inline callable contract %q is not a safe refinement of default contract %q", refined.ID, base.ID)
	}
	return nil
}

// ValidateTrustedInlineCallableContractRefinement adds the one progress
// guarantee required to suppress a conservative foreign wait at an exact
// invocation. ABI equality is enforced by the owning certificate/callable fact
// because ABI is intentionally not a target-neutral contract dimension.
func ValidateTrustedInlineCallableContractRefinement(refined, base CallableContract) error {
	if refined.Progress != ProgressExecutorSafe {
		return fmt.Errorf("trusted-inline callable contract %q is not executor-safe", refined.ID)
	}
	return ValidateCallableContractRefinement(refined, base)
}

func progressRefines(refined, base ProgressClass) bool {
	if refined == base || base == ProgressUnknown {
		return true
	}
	if base == ProgressNoReturn {
		return false
	}
	return refined == ProgressExecutorSafe
}

func affinityRefines(refined, base AffinityClass) bool {
	if refined == base || base == AffinityUnknown || base == AffinityAnyThread {
		return true
	}
	return false
}

func reentryRank(value ReentryClass) int {
	switch value {
	case ReentryNone:
		return 0
	case ReentryManagedCallback:
		return 1
	default:
		return 2
	}
}

func sameCallableContractBehavior(left, right CallableContract) bool {
	return left.Progress == right.Progress && left.Affinity == right.Affinity &&
		left.Reentry == right.Reentry && left.Memory == right.Memory
}
