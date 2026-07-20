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

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestCoroCompilerElidedImplicitFaultHelperInventoryFailsClosed(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocZ(size uintptr) unsafe.Pointer { return nil }
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/implicitinventory", `package implicitinventory
func Root() *byte { return new(byte) }
`)
	testProg.ssa.Build()

	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		t.Fatal(err)
	}

	root := callerPkg.ssa.Func("Root")
	var allocation *ssa.Alloc
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.Alloc); ok && candidate.Heap {
				allocation = candidate
			}
		}
	}
	if allocation == nil {
		t.Fatal("implicit helper inventory fixture has no heap allocation")
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, nil, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if helpers := strings.Join(universe.loweredRuntimeHelpers(audit.ctx, allocation), ","); helpers != "AllocZ" {
		t.Fatalf("heap allocation helper inventory = %q, want AllocZ", helpers)
	}
	if reason := audit.requireOnlyCompilerElidedRuntimeHelpers(
		allocation, "CheckIndexRange", "AssertNilDeref",
	); !strings.Contains(reason, "non-elided runtime helper(s) AllocZ") {
		t.Fatalf("unexpected implicit-fault helper inventory rejection = %q", reason)
	}
}

func TestCoroImplicitIndexAddrRequiresExplicitStatus(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
func Root(values []byte, index int) byte { return values[index] }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		OutcomeMode:          coro.OutcomeExplicitStatus,
	})
	if err != nil {
		t.Fatal(err)
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, "")
	if err != nil {
		t.Fatal(err)
	}
	proof := audit.currentFrameRetentionProof()

	var indexAddr *ssa.IndexAddr
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.IndexAddr); ok {
				indexAddr = candidate
			}
		}
	}
	if indexAddr == nil {
		t.Fatal("explicit-status gate fixture has no IndexAddr")
	}
	if proof == nil || !proof.provesGuardableStableAddress(indexAddr, indexAddr) {
		t.Fatal("dynamic slice IndexAddr lacks its guardable frame-retention proof")
	}
	if helpers := strings.Join(universe.loweredRuntimeHelpers(audit.ctx, indexAddr), ","); helpers != "CheckIndexRange" {
		t.Fatalf("dynamic slice IndexAddr helpers = %q, want CheckIndexRange", helpers)
	}
	if reason := audit.validateIndexAddr(indexAddr); !strings.Contains(reason, "index base is not a fixed-array pointer") {
		t.Fatalf("IndexAddr without ExplicitStatus rejection = %q", reason)
	}
	audit.allowImplicitNilFault = true
	if reason := audit.validateIndexAddr(indexAddr); reason != "" {
		t.Fatalf("IndexAddr with ExplicitStatus rejected: %s", reason)
	}
}

func TestEmissionUniverseImplicitIndexPlainHelperRetainsRawDemand(t *testing.T) {
	testProg := newEmissionTestProgram()
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func CheckIndexRange(ok bool, index int64, signed bool, length int) {}
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/implicitplain", `package implicitplain
func Root(values []byte, index int) byte { return values[index] }
`)
	testProg.ssa.Build()

	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		t.Fatal(err)
	}
	root := callerPkg.ssa.Func("Root")
	helper := runtimePkg.ssa.Func("CheckIndexRange")
	if target, ok, err := universe.ResolveCoroPlainLoweredCall(root, "CheckIndexRange"); err != nil || !ok || target != helper {
		t.Fatalf("plain CheckIndexRange = %v, %t, %v; want exact runtime helper", target, ok, err)
	}
	if calls, err := universe.CoroLoweredCalls(root); err != nil {
		t.Fatal(err)
	} else if len(calls) != 0 {
		t.Fatalf("physical Index lowered calls = %+v, want compiler-owned fault guard", calls)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:                 ssaUniverse,
		FunctionIDs:                      functionIDs,
		MaxPlainInstructions:             -1,
		OutcomeMode:                      coro.OutcomeExplicitStatus,
		ClassifyLoweredCalls:             universe.CoroLoweredCalls,
		ClassifyRawPlainDemandReferences: universe.CoroSyncDemandReferences,
	})
	if err != nil {
		t.Fatal(err)
	}
	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.Effect.Contains(coro.AwaitStructured) {
		t.Fatalf("physical Index owner plan = %+v, present=%t; want coroutine without helper await", rootPlan, ok)
	}
	helperPlan, ok := plan.FunctionPlan(helper)
	if !ok || helperPlan.ManagedDemand != coro.NoDemand || !helperPlan.RawPlainDemand ||
		!helperPlan.RawPlainOnly || helperPlan.Emission != coro.EmitRawPlain || !plan.HasRawPlainVariant(helper) {
		t.Fatalf("plain CheckIndexRange plan = %+v, present=%t, raw-variant=%t", helperPlan, ok, plan.HasRawPlainVariant(helper))
	}
}
