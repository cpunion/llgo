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
	"fmt"

	"golang.org/x/tools/go/ssa"
)

const (
	ssaCallableRefPrefix                       = "llgo.coro.callable-ref.v1/"
	ssaCallableIdentityRefPrefix               = "llgo.coro.declaration-ref.v1/"
	ssaAutoContractProjectionSchema ContractID = "llgo.coro.ssa-auto-contract.v1"
)

// CallableContractFacts projects the immutable callable certificates and
// exact call plans of p into pointer-free, canonical archive facts.
//
// A call is included only when every known candidate owns a frozen callable
// certificate. An open call retains its known candidate subset but always
// selects the content-addressed unknown Auto contract. A partial certified
// candidate set, an identity-less foreign call, or incomplete invocation
// metadata is rejected instead of being represented as a more precise call.
// No fact is recovered from a function address, symbol spelling, or display
// name.
func (p *SSAPlan) CallableContractFacts() (CallableContractFacts, error) {
	if p == nil || p.plan == nil {
		return CallableContractFacts{}, fmt.Errorf("coro: cannot project callable contract facts from a nil SSA plan")
	}

	facts := CallableContractFacts{Schema: CallableContractFactsSchema}
	contracts := make(map[ContractID]CallableContract)
	callablesByFunction := make(map[FunctionID]CallableFact)
	seenIdentities := 0
	seenContracts := 0

	addContract := func(contract CallableContract) error {
		if err := contract.Validate(); err != nil {
			return err
		}
		if previous, exists := contracts[contract.ID]; exists {
			if previous != contract {
				return fmt.Errorf("coro: callable contract ID %q has conflicting frozen behavior", contract.ID)
			}
			return nil
		}
		contracts[contract.ID] = contract
		facts.Contracts = append(facts.Contracts, contract)
		return nil
	}

	for _, function := range p.functions {
		fn := function.Function
		if fn == nil {
			return CallableContractFacts{}, fmt.Errorf("coro: callable facts encounter nil function for ID %q", function.Plan.ID)
		}
		id, ok := p.byFunction[fn]
		if !ok || id != function.Plan.ID {
			return CallableContractFacts{}, fmt.Errorf("coro: callable facts encounter inconsistent function identity %q", function.Plan.ID)
		}
		identity, hasIdentity := p.callableIdentities[fn]
		if hasIdentity {
			seenIdentities++
			if err := identity.Validate(); err != nil {
				return CallableContractFacts{}, fmt.Errorf("coro: callable identity for function %q: %w", id, err)
			}
		}
		certificate, hasContract := p.callableContracts[fn]
		if hasContract {
			seenContracts++
			if err := certificate.Validate(); err != nil {
				return CallableContractFacts{}, fmt.Errorf("coro: callable contract for function %q: %w", id, err)
			}
			if certificate.Scope == CallableContractScopeDeclaration && hasIdentity {
				if err := ValidateCallableContractIdentity(identity, certificate); err != nil {
					return CallableContractFacts{}, fmt.Errorf("coro: callable function %q: %w", id, err)
				}
			}
		}
		if !hasIdentity && !hasContract {
			continue
		}

		defaultContract := CallableContract{}
		ref := CallableRefID("")
		if hasIdentity {
			ref = CallableRefID(ssaCallableIdentityRefPrefix + identity.ID)
		} else {
			// Wrapper contracts do not represent C declarations and therefore do
			// not participate in the total C identity inventory.
			ref = CallableRefID(ssaCallableRefPrefix + certificate.ID)
		}
		if hasContract {
			defaultContract = certificate.Contract
		} else {
			var err error
			defaultContract, err = contentAddressSSAAutoContract(unknownCallableContract())
			if err != nil {
				return CallableContractFacts{}, fmt.Errorf("coro: synthetic unknown callable contract for function %q: %w", id, err)
			}
		}
		if err := addContract(defaultContract); err != nil {
			return CallableContractFacts{}, fmt.Errorf("coro: default callable contract for function %q: %w", id, err)
		}
		callable := CallableFact{
			Ref:      ref,
			Function: id,
			Contract: defaultContract.ID,
		}
		if hasIdentity {
			callable.ABI = identity.CallableABI
		} else {
			callable.ABI = certificate.CallableABI
		}
		if hasContract && certificate.HasTrustedInlineContract {
			if err := addContract(certificate.TrustedInlineContract); err != nil {
				return CallableContractFacts{}, fmt.Errorf("coro: trusted-inline callable contract for function %q: %w", id, err)
			}
			callable.TrustedInlineContract = certificate.TrustedInlineContract.ID
		}
		if previous, duplicate := callablesByFunction[id]; duplicate {
			return CallableContractFacts{}, fmt.Errorf(
				"coro: function %q has duplicate callable facts %q and %q", id, previous.Ref, callable.Ref,
			)
		}
		callablesByFunction[id] = callable
		facts.Callables = append(facts.Callables, callable)
	}
	if seenIdentities != len(p.callableIdentities) {
		return CallableContractFacts{}, fmt.Errorf("coro: callable identity map contains a function outside the canonical SSA plan")
	}
	if seenContracts != len(p.callableContracts) {
		return CallableContractFacts{}, fmt.Errorf("coro: callable contract map contains a function outside the canonical SSA plan")
	}

	for call, callPlan := range p.callPlans {
		invocation, include, err := p.projectCallableInvocation(call, callPlan, callablesByFunction, contracts, addContract)
		if err != nil {
			return CallableContractFacts{}, err
		}
		if include {
			facts.Invocations = append(facts.Invocations, invocation)
		}
	}

	canonical, err := facts.canonical()
	if err != nil {
		return CallableContractFacts{}, fmt.Errorf("coro: verify projected callable contract facts: %w", err)
	}
	return canonical, nil
}

func (p *SSAPlan) projectCallableInvocation(
	call ssa.CallInstruction,
	plan SSACallPlan,
	callables map[FunctionID]CallableFact,
	contracts map[ContractID]CallableContract,
	addContract func(CallableContract) error,
) (InvocationFact, bool, error) {
	if call == nil || plan.Call != call {
		return InvocationFact{}, false, fmt.Errorf("coro: callable invocation has an inconsistent CallPlan owner")
	}
	if err := plan.Kind.validate(); err != nil {
		return InvocationFact{}, false, err
	}
	if err := plan.Unresolved.validate(); err != nil {
		return InvocationFact{}, false, err
	}

	candidates := make([]CallableRefID, 0, len(plan.Targets))
	candidateContracts := make([]CallableContract, 0, len(plan.Targets))
	var abi string
	certified := 0
	seenTargets := make(map[FunctionID]struct{}, len(plan.Targets))
	for _, target := range plan.Targets {
		if _, duplicate := seenTargets[target]; duplicate {
			return InvocationFact{}, false, fmt.Errorf("coro: callable invocation contains duplicate target %q", target)
		}
		seenTargets[target] = struct{}{}
		if _, planned := p.byID[target]; !planned {
			return InvocationFact{}, false, fmt.Errorf("coro: callable invocation references target %q outside the SSA plan", target)
		}
		candidate, ok := callables[target]
		if !ok {
			continue
		}
		certified++
		if abi == "" {
			abi = candidate.ABI
		} else if abi != candidate.ABI {
			return InvocationFact{}, false, fmt.Errorf(
				"coro: callable invocation candidates use incompatible ABIs %q and %q", abi, candidate.ABI,
			)
		}
		contract, ok := contracts[candidate.Contract]
		if !ok {
			return InvocationFact{}, false, fmt.Errorf("coro: callable %q has no frozen default contract %q", candidate.Ref, candidate.Contract)
		}
		candidates = append(candidates, candidate.Ref)
		candidateContracts = append(candidateContracts, contract)
	}

	hasInvocationMetadata := plan.InvocationPolicy != "" || plan.InvocationContract != "" ||
		plan.InvocationABI != "" || plan.InvocationCertificate != ""
	if certified == 0 {
		if hasInvocationMetadata || plan.Kind == CallTrustedInline || plan.Kind == CallForeign || plan.Unresolved == UnknownForeign {
			return InvocationFact{}, false, fmt.Errorf("coro: foreign callable invocation has no frozen callable identity")
		}
		return InvocationFact{}, false, nil
	}
	if certified != len(plan.Targets) {
		return InvocationFact{}, false, fmt.Errorf(
			"coro: callable invocation has contracts for only %d of %d known candidates", certified, len(plan.Targets),
		)
	}

	site, err := p.callableInvocationSite(call)
	if err != nil {
		return InvocationFact{}, false, err
	}
	invocation := InvocationFact{
		Site:       site,
		Candidates: candidates,
		Open:       plan.Open,
		Policy:     InvocationAuto,
		ABI:        abi,
	}

	switch plan.InvocationPolicy {
	case "":
		if plan.InvocationContract != "" || plan.InvocationABI != "" || plan.InvocationCertificate != "" || plan.Kind == CallTrustedInline {
			return InvocationFact{}, false, fmt.Errorf("coro: callable invocation at %+v has incomplete policy metadata", site)
		}
	case InvocationAuto:
		if plan.InvocationContract == "" || plan.InvocationABI == "" || plan.InvocationCertificate == "" {
			return InvocationFact{}, false, fmt.Errorf("coro: callable invocation at %+v has incomplete auto policy metadata", site)
		}
		if err := validateStableToken("auto invocation certificate", plan.InvocationCertificate); err != nil {
			return InvocationFact{}, false, err
		}
	case InvocationTrustedInline:
		if plan.Kind != CallTrustedInline || plan.Open || len(candidates) != 1 || plan.InvocationContract == "" ||
			plan.InvocationABI == "" || plan.InvocationCertificate == "" {
			return InvocationFact{}, false, fmt.Errorf("coro: callable invocation at %+v has invalid trusted-inline policy metadata", site)
		}
		if err := validateStableToken("trusted-inline invocation certificate", plan.InvocationCertificate); err != nil {
			return InvocationFact{}, false, err
		}
		candidate := callables[plan.Targets[0]]
		if candidate.TrustedInlineContract == "" || candidate.TrustedInlineContract != plan.InvocationContract {
			return InvocationFact{}, false, fmt.Errorf(
				"coro: trusted-inline invocation at %+v selects contract %q not owned by candidate %q",
				site, plan.InvocationContract, candidate.Ref,
			)
		}
		if plan.InvocationABI != abi {
			return InvocationFact{}, false, fmt.Errorf(
				"coro: trusted-inline invocation at %+v ABI %q differs from candidate ABI %q", site, plan.InvocationABI, abi,
			)
		}
		if _, ok := contracts[plan.InvocationContract]; !ok {
			return InvocationFact{}, false, fmt.Errorf(
				"coro: trusted-inline invocation at %+v references unknown contract %q", site, plan.InvocationContract,
			)
		}
		invocation.Policy = InvocationTrustedInline
		invocation.Contract = plan.InvocationContract
		return invocation, true, nil
	default:
		return InvocationFact{}, false, fmt.Errorf("coro: callable invocation at %+v has invalid policy %q", site, plan.InvocationPolicy)
	}

	selected := CallableContract{}
	if plan.Open {
		selected, err = contentAddressSSAAutoContract(unknownCallableContract())
	} else if len(candidateContracts) == 1 {
		selected = candidateContracts[0]
	} else {
		selected, err = contentAddressSSAAutoContract(joinCallableContracts(candidateContracts))
	}
	if err != nil {
		return InvocationFact{}, false, fmt.Errorf("coro: callable invocation at %+v: %w", site, err)
	}
	if err := addContract(selected); err != nil {
		return InvocationFact{}, false, fmt.Errorf("coro: callable invocation at %+v: %w", site, err)
	}
	if plan.InvocationPolicy == InvocationAuto {
		if plan.InvocationContract != selected.ID || plan.InvocationABI != abi {
			return InvocationFact{}, false, fmt.Errorf("coro: callable invocation at %+v has inconsistent frozen auto policy", site)
		}
	}
	invocation.Contract = selected.ID
	return invocation, true, nil
}

func (p *SSAPlan) callableInvocationSite(call ssa.CallInstruction) (SourceSiteID, error) {
	if call == nil || call.Parent() == nil || call.Block() == nil {
		return SourceSiteID{}, fmt.Errorf("coro: callable invocation has no exact SSA owner or basic block")
	}
	owner, ok := p.byFunction[call.Parent()]
	if !ok {
		return SourceSiteID{}, fmt.Errorf("coro: callable invocation owner is outside the canonical SSA plan")
	}
	ordinal, err := SemanticInstructionOrdinal(call)
	if err != nil {
		return SourceSiteID{}, fmt.Errorf("coro: identify callable invocation in function %q: %w", owner, err)
	}
	site := SourceSiteID{
		Function:    owner,
		Kind:        SourceInstruction,
		Block:       call.Block().Index,
		Instruction: ordinal,
		Successor:   -1,
		Role:        RoleCall,
		Ordinal:     0,
	}
	if err := site.Validate(); err != nil {
		return SourceSiteID{}, err
	}
	return site, nil
}

func contentAddressSSAAutoContract(contract CallableContract) (CallableContract, error) {
	contract.ID = ssaAutoContractProjectionSchema
	digest, err := CallableContractBehaviorDigest(ssaAutoContractProjectionSchema, contract)
	if err != nil {
		return CallableContract{}, fmt.Errorf("content-address Auto callable contract: %w", err)
	}
	contract.ID = ContractID(string(ssaAutoContractProjectionSchema) + "/" + digest)
	if err := contract.Validate(); err != nil {
		return CallableContract{}, err
	}
	return contract, nil
}
