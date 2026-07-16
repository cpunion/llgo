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
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

type coroBootstrapTestPlan struct {
	rootDemand map[string]coro.Demand
	policy     map[string]coro.SSAFunctionPolicy
}

func TestSelectCoroProgramBootstrapV1ExactInitMain(t *testing.T) {
	ctx, pkg := newCoroBootstrapTestContext(t, nil, coroBootstrapTestPlan{
		rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.AsyncDemand},
	})
	bootstrap := ctx.coroProgramBootstraps[pkg.ID]
	if bootstrap == nil || len(bootstrap.Steps) != 2 {
		t.Fatalf("bootstrap = %+v, want two steps", bootstrap)
	}
	for i, want := range []struct {
		role   uint32
		target string
	}{
		{coroProgramStepRoleInitV1, "example.com/bootstrap.init"},
		{coroProgramStepRoleMainV1, "example.com/bootstrap.main"},
	} {
		got := bootstrap.Steps[i]
		if got.Kind != coroProgramStepDirectPlainV1 || got.Role != want.role || got.Target != want.target || got.FunctionID == "" || got.Aux != 0 {
			t.Fatalf("step %d = %+v, want kind=%d role=%d target=%q nonempty ID aux=0", i, got, coroProgramStepDirectPlainV1, want.role, want.target)
		}
	}

	// The package-pointer cache is preferred, but the exact ID fallback is part
	// of linkMainPkg's package-instance compatibility contract.
	aPkg := ctx.pkgs[pkg]
	delete(ctx.pkgs, pkg)
	ctx.pkgByID[pkg.ID] = aPkg
	again, err := selectCoroProgramBootstrapV1(ctx, pkg)
	if err != nil {
		t.Fatalf("pkgByID fallback: %v", err)
	}
	if again.StepHash != bootstrap.StepHash || len(again.Steps) != len(bootstrap.Steps) {
		t.Fatalf("pkgByID fallback changed bootstrap: %+v != %+v", again, bootstrap)
	}
}

func TestSelectCoroProgramBootstrapV1RejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name string
		plan coroBootstrapTestPlan
		want string
	}{
		{
			name: "missing explicit main root",
			plan: coroBootstrapTestPlan{rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand}},
			want: "main: target is not an explicit plan root",
		},
		{
			name: "sync main root",
			plan: coroBootstrapTestPlan{rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.SyncDemand}},
			want: "main: explicit root demand is sync, want async capability",
		},
		{
			name: "suspending main root",
			plan: coroBootstrapTestPlan{
				rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.AsyncDemand},
				policy:     map[string]coro.SSAFunctionPolicy{"main": {Effect: coro.MayPark}},
			},
			want: "main: target",
		},
		{
			name: "preemptible main root",
			plan: coroBootstrapTestPlan{
				rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.AsyncDemand},
				policy:     map[string]coro.SSAFunctionPolicy{"main": {Exec: coro.NeedsPreempt}},
			},
			want: "main: target",
		},
		{
			name: "dynamic main representation",
			plan: coroBootstrapTestPlan{
				rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.AsyncDemand},
				policy:     map[string]coro.SSAFunctionPolicy{"main": {NeedsDispatch: true}},
			},
			want: "main: target",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := buildCoroBootstrapTestContext(t, nil, test.plan)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("pre-codegen preparation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSelectCoroProgramBootstrapV1AcceptsBothDemandPlainBody(t *testing.T) {
	ctx, pkg := newCoroBootstrapTestContext(t, nil, coroBootstrapTestPlan{
		rootDemand: map[string]coro.Demand{"init": coro.BothDemand, "main": coro.BothDemand},
	})
	if bootstrap := ctx.coroProgramBootstraps[pkg.ID]; bootstrap == nil || len(bootstrap.Steps) != 2 {
		t.Fatalf("both-demand plain bootstrap = %+v, want two steps", bootstrap)
	}
}

func TestSelectCoroProgramBootstrapRuntimeAcceptsLocalUnwindAndRejectsThreadAffinity(t *testing.T) {
	ctx, pkg := newCoroBootstrapTestContext(t, nil, coroBootstrapTestPlan{
		rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.AsyncDemand},
	})
	ctx.buildConf.EnableCoroProgramBootstrapRun = true
	if _, err := selectCoroProgramBootstrapV1(ctx, pkg); err != nil {
		t.Fatalf("production bootstrap rejected conservative local MayUnwind: %v", err)
	}
	// Rebuild through the real analyzer with an unsupported trusted bit.
	ctx, pkg = newCoroBootstrapTestContext(t, nil, coroBootstrapTestPlan{
		rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.AsyncDemand},
		policy:     map[string]coro.SSAFunctionPolicy{"main": {Exec: coro.ThreadAffine}},
	})
	ctx.buildConf.EnableCoroProgramBootstrapRun = true
	if _, err := selectCoroProgramBootstrapV1(ctx, pkg); err == nil || !strings.Contains(err.Error(), "unsupported execution constraints") {
		t.Fatalf("production bootstrap error = %v, want thread-affinity rejection", err)
	}
}

func TestSelectCoroProgramBootstrapV1RejectsMissingExactPackage(t *testing.T) {
	ctx, pkg := newCoroBootstrapTestContext(t, nil, coroBootstrapTestPlan{
		rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.AsyncDemand},
	})
	delete(ctx.pkgs, pkg)
	delete(ctx.pkgByID, pkg.ID)
	if _, err := selectCoroProgramBootstrapV1(ctx, pkg); err == nil || !strings.Contains(err.Error(), "has no exact SSA package") {
		t.Fatalf("select error = %v, want exact-package rejection", err)
	}
}

func TestSelectCoroProgramBootstrapV1RejectsPatchedInitSymbol(t *testing.T) {
	ctx, pkg := newCoroBootstrapTestContext(t, nil, coroBootstrapTestPlan{
		rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.AsyncDemand},
	})
	ctx.patches[pkg.PkgPath] = cl.Patch{}
	if _, err := selectCoroProgramBootstrapV1(ctx, pkg); err == nil || !strings.Contains(err.Error(), "does not use the strict legacy init symbol") {
		t.Fatalf("select error = %v, want patched-init physical-symbol rejection", err)
	}
}

func TestCoroProgramBootstrapRejectsInvalidRootsBeforePackageCodegen(t *testing.T) {
	conf := NewDefaultConf(ModeGen)
	conf.EnableCoroEntryResolution = true
	conf.EnableCoroPhysicalABI = true
	conf.EnableCoroChildAwait = true
	conf.EnableCoroProgramBootstrapABI = true
	moduleCalls := 0
	conf.ModuleHook = func(Package) { moduleCalls++ }
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		mainFn, err := findSingleSSAMain(input.Program)
		if err != nil {
			return nil, err
		}
		// Deliberately omit the synthetic main-package init root. It may still
		// exist in the plan, but the startup ABI requires an explicit root.
		return input.Analyze(coro.Roots{{Function: mainFn, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			MaxPlainInstructions: -1,
		})
	}
	pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
	if err == nil || !strings.Contains(err.Error(), "init: target is not an explicit plan root") {
		t.Fatalf("Do error = %v, want missing explicit init-root rejection", err)
	}
	if len(pkgs) != 0 {
		t.Fatalf("Do packages = %+v, want none", pkgs)
	}
	if moduleCalls != 0 {
		t.Fatalf("ModuleHook calls = %d, want zero before-codegen rejection", moduleCalls)
	}
}

func TestCoroProgramBootstrapHashV1StableAndStepComplete(t *testing.T) {
	ctx, pkg := newCoroBootstrapTestContext(t, nil, coroBootstrapTestPlan{
		rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.AsyncDemand},
	})
	bootstrap := ctx.coroProgramBootstraps[pkg.ID]
	again, err := coroProgramBootstrapHashV1(ctx, bootstrap.Steps)
	if err != nil {
		t.Fatal(err)
	}
	if again != bootstrap.StepHash {
		t.Fatalf("bootstrap hash is unstable: %x != %x", again, bootstrap.StepHash)
	}

	mutations := []struct {
		name   string
		mutate func([]coroProgramBootstrapStepV1)
	}{
		{"order", func(steps []coroProgramBootstrapStepV1) { steps[0], steps[1] = steps[1], steps[0] }},
		{"kind", func(steps []coroProgramBootstrapStepV1) { steps[0].Kind = coroProgramStepCoroRootV1 }},
		{"role", func(steps []coroProgramBootstrapStepV1) { steps[0].Role = coroProgramStepRoleMainV1 }},
		{"function ID", func(steps []coroProgramBootstrapStepV1) { steps[0].FunctionID += ".changed" }},
		{"target", func(steps []coroProgramBootstrapStepV1) { steps[0].Target += ".changed" }},
		{"aux", func(steps []coroProgramBootstrapStepV1) { steps[0].Aux = 1 }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			steps := append([]coroProgramBootstrapStepV1(nil), bootstrap.Steps...)
			mutation.mutate(steps)
			changed, err := coroProgramBootstrapHashV1(ctx, steps)
			if err != nil {
				t.Fatal(err)
			}
			if changed == bootstrap.StepHash {
				t.Fatalf("bootstrap hash ignored %s", mutation.name)
			}
		})
	}

	originalDigest := ctx.coroPlanDigest
	ctx.coroPlanDigest = strings.Repeat("1", len(originalDigest))
	changedPlan, err := coroProgramBootstrapHashV1(ctx, bootstrap.Steps)
	if err != nil {
		t.Fatal(err)
	}
	if changedPlan == bootstrap.StepHash {
		t.Fatal("bootstrap hash ignored the canonical plan digest")
	}
	ctx.coroPlanDigest = originalDigest
	ctx.buildConf.EnableCoroProgramBootstrapRun = true
	changedDriver, err := coroProgramBootstrapHashV1(ctx, bootstrap.Steps)
	if err != nil {
		t.Fatal(err)
	}
	if changedDriver == bootstrap.StepHash {
		t.Fatal("bootstrap hash ignored factory/driver activation")
	}
}

func newCoroBootstrapTestContext(t *testing.T, target *llssa.Target, spec coroBootstrapTestPlan) (*context, *packages.Package) {
	t.Helper()
	ctx, pkg, err := buildCoroBootstrapTestContext(t, target, spec)
	if err != nil {
		t.Fatalf("buildCoroPlan: %v", err)
	}
	return ctx, pkg
}

func buildCoroBootstrapTestContext(t *testing.T, target *llssa.Target, spec coroBootstrapTestPlan) (*context, *packages.Package, error) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", `package main; func main() {}`, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{file}
	ssaPkg, _, err := ssautil.BuildPackage(
		&types.Config{Importer: importer.Default()},
		fset,
		types.NewPackage("example.com/bootstrap", "main"),
		files,
		ssa.SanityCheckFunctions|ssa.InstantiateGenerics,
	)
	if err != nil {
		t.Fatal(err)
	}
	pkg := &packages.Package{
		ID:      "example.com/bootstrap",
		PkgPath: "example.com/bootstrap",
		Name:    "main",
		Types:   ssaPkg.Pkg,
		Syntax:  files,
	}
	aPkg := &aPackage{Package: pkg, SSA: ssaPkg}
	prog := llssa.NewProgram(target)
	t.Cleanup(prog.Dispose)
	conf := &Config{
		BuildMode:                     BuildModeExe,
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroProgramBootstrapABI: true,
	}
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		roots := make(coro.Roots, 0, len(spec.rootDemand))
		for _, name := range []string{"init", "main"} {
			if demand := spec.rootDemand[name]; demand != coro.NoDemand {
				roots = append(roots, coro.Root{Function: ssaPkg.Func(name), Demand: demand})
			}
		}
		return input.Analyze(roots, coro.SSAConfig{
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				return spec.policy[fn.Name()], nil
			},
		})
	}
	ctx := &context{
		progSSA:   ssaPkg.Prog,
		prog:      prog,
		patches:   make(cl.Patches),
		initial:   []*packages.Package{pkg},
		pkgs:      map[*packages.Package]Package{pkg: aPkg},
		pkgByID:   map[string]Package{pkg.ID: aPkg},
		mode:      ModeBuild,
		buildConf: conf,
	}
	err = buildCoroPlan(ctx, aPkg)
	return ctx, pkg, err
}
