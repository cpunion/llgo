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
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSACallableIdentityIsPolicyNeutralAndDigestBound(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "callable_identity_plan.go", `package coroid
func foreign()
func root() { foreign() }
`)
	foreign := packageFunction(t, pkg, "foreign")
	root := packageFunction(t, pkg, "root")
	firstIdentity := testCallableIdentityCertificate(t)
	changedFields := firstIdentity
	changedFields.ID = ""
	changedFields.CanonicalFunctionIdentity = "test/other-exact-declaration"
	secondIdentity, err := FreezeCallableIdentityCertificate(changedFields)
	if err != nil {
		t.Fatal(err)
	}

	build := func(identity CallableIdentityCertificate) *SSAPlan {
		t.Helper()
		config := planDigestSSAConfig()
		config.MaxPlainInstructions = -1
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == foreign {
				return SSAFunctionPolicy{
					IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
					Exec: BlockForeign | IRQUnsafe, CallableIdentityCertificate: identity,
				}, nil
			}
			return SSAFunctionPolicy{}, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	first, second := build(firstIdentity), build(secondIdentity)
	if frozen, ok := first.CallableIdentityCertificate(foreign); !ok || frozen != firstIdentity {
		t.Fatalf("frozen identity = %+v, %t", frozen, ok)
	}
	if _, generic := first.CallableContractCertificate(foreign); generic {
		t.Fatal("identity-only declaration acquired a generic contract")
	}
	firstForeign, _ := first.FunctionPlan(foreign)
	secondForeign, _ := second.FunctionPlan(foreign)
	if firstForeign != secondForeign || firstForeign.External != ExternalUnknownForeign ||
		firstForeign.Exec != BlockForeign|IRQUnsafe || firstForeign.Exec.Contains(ThreadAffine|OpaqueExec) {
		t.Fatalf("identity changed execution policy: first=%+v second=%+v", firstForeign, secondForeign)
	}

	facts, err := first.CallableContractFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Callables) != 1 || len(facts.Invocations) != 1 || len(facts.Contracts) != 1 {
		t.Fatalf("identity-only facts = %+v", facts)
	}
	if callable := facts.Callables[0]; callable.Ref != CallableRefID(ssaCallableIdentityRefPrefix+firstIdentity.ID) ||
		callable.ABI != firstIdentity.CallableABI || callable.TrustedInlineContract != "" {
		t.Fatalf("identity-only callable = %+v", callable)
	}
	if contract := facts.Contracts[0]; contract.Progress != ProgressUnknown || contract.Affinity != AffinityUnknown ||
		contract.Reentry != ReentryUnknown || contract.Memory != MemoryUnknown {
		t.Fatalf("synthetic unknown behavior = %+v", contract)
	}

	metadata := validPlanDigestMetadata()
	firstDigest, err := first.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatalf("callable identity change did not change plan digest %q", firstDigest)
	}
}

func TestAnalyzeSSACallableIdentityRejectsContractIdentityMismatch(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "callable_identity_mismatch.go", `package coroid
func foreign()
func root() { foreign() }
`)
	foreign := packageFunction(t, pkg, "foreign")
	root := packageFunction(t, pkg, "root")
	contract := testCallableContractCertificate(t, CallableContractScopeDeclaration, ProgressMayBlock)
	identity := testCallableIdentityCertificate(t)
	_, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == foreign {
				return SSAFunctionPolicy{
					IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
					Exec:                        BlockForeign | IRQUnsafe,
					CallableIdentityCertificate: identity,
					CallableContractCertificate: contract,
				}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "identity fields differ") {
		t.Fatalf("identity/contract mismatch error = %v", err)
	}
}
