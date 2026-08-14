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
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func testCallableContractCertificate(t *testing.T, scope CallableContractScope, progress ProgressClass) CallableContractCertificate {
	t.Helper()
	contract := CallableContract{
		ID:       ContractID("foreign.v1"),
		Progress: progress,
		Affinity: AffinityAnyThread,
		Reentry:  ReentryNone,
		Memory:   MemoryBorrowUntilComplete,
	}
	return testCallableContractCertificateForBehavior(t, scope, contract)
}

func testCallableContractCertificateForBehavior(t *testing.T, scope CallableContractScope, contract CallableContract) CallableContractCertificate {
	t.Helper()
	schema := contract.ID
	digest, err := CallableContractBehaviorDigest(schema, contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ID = ContractID(string(schema) + "/" + digest)
	id := sha256.Sum256([]byte(string(scope) + ":" + digest))
	certificate := CallableContractCertificate{
		ID:                        hex.EncodeToString(id[:]),
		CanonicalFunctionIdentity: "test-canonical/" + digest,
		LinkIdentity:              "test-link/" + digest,
		Contract:                  contract,
		ContractDigest:            digest,
		Scope:                     scope,
		CallableABI:               "typed.v1/test",
		TypedABISignature:         "test-signature-v1",
	}
	if scope == CallableContractScopeDeclaration {
		certificate.PhysicalSymbol = "test_foreign_" + digest
		certificate.PhysicalABISignature = certificate.TypedABISignature
	}
	if err := certificate.Validate(); err != nil {
		t.Fatalf("test callable certificate = %+v: %v", certificate, err)
	}
	return certificate
}

func TestAnalyzeSSACallableDeclarationConservativeDimensions(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "callable_dimensions.go", `package coroid
func foreign()
func caller() { foreign() }
`)
	foreign := packageFunction(t, pkg, "foreign")
	caller := packageFunction(t, pkg, "caller")
	base := CallableContract{
		ID: ContractID("foreign.v1"), Progress: ProgressExecutorSafe,
		Affinity: AffinityAnyThread, Reentry: ReentryNone, Memory: MemoryBorrowUntilReturn,
	}
	for _, test := range []struct {
		name     string
		contract CallableContract
		extra    ExecFlags
	}{
		{"affinity unknown", func() CallableContract { c := base; c.Affinity = AffinityUnknown; return c }(), ThreadAffine},
		{"owner thread", func() CallableContract { c := base; c.Affinity = AffinityOwnerThread; return c }(), ThreadAffine},
		{"host main", func() CallableContract { c := base; c.Affinity = AffinityHostMain; return c }(), ThreadAffine},
		{"managed callback", func() CallableContract { c := base; c.Reentry = ReentryManagedCallback; return c }(), NeedsRuntimeContext},
		{"reentry unknown", func() CallableContract { c := base; c.Reentry = ReentryUnknown; return c }(), OpaqueExec},
		{"memory retained", func() CallableContract { c := base; c.Memory = MemoryRetained; return c }(), OpaqueExec},
		{"memory unknown", func() CallableContract { c := base; c.Memory = MemoryUnknown; return c }(), OpaqueExec},
	} {
		t.Run(test.name, func(t *testing.T) {
			certificate := testCallableContractCertificateForBehavior(t, CallableContractScopeDeclaration, test.contract)
			exec := IRQUnsafe | test.extra
			config := planDigestSSAConfig()
			config.MaxPlainInstructions = -1
			config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn == foreign {
					return SSAFunctionPolicy{
						IgnoreBody: true, External: ExternalKnown, OverrideExternal: true, Exec: exec,
						CallableContractCertificate: certificate,
					}, nil
				}
				return SSAFunctionPolicy{}, nil
			}
			plan, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: SyncDemand}}, config)
			if err != nil {
				t.Fatal(err)
			}
			foreignPlan, _ := plan.FunctionPlan(foreign)
			callerPlan, _ := plan.FunctionPlan(caller)
			if foreignPlan.Exec != exec || !callerPlan.Exec.Contains(test.extra) {
				t.Fatalf("dimension plans = foreign:%+v caller:%+v; want extra %s", foreignPlan, callerPlan, test.extra)
			}
		})
	}
}

func TestAnalyzeSSACallableDeclarationProgressPolicies(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "callable_declaration.go", `package coroid
func foreign()
func caller() { foreign() }
`)
	foreign := packageFunction(t, pkg, "foreign")
	caller := packageFunction(t, pkg, "caller")
	for _, test := range []struct {
		progress ProgressClass
		external ExternalKind
		exec     ExecFlags
		wait     bool
	}{
		{ProgressExecutorSafe, ExternalKnown, IRQUnsafe, false},
		{ProgressMayBlock, ExternalUnknownForeign, BlockForeign | IRQUnsafe, true},
		{ProgressUnknown, ExternalUnknownForeign, BlockForeign | IRQUnsafe, true},
		{ProgressAsyncCompletion, ExternalUnknownForeign, BlockForeign | IRQUnsafe, true},
		{ProgressNoReturn, ExternalUnknownForeign, BlockForeign | IRQUnsafe | NoReturn, true},
	} {
		t.Run(string(test.progress), func(t *testing.T) {
			certificate := testCallableContractCertificate(t, CallableContractScopeDeclaration, test.progress)
			config := planDigestSSAConfig()
			config.MaxPlainInstructions = -1
			config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn != foreign {
					return SSAFunctionPolicy{}, nil
				}
				return SSAFunctionPolicy{
					IgnoreBody:                  true,
					External:                    test.external,
					OverrideExternal:            true,
					Exec:                        test.exec,
					CallableContractCertificate: certificate,
				}, nil
			}
			plan, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: SyncDemand}}, config)
			if err != nil {
				t.Fatal(err)
			}
			foreignPlan, foreignOK := plan.FunctionPlan(foreign)
			callerPlan, callerOK := plan.FunctionPlan(caller)
			if !foreignOK || !callerOK || foreignPlan.External != test.external || foreignPlan.Exec != test.exec ||
				foreignPlan.Effect != NoSuspend || callerPlan.Effect.Contains(WaitForeign) != test.wait {
				t.Fatalf("plans = foreign:%+v caller:%+v; want external=%s exec=%s wait=%t", foreignPlan, callerPlan, test.external, test.exec, test.wait)
			}
			frozen, ok := plan.CallableContractCertificate(foreign)
			if !ok || frozen != certificate || !plan.IgnoresBody(foreign) {
				t.Fatalf("frozen callable contract = %+v, %t ignored=%t; want %+v", frozen, ok, plan.IgnoresBody(foreign), certificate)
			}
			if _, ok := plan.CallableContractCertificate(caller); ok {
				t.Fatal("callable contract leaked from declaration to caller")
			}
		})
	}
}

func TestAnalyzeSSACallableWrapperStillAnalyzesBody(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "callable_wrapper.go", `package coroid
func wrapper(ch <-chan int) int { return <-ch }
func caller(ch <-chan int) int { return wrapper(ch) }
`)
	wrapper := packageFunction(t, pkg, "wrapper")
	caller := packageFunction(t, pkg, "caller")
	certificate := testCallableContractCertificate(t, CallableContractScopeWrapper, ProgressMayBlock)
	config := planDigestSSAConfig()
	config.MaxPlainInstructions = -1
	config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		if fn == wrapper {
			return SSAFunctionPolicy{CallableContractCertificate: certificate}, nil
		}
		return SSAFunctionPolicy{}, nil
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	wrapperPlan, _ := plan.FunctionPlan(wrapper)
	callerPlan, _ := plan.FunctionPlan(caller)
	if plan.IgnoresBody(wrapper) || !wrapperPlan.Effect.Contains(MayPark) || !callerPlan.Effect.Contains(MayPark) {
		t.Fatalf("wrapper/body plans = wrapper:%+v caller:%+v ignored=%t", wrapperPlan, callerPlan, plan.IgnoresBody(wrapper))
	}
	if frozen, ok := plan.CallableContractCertificate(wrapper); !ok || frozen != certificate {
		t.Fatalf("wrapper callable contract = %+v, %t; want %+v", frozen, ok, certificate)
	}
}

func TestAnalyzeSSACallableWrapperRejectsContradictoryExecutorSafeSummary(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "callable_wrapper_conflict.go", `package coroid
func wrapper(ch <-chan int) int { return <-ch }
func caller(ch <-chan int) int { return wrapper(ch) }
`)
	wrapper := packageFunction(t, pkg, "wrapper")
	caller := packageFunction(t, pkg, "caller")
	certificate := testCallableContractCertificate(t, CallableContractScopeWrapper, ProgressExecutorSafe)
	config := planDigestSSAConfig()
	config.MaxPlainInstructions = -1
	config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		if fn == wrapper {
			return SSAFunctionPolicy{CallableContractCertificate: certificate}, nil
		}
		return SSAFunctionPolicy{}, nil
	}
	_, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: SyncDemand}}, config)
	if err == nil || !strings.Contains(err.Error(), "claims executor-safe") {
		t.Fatalf("AnalyzeSSA error = %v; want contradictory executor-safe wrapper rejection", err)
	}
}

func TestAnalyzeSSACallableWrapperRejectsContradictoryAffinity(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "callable_wrapper_affinity.go", `package coroid
func ownerLeaf() {}
func wrapper() { ownerLeaf() }
func caller() { wrapper() }
`)
	ownerLeaf := packageFunction(t, pkg, "ownerLeaf")
	wrapper := packageFunction(t, pkg, "wrapper")
	caller := packageFunction(t, pkg, "caller")
	certificate := testCallableContractCertificate(t, CallableContractScopeWrapper, ProgressMayBlock)
	config := planDigestSSAConfig()
	config.MaxPlainInstructions = -1
	config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		switch fn {
		case ownerLeaf:
			return SSAFunctionPolicy{Exec: ThreadAffine}, nil
		case wrapper:
			return SSAFunctionPolicy{CallableContractCertificate: certificate}, nil
		default:
			return SSAFunctionPolicy{}, nil
		}
	}
	_, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: SyncDemand}}, config)
	if err == nil || !strings.Contains(err.Error(), "claims affinity") {
		t.Fatalf("AnalyzeSSA error = %v; want contradictory wrapper affinity rejection", err)
	}
}

func TestAnalyzeSSACallableContractsFailClosed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "callable_contract_fail.go", `package coroid
func leaf() {}
func root() { leaf() }
`)
	leaf := packageFunction(t, pkg, "leaf")
	root := packageFunction(t, pkg, "root")
	declaration := testCallableContractCertificate(t, CallableContractScopeDeclaration, ProgressExecutorSafe)
	wrapper := testCallableContractCertificate(t, CallableContractScopeWrapper, ProgressExecutorSafe)
	validDeclaration := SSAFunctionPolicy{
		IgnoreBody: true, External: ExternalKnown, OverrideExternal: true, Exec: IRQUnsafe,
		CallableContractCertificate: declaration,
	}
	for _, test := range []struct {
		name   string
		policy SSAFunctionPolicy
		want   string
	}{
		{"declaration body not ignored", func() SSAFunctionPolicy { p := validDeclaration; p.IgnoreBody = false; return p }(), "requires one ignored"},
		{"declaration wrong progress policy", func() SSAFunctionPolicy { p := validDeclaration; p.Exec |= BlockForeign; return p }(), "requires external"},
		{"wrapper body ignored", SSAFunctionPolicy{IgnoreBody: true, External: ExternalKnown, OverrideExternal: true, CallableContractCertificate: wrapper}, "analyzed defined Go body"},
		{"malformed nonzero certificate", SSAFunctionPolicy{CallableContractCertificate: CallableContractCertificate{Scope: CallableContractScopeWrapper}}, "SHA-256"},
		{"legacy noblock conflict", func() SSAFunctionPolicy { p := validDeclaration; p.ForeignNoBlockCertificate = "legacy"; return p }(), "mutually exclusive"},
		{"legacy sync conflict", func() SSAFunctionPolicy { p := validDeclaration; p.ForeignSyncCertificate = "legacy"; return p }(), "mutually exclusive"},
		{"legacy worker conflict", func() SSAFunctionPolicy { p := validDeclaration; p.ForeignWorkerCertificate = "legacy"; return p }(), "mutually exclusive"},
		{"assembly conflict", func() SSAFunctionPolicy { p := validDeclaration; p.AssemblyNoSuspendCertificate = "legacy"; return p }(), "mutually exclusive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := planDigestSSAConfig()
			config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn == leaf {
					return test.policy, nil
				}
				return SSAFunctionPolicy{}, nil
			}
			_, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AnalyzeSSA error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestCoroPlanDigestIncludesCallableContractCertificate(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "callable_contract_digest.go", `package coroid
func leaf() {}
func root() { leaf() }
`)
	leaf := packageFunction(t, pkg, "leaf")
	root := packageFunction(t, pkg, "root")
	build := func(certificate CallableContractCertificate) *SSAPlan {
		t.Helper()
		config := planDigestSSAConfig()
		config.MaxPlainInstructions = -1
		if !certificate.IsZero() {
			config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn == leaf {
					return SSAFunctionPolicy{CallableContractCertificate: certificate}, nil
				}
				return SSAFunctionPolicy{}, nil
			}
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	without := build(CallableContractCertificate{})
	executorSafe := testCallableContractCertificate(t, CallableContractScopeWrapper, ProgressExecutorSafe)
	mayBlock := testCallableContractCertificate(t, CallableContractScopeWrapper, ProgressMayBlock)
	withExecutorSafe := build(executorSafe)
	withMayBlock := build(mayBlock)
	metadata := validPlanDigestMetadata()
	digests := make(map[string]struct{})
	for _, plan := range []*SSAPlan{without, withExecutorSafe, withMayBlock} {
		digest, err := plan.CoroPlanDigest(metadata)
		if err != nil {
			t.Fatal(err)
		}
		digests[digest] = struct{}{}
	}
	if len(digests) != 3 {
		t.Fatalf("callable contract certificates are absent from plan digest: %v", digests)
	}
	document, err := withExecutorSafe.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	leafID, ok := withExecutorSafe.FunctionID(leaf)
	if !ok {
		t.Fatal("callable digest plan omitted leaf FunctionID")
	}
	found := false
	for _, function := range document.Functions {
		if function.ID == leafID {
			found = function.CallableContractCertificate != nil && *function.CallableContractCertificate == executorSafe
		}
	}
	if !found {
		t.Fatal("canonical plan digest omitted the complete callable contract certificate")
	}
}
