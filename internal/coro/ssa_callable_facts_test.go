//go:build !llgo

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
	"bytes"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestSSAPlanCallableContractFactsProjectsExactAutoAndTrustedInlineCalls(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "callable_facts.go", `package coroid
func foreign(int) int
func root(value int) int {
	value = foreign(value)
	return foreign(value)
}
`)
	foreign := packageFunction(t, pkg, "foreign")
	root := packageFunction(t, pkg, "root")
	calls := staticCallsTo(t, root, foreign)
	if len(calls) != 2 {
		t.Fatalf("root calls to foreign = %d, want 2", len(calls))
	}
	certificate := testTrustedInlineTargetCertificateForContracts(t, CallableContract{
		ID: ContractID("foreign.v1"), Progress: ProgressUnknown,
		Affinity: AffinityUnknown, Reentry: ReentryUnknown, Memory: MemoryUnknown,
	}, CallableContract{
		ID: ContractID("foreign.v1"), Progress: ProgressExecutorSafe,
		Affinity: AffinityAnyThread, Reentry: ReentryNone, Memory: MemoryBorrowUntilReturn,
	})
	defaultExec := CallableContractExecConstraints(certificate.Contract)
	config := planDigestSSAConfig()
	config.MaxPlainInstructions = -1
	config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		if fn != foreign {
			return SSAFunctionPolicy{}, nil
		}
		return SSAFunctionPolicy{
			IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
			Exec: BlockForeign | IRQUnsafe | defaultExec, CallableContractCertificate: certificate,
		}, nil
	}
	config.ClassifyTrustedInlineCall = func(_ *ssa.Function, call ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error) {
		if call != calls[1] {
			return SSATrustedInlineCallCertificate{}, false, nil
		}
		return SSATrustedInlineCallCertificate{
			ID:       "callable-facts.invocation.v1",
			Contract: certificate.TrustedInlineContract.ID,
			ABI:      certificate.CallableABI,
		}, true, nil
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}

	facts, err := plan.CallableContractFacts()
	if err != nil {
		t.Fatal(err)
	}
	if err := facts.Verify(); err != nil {
		t.Fatalf("projected facts do not verify: %v", err)
	}
	if len(facts.Callables) != 1 || len(facts.Contracts) != 2 || len(facts.Invocations) != 2 {
		t.Fatalf("projected facts = %+v", facts)
	}
	contracts := make(map[ContractID]CallableContract, len(facts.Contracts))
	for _, contract := range facts.Contracts {
		contracts[contract.ID] = contract
	}
	if got := CallableContractExecConstraints(contracts[certificate.Contract.ID]); got != ThreadAffine|OpaqueExec {
		t.Fatalf("default fact execution projection = %s", got)
	}
	if got := CallableContractExecConstraints(contracts[certificate.TrustedInlineContract.ID]); got != 0 {
		t.Fatalf("selected fact execution projection = %s", got)
	}
	callable := facts.Callables[0]
	foreignID, _ := plan.FunctionID(foreign)
	if callable.Ref != CallableRefID(ssaCallableRefPrefix+certificate.ID) || callable.Function != foreignID ||
		callable.ABI != certificate.CallableABI || callable.Contract != certificate.Contract.ID ||
		callable.TrustedInlineContract != certificate.TrustedInlineContract.ID {
		t.Fatalf("callable fact = %+v", callable)
	}
	for index, invocation := range facts.Invocations {
		if len(invocation.Candidates) != 1 || invocation.Candidates[0] != callable.Ref || invocation.Open ||
			invocation.ABI != callable.ABI || invocation.Site.Function == "" || invocation.Site.Kind != SourceInstruction ||
			invocation.Site.Role != RoleCall || invocation.Site.Ordinal != 0 {
			t.Fatalf("invocation[%d] = %+v", index, invocation)
		}
	}
	if auto := facts.Invocations[0]; auto.Policy != InvocationAuto || auto.Contract != certificate.Contract.ID {
		t.Fatalf("auto invocation = %+v", auto)
	}
	if trusted := facts.Invocations[1]; trusted.Policy != InvocationTrustedInline ||
		trusted.Contract != certificate.TrustedInlineContract.ID {
		t.Fatalf("trusted-inline invocation = %+v", trusted)
	}

	firstJSON, err := facts.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	again, err := plan.CallableContractFacts()
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := again.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("canonical callable facts changed across projections:\n%s\n%s", firstJSON, secondJSON)
	}
	firstDigest, err := facts.Digest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := again.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == "" || firstDigest != secondDigest {
		t.Fatalf("callable facts digests = %q, %q", firstDigest, secondDigest)
	}
}

func TestSSAPlanCallableContractFactsOpenCallUsesContentAddressedUnknown(t *testing.T) {
	plan, call, certificate := buildCallableFactsAutoPlan(t)
	callPlan := plan.callPlans[call]
	callPlan.Open = true
	callPlan.Unresolved = UnknownForeign
	plan.callPlans[call] = callPlan

	facts, err := plan.CallableContractFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Invocations) != 1 {
		t.Fatalf("invocations = %+v", facts.Invocations)
	}
	invocation := facts.Invocations[0]
	if !invocation.Open || invocation.Policy != InvocationAuto || invocation.Contract == certificate.Contract.ID {
		t.Fatalf("open invocation = %+v", invocation)
	}
	if !strings.HasPrefix(string(invocation.Contract), string(ssaAutoContractProjectionSchema)+"/") {
		t.Fatalf("open invocation contract %q is not content-addressed by the SSA Auto schema", invocation.Contract)
	}
	var selected CallableContract
	for _, contract := range facts.Contracts {
		if contract.ID == invocation.Contract {
			selected = contract
			break
		}
	}
	if selected.Progress != ProgressUnknown || selected.Affinity != AffinityUnknown ||
		selected.Reentry != ReentryUnknown || selected.Memory != MemoryUnknown {
		t.Fatalf("open Auto contract = %+v", selected)
	}
	if err := facts.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestSSAPlanCallableContractFactsFailsClosed(t *testing.T) {
	t.Run("partial candidate contracts", func(t *testing.T) {
		plan, call, _ := buildCallableFactsAutoPlan(t)
		rootID, _ := plan.FunctionID(call.Parent())
		callPlan := plan.callPlans[call]
		callPlan.Targets = append(callPlan.Targets, rootID)
		callPlan.Open = true
		plan.callPlans[call] = callPlan
		_, err := plan.CallableContractFacts()
		if err == nil || !strings.Contains(err.Error(), "only 1 of 2 known candidates") {
			t.Fatalf("partial-candidate error = %v", err)
		}
	})

	t.Run("identity-less foreign", func(t *testing.T) {
		prog, pkg := buildCoroTestSSA(t, "callable_facts_unknown.go", `package coroid
func foreign()
func root() { foreign() }
`)
		foreign := packageFunction(t, pkg, "foreign")
		root := packageFunction(t, pkg, "root")
		config := planDigestSSAConfig()
		config.MaxPlainInstructions = -1
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == foreign {
				return SSAFunctionPolicy{
					IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
					Exec: BlockForeign | IRQUnsafe,
				}, nil
			}
			return SSAFunctionPolicy{}, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		_, err = plan.CallableContractFacts()
		if err == nil || !strings.Contains(err.Error(), "no frozen callable identity") {
			t.Fatalf("identity-less foreign error = %v", err)
		}
	})

	t.Run("trusted-inline metadata mismatch", func(t *testing.T) {
		plan, call, certificate := buildCallableFactsAutoPlan(t)
		callPlan := plan.callPlans[call]
		callPlan.Kind = CallTrustedInline
		callPlan.InvocationPolicy = InvocationTrustedInline
		callPlan.InvocationContract = certificate.Contract.ID
		callPlan.InvocationABI = certificate.CallableABI
		callPlan.InvocationCertificate = "forged.invocation.v1"
		plan.callPlans[call] = callPlan
		_, err := plan.CallableContractFacts()
		if err == nil || !strings.Contains(err.Error(), "not owned by candidate") {
			t.Fatalf("trusted-inline mismatch error = %v", err)
		}
	})
}

func buildCallableFactsAutoPlan(t *testing.T) (*SSAPlan, ssa.CallInstruction, CallableContractCertificate) {
	t.Helper()
	prog, pkg := buildCoroTestSSA(t, "callable_facts_auto.go", `package coroid
func foreign(int) int
func root(value int) int { return foreign(value) }
`)
	foreign := packageFunction(t, pkg, "foreign")
	root := packageFunction(t, pkg, "root")
	call := firstStaticCallTo(t, root, foreign)
	certificate := testTrustedInlineTargetCertificate(t)
	config := planDigestSSAConfig()
	config.MaxPlainInstructions = -1
	config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		if fn == foreign {
			return SSAFunctionPolicy{
				IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
				Exec: BlockForeign | IRQUnsafe, CallableContractCertificate: certificate,
			}, nil
		}
		return SSAFunctionPolicy{}, nil
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	return plan, call, certificate
}

func staticCallsTo(t *testing.T, caller, target *ssa.Function) []ssa.CallInstruction {
	t.Helper()
	var calls []ssa.CallInstruction
	for _, block := range caller.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if ok && call.Common() != nil && call.Common().StaticCallee() == target {
				calls = append(calls, call)
			}
		}
	}
	return calls
}
