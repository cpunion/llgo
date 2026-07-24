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
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
)

func TestCoroPlanInputTotalCallableIdentityKeepsUnknownAndLegacyExecutionConservative(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/callableidentityplan", `package callableidentityplan
//go:linkname Unknown C.callable_identity_unknown
func Unknown()

//llgo:coro worker
//go:linkname Legacy C.callable_identity_legacy
func Legacy()

func Root() { Unknown(); Legacy() }
`, nil)
	program := llssa.NewProgram(nil)
	defer program.Dispose()
	emission, err := cl.PrepareEmissionUniverse(program, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: []*ast.File{files[0]}, Identity: "example.com/callableidentityplan",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	input := CoroPlanInput{
		Program:            ssaPkg.Prog,
		EmissionUniverse:   ssaEmission,
		resolveFunction:    emission.Resolve,
		functionBackground: emission.FunctionBackground,
		foreignWorker:      emission.CoroForeignWorkerCertificate,
		callableIdentity:   emission.CoroCallableIdentityCertificate,
		callableContract:   emission.CoroCallableContractCertificate,
	}
	plan, err := input.Analyze(
		coro.Roots{{Function: ssaPkg.Func("Root"), Demand: coro.SyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1, FunctionIDs: functionIDs},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"Unknown", "Legacy"} {
		fn := ssaPkg.Func(name)
		identity, ok := plan.CallableIdentityCertificate(fn)
		frontend, frontendOK, frontendErr := emission.CoroCallableIdentityCertificate(fn)
		if !ok || frontendErr != nil || !frontendOK || identity != frontend {
			t.Fatalf("%s identity = %+v, %t; frontend=%+v, %t, %v", name, identity, ok, frontend, frontendOK, frontendErr)
		}
		contract, generic := plan.CallableContractCertificate(fn)
		if name == "Unknown" {
			if !generic || contract.Contract.Progress != coro.ProgressMayBlock ||
				contract.Contract.Affinity != coro.AffinityAnyThread ||
				contract.Contract.Reentry != coro.ReentryNone ||
				contract.Contract.Memory != coro.MemoryBorrowUntilComplete {
				t.Fatalf("Unknown default foreign contract = %+v, %t", contract, generic)
			}
		} else if generic {
			t.Fatalf("Legacy policy unexpectedly acquired a generic behavior contract: %+v", contract)
		}
		functionPlan, _ := plan.FunctionPlan(fn)
		if functionPlan.External != coro.ExternalUnknownForeign ||
			functionPlan.Exec != coro.BlockForeign|coro.IRQUnsafe || functionPlan.Effect != coro.NoSuspend {
			t.Fatalf("%s execution policy = %+v", name, functionPlan)
		}
		if functionPlan.Exec.Contains(coro.ThreadAffine | coro.OpaqueExec) {
			t.Fatalf("%s identity-only inventory changed execution constraints: %s", name, functionPlan.Exec)
		}
	}
	rootPlan, _ := plan.FunctionPlan(ssaPkg.Func("Root"))
	if !rootPlan.Effect.Contains(coro.WaitForeign) {
		t.Fatalf("Root plan = %+v; want conservative foreign wait", rootPlan)
	}

	facts, err := plan.CallableContractFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Callables) != 2 || len(facts.Invocations) != 2 {
		t.Fatalf("total callable facts = %+v", facts)
	}
	for _, callable := range facts.Callables {
		var contract coro.CallableContract
		for _, candidate := range facts.Contracts {
			if candidate.ID == callable.Contract {
				contract = candidate
				break
			}
		}
		fn, ok := plan.Function(callable.Function)
		if !ok || fn == nil {
			t.Fatalf("callable %q has no planned function %q", callable.Ref, callable.Function)
		}
		switch fn.Name() {
		case "Unknown":
			if contract.Progress != coro.ProgressMayBlock || contract.Affinity != coro.AffinityAnyThread ||
				contract.Reentry != coro.ReentryNone || contract.Memory != coro.MemoryBorrowUntilComplete {
				t.Fatalf("default foreign callable %q behavior = %+v", callable.Ref, contract)
			}
		case "Legacy":
			if contract.Progress != coro.ProgressUnknown || contract.Affinity != coro.AffinityUnknown ||
				contract.Reentry != coro.ReentryUnknown || contract.Memory != coro.MemoryUnknown {
				t.Fatalf("legacy identity-only callable %q behavior = %+v", callable.Ref, contract)
			}
		default:
			t.Fatalf("unexpected callable function %q", fn.Name())
		}
	}
	if err := facts.Verify(); err != nil {
		t.Fatal(err)
	}
}
