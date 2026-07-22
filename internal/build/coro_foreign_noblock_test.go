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

func TestCoroPlanInputUsesOnlyFrozenForeignNoBlockCertificate(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/noblock", `package noblock
//llgo:coro noblock
//go:linkname Safe C.safe_exact
func Safe(int) int
//go:linkname Memcpy C.memcpy
func Memcpy(uintptr)
func SafeCaller() int { return Safe(1) }
func OrdinaryCaller(n uintptr) { Memcpy(n) }
`, nil)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: []*ast.File{files[0]}, Identity: "example.com/noblock",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:            ssaPkg.Prog,
		EmissionUniverse:   ssaEmission,
		resolveFunction:    emission.Resolve,
		functionBackground: emission.FunctionBackground,
		foreignNoBlock:     emission.CoroForeignNoBlockCertificate,
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	roots := coro.Roots{
		{Function: ssaPkg.Func("SafeCaller"), Demand: coro.SyncDemand},
		{Function: ssaPkg.Func("OrdinaryCaller"), Demand: coro.SyncDemand},
	}
	analyze := func(in CoroPlanInput, classify func(*ssa.Function) (coro.SSAFunctionPolicy, error)) (*coro.SSAPlan, error) {
		return in.Analyze(roots, coro.SSAConfig{
			MaxPlainInstructions: -1,
			FunctionIDs:          functionIDs,
			ClassifyFunction:     classify,
		})
	}
	plan, err := analyze(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	safe := ssaPkg.Func("Safe")
	certificate, ok := plan.ForeignNoBlockCertificate(safe)
	if !ok || certificate == "" {
		t.Fatal("certified C declaration lost its exact proof in SSAPlan")
	}
	safePlan, _ := plan.FunctionPlan(safe)
	if safePlan.External != coro.ExternalKnown || safePlan.Effect != coro.NoSuspend || safePlan.Exec != coro.IRQUnsafe ||
		safePlan.Exec.Contains(coro.BlockForeign) || safePlan.Emission != coro.EmitExternal {
		t.Fatalf("certified Safe plan = %+v; want external-known/no-suspend/irq-unsafe without block-foreign", safePlan)
	}
	safeCaller, _ := plan.FunctionPlan(ssaPkg.Func("SafeCaller"))
	if safeCaller.Effect != coro.NoSuspend || !safeCaller.Exec.Contains(coro.IRQUnsafe) || safeCaller.Exec.Contains(coro.BlockForeign) {
		t.Fatalf("SafeCaller plan = %+v; want direct bounded foreign call with retained IRQUnsafe", safeCaller)
	}
	ordinary, _ := plan.FunctionPlan(ssaPkg.Func("Memcpy"))
	ordinaryCaller, _ := plan.FunctionPlan(ssaPkg.Func("OrdinaryCaller"))
	if ordinary.External != coro.ExternalUnknownForeign || !ordinary.Exec.Contains(coro.BlockForeign|coro.IRQUnsafe) ||
		!ordinaryCaller.Effect.Contains(coro.WaitForeign) {
		t.Fatalf("ordinary foreign plans = leaf:%+v caller:%+v; want default fail-closed boundary", ordinary, ordinaryCaller)
	}

	_, err = analyze(input, func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if fn == safe {
			return coro.SSAFunctionPolicy{ForeignNoBlockCertificate: "forged"}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with the frozen frontend proof") {
		t.Fatalf("conflicting builder certificate error = %v; want fail-closed mismatch", err)
	}
	forgedInput := input
	forgedInput.foreignNoBlock = nil
	_, err = analyze(forgedInput, func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if fn == safe {
			return coro.SSAFunctionPolicy{ForeignNoBlockCertificate: certificate}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "without exact frozen frontend noblock metadata") {
		t.Fatalf("unfrozen builder certificate error = %v; want fail-closed rejection", err)
	}

	// Build the same effective function/call plan without retaining the source
	// proof. The private certificate must still change the archive cache digest.
	uncertifiedInput := input
	uncertifiedInput.foreignNoBlock = nil
	uncertified, err := analyze(uncertifiedInput, func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if fn == safe {
			return coro.SSAFunctionPolicy{
				IgnoreBody: true, External: coro.ExternalKnown, OverrideExternal: true, Exec: coro.IRQUnsafe,
			}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := uncertified.FunctionPlan(safe); got != safePlan {
		t.Fatalf("uncertified effective Safe plan = %+v, want same %+v", got, safePlan)
	}
	metadata := coro.PlanDigestMetadata{
		CoroABI: coro.PhysicalABIV1, SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI: coro.PanicExplicitStatusABIV0, FuncRepABI: coro.FuncRepABIV1,
		LoweringFactsSchema: coro.LoweringFactsSchema, LoweringFactsDigest: strings.Repeat("0", 64),
		TargetTriple: "x86_64-unknown-linux-gnu", PointerBits: 64,
		Endianness: "little", DataLayout: "e-p:64:64",
	}
	certifiedDigest, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	uncertifiedDigest, err := uncertified.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if certifiedDigest == uncertifiedDigest {
		t.Fatalf("foreign noblock certificate did not change archive plan digest %q", certifiedDigest)
	}
}
