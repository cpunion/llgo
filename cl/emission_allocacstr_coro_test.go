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

func TestAllocaCStrIntrinsicClassificationDoesNotGeneralizeAllocCStr(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/allocacstrclassification", `package allocacstrclassification
//llgo:link AllocaCStr llgo.allocaCStr
func AllocaCStr(string) *int8
//llgo:link AllocCStr llgo.allocCStr
func AllocCStr(string) *int8
func UseAlloca(value string) *int8 { return AllocaCStr(value) }
func UseAlloc(value string) *int8 { return AllocCStr(value) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if semantics, intrinsic, err := universe.CoroIntrinsicSemantics(pkg.ssa.Func("AllocaCStr")); err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineWithLoweredCalls {
		t.Fatalf("AllocaCStr function semantics = %v, %v, %v; want inline-with-lowered-calls, true, nil", semantics, intrinsic, err)
	}
	if semantics, intrinsic, err := universe.CoroIntrinsicSemantics(pkg.ssa.Func("AllocCStr")); err != nil || !intrinsic || semantics != CoroIntrinsicCallUnsupported {
		t.Fatalf("AllocCStr function semantics = %v, %v, %v; want unsupported, true, nil", semantics, intrinsic, err)
	}

	owner := universe.packages[pkg.ssa]
	useAlloca := pkg.ssa.Func("UseAlloca")
	ctx, err := universe.functionABIContext(useAlloca, owner)
	if err != nil {
		t.Fatal(err)
	}
	foundCStrCopy := false
	var allocaCall ssa.CallInstruction
	for _, block := range useAlloca.Blocks {
		for _, instruction := range block.Instrs {
			for _, helper := range universe.loweredRuntimeHelpers(ctx, instruction) {
				if helper == "CStrCopy" {
					foundCStrCopy = true
				}
			}
			if call, ok := instruction.(ssa.CallInstruction); ok {
				allocaCall = call
			}
		}
	}
	if !foundCStrCopy {
		t.Fatal("AllocaCStr lowering omitted its CStrCopy runtime helper")
	}
	if allocaCall == nil {
		t.Fatal("UseAlloca has no intrinsic call")
	}
	if semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(allocaCall); err != nil || !intrinsic || semantics != CoroIntrinsicCallUnsupported {
		t.Fatalf("incomplete-runtime AllocaCStr semantics = %v, %v, %v; want legacy unsupported, true, nil", semantics, intrinsic, err)
	}
}

func TestAllocaCStrElidesOnlyIntrinsicAndFreezesCStrCopy(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
type Pointer uintptr
type String struct {
	data Pointer
	len int
}
func AllocU(uintptr) unsafe.Pointer { return nil }
func CStrCopy(Pointer, String) *int8 { return nil }
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/allocacstr", `package allocacstr
//llgo:link AllocaCStr llgo.allocaCStr
func AllocaCStr(string) *int8
func Use(value string) *int8 { return AllocaCStr(value) }
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
	if !universe.CompleteRuntimeABI() {
		t.Fatal("complete AllocaCStr test universe lost its runtime ABI contract")
	}

	owner := callerPkg.ssa.Func("Use")
	allocHelper := runtimePkg.ssa.Func("AllocU")
	helper := runtimePkg.ssa.Func("CStrCopy")
	lowered, err := universe.CoroLoweredCalls(owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered) != 2 || lowered[0].LogicalName != "AllocU" || lowered[0].Target != allocHelper ||
		lowered[1].LogicalName != "CStrCopy" || lowered[1].Target != helper {
		t.Fatalf("AllocaCStr lowered calls = %+v; want exact owner-scoped AllocU and CStrCopy", lowered)
	}
	calls := allocaCStrTestCalls(owner)
	if len(calls) != 1 {
		t.Fatalf("Use calls = %d, want one AllocaCStr SSA call", len(calls))
	}
	call := calls[0]
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineWithLoweredCalls {
		t.Fatalf("AllocaCStr semantics = %v, %v, %v; want inline-with-lowered-calls, true, nil", semantics, intrinsic, err)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.EntryResolutionABIV0
	functionIDs.SchedulerABI = coro.SchedulerNoneABIV0
	functionIDs.ArchiveReady = true
	analyze := func() (*coro.SSAPlan, error) {
		return coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: owner, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			FunctionIDs:      functionIDs,
			EmissionUniverse: ssaUniverse,
			ResolveFunction: func(fn *ssa.Function) (*ssa.Function, bool, error) {
				resolved, ok := universe.Resolve(fn)
				return resolved, ok, nil
			},
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == helper {
					return coro.SSAFunctionPolicy{Effect: coro.WaitPlatform}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyElidedCall: func(_ *ssa.Function, site ssa.CallInstruction) (bool, error) {
				semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(site)
				return intrinsic && semantics.ElidesManagedCall(), err
			},
			ClassifyLoweredCalls: universe.CoroLoweredCalls,
		})
	}
	plan, err := analyze()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ElidesCall(call) {
		t.Fatal("AllocaCStr intrinsic declaration edge was not elided")
	}
	if _, ok := plan.CallPlan(call); ok {
		t.Fatal("AllocaCStr intrinsic declaration unexpectedly retained a managed CallPlan")
	}
	plannedLowered := plan.LoweredCalls(owner)
	if len(plannedLowered) != 2 || plannedLowered[0].LogicalName != "AllocU" || plannedLowered[0].Target != allocHelper ||
		plannedLowered[1].LogicalName != "CStrCopy" || plannedLowered[1].Target != helper {
		t.Fatalf("planned AllocaCStr lowered calls = %+v; want exact AllocU and CStrCopy", plannedLowered)
	}
	ownerPlan, ok := plan.FunctionPlan(owner)
	if !ok || !ownerPlan.Effect.Contains(coro.WaitPlatform) {
		t.Fatalf("AllocaCStr owner plan = %+v, %v; want CStrCopy suspend effect propagation", ownerPlan, ok)
	}

	metadata := coro.PlanDigestMetadata{
		CoroABI: coro.EntryResolutionABIV0, SchedulerABI: coro.SchedulerNoneABIV0,
		PanicABI: coro.PanicLegacyABIV0, FuncRepABI: coro.FuncRepABIV0,
		LoweringFactsSchema: coro.LoweringFactsSchema, LoweringFactsDigest: strings.Repeat("0", 64),
		TargetTriple: "x86_64-unknown-linux-gnu", PointerBits: 64,
		Endianness: "little", DataLayout: "e-p:64:64",
	}
	digest, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	again, err := analyze()
	if err != nil {
		t.Fatal(err)
	}
	againDigest, err := again.CoroPlanDigest(metadata)
	if err != nil || digest != againDigest {
		t.Fatalf("AllocaCStr plan digest = %q, %v; want stable %q", againDigest, err, digest)
	}

	delete(universe.loweredCalls[owner], "CStrCopy")
	if semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineWithLoweredCalls {
		t.Fatalf("AllocaCStr frozen semantics after scratch mutation = %v, %v, %v", semantics, intrinsic, err)
	}
}

func allocaCStrTestCalls(fn *ssa.Function) []ssa.CallInstruction {
	var calls []ssa.CallInstruction
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(ssa.CallInstruction); ok {
				calls = append(calls, call)
			}
		}
	}
	return calls
}
