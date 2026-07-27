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

func TestAnalyzeSSAForeignSyncAndSchedulerWaitCapabilities(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "foreign_capabilities.go", `package coroid
func leaf() {}
func caller() { leaf() }
`)
	leaf := packageFunction(t, pkg, "leaf")
	caller := packageFunction(t, pkg, "caller")
	baseConfig := planDigestSSAConfig()
	baseConfig.MaxPlainInstructions = -1

	t.Run("sync is external-known without a latency claim", func(t *testing.T) {
		config := baseConfig
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == leaf {
				return SSAFunctionPolicy{
					IgnoreBody: true, External: ExternalKnown, OverrideExternal: true,
					Exec: IRQUnsafe, ForeignSyncCertificate: "llgo.coro.foreign-sync.test.v1:exact",
				}, nil
			}
			return SSAFunctionPolicy{}, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		leafPlan, _ := plan.FunctionPlan(leaf)
		callerPlan, _ := plan.FunctionPlan(caller)
		if leafPlan.External != ExternalKnown || leafPlan.Effect != NoSuspend || leafPlan.Exec != IRQUnsafe ||
			callerPlan.Effect != NoSuspend || callerPlan.Exec.Contains(BlockForeign) {
			t.Fatalf("sync plans = leaf:%+v caller:%+v", leafPlan, callerPlan)
		}
		if certificate, ok := plan.ForeignSyncCertificate(leaf); !ok || certificate == "" {
			t.Fatalf("sync certificate = %q, %t", certificate, ok)
		}
	})

	t.Run("worker certificate retains managed foreign wait", func(t *testing.T) {
		config := baseConfig
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == leaf {
				return SSAFunctionPolicy{
					IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
					Exec: BlockForeign | IRQUnsafe, ForeignWorkerCertificate: "llgo.coro.foreign-worker.test.v1:exact",
				}, nil
			}
			return SSAFunctionPolicy{}, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		leafPlan, _ := plan.FunctionPlan(leaf)
		callerPlan, _ := plan.FunctionPlan(caller)
		if leafPlan.External != ExternalUnknownForeign || leafPlan.Effect != NoSuspend ||
			leafPlan.Exec != BlockForeign|IRQUnsafe || !callerPlan.Effect.Contains(WaitForeign) ||
			callerPlan.Emission != EmitCoroutine {
			t.Fatalf("worker managed plans = leaf:%+v caller:%+v", leafPlan, callerPlan)
		}
		if certificate, ok := plan.ForeignWorkerCertificate(leaf); !ok || certificate == "" {
			t.Fatalf("worker certificate = %q, %t", certificate, ok)
		}
	})
}

func TestAnalyzeSSAForeignCapabilitiesFailClosed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "foreign_capability_fail.go", `package coroid
func leaf() {}
func root() { leaf() }
`)
	leaf := packageFunction(t, pkg, "leaf")
	root := packageFunction(t, pkg, "root")
	validSync := SSAFunctionPolicy{
		IgnoreBody: true, External: ExternalKnown, OverrideExternal: true,
		Exec: IRQUnsafe, ForeignSyncCertificate: "sync",
	}
	validWorker := SSAFunctionPolicy{
		IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
		Exec: BlockForeign | IRQUnsafe, ForeignWorkerCertificate: "worker",
	}
	for _, test := range []struct {
		name   string
		policy SSAFunctionPolicy
		want   string
	}{
		{"mutually exclusive", func() SSAFunctionPolicy { p := validSync; p.ForeignWorkerCertificate = "worker"; return p }(), "mutually exclusive"},
		{"sync unknown foreign", func() SSAFunctionPolicy { p := validSync; p.External = ExternalUnknownForeign; return p }(), "foreign sync certificate requires"},
		{"sync claims block", func() SSAFunctionPolicy { p := validSync; p.Exec |= BlockForeign; return p }(), "foreign sync certificate requires"},
		{"worker known", func() SSAFunctionPolicy { p := validWorker; p.External = ExternalKnown; return p }(), "foreign worker certificate requires"},
		{"worker drops block", func() SSAFunctionPolicy { p := validWorker; p.Exec = IRQUnsafe; return p }(), "foreign worker certificate requires"},
		{"worker invalid UTF-8", func() SSAFunctionPolicy {
			p := validWorker
			p.ForeignWorkerCertificate = string([]byte{0xff})
			return p
		}(), "valid UTF-8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, SSAConfig{
				ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
					if fn == leaf {
						return test.policy, nil
					}
					return SSAFunctionPolicy{}, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AnalyzeSSA error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestCoroPlanDigestIncludesDistinctForeignCapabilities(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "foreign_capability_digest.go", `package coroid
func leaf() {}
func root() { leaf() }
`)
	leaf := packageFunction(t, pkg, "leaf")
	root := packageFunction(t, pkg, "root")
	build := func(certificate string) *SSAPlan {
		t.Helper()
		config := planDigestSSAConfig()
		config.MaxPlainInstructions = -1
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn != leaf {
				return SSAFunctionPolicy{}, nil
			}
			policy := SSAFunctionPolicy{
				IgnoreBody: true, External: ExternalKnown, OverrideExternal: true, Exec: IRQUnsafe,
			}
			if certificate != "" {
				policy.ForeignSyncCertificate = certificate
			}
			return policy, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	without := build("")
	withA := build("sync:a")
	withB := build("sync:b")
	metadata := validPlanDigestMetadata()
	digests := make(map[string]struct{})
	for _, plan := range []*SSAPlan{without, withA, withB} {
		digest, err := plan.CoroPlanDigest(metadata)
		if err != nil {
			t.Fatal(err)
		}
		digests[digest] = struct{}{}
	}
	if len(digests) != 3 {
		t.Fatalf("foreign sync certificate identities do not independently affect digest: %v", digests)
	}
}

func TestCoroPlanDigestIncludesWorkerCertificateWithoutWeakeningManagedPlan(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "foreign_worker_digest.go", `package coroid
func leaf() {}
func root() { leaf() }
`)
	leaf := packageFunction(t, pkg, "leaf")
	root := packageFunction(t, pkg, "root")
	build := func(certificate string) *SSAPlan {
		t.Helper()
		config := planDigestSSAConfig()
		config.MaxPlainInstructions = -1
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn != leaf {
				return SSAFunctionPolicy{}, nil
			}
			policy := SSAFunctionPolicy{
				IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
				Exec: BlockForeign | IRQUnsafe,
			}
			if certificate != "" {
				policy.ForeignWorkerCertificate = certificate
			}
			return policy, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	without := build("")
	withA := build("worker:exact:a")
	withB := build("worker:exact:b")
	withoutLeaf, _ := without.FunctionPlan(leaf)
	withLeaf, _ := withA.FunctionPlan(leaf)
	withoutRoot, _ := without.FunctionPlan(root)
	withRoot, _ := withA.FunctionPlan(root)
	if withoutLeaf != withLeaf || withoutRoot != withRoot || !withRoot.Effect.Contains(WaitForeign) {
		t.Fatalf("worker certificate changed managed plan: without leaf=%+v root=%+v; with leaf=%+v root=%+v", withoutLeaf, withoutRoot, withLeaf, withRoot)
	}
	metadata := validPlanDigestMetadata()
	digests := make(map[string]struct{})
	for _, plan := range []*SSAPlan{without, withA, withB} {
		digest, err := plan.CoroPlanDigest(metadata)
		if err != nil {
			t.Fatal(err)
		}
		digests[digest] = struct{}{}
	}
	if len(digests) != 3 {
		t.Fatalf("worker certificate identities do not independently affect digest: %v", digests)
	}
}
