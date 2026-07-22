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

package build

import (
	"go/ast"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

type foreignCapabilityBuildFixture struct {
	program     llssa.Program
	pkg         *ssa.Package
	input       CoroPlanInput
	functionIDs coro.FunctionIDConfig
}

func newForeignCapabilityBuildFixture(t *testing.T) *foreignCapabilityBuildFixture {
	t.Helper()
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/foreigncapability", `package foreigncapability
//llgo:coro sync
//go:linkname Sync C.foreign_sync_exact
func Sync(int) int

//llgo:coro schedulerwait
//go:linkname SchedulerWait C.foreign_schedulerwait_exact
func SchedulerWait(int) int

//llgo:coro worker
//go:linkname Worker C.foreign_worker_exact
func Worker(int) int

func Managed(v int) int { return SchedulerWait(v) }
func ManagedWorker(v int) int { return Worker(v) }
func hostHelper(v int) int { return Sync(v) + SchedulerWait(v) }
func Host(v int) int { return hostHelper(v) }
func SourceRaw(v int) int { return SchedulerWait(v) }
`, nil)
	program := llssa.NewProgram(nil)
	emission, err := cl.PrepareEmissionUniverse(program, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: []*ast.File{files[0]}, Identity: "example.com/foreigncapability",
	}})
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	return &foreignCapabilityBuildFixture{
		program: program,
		pkg:     ssaPkg,
		input: CoroPlanInput{
			Program:              ssaPkg.Prog,
			EmissionUniverse:     ssaEmission,
			resolveFunction:      emission.Resolve,
			functionBackground:   emission.FunctionBackground,
			foreignNoBlock:       emission.CoroForeignNoBlockCertificate,
			foreignSync:          emission.CoroForeignSyncCertificate,
			foreignSchedulerWait: emission.CoroForeignSchedulerWaitCertificate,
			foreignWorker:        emission.CoroForeignWorkerCertificate,
		},
		functionIDs: functionIDs,
	}
}

func TestCoroWorkerCapabilityPreservesManagedWaitAndExactIdentity(t *testing.T) {
	fixture := newForeignCapabilityBuildFixture(t)
	defer fixture.close()
	worker := fixture.pkg.Func("Worker")
	managed := fixture.pkg.Func("ManagedWorker")
	plan, err := fixture.analyze(fixture.input, coro.Roots{{Function: managed, Demand: coro.SyncDemand}})
	if err != nil {
		t.Fatal(err)
	}
	workerPlan, ok := plan.FunctionPlan(worker)
	callerPlan, callerOK := plan.FunctionPlan(managed)
	if !ok || !callerOK || workerPlan.External != coro.ExternalUnknownForeign || workerPlan.Effect != coro.NoSuspend ||
		workerPlan.Exec != coro.BlockForeign|coro.IRQUnsafe || !callerPlan.Effect.Contains(coro.WaitForeign) ||
		callerPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("worker capability plans = leaf:%+v caller:%+v", workerPlan, callerPlan)
	}
	frontend, certified, err := fixture.input.foreignWorker(worker)
	if err != nil || !certified || frontend.ID == "" {
		t.Fatalf("frozen frontend worker certificate = %+v, %t, %v", frontend, certified, err)
	}
	planned, plannedOK := plan.ForeignWorkerCertificate(worker)
	if !plannedOK || planned != frontend.ID {
		t.Fatalf("planned worker certificate = %q, %t; want exact frontend identity %q", planned, plannedOK, frontend.ID)
	}
}

func (fixture *foreignCapabilityBuildFixture) close() {
	fixture.program.Dispose()
}

func (fixture *foreignCapabilityBuildFixture) analyze(input CoroPlanInput, roots coro.Roots) (*coro.SSAPlan, error) {
	return input.Analyze(roots, coro.SSAConfig{
		MaxPlainInstructions: -1,
		FunctionIDs:          fixture.functionIDs,
	})
}

func TestCoroForeignCapabilitiesPreserveManagedWaitAndAuthorizeExactHostIsland(t *testing.T) {
	fixture := newForeignCapabilityBuildFixture(t)
	defer fixture.close()
	wait := fixture.pkg.Func("SchedulerWait")
	syncLeaf := fixture.pkg.Func("Sync")
	managed := fixture.pkg.Func("Managed")
	host := fixture.pkg.Func("Host")
	hostHelper := fixture.pkg.Func("hostHelper")

	managedPlan, err := fixture.analyze(fixture.input, coro.Roots{{Function: managed, Demand: coro.SyncDemand}})
	if err != nil {
		t.Fatal(err)
	}
	waitPlan, _ := managedPlan.FunctionPlan(wait)
	callerPlan, _ := managedPlan.FunctionPlan(managed)
	if waitPlan.External != coro.ExternalUnknownForeign || waitPlan.Effect != coro.NoSuspend ||
		waitPlan.Exec != coro.BlockForeign|coro.IRQUnsafe || !callerPlan.Effect.Contains(coro.WaitForeign) ||
		callerPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("managed schedulerwait plans = leaf:%+v caller:%+v", waitPlan, callerPlan)
	}

	hostInput := fixture.input
	hostInput.requiredRoots = coro.Roots{{Function: host, Demand: coro.SyncDemand}}
	hostInput.requiredPlain = map[*ssa.Function]struct{}{host: {}}
	hostInput.requiredHostPlain = map[*ssa.Function]struct{}{host: {}}
	hostPlan, err := fixture.analyze(hostInput, nil)
	if err != nil {
		t.Fatal(err)
	}
	hostFunctionPlan, _ := hostPlan.FunctionPlan(host)
	if !hostPlan.HasRawPlainVariant(host) || hostFunctionPlan.ManagedDemand != coro.NoDemand || !hostFunctionPlan.RawPlainDemand ||
		!hostFunctionPlan.RawPlainOnly || !hostFunctionPlan.RawPlainEntry || hostFunctionPlan.Emission != coro.EmitRawPlain {
		t.Fatalf("host plan = %+v, raw=%t; want exact raw-only host entry", hostFunctionPlan, hostPlan.HasRawPlainVariant(host))
	}
	hostHelperPlan, _ := hostPlan.FunctionPlan(hostHelper)
	if !hostPlan.HasRawPlainVariant(hostHelper) || hostHelperPlan.ManagedDemand != coro.NoDemand || !hostHelperPlan.RawPlainDemand ||
		!hostHelperPlan.RawPlainOnly || hostHelperPlan.RawPlainEntry || hostHelperPlan.Emission != coro.EmitRawPlain {
		t.Fatalf("host helper plan = %+v, raw=%t; want internal compiler-owned raw-only host closure member", hostHelperPlan, hostPlan.HasRawPlainVariant(hostHelper))
	}
	if got, _ := hostPlan.FunctionPlan(wait); got.External != coro.ExternalUnknownForeign ||
		got.Exec != coro.BlockForeign|coro.IRQUnsafe {
		t.Fatalf("host analysis weakened schedulerwait managed target = %+v", got)
	}
	if got, _ := hostPlan.FunctionPlan(syncLeaf); got.External != coro.ExternalKnown || got.Exec != coro.IRQUnsafe {
		t.Fatalf("host analysis sync target = %+v", got)
	}
}

func TestCoroSchedulerWaitRejectsNonCompilerRawIsland(t *testing.T) {
	fixture := newForeignCapabilityBuildFixture(t)
	defer fixture.close()
	sourceRaw := fixture.pkg.Func("SourceRaw")
	input := fixture.input
	input.requiredRoots = coro.Roots{{Function: sourceRaw, Demand: coro.SyncDemand}}
	input.requiredPlain = map[*ssa.Function]struct{}{sourceRaw: {}}
	_, err := fixture.analyze(input, nil)
	if err == nil || !strings.Contains(err.Error(), "outside a compiler-owned raw host/scheduler-stack island") {
		t.Fatalf("non-host schedulerwait raw error = %v", err)
	}
}

func TestCoroForeignCapabilitiesRejectForgedBuilderCertificates(t *testing.T) {
	fixture := newForeignCapabilityBuildFixture(t)
	defer fixture.close()
	syncLeaf := fixture.pkg.Func("Sync")
	wait := fixture.pkg.Func("SchedulerWait")
	worker := fixture.pkg.Func("Worker")
	managed := fixture.pkg.Func("Managed")

	for _, test := range []struct {
		name   string
		mutate func(*CoroPlanInput)
		policy coro.SSAFunctionPolicy
		target *ssa.Function
		want   string
	}{
		{
			name: "sync callback absent", target: syncLeaf,
			mutate: func(input *CoroPlanInput) { input.foreignSync = nil },
			policy: coro.SSAFunctionPolicy{ForeignSyncCertificate: "forged"},
			want:   "without exact frozen frontend sync metadata",
		},
		{
			name: "schedulerwait mismatch", target: wait,
			mutate: func(input *CoroPlanInput) {},
			policy: coro.SSAFunctionPolicy{ForeignSchedulerWaitCertificate: "forged"},
			want:   "conflicts with the frozen frontend proof",
		},
		{
			name: "worker callback absent", target: worker,
			mutate: func(input *CoroPlanInput) { input.foreignWorker = nil },
			policy: coro.SSAFunctionPolicy{ForeignWorkerCertificate: "forged"},
			want:   "without exact frozen frontend worker metadata",
		},
		{
			name: "worker identity mismatch", target: worker,
			mutate: func(input *CoroPlanInput) {},
			policy: coro.SSAFunctionPolicy{ForeignWorkerCertificate: "forged"},
			want:   "conflicts with the frozen frontend proof",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := fixture.input
			test.mutate(&input)
			_, err := input.Analyze(coro.Roots{{Function: managed, Demand: coro.SyncDemand}}, coro.SSAConfig{
				MaxPlainInstructions: -1,
				FunctionIDs:          fixture.functionIDs,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == test.target {
						return test.policy, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Analyze error = %v; want %q", err, test.want)
			}
		})
	}
}
