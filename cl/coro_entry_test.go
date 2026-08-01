//go:build !llgo
// +build !llgo

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
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroEntryTestSource = `package foo

var channel chan int

func Plain() {}
func Coroutine() { <-channel }
func Boxed() {}
func Box() any { return Boxed }
func External()
`

func buildCoroEntryTestPlan(t *testing.T) (*ssa.Package, *coro.SSAPlan) {
	t.Helper()
	pkg, _, _ := buildGoSSAPkg(t, coroEntryTestSource)
	plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{
		{Function: pkg.Func("Plain"), Demand: coro.SyncDemand},
		{Function: pkg.Func("Coroutine"), Demand: coro.AsyncDemand},
		{Function: pkg.Func("Box"), Demand: coro.AsyncDemand},
		{Function: pkg.Func("External"), Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == pkg.Func("External") {
				return coro.SSAFunctionPolicy{
					Effect:           coro.WaitHost,
					External:         coro.ExternalKnown,
					OverrideExternal: true,
				}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return pkg, plan
}

func newCoroEntryTestContext(t *testing.T, pkg *ssa.Package, compilation *Compilation) (*context, func()) {
	t.Helper()
	prog := newLLSSAProg(t)
	ctx := &context{
		prog:        prog,
		pkg:         prog.NewPackage(pkg.Pkg.Name(), pkg.Pkg.Path()),
		goProg:      pkg.Prog,
		goTyps:      pkg.Pkg,
		goPkg:       pkg,
		compilation: compilation,
	}
	return ctx, prog.Dispose
}

// coroEntryPreflightUniverse is a minimal exact universe for tests that are
// expected to stop in whole-plan preflight before package/codegen validation.
func coroEntryPreflightUniverse(plan *coro.SSAPlan) *EmissionUniverse {
	u := &EmissionUniverse{
		required:      make(map[*ssa.Function]none),
		aliases:       make(map[*ssa.Function]*ssa.Function),
		functionKinds: make(map[emissionFunctionOwnerKey]int),
		finalKeys:     make(map[emissionFunctionOwnerKey]string),
		useOwners:     make(map[*ssa.Function]map[*preparedEmissionPackage]none),
		ownerStates:   make(map[*ssa.Function]map[*preparedEmissionPackage]emissionFunctionState),
	}
	if plan == nil {
		return u
	}
	owners := make(map[*ssa.Package]*preparedEmissionPackage)
	for _, planned := range plan.Functions() {
		fn := planned.Function
		u.functions = append(u.functions, fn)
		u.required[fn] = none{}
		owner := owners[fn.Pkg]
		if owner == nil {
			identity := "test"
			if fn.Pkg != nil && fn.Pkg.Pkg != nil {
				identity = fn.Pkg.Pkg.Path()
			}
			owner = &preparedEmissionPackage{identity: identity, pkgPath: identity, ssa: fn.Pkg, order: len(owners)}
			owners[fn.Pkg] = owner
		}
		u.useOwners[fn] = map[*preparedEmissionPackage]none{owner: {}}
		u.ownerStates[fn] = map[*preparedEmissionPackage]emissionFunctionState{owner: {state: pkgNormal}}
		key := emissionFunctionOwnerKey{function: fn, owner: owner}
		u.functionKinds[key] = goFunc
		u.finalKeys[key] = managedSymbolKey(goFunc, fn.Name(), "preflight-test")
	}
	return u
}

func TestResolveFunctionSymbolUsesPrimaryAndExactPlan(t *testing.T) {
	pkg, plan := buildCoroEntryTestPlan(t)
	ctx, dispose := newCoroEntryTestContext(t, pkg, &Compilation{
		CoroPlan: plan})
	defer dispose()

	plain, err := ctx.resolveFunctionSymbol(pkg.Func("Plain"))
	if err != nil {
		t.Fatal(err)
	}
	if !plain.planned || plain.plan.Emission != coro.EmitPlain || plain.plan.Primary != coro.PrimaryPlain || strings.HasSuffix(plain.name, coroPrimarySuffix) {
		t.Fatalf("plain entry = %+v", plain)
	}
	if err := plain.checkSupported(); err != nil {
		t.Fatalf("plain entry rejected: %v", err)
	}

	coroutine, err := ctx.resolveFunctionSymbol(pkg.Func("Coroutine"))
	if err != nil {
		t.Fatal(err)
	}
	if !coroutine.planned || coroutine.plan.Emission != coro.EmitCoroutine || coroutine.plan.Primary != coro.PrimaryCoroutine || !strings.HasSuffix(coroutine.name, coroPrimarySuffix) {
		t.Fatalf("coroutine entry = %+v", coroutine)
	}
	if err := coroutine.checkSupported(); err != nil {
		t.Fatalf("coroutine primary rejected by the stackless architecture: %v", err)
	}

	boxed, err := ctx.resolveFunctionSymbol(pkg.Func("Boxed"))
	if err != nil {
		t.Fatal(err)
	}
	if boxed.plan.Emission != coro.EmitPlain || boxed.plan.Primary != coro.PrimaryPlain || boxed.plan.FuncRep != coro.Dispatch || strings.HasSuffix(boxed.name, coroPrimarySuffix) {
		t.Fatalf("boxed entry = %+v, want one plain primary plus dispatch descriptor", boxed)
	}
	if err := boxed.checkSupported(); err != nil {
		t.Fatalf("descriptor-backed plain primary rejected by the stackless architecture: %v", err)
	}

	external, err := ctx.resolveFunctionSymbol(pkg.Func("External"))
	if err != nil {
		t.Fatal(err)
	}
	if external.plan.Emission != coro.EmitExternal || external.plan.Primary != coro.PrimaryExternal || external.plan.FuncRep != coro.DirectCoro {
		t.Fatalf("external entry = %+v, want coroutine external primary", external)
	}
	if err := external.checkSupported(); err == nil || !strings.Contains(err.Error(), "external coroutine") {
		t.Fatalf("external support error = %v", err)
	}

	otherPkg, _, _ := buildGoSSAPkg(t, coroEntryTestSource)
	otherCtx, otherDispose := newCoroEntryTestContext(t, otherPkg, &Compilation{
		CoroPlan: plan})
	defer otherDispose()
	if _, err := otherCtx.resolveFunctionSymbol(otherPkg.Func("Plain")); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("other-program resolution error = %v, want exact-pointer plan miss", err)
	}
}

func TestImportedLibraryEntriesUsePublishedPhysicalDeclarations(t *testing.T) {
	const source = `package foo
func Imported(value uint32) uint32
func Caller(value uint32) uint32 { return Imported(value) + 1 }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files, Identity: "example.com/foo"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	planMetadata := coro.PlanDigestMetadata{
		CoroABI:        coro.PhysicalABIV1,
		SchedulerABI:   coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI:       coro.PanicExplicitStatusABIV0,
		FuncRepABI:     coro.FuncRepABIV1,
		TargetTriple:   prog.TargetSpec().Triple,
		TargetCPU:      prog.TargetSpec().CPU,
		TargetFeatures: prog.TargetSpec().Features,
		TargetABI:      prog.TargetSpec().TargetABI,
		PointerBits:    prog.PointerSize() * 8,
		DataLayout:     prog.DataLayout(),
	}
	switch prog.TargetData().ByteOrder() {
	case llvm.LittleEndian:
		planMetadata.Endianness = "little"
	case llvm.BigEndian:
		planMetadata.Endianness = "big"
	default:
		t.Fatal("unknown target byte order")
	}
	libraryMetadata := coro.LibraryEffectMetadata{
		FunctionIDSchema: coro.FunctionIDSchema,
		CoroABI:          planMetadata.CoroABI,
		SchedulerABI:     planMetadata.SchedulerABI,
		PanicABI:         planMetadata.PanicABI,
		FuncRepABI:       planMetadata.FuncRepABI,
		TargetTriple:     planMetadata.TargetTriple,
		TargetCPU:        planMetadata.TargetCPU,
		TargetFeatures:   planMetadata.TargetFeatures,
		TargetABI:        planMetadata.TargetABI,
		PointerBits:      planMetadata.PointerBits,
		Endianness:       planMetadata.Endianness,
		DataLayout:       planMetadata.DataLayout,
	}
	imported := ssaPkg.Func("Imported")
	caller := ssaPkg.Func("Caller")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = libraryMetadata.CoroABI
	functionIDs.SchedulerABI = libraryMetadata.SchedulerABI
	functionIDs.ArchiveReady = true
	importedID, err := coro.StableFunctionID(imported, functionIDs)
	if err != nil {
		t.Fatal(err)
	}
	abiHash, err := universe.CoroLibraryEffects().FunctionABIHash(imported, libraryMetadata)
	if err != nil {
		t.Fatal(err)
	}
	baseSymbol, err := universe.CoroLibraryEffects().FunctionBaseSymbol(imported)
	if err != nil {
		t.Fatal(err)
	}
	fact := coro.LibraryEffectFunction{
		ID:            importedID,
		ABIHash:       abiHash,
		Effect:        coro.MayPark,
		FuncRep:       coro.DirectCoro,
		Primary:       coro.PrimaryCoroutine,
		ManagedEntry:  coro.ManagedEntryCoroutine,
		PrimarySymbol: baseSymbol + coroPrimarySuffix,
	}
	plan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: caller, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if function == imported {
					return fact.ImportedPolicy()
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	importedPlan, found := plan.FunctionPlan(imported)
	if !found || importedPlan.External != coro.ExternalKnown ||
		importedPlan.Emission != coro.EmitExternal ||
		importedPlan.FuncRep != coro.DirectCoro ||
		importedPlan.Effect != fact.Effect {
		t.Fatalf("imported plan = %+v, present=%t", importedPlan, found)
	}
	callerPlan, found := plan.FunctionPlan(caller)
	if !found || callerPlan.Emission != coro.EmitCoroutine ||
		!callerPlan.Effect.Contains(coro.MayPark|coro.AwaitStructured) {
		t.Fatalf("caller was not automatically colored: %+v, present=%t", callerPlan, found)
	}
	newCompilation := func(
		plan *coro.SSAPlan,
		universe *EmissionUniverse,
		fact coro.LibraryEffectFunction,
	) *Compilation {
		compilation := &Compilation{
			CoroPlan:                  plan,
			CoroPlanMetadata:          planMetadata,
			CoroLibraryEffectMetadata: libraryMetadata,
			CoroLibraryEffects:        map[*ssa.Function]coro.LibraryEffectFunction{imported: fact},
			EmissionUniverse:          universe,
		}
		enableCoroChildAwaitCompilation(compilation)
		return compilation
	}
	compilation := newCompilation(plan, universe, fact)
	rawPlan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{
			{Function: caller, Demand: coro.AsyncDemand},
			{Function: imported, RawPlainDemand: true},
		},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if function == imported {
					return fact.ImportedPolicy()
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rawFact := fact
	rawFact.RawPlainSymbol = baseSymbol
	rawCompilation := newCompilation(rawPlan, universe, rawFact)
	if err := rawCompilation.validateCoroLibraryEffects(); err == nil ||
		!strings.Contains(err.Error(), "raw-plain demand") {
		t.Fatalf("external raw-plain v1 error = %v", err)
	}
	// Physical plans are committed once per emission universe. Use an
	// independent consumer compilation to verify the plain-library path rather
	// than reusing the coroutine fixture's backend state.
	plainProg := newLLSSAProg(t)
	defer plainProg.Dispose()
	plainUniverse, err := prepareStacklessEmissionUniverse(
		plainProg, nil, []EmissionPackage{{SSA: ssaPkg, Files: files, Identity: "example.com/foo"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	plainSSAUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, plainUniverse.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plainFunctionIDs := plainUniverse.FunctionIDConfig()
	plainFunctionIDs.CoroABI = libraryMetadata.CoroABI
	plainFunctionIDs.SchedulerABI = libraryMetadata.SchedulerABI
	plainFunctionIDs.ArchiveReady = true
	plainID, err := coro.StableFunctionID(imported, plainFunctionIDs)
	if err != nil {
		t.Fatal(err)
	}
	plainABIHash, err := plainUniverse.CoroLibraryEffects().FunctionABIHash(imported, libraryMetadata)
	if err != nil {
		t.Fatal(err)
	}
	plainBaseSymbol, err := plainUniverse.CoroLibraryEffects().FunctionBaseSymbol(imported)
	if err != nil {
		t.Fatal(err)
	}
	plainFact := coro.LibraryEffectFunction{
		ID:            plainID,
		ABIHash:       plainABIHash,
		Effect:        coro.NoSuspend,
		FuncRep:       coro.DirectPlain,
		Primary:       coro.PrimaryPlain,
		ManagedEntry:  coro.ManagedEntryPlain,
		PrimarySymbol: plainBaseSymbol,
	}
	plainPlan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: caller, Demand: coro.SyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     plainSSAUniverse,
			FunctionIDs:          plainFunctionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if function == imported {
					return plainFact.ImportedPolicy()
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	plainImportedPlan, found := plainPlan.FunctionPlan(imported)
	if !found || plainImportedPlan.External != coro.ExternalKnown ||
		plainImportedPlan.Emission != coro.EmitExternal ||
		plainImportedPlan.FuncRep != coro.DirectPlain ||
		plainImportedPlan.Effect != coro.NoSuspend {
		t.Fatalf("plain imported plan = %+v, present=%t", plainImportedPlan, found)
	}
	plainCallerPlan, found := plainPlan.FunctionPlan(caller)
	if !found || plainCallerPlan.Emission != coro.EmitPlain ||
		plainCallerPlan.Effect != coro.NoSuspend {
		t.Fatalf("plain caller was unnecessarily colored: %+v, present=%t", plainCallerPlan, found)
	}
	plainCompilation := newCompilation(plainPlan, plainUniverse, plainFact)
	plainPkg, _, err := NewPackageExWithEmbedOptions(
		plainProg, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: plainCompilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	plainModule := plainPkg.Module()
	if err := llvm.VerifyModule(plainModule, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify imported plain consumer: %v\n%s", err, plainModule.String())
	}
	plainDeclaration := plainModule.NamedFunction(plainFact.PrimarySymbol)
	if plainDeclaration.IsNil() || !plainDeclaration.FirstBasicBlock().IsNil() {
		t.Fatalf("published plain library entry is not one external declaration:\n%s", plainModule.String())
	}
	if !plainModule.NamedFunction(plainBaseSymbol + coroPrimarySuffix).IsNil() {
		t.Fatalf("consumer manufactured a coroutine declaration for plain import:\n%s", plainModule.String())
	}
	plainCaller := plainModule.NamedFunction("foo.Caller")
	if plainCaller.IsNil() || !strings.Contains(plainCaller.String(), plainFact.PrimarySymbol) {
		t.Fatalf("plain caller does not use producer-published entry:\n%s", plainModule.String())
	}
	for _, test := range []struct {
		name string
		edit func(*coro.LibraryEffectFunction)
		want string
	}{
		{
			name: "ABI hash",
			edit: func(fact *coro.LibraryEffectFunction) {
				fact.ABIHash = strings.Repeat("f", 64)
			},
			want: "ABI hash",
		},
		{
			name: "primary symbol",
			edit: func(fact *coro.LibraryEffectFunction) {
				fact.PrimarySymbol += ".wrong"
			},
			want: "primary symbol",
		},
	} {
		t.Run("reject "+test.name+" mismatch", func(t *testing.T) {
			badFact := fact
			test.edit(&badFact)
			bad := newCompilation(plan, universe, badFact)
			if err := bad.validateCoroLibraryEffects(); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s mismatch error = %v", test.name, err)
			}
		})
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify imported coroutine consumer: %v\n%s", err, module.String())
	}
	declaration := module.NamedFunction(fact.PrimarySymbol)
	if declaration.IsNil() || !declaration.FirstBasicBlock().IsNil() {
		t.Fatalf("published library entry is not one external declaration:\n%s", module.String())
	}
	if !module.NamedFunction(baseSymbol).IsNil() {
		t.Fatalf("consumer manufactured a legacy plain declaration for imported coroutine:\n%s", module.String())
	}
	body := requireCoroPhysicalFunction(t, module, "foo.Caller").String()
	if !strings.Contains(body, fact.PrimarySymbol) {
		t.Fatalf("caller does not await the producer-published physical symbol:\n%s", body)
	}
}

func TestCoroEntryOmitsUndemandedEffectfulComplexFunction(t *testing.T) {
	const source = `package foo
func Complex(ch chan int) int {
	value := <-ch
	if value == 0 {
		return 1
	}
	return value
}
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
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, nil, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	complexPlan, ok := plan.FunctionPlan(ssaPkg.Func("Complex"))
	if !ok || complexPlan.Demand != coro.NoDemand || complexPlan.Emission != coro.EmitNone || complexPlan.Primary != coro.PrimaryCoroutine || !complexPlan.Effect.MaySuspend() {
		t.Fatalf("Complex plan = %+v, present=%t; want undemanded, non-emitted logical coroutine", complexPlan, ok)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,
	}
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("undemanded complex function blocked package emission: %v", err)
	}
	module := pkg.Module()
	if !module.NamedFunction("foo.Complex").IsNil() || !module.NamedFunction("foo.Complex"+coroPrimarySuffix).IsNil() {
		t.Fatalf("EmitNone function acquired an LLVM symbol:\n%s", module.String())
	}
	ir := module.String()
	for _, marker := range []string{
		coroPrimarySuffix,
		coroDescriptorPrefixV1,
		coroRootFactoryDescriptorPrefix,
		coroRootPackageAnchorPrefix,
	} {
		if strings.Contains(ir, marker) {
			t.Fatalf("EmitNone package unexpectedly contains coroutine marker %q:\n%s", marker, ir)
		}
	}
}

func TestCoroEntryDirectFunctionValueDemandsTargetAndOmitsDeadExternal(t *testing.T) {
	const source = `package foo
func External()
func Target() {}
func Owner() bool { return Target != nil }
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
	owner := ssaPkg.Func("Owner")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: owner, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          universe.FunctionIDConfig(),
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, ok := plan.FunctionPlan(ssaPkg.Func("Target"))
	if !ok || targetPlan.Demand != coro.SyncDemand || targetPlan.Emission != coro.EmitPlain || targetPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("Target plan = %+v, present=%t; want demanded direct plain body", targetPlan, ok)
	}
	externalPlan, ok := plan.FunctionPlan(ssaPkg.Func("External"))
	if !ok || externalPlan.Demand != coro.NoDemand || externalPlan.Emission != coro.EmitNone || externalPlan.Primary != coro.PrimaryExternal {
		t.Fatalf("External plan = %+v, present=%t; want non-emitted logical external", externalPlan, ok)
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe}},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	for _, name := range []string{"foo.Owner", "foo.Target"} {
		if module.NamedFunction(name).IsNil() {
			t.Fatalf("missing demanded function %q:\n%s", name, module.String())
		}
	}
	if !module.NamedFunction("foo.External").IsNil() {
		t.Fatalf("dead external acquired an LLVM declaration:\n%s", module.String())
	}
}

func TestCoroEntryDemandedEffectfulComplexFunctionCompiles(t *testing.T) {
	const source = `package foo
func Complex(ch chan int) int {
	value := <-ch
	if value == 0 {
		return 1
	}
	return value
}
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
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	complex := ssaPkg.Func("Complex")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: complex, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	complexPlan, ok := plan.FunctionPlan(complex)
	if !ok || complexPlan.Demand != coro.AsyncDemand || complexPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("Complex plan = %+v, present=%t; want demanded coroutine emission", complexPlan, ok)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,
	}
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile demanded channel/control-flow coroutine: %v", err)
	}
	module := pkg.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify demanded channel/control-flow coroutine: %v\n%s", err, module.String())
	}
	body := requireCoroPhysicalFunction(t, module, "foo.Complex").String()
	if !strings.Contains(body, coroChanRecvParkHookV1) || !strings.Contains(body, "switch i32") {
		t.Fatalf("complex coroutine lacks channel park/control-flow lowering:\n%s", body)
	}
}

func TestCoroPhysicalConsumerRejectsReferenceToEmitNone(t *testing.T) {
	tests := []struct {
		name        string
		hiddenName  string
		selectInstr func(ssa.Instruction) bool
		want        string
	}{
		{
			name:       "call",
			hiddenName: "HiddenCall",
			selectInstr: func(instr ssa.Instruction) bool {
				_, ok := instr.(*ssa.Call)
				return ok
			},
			want: "non-emitted call target",
		},
		{
			name:       "function value",
			hiddenName: "HiddenValue",
			selectInstr: func(instr ssa.Instruction) bool {
				for _, operand := range instr.Operands(nil) {
					if operand != nil {
						if _, ok := (*operand).(*ssa.Function); ok {
							return true
						}
					}
				}
				return false
			},
			want: "non-emitted function value",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const source = `package foo
func Target() {}
func HiddenCall() { Target() }
func HiddenValue() any { return Target }
func Caller() {}
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
			caller := ssaPkg.Func("Caller")
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: caller, Demand: coro.SyncDemand}}, coro.SSAConfig{
				EmissionUniverse:     ssaUniverse,
				FunctionIDs:          universe.FunctionIDConfig(),
				MaxPlainInstructions: -1,
			})
			if err != nil {
				t.Fatal(err)
			}
			targetPlan, ok := plan.FunctionPlan(ssaPkg.Func("Target"))
			if !ok || targetPlan.Emission != coro.EmitNone {
				t.Fatalf("Target plan = %+v, present=%t; want EmitNone before injected inconsistent consumer", targetPlan, ok)
			}
			var injected ssa.Instruction
			for _, block := range ssaPkg.Func(test.hiddenName).Blocks {
				for _, instr := range block.Instrs {
					if test.selectInstr(instr) {
						injected = instr
						break
					}
				}
			}
			if injected == nil {
				t.Fatalf("%s has no instruction suitable for the test", test.hiddenName)
			}
			// Deliberately mutate SSA after the immutable plan was built. This
			// models a stale/mismatched consumer and proves cl will not create a
			// declaration for an EmitNone target.
			caller.Blocks[0].Instrs = append([]ssa.Instruction{injected}, caller.Blocks[0].Instrs...)
			got, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
				PackageOptions{Compilation: &Compilation{
					CoroPlan:         plan,
					EmissionUniverse: universe}},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("inconsistent emitted consumer = %v, %v; want error containing %q", got, err, test.want)
			}
			if got != nil {
				t.Fatal("consumer preflight returned a partial package")
			}
		})
	}
}

func TestCoroEntryResolutionPreflightRejectsWholePlanBeforeCodegen(t *testing.T) {
	tests := []struct {
		name   string
		source string
		plan   func(*ssa.Package) (*coro.SSAPlan, error)
		want   string
	}{
		{
			name: "later coroutine body",
			source: `package foo
func APlain() {}
func ZCoroutine(ch chan int) { <-ch }
`,
			plan: func(pkg *ssa.Package) (*coro.SSAPlan, error) {
				return coro.AnalyzeSSA(pkg.Prog, coro.Roots{
					{Function: pkg.Func("APlain"), Demand: coro.SyncDemand},
					{Function: pkg.Func("ZCoroutine"), Demand: coro.AsyncDemand},
				}, coro.SSAConfig{})
			},
			want: "physical ABI",
		},
		{
			name: "dispatch descriptor",
			source: `package foo
func Target() {}
func Box() any { return Target }
`,
			plan: func(pkg *ssa.Package) (*coro.SSAPlan, error) {
				return coro.AnalyzeSSA(pkg.Prog, coro.Roots{
					{Function: pkg.Func("Box"), Demand: coro.AsyncDemand},
				}, coro.SSAConfig{})
			},
			want: "physical plan commit requires the call SitePlan stage",
		},
		{
			name:   "external coroutine",
			source: `package foo; func External()`,
			plan: func(pkg *ssa.Package) (*coro.SSAPlan, error) {
				external := pkg.Func("External")
				return coro.AnalyzeSSA(pkg.Prog, coro.Roots{
					{Function: external, Demand: coro.SyncDemand},
				}, coro.SSAConfig{
					ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
						if fn == external {
							return coro.SSAFunctionPolicy{
								Effect:           coro.WaitHost,
								External:         coro.ExternalKnown,
								OverrideExternal: true,
							}, nil
						}
						return coro.SSAFunctionPolicy{}, nil
					},
				})
			},
			want: "requires a defined body",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg, _, files := buildGoSSAPkg(t, tt.source)
			plan, err := tt.plan(pkg)
			if err != nil {
				t.Fatal(err)
			}
			observerCalls := 0
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			got, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{
				Compilation: &Compilation{
					CoroPlan:         plan,
					EmissionUniverse: coroEntryPreflightUniverse(plan),
					CoroPlanObserver: func(*ssa.Package, *coro.SSAPlan) {
						observerCalls++
					}},
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("preflight result = %v, %v; want error containing %q", got, err, tt.want)
			}
			if got != nil {
				t.Fatal("preflight failure returned a partial package")
			}
			if observerCalls != 0 {
				t.Fatalf("observer calls = %d, want pre-codegen rejection", observerCalls)
			}
		})
	}
}

func TestCoroEntryPreflightUsesFrozenCEmissionInsteadOfStubBlocks(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo
var channel chan int
//llgo:link External C.external
func External() { <-channel }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	external, ok := universe.Resolve(pkg.Func("External"))
	if !ok || external == nil || len(external.Blocks) == 0 {
		t.Fatal("fixture has no canonical bodyful C stub")
	}
	if background, classified, err := universe.FunctionBackground(external); err != nil || !classified || background != llssa.InC {
		t.Fatalf("External frozen background = %v, %v, %v; want InC, true, nil", background, classified, err)
	}

	buildPlan := func(ignore bool) *coro.SSAPlan {
		t.Helper()
		plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: external, Demand: coro.SyncDemand}}, coro.SSAConfig{
			EmissionUniverse: ssaUniverse,
			FunctionIDs:      universe.FunctionIDConfig(),
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == external {
					if !ignore {
						return coro.SSAFunctionPolicy{}, nil
					}
					return coro.SSAFunctionPolicy{
						IgnoreBody:       true,
						External:         coro.ExternalUnknownForeign,
						OverrideExternal: true,
					}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	ignored := buildPlan(true)
	if !ignored.IgnoresBody(external) {
		t.Fatal("bodyful C stub was not excluded from the physical plan")
	}
	if err := (&Compilation{
		CoroPlan:         ignored,
		EmissionUniverse: universe}).preflightCoroPlan(); err == nil || !strings.Contains(err.Error(), "requires a defined body") {
		t.Fatalf("bodyful frozen C root preflight error = %v, want defined-body rejection", err)
	}

	notIgnored := buildPlan(false)
	err = (&Compilation{
		CoroPlan:         notIgnored,
		EmissionUniverse: universe}).preflightCoroPlan()
	if err == nil || !strings.Contains(err.Error(), "synchronous demand without a planned raw plain entry") {
		t.Fatalf("non-ignored C stub preflight error = %v", err)
	}
}

func TestCoroEntryResolutionPreflightRejectsMissingPlanAndCache(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo; func F() {}`)
	for _, tt := range []struct {
		name        string
		compilation *Compilation
		cacheHit    bool
		want        string
	}{
		{
			name:        "missing plan",
			compilation: &Compilation{},
			want:        "requires a compilation CoroPlan",
		},
		{
			name: "missing universe",
			compilation: &Compilation{
				CoroPlan: &coro.SSAPlan{}},
			want: "prepared emission universe",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			got, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{
				Compilation: tt.compilation,
				CacheHit:    tt.cacheHit,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("preflight result = %v, %v; want error containing %q", got, err, tt.want)
			}
			if got != nil {
				t.Fatal("preflight failure returned a partial package")
			}
		})
	}

	cacheCompilation := &Compilation{
		CoroABI:      coro.PhysicalABIV1,
		SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI:     coro.PanicExplicitStatusABIV0,
		FuncRepABI:   coro.FuncRepABIV1,
	}
	if err := cacheCompilation.validateCoroCacheIdentity(); err == nil || !strings.Contains(err.Error(), "CoroPlanDigest") {
		t.Fatalf("cache identity error = %v, want missing CoroPlanDigest rejection", err)
	}
}
