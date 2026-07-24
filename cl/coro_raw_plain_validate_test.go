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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroRawPlainPreflightRejectsForgedStaticEdge(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
func A() int { return 1 }
func B() int { return 2 }
func Host() int { return A() }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	host := ssaPkg.Func("Host")
	plan := analyzeCoroRawPlainValidationPlan(t, universe, ssaPkg, host, coro.SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == host {
				return coro.SSAFunctionPolicy{RawPlainEntry: true}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	var staticCall *ssa.Call
	for _, block := range host.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common().StaticCallee() == ssaPkg.Func("A") {
				staticCall = call
			}
		}
	}
	if staticCall == nil {
		t.Fatal("Host has no static A call")
	}
	// The immutable plan still names A. Mutating the source operand to the
	// signature-compatible B models any frontend/codegen edge that diverges
	// after analysis; preflight must stop before an LLVM package is created.
	staticCall.Call.Value = ssaPkg.Func("B")
	err = rawPlainValidationCompilation(plan, universe, false).preflightCoroPlan()
	if err == nil || !strings.Contains(err.Error(), "raw plain static call target disagrees with its frozen CallPlan target") {
		t.Fatalf("forged raw static edge preflight error = %v", err)
	}
}

func TestCoroRawPlainPreflightRejectsForgedLoweredEdge(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
func Helper() {}
func Host() {}
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	host, helper := ssaPkg.Func("Host"), ssaPkg.Func("Helper")
	plan := analyzeCoroRawPlainValidationPlan(t, universe, ssaPkg, host, coro.SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == host {
				return coro.SSAFunctionPolicy{RawPlainEntry: true}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyLoweredCalls: func(owner *ssa.Function) ([]coro.SSALoweredCall, error) {
			if owner == host {
				return []coro.SSALoweredCall{{LogicalName: "forged.helper", Target: helper}}, nil
			}
			return nil, nil
		},
	})
	if got := plan.LoweredCalls(host); len(got) != 1 || got[0].Target != helper {
		t.Fatalf("forged lowered plan = %+v", got)
	}
	err = rawPlainValidationCompilation(plan, universe, false).preflightCoroPlan()
	if err == nil || !strings.Contains(err.Error(), "lowered call \"forged.helper\" disagrees between the frozen emission universe and SSA plan") {
		t.Fatalf("forged raw lowered edge preflight error = %v", err)
	}
}

func TestCoroRawPlainPreflightAcceptsValidatedLocalDescriptorProducer(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
func Target(value int) int { return value + 1 }
func Apply(fn func(int) int, value int) int {
	if fn == nil { return 0 }
	return fn(value)
}
func Host(value int) int { return Apply(Target, value) }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	host, apply, target := ssaPkg.Func("Host"), ssaPkg.Func("Apply"), ssaPkg.Func("Target")
	dynamicCall := coroPlainDispatchOnlyDynamicCall(t, apply)
	var publicationCall ssa.CallInstruction
	for _, block := range host.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if ok && call.Common() != nil && call.Common().StaticCallee() == apply {
				publicationCall = call
			}
		}
	}
	if publicationCall == nil {
		t.Fatal("Host has no Apply publication call")
	}
	plan := analyzeCoroRawPlainValidationPlan(t, universe, ssaPkg, host, coro.SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == host {
				return coro.SSAFunctionPolicy{RawPlainEntry: true}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
			if call != dynamicCall {
				return coro.SSAClosedDynamicCallCertificate{}, false, nil
			}
			return coro.SSAClosedDynamicCallCertificate{
				Targets:      []*ssa.Function{target},
				MayBeNil:     true,
				SyncDispatch: true,
				SyncOnlyCallArguments: []coro.SSASyncOnlyCallArgument{{
					Call: publicationCall, Argument: 0,
				}},
			}, true, nil
		},
	})
	targetValue, found := plan.ValuePlan(target)
	if !found || len(targetValue.Funcs) != 1 || targetValue.Funcs[0].Rep != coro.Dispatch {
		t.Fatalf("Target ValuePlan = %+v, present=%t; want a local descriptor producer", targetValue, found)
	}
	err = rawPlainValidationCompilation(plan, universe, true).preflightCoroPlan()
	if err != nil {
		t.Fatalf("validated raw local descriptor producer was rejected: %v", err)
	}
}

func TestCoroRawPlainAcceptsCompilerElidedStaticCodeAddressBox(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(any) uintptr
func libc_execve_trampoline()
func Host() uintptr { return funcPCABI0(libc_execve_trampoline) }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	host := ssaPkg.Func("Host")
	plan := analyzeCoroRawPlainValidationPlan(t, universe, ssaPkg, host, coro.SSAConfig{
		ClassifyStaticCodeAddressCallArgument: func(_ *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			return universe.CoroStaticCodeAddressCallArgument(call, argument)
		},
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == host {
				return coro.SSAFunctionPolicy{RawPlainEntry: true}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
	})
	var box *ssa.MakeInterface
	for _, block := range host.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.MakeInterface); ok {
				box = candidate
			}
		}
	}
	if box != nil {
		refs := box.Referrers()
		if refs == nil || len(*refs) != 1 {
			t.Fatalf("raw funcPCABI0 box referrers = %v", refs)
		}
		direct, ok := (*refs)[0].(*ssa.Call)
		if !ok || !plan.StaticCodeAddressArgument(direct, 0) {
			t.Fatalf("raw funcPCABI0 call has no frozen static code-address argument: call=%T %v", (*refs)[0], (*refs)[0])
		}
	}
	if box == nil || !coroCompilerElidedFunctionAddressBox(plan, universe, host, box) {
		t.Fatal("raw funcPCABI0 operand is not recognized as one compiler-elided static code address")
	}
	if err := rawPlainValidationCompilation(plan, universe, false).preflightCoroPlan(); err != nil {
		t.Fatalf("compiler-elided raw static code address rejected: %v", err)
	}
}

func TestCoroRawPlainPreflightRejectsCriticalMarker(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
import _ "unsafe"
//go:linkname enter llgo.coroCriticalEnter
func enter()
//go:linkname exit llgo.coroCriticalExit
func exit()
var cell uint32
func Host(value uint32) { enter(); cell = value; exit() }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	host := ssaPkg.Func("Host")
	plan := analyzeCoroRawPlainValidationPlan(t, universe, ssaPkg, host, coro.SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == host {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, Exec: coro.NeedsPreempt, RawPlainEntry: true}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
	})
	err = rawPlainValidationCompilation(plan, universe, false).preflightCoroPlan()
	if err == nil || !strings.Contains(err.Error(), "managed critical intrinsic is invalid in a raw/plain body") {
		t.Fatalf("raw critical marker preflight error = %v", err)
	}
}

func analyzeCoroRawPlainValidationPlan(
	t *testing.T,
	universe *EmissionUniverse,
	ssaPkg *ssa.Package,
	root *ssa.Function,
	extra coro.SSAConfig,
) *coro.SSAPlan {
	t.Helper()
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	extra.EmissionUniverse = ssaUniverse
	extra.FunctionIDs = functionIDs
	extra.MaxPlainInstructions = -1
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, RawPlainDemand: true}}, extra)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func rawPlainValidationCompilation(plan *coro.SSAPlan, universe *EmissionUniverse, plainDispatch bool) *Compilation {
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	if plainDispatch {
		compilation.FuncRepABI = coro.FuncRepABIV1
	}
	return compilation
}
