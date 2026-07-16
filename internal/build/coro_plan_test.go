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

package build

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func TestBuildCoroPlanInstallsArchiveDigest(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", `package p; func F(value int) int { return value + 1 }`, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{file}
	ssaPkg, _, err := ssautil.BuildPackage(
		&types.Config{Importer: importer.Default()},
		fset,
		types.NewPackage("example.com/p", "p"),
		files,
		ssa.SanityCheckFunctions|ssa.InstantiateGenerics,
	)
	if err != nil {
		t.Fatal(err)
	}
	aPkg := &aPackage{
		Package: &packages.Package{
			ID:      "example.com/p",
			PkgPath: "example.com/p",
			Name:    "p",
			Types:   ssaPkg.Pkg,
			Syntax:  files,
		},
		SSA: ssaPkg,
	}
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		progSSA: ssaPkg.Prog,
		prog:    prog,
		buildConf: &Config{
			EnableCoroEntryResolution: true,
			CoroPlanBuilder: func(input CoroPlanInput) (*coro.SSAPlan, error) {
				return input.Analyze(coro.Roots{{Function: ssaPkg.Func("F"), Demand: coro.SyncDemand}}, coro.SSAConfig{
					MaxPlainInstructions: -1,
				})
			},
		},
	}
	if err := buildCoroPlan(ctx, aPkg); err != nil {
		t.Fatal(err)
	}
	if len(ctx.coroPlanDigest) != sha256.Size*2 {
		t.Fatalf("CoroPlanDigest length = %d, want %d", len(ctx.coroPlanDigest), sha256.Size*2)
	}
	if ctx.clCompilation == nil || ctx.clCompilation.CoroPlanDigest != ctx.coroPlanDigest {
		t.Fatalf("compilation digest = %+v, want %q", ctx.clCompilation, ctx.coroPlanDigest)
	}
	if ctx.coroPlanMetadata.CoroABI != coro.EntryResolutionABIV0 ||
		ctx.coroPlanMetadata.SchedulerABI != coro.SchedulerNoneABIV0 ||
		ctx.coroPlanMetadata.TargetTriple != prog.TargetSpec().Triple {
		t.Fatalf("installed digest metadata = %+v", ctx.coroPlanMetadata)
	}
	if !ctx.canUsePackageCache() {
		t.Fatal("complete active coroutine plan did not enable package cache")
	}
	manifest := newManifestBuilder()
	ctx.collectCommonInputs(manifest)
	if manifest.common.CoroPlanDigest != ctx.coroPlanDigest || manifest.common.CoroDataLayout != prog.DataLayout() {
		t.Fatalf("manifest coroutine inputs = %+v", manifest.common)
	}

	badProg := llssa.NewProgram(nil)
	defer badProg.Dispose()
	badCtx := &context{
		progSSA: ssaPkg.Prog,
		prog:    badProg,
		buildConf: &Config{
			EnableCoroEntryResolution: true,
			CoroPlanBuilder: func(input CoroPlanInput) (*coro.SSAPlan, error) {
				return input.Analyze(coro.Roots{{Function: ssaPkg.Func("F"), Demand: coro.SyncDemand}}, coro.SSAConfig{
					FunctionIDs:          coro.FunctionIDConfig{CoroABI: "conflicting-coro-abi"},
					MaxPlainInstructions: -1,
				})
			},
		},
	}
	if err := buildCoroPlan(badCtx, aPkg); err == nil || !strings.Contains(err.Error(), "does not match FunctionID ABI") {
		t.Fatalf("conflicting builder ABI error = %v", err)
	}
	if badCtx.coroPlan != nil || badCtx.clCompilation != nil || badCtx.coroPlanDigest != "" {
		t.Fatal("conflicting builder ABI installed partial coroutine state")
	}
}

func TestCoroPhysicalABICacheRegistrationPreservesCollectedFuncInfo(t *testing.T) {
	const source = `package p
func Leaf(value uint32) uint32 { return value + 1 }
`
	compile := func(cacheHit bool) []funcInfoRecord {
		t.Helper()
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "p.go", source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files := []*ast.File{file}
		ssaPkg, _, err := ssautil.BuildPackage(
			&types.Config{Importer: importer.Default()},
			fset,
			types.NewPackage("example.com/p", "p"),
			files,
			ssa.SanityCheckFunctions|ssa.InstantiateGenerics,
		)
		if err != nil {
			t.Fatal(err)
		}
		prog := llssa.NewProgram(nil)
		defer prog.Dispose()
		prog.EnableFuncInfoMetadata(true)
		universe, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{SSA: ssaPkg, Files: files}})
		if err != nil {
			t.Fatal(err)
		}
		ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		functionIDs := universe.FunctionIDConfig()
		functionIDs.CoroABI = coro.PhysicalABIV0
		functionIDs.SchedulerABI = coro.SchedulerNoneABIV0
		functionIDs.ArchiveReady = true
		leaf := ssaPkg.Func("Leaf")
		plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: leaf, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == leaf {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		lpkg, _, err := cl.NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, cl.PackageOptions{
			Compilation: &cl.Compilation{
				CoroPlan:                  plan,
				EnableCoroEntryResolution: true,
				EnableCoroPhysicalABI:     true,
				CoroPlanDigest:            strings.Repeat("0", 64),
				CoroABI:                   coro.PhysicalABIV0,
				SchedulerABI:              coro.SchedulerNoneABIV0,
				PanicABI:                  coro.PanicLegacyABIV0,
				FuncRepABI:                coro.FuncRepABIV0,
				EmissionUniverse:          universe,
			},
			CacheHit: cacheHit,
		})
		if err != nil {
			t.Fatal(err)
		}
		return collectFuncInfo([]Package{{LPkg: lpkg}})
	}

	sourceRecords := compile(false)
	cachedRecords := compile(true)
	if !reflect.DeepEqual(cachedRecords, sourceRecords) {
		t.Fatalf("cache registration funcinfo differs from source compilation:\nsource: %+v\ncached: %+v", sourceRecords, cachedRecords)
	}
	wantSymbol := "example.com/p.Leaf$coro"
	wantDisplay := "example.com/p.Leaf"
	found := false
	for _, record := range cachedRecords {
		if record.symbol == "example.com/p.Leaf" {
			t.Fatalf("cache registration exposed legacy plain symbol: %+v", record)
		}
		if record.symbol == wantSymbol {
			found = true
			if record.name != wantDisplay {
				t.Fatalf("coroutine funcinfo display name = %q, want %q", record.name, wantDisplay)
			}
		}
	}
	if !found {
		t.Fatalf("cache registration funcinfo is missing %q: %+v", wantSymbol, cachedRecords)
	}
}

func TestCoroPlanBuilderRunsBeforeCodegenWithoutChangingIR(t *testing.T) {
	t.Setenv(llgoBuildCache, "on")
	cacheRoot := t.TempDir()
	oldCacheRootFunc := cacheRootFunc
	cacheRootFunc = func() string { return cacheRoot }
	t.Cleanup(func() { cacheRootFunc = oldCacheRootFunc })

	var (
		builderCalls       int
		builderDone        bool
		planned            *coro.SSAPlan
		mainFn             *ssa.Function
		cacheRegistrations int
		sourceCompilations int
	)
	observed := make(map[*ssa.Package]int)
	builder := func(input CoroPlanInput) (*coro.SSAPlan, error) {
		builderCalls++
		var err error
		mainFn, err = findSingleSSAMain(input.Program)
		if err != nil {
			return nil, err
		}
		planned, err = input.Analyze(coro.Roots{{Function: mainFn, Demand: coro.AsyncDemand}}, coro.SSAConfig{})
		if err == nil {
			builderDone = true
		}
		return planned, err
	}

	baselineIR, baselineModules := buildModeGenIR(t, "../../cl/_testgo/chan", nil, nil, nil)
	plannedIR, plannedModules := buildModeGenIR(t, "../../cl/_testgo/chan", builder, func(pkg *ssa.Package, plan *coro.SSAPlan) {
		if plan != planned {
			t.Errorf("package %s observed plan %p, want compilation plan %p", pkg, plan, planned)
		}
		observed[pkg]++
	}, func(Package) {
		if !builderDone {
			t.Error("ModuleHook ran before CoroPlanBuilder completed")
		}
	}, func(pkg Package) {
		if pkg.CacheHit {
			cacheRegistrations++
			if observed[pkg.SSA] != 0 {
				t.Errorf("cached package %s reported coroutine source compilation", pkg.PkgPath)
			}
			return
		}
		sourceCompilations++
		if observed[pkg.SSA] != 1 {
			t.Errorf("source package %s observed coroutine plan %d times, want 1", pkg.PkgPath, observed[pkg.SSA])
		}
	})
	if builderCalls != 1 {
		t.Fatalf("CoroPlanBuilder calls = %d, want 1", builderCalls)
	}
	if planned == nil || mainFn == nil {
		t.Fatal("CoroPlanBuilder did not publish a plan for main")
	}
	if sourceCompilations == 0 || len(observed) != sourceCompilations {
		t.Fatalf("source compilation observations = %d for %d packages, want one per package", len(observed), sourceCompilations)
	}
	if cacheRegistrations == 0 {
		t.Fatal("planned build had no cache registration to verify")
	}
	id, ok := planned.FunctionID(mainFn)
	if !ok {
		t.Fatal("main function is absent from coroutine plan")
	}
	mainPlan, ok := planned.BasePlan().Lookup(id)
	if !ok || !mainPlan.Effect.Contains(coro.MayPark) || mainPlan.Demand != coro.AsyncDemand {
		t.Fatalf("main coroutine plan = %+v, %v", mainPlan, ok)
	}

	if plannedIR != baselineIR {
		t.Fatal("report-only CoroPlanBuilder changed emitted LLVM IR")
	}
	if len(plannedModules) == 0 || !reflect.DeepEqual(plannedModules, baselineModules) {
		t.Fatalf("report-only CoroPlanBuilder changed generated package modules:\nbaseline: %x\nplanned: %x", baselineModules, plannedModules)
	}
}

func TestCoroPlanInputCanonicalizesPatchedRoot(t *testing.T) {
	original := buildSSAOrderTestPackage(t, `package p
func f() {}
func g() {}
`)
	canonical := original.Pkg.Func("g")
	universe, err := coro.NewSSAEmissionUniverse(original.Prog, []*ssa.Function{canonical})
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:          original.Prog,
		EmissionUniverse: universe,
		resolveFunction: func(fn *ssa.Function) (*ssa.Function, bool) {
			if fn == original {
				return canonical, true
			}
			return fn, universe.Contains(fn)
		},
	}
	roots := coro.Roots{{Function: original, Demand: coro.SyncDemand}}
	builderResolverCalls := 0
	plan, err := input.Analyze(roots, coro.SSAConfig{
		ResolveFunction: func(*ssa.Function) (*ssa.Function, bool, error) {
			builderResolverCalls++
			return nil, false, fmt.Errorf("builder resolver must not override frozen frontend aliases")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if builderResolverCalls != 0 {
		t.Fatalf("builder ResolveFunction calls = %d, want 0", builderResolverCalls)
	}
	if roots[0].Function != original {
		t.Fatal("Analyze mutated the builder-owned root slice")
	}
	if resolved, ok := input.ResolveFunction(original); !ok || resolved != canonical {
		t.Fatalf("ResolveFunction(original) = %v, %v; want exact canonical function", resolved, ok)
	}
	if _, ok := plan.FunctionPlan(original); ok {
		t.Fatal("original patched declaration entered the exact-pointer plan")
	}
	got, ok := plan.FunctionPlan(canonical)
	if !ok || got.Demand != coro.SyncDemand {
		t.Fatalf("canonical plan = %+v, %v; want SyncDemand", got, ok)
	}
}

func TestActiveCoroABIVersions(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		coroABI   string
		scheduler string
	}{
		{"entry resolution", &Config{}, coro.EntryResolutionABIV0, coro.SchedulerNoneABIV0},
		{"physical leaf", &Config{EnableCoroPhysicalABI: true}, coro.PhysicalABIV0, coro.SchedulerNoneABIV0},
		{"child await", &Config{EnableCoroPhysicalABI: true, EnableCoroChildAwait: true}, coro.PhysicalABIV1, coro.SchedulerChildAwaitABIV0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := activeCoroABIVersion(test.config); got != test.coroABI {
				t.Fatalf("coroutine ABI = %q, want %q", got, test.coroABI)
			}
			if got := activeCoroSchedulerABIVersion(test.config); got != test.scheduler {
				t.Fatalf("scheduler ABI = %q, want %q", got, test.scheduler)
			}
		})
	}
}

func TestBuildCoroPlanErrors(t *testing.T) {
	t.Run("builder error", func(t *testing.T) {
		sentinel := errors.New("sentinel")
		ctx := &context{
			buildConf: &Config{CoroPlanBuilder: func(CoroPlanInput) (*coro.SSAPlan, error) {
				return nil, sentinel
			}},
		}
		err := buildCoroPlan(ctx)
		if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "build coroutine plan") {
			t.Fatalf("buildCoroPlan error = %v", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("failed builder installed coroutine compilation state")
		}
	})

	t.Run("nil plan", func(t *testing.T) {
		ctx := &context{
			buildConf: &Config{CoroPlanBuilder: func(CoroPlanInput) (*coro.SSAPlan, error) {
				return nil, nil
			}},
		}
		if err := buildCoroPlan(ctx); err == nil || !strings.Contains(err.Error(), "nil plan") {
			t.Fatalf("buildCoroPlan error = %v, want nil-plan rejection", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("nil-plan builder installed coroutine compilation state")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		ctx := &context{buildConf: &Config{}}
		if err := buildCoroPlan(ctx); err != nil || ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatalf("disabled buildCoroPlan = %v, plan %v, compilation %v", err, ctx.coroPlan, ctx.clCompilation)
		}
	})

	t.Run("nil context or config", func(t *testing.T) {
		if err := buildCoroPlan(nil); err != nil {
			t.Fatalf("nil-context buildCoroPlan = %v", err)
		}
		ctx := &context{}
		if err := buildCoroPlan(ctx); err != nil || ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatalf("nil-config buildCoroPlan = %v, plan %v, compilation %v", err, ctx.coroPlan, ctx.clCompilation)
		}
	})

	t.Run("entry resolution requires builder", func(t *testing.T) {
		ctx := &context{buildConf: &Config{EnableCoroEntryResolution: true}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "CoroPlanBuilder is required") {
			t.Fatalf("buildCoroPlan error = %v, want missing-builder rejection", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("missing builder installed coroutine compilation state")
		}
	})

	t.Run("physical ABI requires entry resolution", func(t *testing.T) {
		ctx := &context{buildConf: &Config{EnableCoroPhysicalABI: true}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "entry resolution is required") {
			t.Fatalf("buildCoroPlan error = %v, want entry-resolution requirement", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("invalid physical ABI configuration installed coroutine compilation state")
		}
	})

	t.Run("child await requires physical ABI", func(t *testing.T) {
		ctx := &context{buildConf: &Config{
			EnableCoroEntryResolution: true,
			EnableCoroChildAwait:      true,
		}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "physical ABI is required") {
			t.Fatalf("buildCoroPlan error = %v, want physical-ABI requirement", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("invalid child-await configuration installed coroutine compilation state")
		}
	})

	t.Run("entry resolution requires prepared emission universe", func(t *testing.T) {
		builderCalls := 0
		ctx := &context{buildConf: &Config{
			EnableCoroEntryResolution: true,
			CoroPlanBuilder: func(CoroPlanInput) (*coro.SSAPlan, error) {
				builderCalls++
				return &coro.SSAPlan{}, nil
			},
		}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "prepared emission universe is required") {
			t.Fatalf("buildCoroPlan error = %v, want missing-universe rejection", err)
		}
		if builderCalls != 0 {
			t.Fatalf("CoroPlanBuilder calls = %d, want 0", builderCalls)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("missing universe installed coroutine compilation state")
		}
	})

	for _, tt := range []struct {
		name string
	}{
		{name: "report only"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := &coro.SSAPlan{}
			builderCalls := 0
			observerCalls := 0
			ctx := &context{buildConf: &Config{
				CoroPlanBuilder: func(CoroPlanInput) (*coro.SSAPlan, error) {
					builderCalls++
					return plan, nil
				},
				CoroPlanObserver: func(_ *ssa.Package, got *coro.SSAPlan) {
					observerCalls++
					if got != plan {
						t.Errorf("observed plan = %p, want %p", got, plan)
					}
				},
			}}

			if err := buildCoroPlan(ctx); err != nil {
				t.Fatalf("buildCoroPlan: %v", err)
			}
			if builderCalls != 1 {
				t.Fatalf("CoroPlanBuilder calls = %d, want 1", builderCalls)
			}
			if ctx.coroPlan != plan || ctx.clCompilation == nil || ctx.clCompilation.CoroPlan != plan {
				t.Fatalf("installed plan = %p, compilation = %+v, want %p", ctx.coroPlan, ctx.clCompilation, plan)
			}
			if ctx.clCompilation.EnableCoroEntryResolution {
				t.Fatal("report-only compilation unexpectedly enabled entry resolution")
			}
			ctx.clCompilation.CoroPlanObserver(nil, plan)
			if observerCalls != 1 {
				t.Fatalf("CoroPlanObserver calls = %d, want 1", observerCalls)
			}
		})
	}

	t.Run("Do stops before codegen", func(t *testing.T) {
		sentinel := errors.New("sentinel")
		conf := NewDefaultConf(ModeGen)
		conf.CoroPlanBuilder = func(CoroPlanInput) (*coro.SSAPlan, error) {
			return nil, sentinel
		}
		moduleCalls := 0
		conf.ModuleHook = func(Package) {
			moduleCalls++
		}

		pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
		if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "build coroutine plan") {
			t.Fatalf("Do error = %v", err)
		}
		if len(pkgs) != 0 {
			t.Fatalf("Do packages = %+v, want none", pkgs)
		}
		if moduleCalls != 0 {
			t.Fatalf("ModuleHook calls = %d, want 0", moduleCalls)
		}
	})

	t.Run("Do rejects entry resolution without builder before codegen", func(t *testing.T) {
		conf := NewDefaultConf(ModeGen)
		conf.EnableCoroEntryResolution = true
		moduleCalls := 0
		conf.ModuleHook = func(Package) {
			moduleCalls++
		}

		pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
		if err == nil || !strings.Contains(err.Error(), "CoroPlanBuilder is required") {
			t.Fatalf("Do error = %v, want missing-builder rejection", err)
		}
		if len(pkgs) != 0 {
			t.Fatalf("Do packages = %+v, want none", pkgs)
		}
		if moduleCalls != 0 {
			t.Fatalf("ModuleHook calls = %d, want 0", moduleCalls)
		}
	})

	t.Run("Do rejects active builder that bypasses input Analyze", func(t *testing.T) {
		conf := NewDefaultConf(ModeGen)
		conf.EnableCoroEntryResolution = true
		conf.CoroPlanBuilder = func(CoroPlanInput) (*coro.SSAPlan, error) {
			return &coro.SSAPlan{}, nil
		}
		observerCalls := 0
		moduleCalls := 0
		conf.CoroPlanObserver = func(*ssa.Package, *coro.SSAPlan) {
			observerCalls++
		}
		conf.ModuleHook = func(Package) {
			moduleCalls++
		}

		pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
		if err == nil || !strings.Contains(err.Error(), "plan created by CoroPlanInput.Analyze") {
			t.Fatalf("Do error = %v, want Analyze bypass rejection", err)
		}
		if len(pkgs) != 0 {
			t.Fatalf("Do packages = %+v, want none", pkgs)
		}
		if observerCalls != 0 || moduleCalls != 0 {
			t.Fatalf("observer/module calls = %d/%d, want 0/0", observerCalls, moduleCalls)
		}
	})
}

func TestCoroEntryResolutionUsesPlanMatchedPackageCache(t *testing.T) {
	t.Setenv(llgoBuildCache, "on")
	cacheRoot := t.TempDir()
	oldCacheRootFunc := cacheRootFunc
	cacheRootFunc = func() string { return cacheRoot }
	t.Cleanup(func() { cacheRootFunc = oldCacheRootFunc })

	archive, err := os.CreateTemp(t.TempDir(), "seed-*.a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.WriteString("plain archive"); err != nil {
		archive.Close()
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	const pkgPath = "example.com/coro-cache"
	metadata := coro.PlanDigestMetadata{
		CoroABI:        coro.EntryResolutionABIV0,
		SchedulerABI:   coro.SchedulerNoneABIV0,
		PanicABI:       coro.PanicLegacyABIV0,
		FuncRepABI:     coro.FuncRepABIV0,
		TargetTriple:   "x86_64-unknown-linux-gnu",
		TargetCPU:      "x86-64",
		TargetFeatures: "+sse2",
		TargetABI:      "gnu",
		PointerBits:    64,
		Endianness:     "little",
		DataLayout:     "e-p:64:64",
	}
	newContext := func(digest string) *context {
		plan := &coro.SSAPlan{}
		emission := &cl.EmissionUniverse{}
		compilation := &cl.Compilation{
			CoroPlan:                  plan,
			EnableCoroEntryResolution: true,
			CoroPlanDigest:            digest,
			CoroABI:                   metadata.CoroABI,
			SchedulerABI:              metadata.SchedulerABI,
			PanicABI:                  metadata.PanicABI,
			FuncRepABI:                metadata.FuncRepABI,
			EmissionUniverse:          emission,
		}
		return &context{
			buildConf: &Config{
				Goos:                      "linux",
				Goarch:                    "amd64",
				EnableCoroEntryResolution: true,
			},
			coroPlan:         plan,
			coroEmission:     emission,
			coroPlanDigest:   digest,
			coroPlanMetadata: metadata,
			clCompilation:    compilation,
		}
	}
	manifest := func(ctx *context, path string) (string, string) {
		m := newManifestBuilder()
		ctx.collectCommonInputs(m)
		m.pkg.PkgPath = path
		return m.Build(), m.Fingerprint()
	}
	newPackage := func(ctx *context) *aPackage {
		manifestText, fingerprint := manifest(ctx, pkgPath)
		return &aPackage{
			Package: &packages.Package{
				PkgPath: pkgPath,
				Name:    "corocache",
			},
			Fingerprint: fingerprint,
			Manifest:    manifestText,
		}
	}

	digestA := strings.Repeat("a", 64)
	seedCtx := newContext(digestA)
	seedPkg := newPackage(seedCtx)
	seedPkg.ArchiveFile = archive.Name()
	seedPkg.NeedRt = true
	seedPkg.NeedPyInit = true
	if err := seedCtx.saveToCache(seedPkg); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	seedPaths := seedCtx.ensureCacheManager().PackagePaths(seedCtx.targetTriple(), pkgPath, seedPkg.Fingerprint)
	if _, err := os.Stat(seedPaths.Archive); err != nil {
		t.Fatalf("seed archive: %v", err)
	}

	matchingPkg := newPackage(seedCtx)
	if !seedCtx.tryLoadFromCache(matchingPkg) || !matchingPkg.CacheHit {
		t.Fatal("matching coroutine plan did not reuse the package archive")
	}
	if !matchingPkg.NeedRt || !matchingPkg.NeedPyInit {
		t.Fatalf("cache metadata runtime flags = %v/%v, want true/true", matchingPkg.NeedRt, matchingPkg.NeedPyInit)
	}

	digestB := strings.Repeat("b", 64)
	mismatchCtx := newContext(digestB)
	mismatchPkg := newPackage(mismatchCtx)
	mismatchPaths := mismatchCtx.ensureCacheManager().PackagePaths(mismatchCtx.targetTriple(), pkgPath, mismatchPkg.Fingerprint)
	if err := mismatchCtx.cacheManager.EnsureDir(mismatchPaths); err != nil {
		t.Fatal(err)
	}
	if err := copyFileAtomic(seedPaths.Archive, mismatchPaths.Archive); err != nil {
		t.Fatal(err)
	}
	if err := copyFileAtomic(seedPaths.Manifest, mismatchPaths.Manifest); err != nil {
		t.Fatal(err)
	}
	if mismatchCtx.tryLoadFromCache(mismatchPkg) {
		t.Fatal("mismatched coroutine manifest was accepted from a forced cache path")
	}
	if mismatchPkg.CacheHit || mismatchPkg.ArchiveFile != "" {
		t.Fatalf("mismatched cache read mutated package: hit=%v archive=%q", mismatchPkg.CacheHit, mismatchPkg.ArchiveFile)
	}
	forgedPkg := newPackage(seedCtx)
	forgedPkg.Fingerprint = strings.Repeat("c", 64)
	forgedPaths := seedCtx.ensureCacheManager().PackagePaths(seedCtx.targetTriple(), pkgPath, forgedPkg.Fingerprint)
	if err := seedCtx.cacheManager.EnsureDir(forgedPaths); err != nil {
		t.Fatal(err)
	}
	if err := copyFileAtomic(seedPaths.Archive, forgedPaths.Archive); err != nil {
		t.Fatal(err)
	}
	if err := copyFileAtomic(seedPaths.Manifest, forgedPaths.Manifest); err != nil {
		t.Fatal(err)
	}
	if seedCtx.tryLoadFromCache(forgedPkg) {
		t.Fatal("manifest stored under a forged fingerprint path was accepted")
	}

	incomplete := newContext("")
	if incomplete.canUsePackageCache() {
		t.Fatal("active context without CoroPlanDigest unexpectedly permits package cache")
	}
	incompletePkg := newPackage(incomplete)
	if incomplete.tryLoadFromCache(incompletePkg) {
		t.Fatal("active context without CoroPlanDigest read a cache archive")
	}
	if incomplete.cacheManager != nil {
		t.Fatal("incomplete coroutine context initialized a cache manager")
	}

	mismatchedUniverse := newContext(digestA)
	mismatchedUniverse.clCompilation.EmissionUniverse = &cl.EmissionUniverse{}
	if mismatchedUniverse.canUsePackageCache() {
		t.Fatal("active context with mismatched emission universe unexpectedly permits package cache")
	}
}

func TestCoroEntryResolutionBuildsPreparedRuntimePackages(t *testing.T) {
	for _, test := range []struct {
		name        string
		conf        Config
		needRuntime bool
		needPyInit  bool
		want        bool
	}{
		{name: "host report only", conf: Config{}, want: true},
		{name: "target report only stays lazy", conf: Config{Target: "embedded"}},
		{name: "target active emits frozen universe", conf: Config{Target: "embedded", EnableCoroEntryResolution: true}, want: true},
		{name: "target runtime lowering", conf: Config{Target: "embedded"}, needRuntime: true, want: true},
		{name: "target python lowering", conf: Config{Target: "embedded"}, needPyInit: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldBuildRuntimePackages(&test.conf, test.needRuntime, test.needPyInit); got != test.want {
				t.Fatalf("shouldBuildRuntimePackages = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCoroEmissionCoverageStopsBeforeAnyPackageCodegen(t *testing.T) {
	conf := NewDefaultConf(ModeGen)
	conf.EnableCoroEntryResolution = true

	var (
		builderCalls  int
		observerCalls int
		moduleCalls   int
	)
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		builderCalls++
		if input.EmissionUniverse == nil {
			return nil, fmt.Errorf("missing prepared emission universe")
		}
		mainFn, err := findSingleSSAMain(input.Program)
		if err != nil {
			return nil, err
		}
		return input.Analyze(coro.Roots{{Function: mainFn, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			Include: func(fn *ssa.Function) (bool, error) {
				return fn.Pkg == nil || fn.Pkg.Pkg == nil ||
					fn.Pkg.Pkg.Path() != "github.com/goplus/llgo/internal/build/_testgo/coro_emission/zmiss" ||
					fn.Name() != "Missing", nil
			},
		})
	}
	conf.CoroPlanObserver = func(*ssa.Package, *coro.SSAPlan) {
		observerCalls++
	}
	conf.ModuleHook = func(Package) {
		moduleCalls++
	}

	pkgs, err := Do([]string{"./_testgo/coro_emission"}, conf)
	if err == nil || !strings.Contains(err.Error(), "zmiss") || !strings.Contains(err.Error(), "Missing") {
		t.Fatalf("Do error = %v, want missing zmiss.Missing coverage", err)
	}
	if len(pkgs) != 0 {
		t.Fatalf("Do packages = %+v, want none", pkgs)
	}
	if builderCalls != 1 {
		t.Fatalf("CoroPlanBuilder calls = %d, want 1", builderCalls)
	}
	if observerCalls != 0 {
		t.Fatalf("CoroPlanObserver calls = %d, want 0", observerCalls)
	}
	if moduleCalls != 0 {
		t.Fatalf("ModuleHook calls = %d, want 0", moduleCalls)
	}
}

func TestCoroUnsupportedEntryResolutionReturnsErrorBeforeCodegen(t *testing.T) {
	conf := NewDefaultConf(ModeGen)
	conf.EnableCoroEntryResolution = true
	var (
		observerCalls int
		moduleCalls   int
		builderBuilt  bool
	)
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		mainFn, err := findSingleSSAMain(input.Program)
		if err != nil {
			return nil, err
		}
		plan, err := input.Analyze(coro.Roots{{Function: mainFn, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == mainFn {
					return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		})
		if err == nil {
			builderBuilt = true
		}
		return plan, err
	}
	conf.CoroPlanObserver = func(*ssa.Package, *coro.SSAPlan) {
		observerCalls++
	}
	conf.ModuleHook = func(Package) {
		moduleCalls++
	}

	pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
	if !builderBuilt {
		t.Fatalf("CoroPlanBuilder did not successfully return a plan: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "compile package") ||
		(!strings.Contains(err.Error(), "requires coroutine physical ABI lowering") &&
			!strings.Contains(err.Error(), "requires an unimplemented dispatch descriptor")) {
		t.Fatalf("Do error = %v, want cl coroutine preflight error returned from buildPkg", err)
	}
	if len(pkgs) != 0 {
		t.Fatalf("Do packages = %+v, want none", pkgs)
	}
	if observerCalls != 0 || moduleCalls != 0 {
		t.Fatalf("observer/module calls = %d/%d, want 0/0", observerCalls, moduleCalls)
	}
}

func TestCoroEmissionUniverseAcceptsModeTestVariants(t *testing.T) {
	conf := NewDefaultConf(ModeTest)
	sentinel := errors.New("mode-test emission universe prepared")
	var (
		builderCalls int
		moduleCalls  int
	)
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		builderCalls++
		if input.EmissionUniverse == nil {
			return nil, fmt.Errorf("missing prepared emission universe")
		}
		if _, err := input.Analyze(nil, coro.SSAConfig{}); err != nil {
			return nil, fmt.Errorf("analyze ModeTest emission universe: %w", err)
		}
		return nil, sentinel
	}
	conf.ModuleHook = func(Package) { moduleCalls++ }

	pkgs, err := Do([]string{"../../cl/_testgo/runtest"}, conf)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Do error = %v, want builder sentinel after ModeTest universe preparation", err)
	}
	if len(pkgs) != 0 {
		t.Fatalf("Do packages = %+v, want none", pkgs)
	}
	if builderCalls != 1 || moduleCalls != 0 {
		t.Fatalf("builder/module calls = %d/%d, want 1/0", builderCalls, moduleCalls)
	}
	// ABI-identical functions copied into a test variant intentionally resolve
	// to one physical symbol. Distinct same-path bodies remain exact and are
	// covered by cl.TestEmissionUniverseKeepsSamePathTestVariantsExact.
}

func buildModeGenIR(t *testing.T, pattern string, builder CoroPlanBuilder, observer CoroPlanObserver, moduleHooks ...ModuleHook) (string, map[string][sha256.Size]byte) {
	t.Helper()
	conf := NewDefaultConf(ModeGen)
	conf.CoroPlanBuilder = builder
	conf.CoroPlanObserver = observer
	modules := make(map[string][sha256.Size]byte)
	conf.ModuleHook = func(pkg Package) {
		key := pkg.ID
		if _, exists := modules[key]; exists {
			t.Errorf("ModuleHook ran more than once for %s", key)
		}
		modules[key] = sha256.Sum256([]byte(pkg.LPkg.String()))
		for _, hook := range moduleHooks {
			if hook != nil {
				hook(pkg)
			}
		}
	}
	pkgs, err := Do([]string{pattern}, conf)
	if err != nil {
		t.Fatalf("Do(%q): %v", pattern, err)
	}
	if len(pkgs) != 1 || pkgs[0].LPkg == nil {
		t.Fatalf("Do(%q) packages = %+v, want one generated package", pattern, pkgs)
	}
	ir := pkgs[0].LPkg.String()
	pkgs[0].LPkg.Prog.Dispose()
	return ir, modules
}

func findSingleSSAMain(prog *ssa.Program) (*ssa.Function, error) {
	if prog == nil {
		return nil, fmt.Errorf("nil SSA program")
	}
	var found *ssa.Function
	for _, pkg := range prog.AllPackages() {
		if pkg == nil || pkg.Pkg == nil || pkg.Pkg.Name() != "main" {
			continue
		}
		fn := pkg.Func("main")
		if fn == nil {
			continue
		}
		if found != nil && found != fn {
			return nil, fmt.Errorf("multiple SSA main functions: %s and %s", found, fn)
		}
		found = fn
	}
	if found == nil {
		return nil, fmt.Errorf("SSA main function not found")
	}
	return found, nil
}
