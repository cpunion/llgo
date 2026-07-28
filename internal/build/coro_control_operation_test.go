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
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
)

func TestCoroPlanInputSeedsTypedControlExecutionWithoutForeignLeaf(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/control", `package control
//llgo:link Fork llgo.controlFork
func Fork() int32
//llgo:link Exit llgo.controlExit
func Exit(int32)
func root(stop bool) int32 {
	pid := Fork()
	if stop { Exit(2) }
	return pid
}
`, nil)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: "example.com/control",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("root")
	input := CoroPlanInput{
		Program:            ssaPkg.Prog,
		EmissionUniverse:   ssaEmission,
		resolveFunction:    emission.Resolve,
		functionBackground: emission.FunctionBackground,
		callSitePlan:       emission.CoroCallSitePlan,
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := input.Analyze(
		coro.Roots{{Function: root, Demand: coro.AsyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1, FunctionIDs: functionIDs},
	)
	if err != nil {
		t.Fatal(err)
	}
	rootPlan, planned := plan.FunctionPlan(root)
	if !planned || !rootPlan.LocalExec.Contains(coro.IRQUnsafe) ||
		!rootPlan.Exec.Contains(coro.IRQUnsafe) {
		t.Fatalf("typed-control root plan = %+v, present=%t; want local irq-unsafe execution", rootPlan, planned)
	}
	if rootPlan.LocalExec.Contains(coro.NoReturn) || rootPlan.Exec.Contains(coro.NoReturn) {
		t.Fatalf("conditional process exit incorrectly made the complete root no-return: %+v", rootPlan)
	}
	for _, call := range coroPlanTestCalls(root) {
		site, frozen, err := emission.CoroCallSitePlan(call)
		if err != nil || !frozen || site.ControlOperation == cl.CoroControlNone ||
			!plan.ElidesCall(call) {
			t.Fatalf("typed-control site %q = %+v, %v, %v (elided=%t)", call, site, frozen, err, plan.ElidesCall(call))
		}
		if _, managed := plan.CallPlan(call); managed {
			t.Fatalf("typed-control site %q retained a managed callee edge", call)
		}
	}
}
