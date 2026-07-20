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

func testTrustedInlineTargetCertificate(t *testing.T) CallableContractCertificate {
	t.Helper()
	base := CallableContract{
		ID: ContractID("foreign.v1"), Progress: ProgressMayBlock,
		Affinity: AffinityAnyThread, Reentry: ReentryNone, Memory: MemoryBorrowUntilComplete,
	}
	trusted := CallableContract{
		ID: ContractID("foreign.v1"), Progress: ProgressExecutorSafe,
		Affinity: AffinityAnyThread, Reentry: ReentryNone, Memory: MemoryBorrowUntilReturn,
	}
	return testTrustedInlineTargetCertificateForContracts(t, base, trusted)
}

func testTrustedInlineTargetCertificateForContracts(
	t *testing.T,
	base, trusted CallableContract,
) CallableContractCertificate {
	t.Helper()
	certificate := testCallableContractCertificateForBehavior(t, CallableContractScopeDeclaration, base)
	digest, err := CallableContractBehaviorDigest(trusted.ID, trusted)
	if err != nil {
		t.Fatal(err)
	}
	trusted.ID = ContractID(string(trusted.ID) + "/" + digest)
	certificate.TrustedInlineContract = trusted
	certificate.TrustedInlineContractDigest = digest
	certificate.HasTrustedInlineContract = true
	id := sha256.Sum256([]byte(certificate.ID + ":trusted-inline:" + digest))
	certificate.ID = hex.EncodeToString(id[:])
	if err := certificate.Validate(); err != nil {
		t.Fatalf("trusted-inline target certificate = %+v: %v", certificate, err)
	}
	return certificate
}

func TestAnalyzeSSATrustedInlineReplacesDefaultContractExecProjection(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "trusted_inline_projection.go", `package coroid
func foreign(int) int { return 0 }
func root(v int) int { return foreign(v) }
`)
	foreign := packageFunction(t, pkg, "foreign")
	root := packageFunction(t, pkg, "root")
	call := firstStaticCallTo(t, root, foreign)
	targetCertificate := testTrustedInlineTargetCertificateForContracts(t, CallableContract{
		ID: ContractID("foreign.v1"), Progress: ProgressUnknown,
		Affinity: AffinityUnknown, Reentry: ReentryUnknown, Memory: MemoryUnknown,
	}, CallableContract{
		ID: ContractID("foreign.v1"), Progress: ProgressExecutorSafe,
		Affinity: AffinityAnyThread, Reentry: ReentryNone, Memory: MemoryBorrowUntilReturn,
	})
	defaultExec := CallableContractExecConstraints(targetCertificate.Contract)
	selectedExec := CallableContractExecConstraints(targetCertificate.TrustedInlineContract)
	if defaultExec != ThreadAffine|OpaqueExec || selectedExec != 0 {
		t.Fatalf("test projections = default:%s selected:%s", defaultExec, selectedExec)
	}

	config := planDigestSSAConfig()
	config.MaxPlainInstructions = -1
	config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		if fn == foreign {
			return SSAFunctionPolicy{
				IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
				Exec:                        BlockForeign | IRQUnsafe | defaultExec,
				CallableContractCertificate: targetCertificate,
			}, nil
		}
		return SSAFunctionPolicy{}, nil
	}
	auto, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	autoRoot, _ := auto.FunctionPlan(root)
	if !autoRoot.Effect.Contains(WaitForeign) || autoRoot.Exec != ThreadAffine|OpaqueExec|IRQUnsafe|MayUnwind ||
		autoRoot.Primary != PrimaryCoroutine {
		t.Fatalf("auto root = %+v", autoRoot)
	}

	config.ClassifyTrustedInlineCall = func(_ *ssa.Function, candidate ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error) {
		if candidate != call {
			return SSATrustedInlineCallCertificate{}, false, nil
		}
		return SSATrustedInlineCallCertificate{
			ID: "invocation.unknown.safe.v1", Contract: targetCertificate.TrustedInlineContract.ID,
			ABI: targetCertificate.CallableABI,
		}, true, nil
	}
	trusted, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	trustedRoot, _ := trusted.FunctionPlan(root)
	if trustedRoot.Effect != NoSuspend || trustedRoot.Exec != IRQUnsafe || trustedRoot.Primary != PrimaryPlain {
		t.Fatalf("trusted root = %+v", trustedRoot)
	}
	targetPlan, _ := trusted.FunctionPlan(foreign)
	if targetPlan.Exec != BlockForeign|IRQUnsafe|ThreadAffine|OpaqueExec {
		t.Fatalf("trusted target default plan changed = %+v", targetPlan)
	}
	callPlan, ok := trusted.CallPlan(call)
	if !ok || callPlan.Kind != CallTrustedInline || callPlan.InvocationContract != targetCertificate.TrustedInlineContract.ID {
		t.Fatalf("trusted call plan = %+v, present=%t", callPlan, ok)
	}
	explicitConfig := config
	explicitConfig.OutcomeMode = OutcomeExplicitStatus
	explicit, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, explicitConfig)
	if err != nil {
		t.Fatal(err)
	}
	explicitRoot, _ := explicit.FunctionPlan(root)
	if explicitRoot.LocalEffect != NoSuspend || explicitRoot.Effect != NoSuspend ||
		explicitRoot.Exec.Contains(MayUnwind) || explicitRoot.Primary != PrimaryPlain {
		t.Fatalf("explicit-status trusted root = %+v", explicitRoot)
	}
	metadata := validPlanDigestMetadata()
	autoDigest, err := auto.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	trustedDigest, err := trusted.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if autoDigest == trustedDigest {
		t.Fatalf("selected execution projection did not change plan digest %q", autoDigest)
	}
}

func TestAnalyzeSSATrustedInlineIsAnExactInvocationCapability(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "trusted_inline.go", `package coroid
func foreign(int) int { return 0 }
func root(v int) int { return foreign(v) }
`)
	foreign := packageFunction(t, pkg, "foreign")
	root := packageFunction(t, pkg, "root")
	call := firstStaticCallTo(t, root, foreign)
	targetCertificate := testTrustedInlineTargetCertificate(t)

	base := planDigestSSAConfig()
	base.MaxPlainInstructions = -1
	base.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		if fn == foreign {
			return SSAFunctionPolicy{
				IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
				Exec: BlockForeign | IRQUnsafe, CallableContractCertificate: targetCertificate,
			}, nil
		}
		return SSAFunctionPolicy{}, nil
	}
	auto, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, base)
	if err != nil {
		t.Fatal(err)
	}
	autoRoot, _ := auto.FunctionPlan(root)
	if !autoRoot.Effect.Contains(WaitForeign) || autoRoot.Primary != PrimaryCoroutine {
		t.Fatalf("auto root = %+v", autoRoot)
	}

	trustedConfig := base
	trustedConfig.ClassifyTrustedInlineCall = func(_ *ssa.Function, candidate ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error) {
		if candidate != call {
			return SSATrustedInlineCallCertificate{}, false, nil
		}
		return SSATrustedInlineCallCertificate{
			ID:       "invocation.read.nonblocking.v1",
			Contract: targetCertificate.TrustedInlineContract.ID,
			ABI:      targetCertificate.CallableABI,
		}, true, nil
	}
	trusted, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, trustedConfig)
	if err != nil {
		t.Fatal(err)
	}
	trustedRoot, _ := trusted.FunctionPlan(root)
	if trustedRoot.Effect != NoSuspend || trustedRoot.Exec.Contains(BlockForeign) ||
		!trustedRoot.Exec.Contains(IRQUnsafe) || trustedRoot.Primary != PrimaryPlain {
		t.Fatalf("trusted root = %+v", trustedRoot)
	}
	callPlan, ok := trusted.CallPlan(call)
	if !ok || callPlan.Kind != CallTrustedInline || callPlan.Rep != DirectPlain ||
		callPlan.InvocationPolicy != InvocationTrustedInline || callPlan.InvocationContract != targetCertificate.TrustedInlineContract.ID ||
		callPlan.InvocationABI != targetCertificate.CallableABI || callPlan.InvocationCertificate != "invocation.read.nonblocking.v1" {
		t.Fatalf("trusted call plan = %+v, present=%t", callPlan, ok)
	}
	if frozen, ok := trusted.CallableContractCertificate(foreign); !ok || frozen != targetCertificate {
		t.Fatalf("trusted-inline target certificate = %+v, %t; want %+v", frozen, ok, targetCertificate)
	}

	metadata := validPlanDigestMetadata()
	autoDigest, err := auto.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	trustedDigest, err := trusted.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if autoDigest == trustedDigest {
		t.Fatalf("trusted-inline invocation did not change plan digest %q", autoDigest)
	}
}

func TestCoroPlanDigestBindsTargetOwnedTrustedInlineRefinement(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "trusted_inline_digest.go", `package coroid
func foreign() {}
func root() { foreign() }
`)
	foreign := packageFunction(t, pkg, "foreign")
	root := packageFunction(t, pkg, "root")
	withoutRefinement := testCallableContractCertificate(t, CallableContractScopeDeclaration, ProgressMayBlock)
	withRefinement := testTrustedInlineTargetCertificate(t)
	if withoutRefinement.Contract != withRefinement.Contract || withoutRefinement.ContractDigest != withRefinement.ContractDigest ||
		withoutRefinement.ID == withRefinement.ID {
		t.Fatalf("test certificates do not isolate trusted-inline binding: without=%+v with=%+v", withoutRefinement, withRefinement)
	}
	// Freeze-side tests prove the certificate ID changes. Hold it constant here
	// to prove the canonical plan digest also retains the complete value+bool
	// refinement rather than relying only on that opaque ID.
	withSameID := withRefinement
	withSameID.ID = withoutRefinement.ID
	if err := withSameID.Validate(); err != nil {
		t.Fatalf("same-ID structural certificate used for plan-digest isolation is invalid: %v", err)
	}
	build := func(certificate CallableContractCertificate) *SSAPlan {
		t.Helper()
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
		return plan
	}
	metadata := validPlanDigestMetadata()
	withoutDigest, err := build(withoutRefinement).CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	withDigest, err := build(withSameID).CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if withoutDigest == withDigest {
		t.Fatalf("target-owned trusted-inline refinement is absent from plan digest %q", withDigest)
	}
}

func TestAnalyzeSSATrustedInlineFailsClosed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "trusted_inline_fail.go", `package coroid
func foreign() {}
func root() { foreign() }
`)
	foreign := packageFunction(t, pkg, "foreign")
	root := packageFunction(t, pkg, "root")
	call := firstStaticCallTo(t, root, foreign)
	targetCertificate := testTrustedInlineTargetCertificate(t)
	foreignPolicy := func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		if fn == foreign {
			return SSAFunctionPolicy{
				IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
				Exec: BlockForeign | IRQUnsafe, CallableContractCertificate: targetCertificate,
			}, nil
		}
		return SSAFunctionPolicy{}, nil
	}
	for _, test := range []struct {
		name     string
		classify func(*ssa.Function, ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error)
		policy   func(*ssa.Function) (SSAFunctionPolicy, error)
		want     string
	}{
		{
			name: "facts without classification",
			classify: func(*ssa.Function, ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error) {
				return SSATrustedInlineCallCertificate{ID: "forged", Contract: "inline", ABI: "typed"}, false, nil
			},
			policy: foreignPolicy, want: "unclassified",
		},
		{
			name: "missing ABI",
			classify: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error) {
				if candidate == call {
					return SSATrustedInlineCallCertificate{ID: "exact", Contract: "inline"}, true, nil
				}
				return SSATrustedInlineCallCertificate{}, false, nil
			},
			policy: foreignPolicy, want: "empty trusted-inline ABI",
		},
		{
			name: "globally reclassified target",
			classify: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error) {
				if candidate == call {
					return SSATrustedInlineCallCertificate{ID: "exact", Contract: "inline", ABI: "typed"}, true, nil
				}
				return SSATrustedInlineCallCertificate{}, false, nil
			},
			policy: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn == foreign {
					return SSAFunctionPolicy{
						IgnoreBody: true, External: ExternalKnown, OverrideExternal: true, Exec: IRQUnsafe,
						CallableContractCertificate: testCallableContractCertificate(t, CallableContractScopeDeclaration, ProgressExecutorSafe),
					}, nil
				}
				return SSAFunctionPolicy{}, nil
			},
			want: "must remain",
		},
		{
			name: "target has no callable certificate",
			classify: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error) {
				if candidate == call {
					return SSATrustedInlineCallCertificate{ID: "exact", Contract: targetCertificate.TrustedInlineContract.ID, ABI: targetCertificate.CallableABI}, true, nil
				}
				return SSATrustedInlineCallCertificate{}, false, nil
			},
			policy: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn == foreign {
					return SSAFunctionPolicy{IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true, Exec: BlockForeign | IRQUnsafe}, nil
				}
				return SSAFunctionPolicy{}, nil
			},
			want: "no target-owned callable contract",
		},
		{
			name: "target owns no refinement",
			classify: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error) {
				if candidate == call {
					return SSATrustedInlineCallCertificate{ID: "exact", Contract: targetCertificate.TrustedInlineContract.ID, ABI: targetCertificate.CallableABI}, true, nil
				}
				return SSATrustedInlineCallCertificate{}, false, nil
			},
			policy: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn == foreign {
					without := testCallableContractCertificate(t, CallableContractScopeDeclaration, ProgressMayBlock)
					return SSAFunctionPolicy{
						IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
						Exec: BlockForeign | IRQUnsafe, CallableContractCertificate: without,
					}, nil
				}
				return SSAFunctionPolicy{}, nil
			},
			want: "owns no trusted-inline refinement",
		},
		{
			name: "wrong target contract",
			classify: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error) {
				if candidate == call {
					return SSATrustedInlineCallCertificate{ID: "exact", Contract: "foreign.v1/wrong", ABI: targetCertificate.CallableABI}, true, nil
				}
				return SSATrustedInlineCallCertificate{}, false, nil
			},
			policy: foreignPolicy, want: "claims contract",
		},
		{
			name: "wrong target ABI",
			classify: func(_ *ssa.Function, candidate ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error) {
				if candidate == call {
					return SSATrustedInlineCallCertificate{ID: "exact", Contract: targetCertificate.TrustedInlineContract.ID, ABI: "typed.v1/wrong"}, true, nil
				}
				return SSATrustedInlineCallCertificate{}, false, nil
			},
			policy: foreignPolicy, want: "claims ABI",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := planDigestSSAConfig()
			config.MaxPlainInstructions = -1
			config.ClassifyFunction = test.policy
			config.ClassifyTrustedInlineCall = test.classify
			_, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AnalyzeSSA error = %v; want %q", err, test.want)
			}
		})
	}
}

func firstStaticCallTo(t *testing.T, owner, target *ssa.Function) ssa.CallInstruction {
	t.Helper()
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if ok && call.Common() != nil && call.Common().StaticCallee() == target {
				return call
			}
		}
	}
	t.Fatalf("function %q has no static call to %q", owner.Name(), target.Name())
	return nil
}
