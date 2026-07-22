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
	"github.com/goplus/llgo/internal/goembed"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroPlainDispatchCompilesClosedSingletonFunctionValue(t *testing.T) {
	const source = `package foo

func Target(value int) int { return value + 1 }

func Apply(fn func(int) int, value int) int {
	if fn == nil {
		return 0
	}
	return fn(value)
}

func Root() int { return Apply(Target, 41) }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	target := ssaPkg.Func("Target")
	apply := ssaPkg.Func("Apply")
	dynamicCall := coroPlainDispatchOnlyDynamicCall(t, apply)
	hashContext := &context{
		prog:             prog,
		goProg:           ssaPkg.Prog,
		goTyps:           ssaPkg.Pkg,
		goPkg:            ssaPkg,
		emissionUniverse: universe,
	}
	targetABI, err := newCoroPlainDispatchABI(hashContext, target.Signature)
	if err != nil {
		t.Fatal(err)
	}
	callABI, err := newCoroPlainDispatchABI(hashContext, dynamicCall.Common().Signature())
	if err != nil {
		t.Fatal(err)
	}
	if targetABI.hash != callABI.hash {
		t.Fatalf("target ABI hash %x differs from name-less call signature hash %x", targetABI.hash, callABI.hash)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: ssaPkg.Func("Root"), Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
			if call != dynamicCall {
				return coro.SSAClosedDynamicCallCertificate{}, false, nil
			}
			return coro.SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{target}, MayBeNil: true, SyncDispatch: true}, true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.FuncRep != coro.Dispatch || targetPlan.Emission != coro.EmitPlain || targetPlan.Primary != coro.PrimaryPlain || targetPlan.Effect != coro.NoSuspend {
		t.Fatalf("Target plan = %+v, present=%t; want one descriptor-backed plain body", targetPlan, ok)
	}
	callPlan, ok := plan.CallPlan(dynamicCall)
	if !ok || callPlan.Rep != coro.Dispatch || callPlan.Open || !callPlan.SyncDispatch || !callPlan.MayBeNil ||
		len(callPlan.Targets) != 1 || callPlan.Targets[0] != targetPlan.ID {
		t.Fatalf("Apply dynamic CallPlan = %+v, present=%t; want closed synchronous nullable singleton Dispatch", callPlan, ok)
	}

	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe,

			CoroABI:      coro.PhysicalABIV1,
			SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
			PanicABI:     coro.PanicExplicitStatusABIV0,
			FuncRepABI:   coro.FuncRepABIV1, CoroProfile: CoroProfileStackless,
		}},
	)
	if err != nil {
		t.Fatalf("compile plain dispatch package: %v", err)
	}
	module := compiled.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify plain dispatch module: %v\n%s", err, module.String())
	}
	ir := module.String()
	for _, marker := range []string{
		coroPlainDispatchDescriptorPrefix,
		coroPlainDispatchThunkPrefix,
		"llvm.trap",
		"AssertNilDeref",
		"coro.dispatch.result.size.invalid",
		"coro.dispatch.result.align.invalid",
	} {
		if !strings.Contains(ir, marker) {
			t.Fatalf("plain dispatch IR is missing %q:\n%s", marker, ir)
		}
	}
	if strings.Contains(ir, coroPrimarySuffix) {
		t.Fatalf("plain descriptor unexpectedly emitted a second coroutine body:\n%s", ir)
	}
	if got := strings.Count(ir, "define i64 @foo.Target("); got != 1 {
		t.Fatalf("Target plain body definitions = %d, want exactly one:\n%s", got, ir)
	}
}

func TestCoroPlainDispatchGateAndTargetShapeFailClosed(t *testing.T) {
	pkg, plan := buildCoroEntryTestPlan(t)
	boxedPlan, ok := plan.FunctionPlan(pkg.Func("Boxed"))
	if !ok || boxedPlan.FuncRep != coro.Dispatch {
		t.Fatalf("Boxed plan = %+v, present=%t", boxedPlan, ok)
	}
	entry := plannedFunctionSymbol{function: pkg.Func("Boxed"), plan: boxedPlan, planned: true, coroPlan: plan}
	if err := entry.checkSupported(); err == nil || !strings.Contains(err.Error(), "unimplemented dispatch descriptor") {
		t.Fatalf("gate-off dispatch error = %v", err)
	}
	entry.plainDispatch = true
	if err := entry.checkSupported(); err != nil {
		t.Fatalf("gate-on plain target rejected: %v", err)
	}

	badSignatures := []struct {
		name string
		src  string
		want string
	}{
		{"multiple results", "func Bad() (int, int) { return 1, 2 }", "multiple results"},
		{"aggregate parameter", "func Bad(value string) { _ = value }", "not a supported scalar"},
		{"variadic", "func Bad(values ...int) { _ = values }", "variadic"},
		{"nested function", "func Bad(value func()) { _ = value }", "not a supported scalar"},
	}
	for _, test := range badSignatures {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, _ := buildGoSSAPkg(t, "package foo\n"+test.src)
			fn := ssaPkg.Func("Bad")
			plan := coro.FunctionPlan{
				ID:       "bad",
				Effect:   coro.NoSuspend,
				Emission: coro.EmitPlain,
				FuncRep:  coro.Dispatch,
				External: coro.Defined,
				Primary:  coro.PrimaryPlain,
			}
			err := validateCoroPlainDispatchTarget(fn, plan)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("target validation error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCoroPlainDispatchCompilesZeroBindingClosure(t *testing.T) {
	const source = `package foo

func Apply(fn func(int) int, value int) int { return fn(value) }

func Root() int {
	fn := func(value int) int { return value + 2 }
	return Apply(fn, 40)
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	root := ssaPkg.Func("Root")
	if len(root.AnonFuncs) != 1 || len(root.AnonFuncs[0].FreeVars) != 0 {
		t.Fatalf("Root anonymous functions = %+v, want one zero-binding closure", root.AnonFuncs)
	}
	target := root.AnonFuncs[0]
	apply := ssaPkg.Func("Apply")
	dynamicCall := coroPlainDispatchOnlyDynamicCall(t, apply)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
			if call == dynamicCall {
				return coro.SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{target}, SyncDispatch: true}, true, nil
			}
			return coro.SSAClosedDynamicCallCertificate{}, false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.FuncRep != coro.Dispatch || targetPlan.Emission != coro.EmitPlain {
		t.Fatalf("zero-binding target plan = %+v, present=%t", targetPlan, ok)
	}
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe,

			CoroABI:      coro.PhysicalABIV1,
			SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
			PanicABI:     coro.PanicExplicitStatusABIV0,
			FuncRepABI:   coro.FuncRepABIV1, CoroProfile: CoroProfileStackless,
		}},
	)
	if err != nil {
		t.Fatalf("compile zero-binding descriptor closure: %v", err)
	}
	if err := llvm.VerifyModule(compiled.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify zero-binding descriptor closure: %v\n%s", err, compiled.Module().String())
	}
	ir := compiled.Module().String()
	if !strings.Contains(ir, coroPlainDispatchDescriptorPrefix) || !strings.Contains(ir, coroPlainDispatchThunkPrefix) || strings.Contains(ir, coroPrimarySuffix) {
		t.Fatalf("zero-binding closure did not use one plain descriptor body:\n%s", ir)
	}
}

func coroPlainDispatchOnlyDynamicCall(t *testing.T, fn *ssa.Function) ssa.CallInstruction {
	t.Helper()
	var found ssa.CallInstruction
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			call, ok := instr.(ssa.CallInstruction)
			if !ok || call.Common() == nil || call.Common().StaticCallee() != nil {
				continue
			}
			if found != nil {
				t.Fatalf("function %q has multiple dynamic calls", fn.Name())
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("function %q has no dynamic call", fn.Name())
	}
	return found
}
