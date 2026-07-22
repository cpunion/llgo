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

func TestCoroPlanInputUsesOnlyFrozenAssemblyNoSuspendCertificate(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/asmcert", `package asmcert
func Leaf(value int) int
func Call(value int) int { return Leaf(value) }
`, nil)
	physical := "example.com/asmcert.Leaf"
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: []*ast.File{files[0]}, Identity: ssaPkg.Pkg.Path(),
		AssemblyNoSuspendProofs: []cl.CoroAssemblyNoSuspendProof{{
			PhysicalSymbol: physical,
			ABISignature:   `{"args":["i64"],"results":["i64"]}`,
			CallClosure:    []string{physical},
			ClosureSHA256:  strings.Repeat("2b", 32),
		}},
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
		assemblyNoSuspend: func(fn *ssa.Function) (string, bool, error) {
			certificate, ok, err := emission.CoroAssemblyNoSuspendCertificate(fn)
			return certificate.ID, ok, err
		},
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.EntryResolutionABIV0
	functionIDs.SchedulerABI = coro.SchedulerNoneABIV0
	functionIDs.ArchiveReady = true
	roots := coro.Roots{{Function: ssaPkg.Func("Call"), Demand: coro.SyncDemand}}
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
	leaf := ssaPkg.Func("Leaf")
	certificate, ok := plan.AssemblyNoSuspendCertificate(leaf)
	if !ok || certificate == "" {
		t.Fatal("assembly declaration lost its exact no-suspend certificate")
	}
	leafPlan, ok := plan.FunctionPlan(leaf)
	if !ok || leafPlan.External != coro.ExternalKnown || leafPlan.Effect != coro.NoSuspend ||
		leafPlan.Exec != coro.IRQUnsafe || leafPlan.Emission != coro.EmitExternal || !plan.IgnoresBody(leaf) {
		t.Fatalf("assembly Leaf plan = %+v, %t; want ignored external-known no-suspend IRQ-unsafe", leafPlan, ok)
	}
	callerPlan, _ := plan.FunctionPlan(ssaPkg.Func("Call"))
	if callerPlan.Effect != coro.NoSuspend || !callerPlan.Exec.Contains(coro.IRQUnsafe) || callerPlan.Exec.Contains(coro.BlockForeign) {
		t.Fatalf("Call plan = %+v; want retained no-suspend assembly edge", callerPlan)
	}

	_, err = analyze(input, func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if fn == leaf {
			return coro.SSAFunctionPolicy{AssemblyNoSuspendCertificate: "forged"}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with the frozen proof") {
		t.Fatalf("forged assembly certificate error = %v; want frozen-proof mismatch", err)
	}

	withoutFrozenProof := input
	withoutFrozenProof.assemblyNoSuspend = nil
	_, err = analyze(withoutFrozenProof, func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if fn == leaf {
			return coro.SSAFunctionPolicy{AssemblyNoSuspendCertificate: certificate}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "without exact frozen translated-module metadata") {
		t.Fatalf("unfrozen assembly certificate error = %v; want fail-closed rejection", err)
	}
}
