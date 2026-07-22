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
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroClosedInterfacePlainSource = `package foo

var gate chan uint32

type Value interface { Value() uint32 }
type concrete uint32

func (value concrete) Value() uint32 { return uint32(value) + 1 }

func Root(value Value) uint32 {
	<-gate
	return value.Value()
}
`

func TestCoroClosedInterfacePlainInvokeKeepsItabAcrossCoroSplit(t *testing.T) {
	prog, pkg, plan, root, method, invoke := compileCoroClosedInterfacePlainFixture(t, coroClosedInterfacePlainSource, coro.DynamicCHAClosed)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.FuncRep != coro.DirectCoro || !rootPlan.Effect.Contains(coro.MayPark) {
		t.Fatalf("Root plan = %+v, present=%t; want a parking direct coroutine", rootPlan, ok)
	}
	methodPlan, ok := plan.FunctionPlan(method)
	if !ok || methodPlan.Emission != coro.EmitPlain || methodPlan.Primary != coro.PrimaryPlain || methodPlan.FuncRep != coro.Dispatch || methodPlan.Effect != coro.NoSuspend {
		t.Fatalf("concrete.Value plan = %+v, present=%t; want a no-suspend plain interface target", methodPlan, ok)
	}
	callPlan, ok := plan.CallPlan(invoke)
	if !ok || callPlan.Open || callPlan.Rep != coro.Dispatch || len(callPlan.Targets) == 0 || !coroInterfaceTargetContains(callPlan.Targets, methodPlan.ID) {
		t.Fatalf("interface CallPlan = %+v, present=%t; want a nonempty closed Dispatch target set containing the declared method", callPlan, ok)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify closed interface coroutine before CoroSplit: %v\n%s", err, module.String())
	}
	rootIR := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	assertCoroClosedInterfacePlainIR(t, rootIR)
	if ir := module.String(); strings.Contains(ir, coroPlainDispatchDescriptorPrefix) || strings.Contains(ir, coroPlainDispatchThunkPrefix) {
		t.Fatalf("interface invoke incorrectly materialized a function-value descriptor:\n%s", ir)
	}

	runCoroABITestPipeline(t, prog, module)
	resume := module.NamedFunction("foo.Root$coro.resume")
	if resume.IsNil() {
		t.Fatalf("CoroSplit did not create Root resume entry:\n%s", module.String())
	}
	assertCoroClosedInterfacePlainIR(t, resume.String())
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify closed interface coroutine after CoroSplit: %v\n%s", err, module.String())
	}
}

func TestCoroClosedInterfacePlainTargetMayAlsoHaveStaticCalls(t *testing.T) {
	const source = `package foo
var gate chan uint32
type Value interface { Value() uint32 }
type concrete uint32
func (value concrete) Value() uint32 { return uint32(value) + 1 }
func Root(value Value, direct concrete) uint32 {
	<-gate
	observed := direct.Value()
	return observed + value.Value()
}
`
	prog, pkg, _, _, _, _ := compileCoroClosedInterfacePlainFixture(t, source, coro.DynamicCHAClosed)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify interface target with static call: %v\n%s", err, module.String())
	}
	if ir := module.String(); strings.Contains(ir, coroPlainDispatchDescriptorPrefix) || strings.Contains(ir, coroPlainDispatchThunkPrefix) {
		t.Fatalf("static method call incorrectly forced an interface target descriptor:\n%s", ir)
	}
}

func TestCoroDormantInterfaceInvokeDoesNotTurnStaticMethodIntoFunctionValue(t *testing.T) {
	const source = `package foo
var gate chan struct{}
type text interface { String() string }
type concrete string
func (value concrete) String() string { return string(value) }
func dormant(value text) string { return value.String() }
func Root(value concrete) string {
	<-gate
	return value.String()
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	program := newLLSSAProg(t)
	defer program.Dispose()
	universe, plan, root, dormant, method, invoke := prepareCoroDormantInterfaceFixture(t, program, ssaPkg, files)

	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("Root plan = %+v, present=%t; want one emitted coroutine", rootPlan, ok)
	}
	dormantPlan, ok := plan.FunctionPlan(dormant)
	if !ok || dormantPlan.Emission != coro.EmitNone {
		t.Fatalf("dormant plan = %+v, present=%t; want EmitNone", dormantPlan, ok)
	}
	methodPlan, ok := plan.FunctionPlan(method)
	if !ok || methodPlan.Emission != coro.EmitPlain || methodPlan.FuncRep != coro.Dispatch {
		t.Fatalf("concrete.String plan = %+v, present=%t; want emitted plain Dispatch solely from dormant CHA", methodPlan, ok)
	}
	callPlan, ok := plan.CallPlan(invoke)
	if !ok || callPlan.Open || callPlan.Rep != coro.Dispatch || !coroInterfaceTargetContains(callPlan.Targets, methodPlan.ID) {
		t.Fatalf("dormant invoke CallPlan = %+v, present=%t; want closed Dispatch target %q", callPlan, ok, methodPlan.ID)
	}

	receivers, err := analyzeCoroClosedInterfacePlainPlan(plan, universe, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !receivers.acceptsTarget(method, methodPlan) {
		t.Fatalf("dormant receiver proof did not freeze exact static method target %q", methodPlan.ID)
	}
	if err := validateCoroDynamicDispatchTarget(method, methodPlan); err == nil ||
		!strings.Contains(err.Error(), "methods require receiver-aware dispatch lowering") {
		t.Fatalf("receiver-free function-value validator accepted method target: %v", err)
	}

	pkg, _, err := NewPackageExWithEmbedOptions(
		program, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: coroClosedInterfacePlainCompilation(plan, universe)},
	)
	if err != nil {
		t.Fatalf("compile static method with dormant interface CHA source: %v", err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify static method with dormant interface CHA source: %v\n%s", err, module.String())
	}
	if ir := module.String(); strings.Contains(ir, coroPlainDispatchDescriptorPrefix) || strings.Contains(ir, coroPlainDispatchThunkPrefix) {
		t.Fatalf("dormant invoke incorrectly materialized a function-value descriptor:\n%s", ir)
	}
}

func TestCoroDormantInterfaceTargetSupportsLiveFirstClassMethodExpression(t *testing.T) {
	const source = `package foo
var gate chan struct{}
type text interface { String() string }
type concrete string
func (value concrete) String() string { return string(value) }
func dormant(value text) string { return value.String() }
func consume(func(concrete) string) {}
func Root(value concrete) string {
	<-gate
	consume(concrete.String)
	return value.String()
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	program := newLLSSAProg(t)
	defer program.Dispose()
	universe, plan, _, _, _, _ := prepareCoroDormantInterfaceFixture(t, program, ssaPkg, files)

	if _, err := analyzeCoroClosedInterfacePlainPlan(plan, universe, false, true); err != nil {
		t.Fatalf("declared receiver body was confused with its first-class method-expression wrapper: %v", err)
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		program, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: coroClosedInterfacePlainCompilation(plan, universe)},
	)
	if err != nil {
		t.Fatalf("compile live first-class method expression: %v", err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify live first-class method expression: %v\n%s", err, module.String())
	}
	if ir := module.String(); !strings.Contains(ir, coroPlainDispatchDescriptorPrefix) || !strings.Contains(ir, coroPlainDispatchThunkPrefix) {
		t.Fatalf("live first-class method expression did not materialize its descriptor and entry thunk:\n%s", ir)
	}
}

func prepareCoroDormantInterfaceFixture(
	t *testing.T,
	program llssa.Program,
	ssaPkg *ssa.Package,
	files []*ast.File,
) (*EmissionUniverse, *coro.SSAPlan, *ssa.Function, *ssa.Function, *ssa.Function, *ssa.Call) {
	t.Helper()
	universe, err := PrepareEmissionUniverseWithOptions(
		program, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{EnableCoroChannel: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	dormant := ssaPkg.Func("dormant")
	var method *ssa.Function
	for _, fn := range universe.Functions() {
		if fn != nil && fn.Name() == "String" && fn.Signature != nil && fn.Signature.Recv() != nil {
			method = fn
			break
		}
	}
	if root == nil || dormant == nil || method == nil {
		t.Fatalf("fixture functions root=%v dormant=%v method=%v", root, dormant, method)
	}
	var invoke *ssa.Call
	for _, block := range dormant.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok && call.Common().IsInvoke() {
				invoke = call
			}
		}
	}
	if invoke == nil {
		t.Fatal("dormant interface invoke not found")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    coro.DynamicCHAClosed,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return universe, plan, root, dormant, method, invoke
}

func TestCoroClosedInterfacePlainInvokeCompatibility(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		resolution coro.DynamicResolution
		want       string
	}{
		{
			name:       "open world",
			source:     coroClosedInterfacePlainSource,
			resolution: coro.DynamicCHAOpen,
			want:       "closed nonempty Dispatch CallPlan",
		},
		{
			name: "suspending target with method-expression consumer",
			source: `package foo
var gate chan uint32
type Value interface { Value() uint32 }
type concrete struct{}
func (value *concrete) Value() uint32 { return <-gate }
func consume(func(*concrete) uint32) {}
func Root(value Value) uint32 {
	<-gate
	consume((*concrete).Value)
	return value.Value()
}
`,
			resolution: coro.DynamicCHAClosed,
			want:       "",
		},
		{
			name: "plain method-expression consumer",
			source: `package foo
var gate chan uint32
type Value interface { Value() uint32 }
type concrete uint32
func (value concrete) Value() uint32 { return uint32(value) + 1 }
func consume(func(concrete) uint32) {}
func Root(value Value) uint32 {
	<-gate
	consume(concrete.Value)
	return value.Value()
}
`,
			resolution: coro.DynamicCHAClosed,
			want:       "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, files := buildGoSSAPkg(t, test.source)
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, plan, _, _, _ := prepareCoroClosedInterfacePlainPlan(t, prog, ssaPkg, files, test.resolution)
			pkg, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
				PackageOptions{Compilation: coroClosedInterfacePlainCompilation(plan, universe)},
			)
			if test.want == "" {
				if err != nil {
					t.Fatalf("compile exact method-expression consumer: %v", err)
				}
				module := pkg.Module()
				defer module.Dispose()
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("verify exact method-expression consumer: %v\n%s", err, module.String())
				}
				if ir := module.String(); !strings.Contains(ir, coroPlainDispatchDescriptorPrefix) {
					t.Fatalf("exact method-expression consumer did not materialize a descriptor:\n%s", ir)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("compile error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCoroRawABIPlainTargetRejectsThreadAffineMethod(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, coroClosedInterfacePlainSource)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	_, plan, _, method, _ := prepareCoroClosedInterfacePlainPlan(t, prog, ssaPkg, files, coro.DynamicCHAClosed)
	methodPlan, ok := plan.FunctionPlan(method)
	if !ok {
		t.Fatal("concrete.Value has no function plan")
	}
	methodPlan.Exec |= coro.ThreadAffine
	if err := validateCoroRawABIPlainTarget(method, methodPlan); err == nil || !strings.Contains(err.Error(), "thread-affine") {
		t.Fatalf("thread-affine raw method error = %v; want fail-closed execution constraint", err)
	}
}

func TestCoroRawABIPlainTargetAcceptsExternalMethodAddressOnly(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, coroClosedInterfacePlainSource)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	_, plan, _, method, _ := prepareCoroClosedInterfacePlainPlan(t, prog, ssaPkg, files, coro.DynamicCHAClosed)
	methodPlan, ok := plan.FunctionPlan(method)
	if !ok {
		t.Fatal("concrete.Value has no function plan")
	}
	methodPlan.External = coro.ExternalUnknownForeign
	methodPlan.Emission = coro.EmitExternal
	methodPlan.Primary = coro.PrimaryExternal
	methodPlan.FuncRep = coro.DirectPlain
	methodPlan.Effect = coro.NoSuspend
	methodPlan.Exec = coro.BlockForeign | coro.IRQUnsafe
	if err := validateCoroRawABIPlainTarget(method, methodPlan); err != nil {
		t.Fatalf("external raw method address rejected: %v", err)
	}
}

func TestCoroClosedInterfacePlainCandidateRejectsMethodMismatch(t *testing.T) {
	const source = `package foo
var gate chan uint32
type Value interface { Value() uint32 }
type concrete uint32
func (value concrete) Value() uint32 { return uint32(value) + 1 }
func (value concrete) Other() uint32 { return uint32(value) + 2 }
func Root(value Value) uint32 { <-gate; return value.Value() }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, plan, _, method, invoke := prepareCoroClosedInterfacePlainPlan(t, prog, ssaPkg, files, coro.DynamicCHAClosed)
	methodPlan, ok := plan.FunctionPlan(method)
	if !ok {
		t.Fatal("Value method has no plan")
	}
	var other *ssa.Function
	for _, fn := range universe.Functions() {
		if fn != nil && fn.Name() == "Other" && fn.Signature != nil && fn.Signature.Recv() != nil {
			other = fn
			break
		}
	}
	if other == nil {
		t.Fatal("Other method not found in emission universe")
	}
	iface := invoke.Common().Value.Type().Underlying().(*types.Interface)
	err := validateCoroClosedInterfacePlainCandidate(invoke.Common(), iface, methodPlan.ID, other, methodPlan)
	if err == nil || !strings.Contains(err.Error(), "method ID") {
		t.Fatalf("method mismatch error = %v", err)
	}
}

func TestCoroClosedInterfacePlainInvokeRequiresLegacyPanicABI(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, coroClosedInterfacePlainSource)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, plan, _, _, _ := prepareCoroClosedInterfacePlainPlan(t, prog, ssaPkg, files, coro.DynamicCHAClosed)
	compilation := coroClosedInterfacePlainCompilation(plan, universe)
	compilation.EnableCoroExplicitStatusPanicABI = true
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	_, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err == nil || !strings.Contains(err.Error(), "requires the legacy panic ABI") {
		t.Fatalf("explicit-status compile error = %v", err)
	}
}

func compileCoroClosedInterfacePlainFixture(t *testing.T, source string, resolution coro.DynamicResolution) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Function, *ssa.Call,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	universe, plan, root, method, invoke := prepareCoroClosedInterfacePlainPlan(t, prog, ssaPkg, files, resolution)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: coroClosedInterfacePlainCompilation(plan, universe)},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, root, method, invoke
}

func prepareCoroClosedInterfacePlainPlan(t *testing.T, prog llssa.Program, ssaPkg *ssa.Package, files []*ast.File, resolution coro.DynamicResolution) (
	*EmissionUniverse, *coro.SSAPlan, *ssa.Function, *ssa.Function, *ssa.Call,
) {
	t.Helper()
	universe, err := PrepareEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{EnableCoroChannel: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	var method *ssa.Function
	for _, fn := range universe.Functions() {
		if fn != nil && fn.Name() == "Value" && fn.Signature != nil && fn.Signature.Recv() != nil {
			method = fn
			break
		}
	}
	if method == nil {
		t.Fatal("concrete Value method not found in emission universe")
	}
	var invoke *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok && call.Common().IsInvoke() {
				invoke = call
			}
		}
	}
	if invoke == nil {
		t.Fatal("Root interface invoke not found")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    resolution,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return universe, plan, root, method, invoke
}

func coroClosedInterfacePlainCompilation(plan *coro.SSAPlan, universe *EmissionUniverse) *Compilation {
	return &Compilation{
		CoroPlan:                      plan,
		EmissionUniverse:              universe,
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroPlainDispatch:       true,
		EnableCoroProgramBootstrapRun: true,
		EnableCoroChannel:             true,
		CoroABI:                       coro.PhysicalABIV1,
		SchedulerABI:                  coro.SchedulerProgramBootstrapChannelABIV0,
		PanicABI:                      coro.PanicLegacyABIV0,
		FuncRepABI:                    coro.FuncRepABIV1,
	}
}

func assertCoroClosedInterfacePlainIR(t *testing.T, ir string) {
	t.Helper()
	if !strings.Contains(ir, "llvm.coro.suspend") && !strings.Contains(ir, ".resume") {
		t.Fatalf("coroutine body has no suspension/resume marker:\n%s", ir)
	}
	if !strings.Contains(ir, "call i32 %") {
		t.Fatalf("coroutine body does not retain an ordinary indirect itab call:\n%s", ir)
	}
	if strings.Contains(ir, "coro.dispatch") || strings.Contains(ir, coroPlainDispatchDescriptorPrefix) {
		t.Fatalf("interface invoke used coroutine function-value dispatch:\n%s", ir)
	}
}

func coroInterfaceTargetContains(targets []coro.FunctionID, want coro.FunctionID) bool {
	for _, target := range targets {
		if target == want {
			return true
		}
	}
	return false
}
