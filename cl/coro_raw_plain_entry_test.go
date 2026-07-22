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

func TestCoroRawPlainEntryDualLoweringKeepsManagedCallsManaged(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
func RawHelper(value uint32) uint32 {
	for value != 0 { value-- }
	return value
}

func Dual(value uint32) uint32 { return RawHelper(value) }
func Parent(value uint32) uint32 { return Dual(value) }
`)
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	parent, dual, helper := ssaPkg.Func("Parent"), ssaPkg.Func("Dual"), ssaPkg.Func("RawHelper")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: parent, Demand: coro.AsyncDemand},
		{Function: dual, Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == dual {
				return coro.SSAFunctionPolicy{RawPlainEntry: true}, nil
			}
			if fn == helper {
				return coro.SSAFunctionPolicy{RawPlainVariant: true}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	dualPlan, ok := plan.FunctionPlan(dual)
	if !ok || !dualPlan.RawPlainEntry || !plan.HasRawPlainVariant(dual) || dualPlan.Emission != coro.EmitCoroutine || dualPlan.Primary != coro.PrimaryCoroutine {
		prog.Dispose()
		t.Fatalf("Dual plan = %+v, present=%t; want managed coroutine plus physical raw plain entry", dualPlan, ok)
	}
	helperPlan, ok := plan.FunctionPlan(helper)
	if !ok || helperPlan.RawPlainEntry || !plan.HasRawPlainVariant(helper) || helperPlan.Emission != coro.EmitCoroutine || helperPlan.Primary != coro.PrimaryCoroutine {
		prog.Dispose()
		t.Fatalf("RawHelper plan = %+v, present=%t; want managed coroutine plus internal raw plain variant", helperPlan, ok)
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify dual raw/managed module: %v\n%s", err, module.String())
	}

	for _, name := range []string{"foo.Dual", "foo.Dual$coro", "foo.RawHelper", "foo.RawHelper$coro"} {
		if module.NamedFunction(name).IsNil() {
			t.Fatalf("dual lowering is missing %q:\n%s", name, module.String())
		}
	}
	rawDual := module.NamedFunction("foo.Dual").String()
	if !strings.Contains(rawDual, "@foo.RawHelper(") || strings.Contains(rawDual, "RawHelper$coro") {
		t.Fatalf("raw Dual did not call the raw/plain helper variant:\n%s", rawDual)
	}
	managedDual := module.NamedFunction("foo.Dual$coro").String()
	if !strings.Contains(managedDual, "RawHelper$coro") {
		t.Fatalf("managed Dual did not await the managed helper entry:\n%s", managedDual)
	}
	managedParent := module.NamedFunction("foo.Parent$coro").String()
	if !strings.Contains(managedParent, "Dual$coro") || strings.Contains(managedParent, "@foo.Dual(") {
		t.Fatalf("ordinary managed Parent selected the raw Dual entry:\n%s", managedParent)
	}
}

func TestCoroRawPlainOnlyEmitsOneLegacyBody(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
func RawHelper(value uint32) uint32 {
	for value != 0 { value-- }
	return value
}
func Host(value uint32) uint32 { return RawHelper(value) }
`)
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	host, helper := ssaPkg.Func("Host"), ssaPkg.Func("RawHelper")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{
		Function: host, RawPlainDemand: true,
	}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			switch fn {
			case host:
				return coro.SSAFunctionPolicy{RawPlainEntry: true}, nil
			case helper:
				return coro.SSAFunctionPolicy{RawPlainVariant: true}, nil
			default:
				return coro.SSAFunctionPolicy{}, nil
			}
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	for _, fn := range []*ssa.Function{host, helper} {
		got, ok := plan.FunctionPlan(fn)
		if !ok || !got.RawPlainOnly || got.ManagedDemand != coro.NoDemand || !got.RawPlainDemand ||
			got.Emission != coro.EmitRawPlain || got.Primary != coro.PrimaryPlain ||
			got.FuncRep != coro.DirectPlain || !plan.HasRawPlainVariant(fn) {
			prog.Dispose()
			t.Fatalf("%s raw-only plan = %+v, present=%t variant=%t", fn.Name(), got, ok, plan.HasRawPlainVariant(fn))
		}
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify raw-only module: %v\n%s", err, module.String())
	}
	for _, name := range []string{"foo.Host", "foo.RawHelper"} {
		if module.NamedFunction(name).IsNil() {
			t.Fatalf("raw-only lowering is missing base %q:\n%s", name, module.String())
		}
		if !module.NamedFunction(name + coroPrimarySuffix).IsNil() {
			t.Fatalf("raw-only lowering emitted managed twin %q:\n%s", name+coroPrimarySuffix, module.String())
		}
	}
	hostBody := module.NamedFunction("foo.Host").String()
	if !strings.Contains(hostBody, "@foo.RawHelper(") || strings.Contains(hostBody, "RawHelper$coro") {
		t.Fatalf("raw-only Host did not call the helper base:\n%s", hostBody)
	}
	if strings.Contains(module.String(), "llvm.coro.") || strings.Contains(module.String(), coroRootFactoryPrefix) {
		t.Fatalf("raw-only module contains coroutine machinery:\n%s", module.String())
	}
}

func TestCoroRawPlainOnlyCompilesClosedSingletonSyncDispatch(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
func Target(value int) int { return value + 1 }
func Host(fn func(int) int, value int) int {
	if fn == nil { return 0 }
	return fn(value)
}
`)
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	host, target := ssaPkg.Func("Host"), ssaPkg.Func("Target")
	dynamicCall := coroPlainDispatchOnlyDynamicCall(t, host)
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{
		Function: host, RawPlainDemand: true,
	}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
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
				Targets: []*ssa.Function{target}, MayBeNil: true, SyncDispatch: true,
			}, true, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	hostPlan, ok := plan.FunctionPlan(host)
	if !ok || hostPlan.Emission != coro.EmitRawPlain || !hostPlan.RawPlainOnly ||
		hostPlan.ManagedDemand != coro.NoDemand || !hostPlan.RawPlainDemand || !plan.HasRawPlainVariant(host) {
		prog.Dispose()
		t.Fatalf("Host plan = %+v, present=%t variant=%t; want final raw-only body", hostPlan, ok, plan.HasRawPlainVariant(host))
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.Emission != coro.EmitPlain || targetPlan.Effect != coro.NoSuspend ||
		targetPlan.FuncRep != coro.Dispatch || targetPlan.ManagedDemand != coro.SyncDemand || targetPlan.RawPlainDemand {
		prog.Dispose()
		t.Fatalf("Target plan = %+v, present=%t; want managed-sync plain descriptor", targetPlan, ok)
	}
	callPlan, ok := plan.CallPlan(dynamicCall)
	if !ok || !callPlan.SyncDispatch || callPlan.Open || callPlan.Rep != coro.Dispatch ||
		!callPlan.MayBeNil || len(callPlan.Targets) != 1 || callPlan.Targets[0] != targetPlan.ID {
		prog.Dispose()
		t.Fatalf("Host SyncDispatch plan = %+v, present=%t", callPlan, ok)
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.CoroProfile = CoroProfileStackless
	compilation.FuncRepABI = coro.FuncRepABIV1
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatalf("compile raw-only SyncDispatch package: %v", err)
	}
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify raw-only SyncDispatch module: %v\n%s", err, module.String())
	}
	hostIR := module.NamedFunction("foo.Host")
	if hostIR.IsNil() || !module.NamedFunction("foo.Host"+coroPrimarySuffix).IsNil() {
		t.Fatalf("raw-only SyncDispatch did not emit exactly the base Host body:\n%s", module.String())
	}
	if body := hostIR.String(); !strings.Contains(body, "coro.dispatch") || !strings.Contains(body, "llvm.trap") {
		t.Fatalf("raw-only Host did not lower its certified nullable descriptor call:\n%s", body)
	}
	if targetIR := module.NamedFunction("foo.Target"); targetIR.IsNil() || !module.NamedFunction("foo.Target"+coroPrimarySuffix).IsNil() {
		t.Fatalf("SyncDispatch target did not retain one plain primary:\n%s", module.String())
	}
	if strings.Contains(module.String(), "llvm.coro.") || strings.Contains(module.String(), coroRootFactoryPrefix) {
		t.Fatalf("raw-only SyncDispatch module contains coroutine machinery:\n%s", module.String())
	}
}

func TestCoroExactManagedGoLinknameAliasNeedsNoRawPlainEntry(t *testing.T) {
	testProg := newEmissionTestProgram()
	declarationPkg := testProg.addPackage(t, "example.com/coro/linkdecl", `package linkdecl
func runtimeHook(value uint32) uint32
func Root(value uint32) uint32 { return runtimeHook(value) }
`)
	definitionPkg := testProg.addPackage(t, "example.com/coro/linkdef", `package linkdef
//go:linkname implementation example.com/coro/linkdecl.runtimeHook
func implementation(value uint32) uint32 { return value + 1 }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: declarationPkg.ssa, Files: []*ast.File{declarationPkg.file}},
		{SSA: definitionPkg.ssa, Files: []*ast.File{definitionPkg.file}},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	declaration := declarationPkg.ssa.Func("runtimeHook")
	implementation := definitionPkg.ssa.Func("implementation")
	if resolved, ok := universe.Resolve(declaration); !ok || resolved != implementation {
		prog.Dispose()
		t.Fatalf("runtimeHook resolution = %v, %t; want exact implementation %v", resolved, ok, implementation)
	}
	managed, err := universe.exactManagedGoLinknameDefinition(implementation)
	if err != nil || !managed {
		prog.Dispose()
		t.Fatalf("managed go:linkname proof = %t, %v; want true, nil", managed, err)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	root := declarationPkg.ssa.Func("Root")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ResolveFunction: func(fn *ssa.Function) (*ssa.Function, bool, error) {
			canonical, ok := universe.Resolve(fn)
			return canonical, ok, nil
		},
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == implementation {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	implementationPlan, ok := plan.FunctionPlan(implementation)
	if !ok || implementationPlan.Emission != coro.EmitCoroutine || implementationPlan.Primary != coro.PrimaryCoroutine ||
		implementationPlan.RawPlainEntry || plan.HasRawPlainVariant(implementation) {
		prog.Dispose()
		t.Fatalf("implementation plan = %+v, present=%t raw-variant=%t; want one managed coroutine primary",
			implementationPlan, ok, plan.HasRawPlainVariant(implementation))
	}
	// A dynamically transported reference to the same canonical body publishes
	// a descriptor for the managed primary. The exact declaration/definition
	// alias remains a managed Go symbol; only validation without the frozen
	// universe must continue to treat the redirecting directive as a raw edge.
	dispatchPlan := implementationPlan
	dispatchPlan.FuncRep = coro.Dispatch
	if err := validateCoroDynamicDispatchTarget(implementation, dispatchPlan); err == nil ||
		!strings.Contains(err.Error(), "ABI directive") {
		prog.Dispose()
		t.Fatalf("unfrozen managed-linkname descriptor validation = %v; want fail-closed directive rejection", err)
	}
	if err := validateCoroDynamicDispatchTarget(implementation, dispatchPlan, universe); err != nil {
		prog.Dispose()
		t.Fatalf("frozen managed-linkname descriptor validation: %v", err)
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	definitionLL, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, definitionPkg.ssa, []*ast.File{definitionPkg.file}, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	declarationLL, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, declarationPkg.ssa, []*ast.File{declarationPkg.file}, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		definitionLL.Module().Dispose()
		prog.Dispose()
		t.Fatal(err)
	}
	defer prog.Dispose()
	definitionModule := definitionLL.Module()
	declarationModule := declarationLL.Module()
	defer definitionModule.Dispose()
	defer declarationModule.Dispose()
	for name, module := range map[string]llvm.Module{"definition": definitionModule, "declaration": declarationModule} {
		if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
			t.Fatalf("verify managed go:linkname %s module: %v\n%s", name, err, module.String())
		}
	}
	const baseName = "example.com/coro/linkdecl.runtimeHook"
	if raw := definitionModule.NamedFunction(baseName); !raw.IsNil() {
		t.Fatalf("managed go:linkname unexpectedly emitted a raw/plain body:\n%s", raw.String())
	}
	managedEntry := definitionModule.NamedFunction(baseName + coroPrimarySuffix)
	if managedEntry.IsNil() {
		t.Fatalf("managed go:linkname coroutine primary is absent:\n%s", definitionModule.String())
	}
	declarationEntry := declarationModule.NamedFunction(baseName + coroPrimarySuffix)
	if declarationEntry.IsNil() || !declarationEntry.FirstBasicBlock().IsNil() {
		t.Fatalf("bodyless go:linkname declaration archive did not retain a declaration-only canonical coroutine entry:\n%s", declarationModule.String())
	}
	rootBody := declarationModule.NamedFunction("example.com/coro/linkdecl.Root" + coroPrimarySuffix).String()
	if !strings.Contains(rootBody, "runtimeHook$coro") || strings.Contains(rootBody, "runtimeHook\"(") {
		t.Fatalf("managed root did not select the canonical coroutine alias:\n%s", rootBody)
	}
}

func TestCoroUnpairedGoLinknameDefinitionRemainsRawBoundary(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
import _ "unsafe"

//go:linkname Unpaired example.com/external.runtimeHook
func Unpaired(value uint32) uint32 { return value + 1 }
`)
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
	fn := ssaPkg.Func("Unpaired")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: fn, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          universe.FunctionIDConfig(),
		MaxPlainInstructions: -1,
		ClassifyFunction: func(candidate *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if candidate == fn {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	functionPlan, ok := plan.FunctionPlan(fn)
	if !ok {
		t.Fatal("unpaired function has no coroutine plan")
	}
	if managed, err := universe.exactManagedGoLinknameDefinition(fn); err != nil || managed {
		t.Fatalf("unpaired managed go:linkname proof = %t, %v; want false, nil", managed, err)
	}
	if err := validateCoroPhysicalABIWithUniverse(fn, functionPlan, plan, universe, true, true); err == nil ||
		!strings.Contains(err.Error(), "ABI directive") {
		t.Fatalf("unpaired go:linkname validation = %v; want fail-closed ABI directive rejection", err)
	}
}

func TestCoroRawPlainEntryOwnsABIDirectiveWhileManagedPrimaryUsesSuffix(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
func RawHelper(value uint32) uint32 {
	for value != 0 { value-- }
	return value
}
//export Host
func Host(value uint32) uint32 { return RawHelper(value) }
func Parent(value uint32) uint32 { return Host(value) }
`)
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	parent, host, helper := ssaPkg.Func("Parent"), ssaPkg.Func("Host"), ssaPkg.Func("RawHelper")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: parent, Demand: coro.AsyncDemand},
		{Function: host, Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			switch fn {
			case host:
				return coro.SSAFunctionPolicy{RawPlainEntry: true}, nil
			case helper:
				return coro.SSAFunctionPolicy{RawPlainVariant: true}, nil
			default:
				return coro.SSAFunctionPolicy{}, nil
			}
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	hostPlan, ok := plan.FunctionPlan(host)
	if !ok || !hostPlan.RawPlainEntry || !plan.HasRawPlainVariant(host) || hostPlan.Emission != coro.EmitCoroutine {
		prog.Dispose()
		t.Fatalf("Host plan = %+v, present=%t raw-variant=%t", hostPlan, ok, plan.HasRawPlainVariant(host))
	}
	withoutRawEntry := hostPlan
	withoutRawEntry.RawPlainEntry = false
	if err := validateCoroPhysicalABIWithUniverse(host, withoutRawEntry, plan, universe, true, true); err == nil || !strings.Contains(err.Error(), "ABI directive") {
		prog.Dispose()
		t.Fatalf("non-raw ABI directive validation = %v; want rejection", err)
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify ABI-directed dual module: %v\n%s", err, module.String())
	}
	baseName := "Host"
	raw := module.NamedFunction(baseName)
	managed := module.NamedFunction(baseName + coroPrimarySuffix)
	if raw.IsNil() || managed.IsNil() {
		t.Fatalf("ABI-directed dual lowering missing raw=%q or managed=%q:\n%s", baseName, baseName+coroPrimarySuffix, module.String())
	}
	if strings.Contains(raw.Name(), coroPrimarySuffix) || managed.Name() == baseName {
		t.Fatalf("export ownership crossed variants: raw=%q managed=%q", raw.Name(), managed.Name())
	}
	moduleIR := module.String()
	compilerUsedStart := strings.Index(moduleIR, "@llvm.compiler.used")
	compilerUsedEnd := -1
	if compilerUsedStart >= 0 {
		compilerUsedEnd = strings.Index(moduleIR[compilerUsedStart:], "\n")
	}
	if compilerUsedStart < 0 || compilerUsedEnd < 0 {
		t.Fatalf("ABI-directed raw base has no llvm.compiler.used export retention:\n%s", moduleIR)
	}
	compilerUsed := moduleIR[compilerUsedStart : compilerUsedStart+compilerUsedEnd]
	if !strings.Contains(compilerUsed, "@Host") || strings.Contains(compilerUsed, "Host$coro") {
		t.Fatalf("ABI export retention is not owned exclusively by the raw base: %s", compilerUsed)
	}
	if !strings.Contains(raw.String(), "@foo.RawHelper(") || strings.Contains(raw.String(), "RawHelper$coro") {
		t.Fatalf("ABI-directed raw base did not keep the raw helper call:\n%s", raw.String())
	}
	if !strings.Contains(managed.String(), "RawHelper$coro") {
		t.Fatalf("managed suffixed primary did not keep managed helper lowering:\n%s", managed.String())
	}
}

func TestCoroRawPlainVariantCapturedClosurePreservesBindingABI(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocU(size uintptr) unsafe.Pointer {
	if size == 0 { return nil }
	return nil
}
`)
	fooPkg := testProg.addPackage(t, "foo", `package foo
type Box int
func (seed *Box) Add(delta int) int {
	if seed == nil { return delta }
	return delta
}
func Dual(seed *Box, value int) int {
	callback := seed.Add
	return callback(value)
}
func Parent(seed *Box, value int) int { return Dual(seed, value) }
`)
	testProg.ssa.Build()
	ssaPkg := fooPkg.ssa
	files := []*ast.File{fooPkg.file}
	dual := ssaPkg.Func("Dual")
	parent := ssaPkg.Func("Parent")
	var makeClosure *ssa.MakeClosure
	for _, block := range dual.Blocks {
		for _, instruction := range block.Instrs {
			if closure, ok := instruction.(*ssa.MakeClosure); ok {
				makeClosure = closure
			}
		}
	}
	if makeClosure == nil {
		t.Fatal("Dual has no bound-method closure")
	}
	captured, ok := makeClosure.Fn.(*ssa.Function)
	if !ok || captured == nil || len(captured.FreeVars) != 1 || len(makeClosure.Bindings) != 1 {
		t.Fatalf("Dual captured closure = %v, bindings=%v", makeClosure.Fn, makeClosure.Bindings)
	}
	var add *ssa.Function
	for _, block := range captured.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok {
				add = call.Common().StaticCallee()
			}
		}
	}
	if add == nil {
		t.Fatal("bound-method closure has no exact Add target")
	}
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: ssaPkg, Files: files},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: parent, Demand: coro.AsyncDemand},
		{Function: dual, Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			switch fn {
			case dual:
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, RawPlainEntry: true}, nil
			case captured:
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, RawPlainVariant: true}, nil
			case add:
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, RawPlainVariant: true}, nil
			default:
				return coro.SSAFunctionPolicy{}, nil
			}
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	capturedPlan, ok := plan.FunctionPlan(captured)
	if !ok || capturedPlan.RawPlainEntry || !plan.HasRawPlainVariant(captured) ||
		capturedPlan.Emission != coro.EmitCoroutine || capturedPlan.Primary != coro.PrimaryCoroutine || capturedPlan.FuncRep != coro.DirectCoro {
		prog.Dispose()
		t.Fatalf("captured plan = %+v, present=%t variant=%t; want internal dual body only", capturedPlan, ok, plan.HasRawPlainVariant(captured))
	}
	if valuePlan, present := plan.ValuePlan(makeClosure); !present || len(valuePlan.Funcs) != 1 || valuePlan.Funcs[0].Rep != coro.DirectCoro {
		prog.Dispose()
		t.Fatalf("captured closure value plan = %+v, present=%t; want one exact direct coroutine context", valuePlan, present)
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.CoroProfile = CoroProfileStackless
	compilation.CoroProfile = CoroProfileStackless
	compilation.FuncRepABI = coro.FuncRepABIV1
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify captured raw variant module: %v\n%s", err, module.String())
	}
	capturedName, err := universe.physicalName(ssaPkg, captured, funcName(ssaPkg.Pkg, captured, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"foo.Dual", "foo.Dual$coro", capturedName, capturedName + "$coro"} {
		if module.NamedFunction(name).IsNil() {
			t.Fatalf("captured dual lowering is missing %q:\n%s", name, module.String())
		}
	}
	rawDual := module.NamedFunction("foo.Dual").String()
	if !strings.Contains(rawDual, "@\""+capturedName+"\"") && !strings.Contains(rawDual, "@"+capturedName) ||
		strings.Contains(rawDual, capturedName+"$coro") {
		t.Fatalf("raw Dual did not construct its closure from the internal raw variant:\n%s", rawDual)
	}
	if !strings.Contains(rawDual, "store ptr %0") {
		t.Fatalf("raw Dual did not store the exact seed binding into its closure context:\n%s", rawDual)
	}
	rawCaptured := module.NamedFunction(capturedName).String()
	if !strings.Contains(rawCaptured, "load { ptr }") || !strings.Contains(rawCaptured, "extractvalue { ptr }") || !strings.Contains(rawCaptured, "Add") {
		t.Fatalf("captured raw variant did not load the closure binding and combine it with its source argument:\n%s", rawCaptured)
	}
	managedDual := module.NamedFunction("foo.Dual$coro").String()
	if !strings.Contains(managedDual, capturedName+"$coro") || strings.Contains(managedDual, "@\""+capturedName+"\"(") {
		t.Fatalf("managed Dual selected the internal raw closure body:\n%s", managedDual)
	}
}
