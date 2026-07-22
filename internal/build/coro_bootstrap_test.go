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
	"sort"
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
	ctx.buildConf.CoroProfile = CoroProfileStackless
	if _, err := selectCoroProgramBootstrapV1(ctx, pkg); err != nil {
		t.Fatalf("production bootstrap rejected conservative local MayUnwind: %v", err)
	}
	// Rebuild through the real analyzer with an unsupported trusted bit.
	ctx, pkg = newCoroBootstrapTestContext(t, nil, coroBootstrapTestPlan{
		rootDemand: map[string]coro.Demand{"init": coro.AsyncDemand, "main": coro.AsyncDemand},
		policy:     map[string]coro.SSAFunctionPolicy{"main": {Exec: coro.ThreadAffine}},
	})
	ctx.buildConf.CoroProfile = CoroProfileStackless
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

func TestSelectCoroProgramBootstrapV2ExactMixedFiveStageProgram(t *testing.T) {
	fixture := newCoroBootstrapV2TestContext(t)
	bootstrap, err := selectCoroProgramBootstrapV2(fixture.ctx, fixture.mainPackage)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap == nil || bootstrap.Version != coroProgramBootstrapVersionV2 || len(bootstrap.Steps) != 5 {
		t.Fatalf("v2 bootstrap = %+v, want version 2 and five steps", bootstrap)
	}
	if frozen := fixture.ctx.coroProgramBootstraps[fixture.mainPackage.ID]; frozen == nil ||
		frozen.StepHash != bootstrap.StepHash {
		t.Fatalf("pre-codegen frozen bootstrap = %+v, want stable selection hash %x", frozen, bootstrap.StepHash)
	}

	runtimeIndex := expectedCoroBootstrapV2DescriptorIndex(t, fixture.ctx.coroPlan, fixture.runtimeInit)
	publicRuntimeIndex := expectedCoroBootstrapV2DescriptorIndex(t, fixture.ctx.coroPlan, fixture.publicRuntimeInit)
	mainInitIndex := expectedCoroBootstrapV2DescriptorIndex(t, fixture.ctx.coroPlan, fixture.mainInit)
	wants := []struct {
		kind   uint32
		role   uint32
		target string
		owner  string
		aux    uint64
	}{
		{
			kind: coroProgramStepCoroRootV1, role: coroProgramStepRoleRuntimeInitV2,
			target: llssa.PkgRuntime + ".init$coro", owner: llssa.PkgRuntime, aux: runtimeIndex,
		},
		{
			kind: coroProgramStepDirectPlainV1, role: coroProgramStepRoleABIInitV2,
			target: "init$abitypes",
		},
		{
			kind: coroProgramStepCoroRootV1, role: coroProgramStepRolePublicRuntimeInitV2,
			target: "runtime.init$coro", owner: "runtime", aux: publicRuntimeIndex,
		},
		{
			kind: coroProgramStepCoroRootV1, role: coroProgramStepRolePackageInitV2,
			target: fixture.mainPackage.PkgPath + ".init$coro", owner: fixture.mainPackage.PkgPath, aux: mainInitIndex,
		},
		{
			kind: coroProgramStepDirectPlainV1, role: coroProgramStepRoleMainV2,
			target: fixture.mainPackage.PkgPath + ".main",
		},
	}
	for index, want := range wants {
		got := bootstrap.Steps[index]
		if got.Kind != want.kind || got.Role != want.role || got.Target != want.target ||
			got.Owner != want.owner || got.Aux != want.aux || got.FunctionID == "" || got.CatalogTarget != "" {
			t.Fatalf("v2 step %d = %+v, want kind=%d role=%d target=%q owner=%q aux=%d, nonempty ID and unbound catalog",
				index, got, want.kind, want.role, want.target, want.owner, want.aux)
		}
	}

	for _, check := range []struct {
		name string
		fn   *ssa.Function
		kind uint32
	}{
		{name: "runtime init", fn: fixture.runtimeInit, kind: coroProgramStepCoroRootV1},
		{name: "public runtime init", fn: fixture.publicRuntimeInit, kind: coroProgramStepCoroRootV1},
		{name: "main package init", fn: fixture.mainInit, kind: coroProgramStepCoroRootV1},
		{name: "main", fn: fixture.mainMain, kind: coroProgramStepDirectPlainV1},
	} {
		plan, ok := fixture.ctx.coroPlan.FunctionPlan(check.fn)
		if !ok {
			t.Fatalf("%s has no exact function plan", check.name)
		}
		if check.kind == coroProgramStepCoroRootV1 {
			if plan.Emission != coro.EmitCoroutine || plan.FuncRep != coro.DirectCoro || plan.Primary != coro.PrimaryCoroutine {
				t.Fatalf("%s plan = %+v, want one direct coroutine primary", check.name, plan)
			}
		} else if plan.Emission != coro.EmitPlain || plan.FuncRep != coro.DirectPlain || plan.Primary != coro.PrimaryPlain {
			t.Fatalf("%s plan = %+v, want one direct plain primary", check.name, plan)
		}
	}
}

func TestSelectCoroProgramBootstrapV2UsesOwnedNoopWhenPublicRuntimeIsAbsent(t *testing.T) {
	fixture := newCoroBootstrapV2TestContextWithPublicRuntime(t, false)
	bootstrap, err := selectCoroProgramBootstrapV2(fixture.ctx, fixture.mainPackage)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap == nil || len(bootstrap.Steps) != 5 {
		t.Fatalf("v2 bootstrap = %+v, want five fixed roles", bootstrap)
	}
	step := bootstrap.Steps[2]
	if step.Kind != coroProgramStepDirectPlainV1 || step.Role != coroProgramStepRolePublicRuntimeInitV2 ||
		step.FunctionID != coroProgramPublicRuntimeNoopIDV2 || step.Target != coroProgramPublicRuntimeNoopSymbolV2 ||
		step.Owner != "" || step.CatalogTarget != "" || step.Aux != 0 {
		t.Fatalf("absent public runtime step = %+v, want compiler-owned canonical no-op", step)
	}
	for _, root := range fixture.ctx.coroPlan.Roots() {
		if root.Function != nil && root.Function.Pkg != nil && root.Function.Pkg.Pkg != nil &&
			llssa.PathOf(root.Function.Pkg.Pkg) == "runtime" {
			t.Fatalf("absent public runtime created a guessed managed root: %+v", root)
		}
	}
}

func TestSelectCoroProgramBootstrapV2AllowsIRQUnsafeOnOrdinaryG(t *testing.T) {
	fixture := newCoroBootstrapV2TestContext(t)
	step, err := selectCoroProgramManagedStepV2(
		fixture.ctx,
		fixture.irqRuntimeRoot,
		llssa.PkgRuntime+".irqRuntimeRoot",
		llssa.PkgRuntime,
		"IRQ-unsafe bounded startup fixture",
		coroProgramStepRoleRuntimeInitV2,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := fixture.ctx.coroPlan.FunctionPlan(fixture.irqRuntimeRoot)
	if !ok || plan.Effect != coro.NoSuspend || !plan.Exec.Contains(coro.IRQUnsafe) || plan.Exec.Contains(coro.ThreadAffine) {
		t.Fatalf("IRQ-unsafe fixture plan = %+v, present=%t", plan, ok)
	}
	if step.Kind != coroProgramStepDirectPlainV1 || step.Target != llssa.PkgRuntime+".irqRuntimeRoot" || step.Owner != "" {
		t.Fatalf("IRQ-unsafe ordinary-G step = %+v, want direct plain", step)
	}
}

func TestBindCoroProgramBootstrapV2OwnersAndAnchors(t *testing.T) {
	fixture := newCoroBootstrapV2TestContext(t)
	semantic := fixture.ctx.coroProgramBootstraps[fixture.mainPackage.ID]
	const (
		runtimeAnchor       = coroRootPackageAnchorPrefixV1 + "11111111111111111111111111111111"
		publicRuntimeAnchor = coroRootPackageAnchorPrefixV1 + "22222222222222222222222222222222"
		mainAnchor          = coroRootPackageAnchorPrefixV1 + "33333333333333333333333333333333"
	)
	linked := []Package{
		coroBootstrapV2LinkedPackage(llssa.PkgRuntime, runtimeAnchor),
		coroBootstrapV2LinkedPackage("runtime", publicRuntimeAnchor),
		coroBootstrapV2LinkedPackage(fixture.mainPackage.PkgPath, mainAnchor),
	}
	bound, err := bindCoroProgramBootstrapV2(semantic, linked)
	if err != nil {
		t.Fatal(err)
	}
	if bound == semantic || &bound.Steps[0] == &semantic.Steps[0] {
		t.Fatal("v2 binding mutated or aliased the immutable semantic bootstrap")
	}
	for index, step := range semantic.Steps {
		if step.CatalogTarget != "" {
			t.Fatalf("semantic step %d was modified by binding: %+v", index, step)
		}
	}
	for index, step := range bound.Steps {
		switch step.Owner {
		case llssa.PkgRuntime:
			if step.CatalogTarget != runtimeAnchor {
				t.Fatalf("runtime-owned step %d bound to %q, want %q", index, step.CatalogTarget, runtimeAnchor)
			}
		case "runtime":
			if step.CatalogTarget != publicRuntimeAnchor {
				t.Fatalf("public-runtime-owned step %d bound to %q, want %q", index, step.CatalogTarget, publicRuntimeAnchor)
			}
		case fixture.mainPackage.PkgPath:
			if step.CatalogTarget != mainAnchor {
				t.Fatalf("main-owned step %d bound to %q, want %q", index, step.CatalogTarget, mainAnchor)
			}
		default:
			if step.Kind != coroProgramStepDirectPlainV1 || step.CatalogTarget != "" {
				t.Fatalf("compiler-owned step %d acquired catalog state: %+v", index, step)
			}
		}
	}
}

func TestBindCoroProgramBootstrapV2RejectsMissingConflictingAndInvalidAnchors(t *testing.T) {
	fixture := newCoroBootstrapV2TestContext(t)
	semantic := fixture.ctx.coroProgramBootstraps[fixture.mainPackage.ID]
	const (
		anchorA = coroRootPackageAnchorPrefixV1 + "11111111111111111111111111111111"
		anchorB = coroRootPackageAnchorPrefixV1 + "22222222222222222222222222222222"
		anchorC = coroRootPackageAnchorPrefixV1 + "33333333333333333333333333333333"
	)
	tests := []struct {
		name   string
		linked []Package
		want   string
	}{
		{
			name: "missing main owner",
			linked: []Package{
				coroBootstrapV2LinkedPackage(llssa.PkgRuntime, anchorA),
				coroBootstrapV2LinkedPackage("runtime", anchorB),
			},
			want: `owner "example.com/bootstrapv2" has no linked root anchor`,
		},
		{
			name: "conflicting runtime owner",
			linked: []Package{
				coroBootstrapV2LinkedPackage(llssa.PkgRuntime, anchorA),
				coroBootstrapV2LinkedPackage(llssa.PkgRuntime, anchorB),
				coroBootstrapV2LinkedPackage("runtime", anchorB),
				coroBootstrapV2LinkedPackage(fixture.mainPackage.PkgPath, anchorC),
			},
			want: "conflicting coroutine root anchors",
		},
		{
			name: "invalid runtime anchor",
			linked: []Package{
				coroBootstrapV2LinkedPackage(llssa.PkgRuntime, "invalid"),
				coroBootstrapV2LinkedPackage("runtime", anchorB),
				coroBootstrapV2LinkedPackage(fixture.mainPackage.PkgPath, anchorC),
			},
			want: "invalid coroutine root anchor",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bound, err := bindCoroProgramBootstrapV2(semantic, test.linked)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("bind result = %+v, %v; want error containing %q", bound, err, test.want)
			}
			for index, step := range semantic.Steps {
				if step.CatalogTarget != "" {
					t.Fatalf("failed binding modified semantic step %d: %+v", index, step)
				}
			}
		})
	}
}

func TestCoroProgramManifestHashV1CoversV2OwnerAnchorBinding(t *testing.T) {
	fixture := newCoroBootstrapV2TestContext(t)
	semantic := fixture.ctx.coroProgramBootstraps[fixture.mainPackage.ID]
	const (
		anchorA = coroRootPackageAnchorPrefixV1 + "11111111111111111111111111111111"
		anchorB = coroRootPackageAnchorPrefixV1 + "22222222222222222222222222222222"
		anchorC = coroRootPackageAnchorPrefixV1 + "33333333333333333333333333333333"
	)
	firstLinked := []Package{
		coroBootstrapV2LinkedPackage(llssa.PkgRuntime, anchorA),
		coroBootstrapV2LinkedPackage("runtime", anchorB),
		coroBootstrapV2LinkedPackage(fixture.mainPackage.PkgPath, anchorC),
	}
	secondLinked := []Package{
		coroBootstrapV2LinkedPackage(llssa.PkgRuntime, anchorC),
		coroBootstrapV2LinkedPackage("runtime", anchorB),
		coroBootstrapV2LinkedPackage(fixture.mainPackage.PkgPath, anchorA),
	}
	first, err := bindCoroProgramBootstrapV2(semantic, firstLinked)
	if err != nil {
		t.Fatal(err)
	}
	second, err := bindCoroProgramBootstrapV2(semantic, secondLinked)
	if err != nil {
		t.Fatal(err)
	}
	firstCatalog, err := collectLinkedCoroRootAnchors(firstLinked)
	if err != nil {
		t.Fatal(err)
	}
	secondCatalog, err := collectLinkedCoroRootAnchors(secondLinked)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(firstCatalog, "\x00") != strings.Join(secondCatalog, "\x00") {
		t.Fatalf("test did not preserve the same sorted anchor catalog: %q != %q", firstCatalog, secondCatalog)
	}
	if first.StepHash != second.StepHash || first.StepHash != semantic.StepHash {
		t.Fatalf("binding changed semantic StepHash: %x, %x, want %x", first.StepHash, second.StepHash, semantic.StepHash)
	}
	firstHash, err := coroProgramManifestHashV1(fixture.ctx, firstCatalog, first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := coroProgramManifestHashV1(fixture.ctx, secondCatalog, second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash == secondHash {
		t.Fatalf("final manifest hash ignored owner-to-CatalogTarget binding: %x", firstHash)
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
	ctx.buildConf.CoroProfile = CoroProfileStackless
	changedDriver, err := coroProgramBootstrapHashV1(ctx, bootstrap.Steps)
	if err != nil {
		t.Fatal(err)
	}
	if changedDriver == bootstrap.StepHash {
		t.Fatal("bootstrap hash ignored factory/driver activation")
	}
	ctx.buildConf.Goos = "wasip1"
	ctx.buildConf.Goarch = "wasm"
	hostPullDriver, err := coroProgramBootstrapHashV1(ctx, bootstrap.Steps)
	if err != nil {
		t.Fatal(err)
	}
	if hostPullDriver == changedDriver || hostPullDriver == bootstrap.StepHash {
		t.Fatal("bootstrap hash ignored the host-owned V2 pull reactor ABI")
	}
	hostPullAgain, err := coroProgramBootstrapHashV1(ctx, bootstrap.Steps)
	if err != nil || hostPullAgain != hostPullDriver {
		t.Fatalf("host-pull bootstrap hash is unstable: %x, %v, want %x", hostPullAgain, err, hostPullDriver)
	}
	ctx.buildConf.Goos = "linux"
	ctx.buildConf.Goarch = "386"
	withoutNativeTimer, err := coroProgramBootstrapHashV1(ctx, bootstrap.Steps)
	if err != nil {
		t.Fatal(err)
	}
	ctx.buildConf.Goarch = "amd64"
	withNativeTimer, err := coroProgramBootstrapHashV1(ctx, bootstrap.Steps)
	if err != nil {
		t.Fatal(err)
	}
	if withNativeTimer == withoutNativeTimer {
		t.Fatal("bootstrap hash ignored native monotonic timer owner ABI")
	}
	ctx.buildConf.Goarch = "386"
	afterCapabilityMismatch, err := coroProgramBootstrapHashV1(ctx, bootstrap.Steps)
	if err != nil {
		t.Fatal(err)
	}
	if afterCapabilityMismatch != withoutNativeTimer || afterCapabilityMismatch == withNativeTimer {
		t.Fatalf("native timer capability mismatch did not select a distinct stable hash: timer=%x no-timer=%x after=%x", withNativeTimer, withoutNativeTimer, afterCapabilityMismatch)
	}
}

type coroBootstrapV2TestFixture struct {
	ctx               *context
	mainPackage       *packages.Package
	runtimeInit       *ssa.Function
	irqRuntimeRoot    *ssa.Function
	publicRuntimeInit *ssa.Function
	mainInit          *ssa.Function
	mainMain          *ssa.Function
}

func newCoroBootstrapV2TestContext(t *testing.T) coroBootstrapV2TestFixture {
	return newCoroBootstrapV2TestContextWithPublicRuntime(t, true)
}

func newCoroBootstrapV2TestContextWithPublicRuntime(t *testing.T, includePublicRuntime bool) coroBootstrapV2TestFixture {
	t.Helper()
	fset := token.NewFileSet()
	ssaProg := ssa.NewProgram(fset, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	runtimeSSA, runtimeFiles, _, _ := createCoroBootstrapV2SSAPackage(t, ssaProg, fset, llssa.PkgRuntime, `package runtime
func aRuntimeRoot() {}
func irqRuntimeRoot() {}
func zRuntimeRoot() {}
`)
	var publicRuntimeSSA *ssa.Package
	var publicRuntimeFiles []*ast.File
	if includePublicRuntime {
		publicRuntimeSSA, publicRuntimeFiles, _, _ = createCoroBootstrapV2SSAPackage(t, ssaProg, fset, "runtime", `package runtime
func publicRuntimeBody() {}
`)
	}
	mainSSA, mainFiles, mainTypes, mainInfo := createCoroBootstrapV2SSAPackage(t, ssaProg, fset, "example.com/bootstrapv2", `package main
func aMainRoot() {}
func zMainRoot() {}
func main() {}
`)
	ssaProg.Build()

	prog := llssa.NewProgram(nil)
	t.Cleanup(prog.Dispose)
	emissionInputs := []cl.EmissionPackage{
		{SSA: runtimeSSA, Files: runtimeFiles, Identity: llssa.PkgRuntime},
		{SSA: mainSSA, Files: mainFiles, Identity: "example.com/bootstrapv2"},
	}
	if includePublicRuntime {
		emissionInputs = append(emissionInputs[:1], append([]cl.EmissionPackage{
			{SSA: publicRuntimeSSA, Files: publicRuntimeFiles, Identity: "runtime"},
		}, emissionInputs[1:]...)...)
	}
	emission, err := cl.PrepareEmissionUniverse(prog, nil, emissionInputs)
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaProg, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	mainPackage := &packages.Package{
		ID: "example.com/bootstrapv2", PkgPath: "example.com/bootstrapv2", Name: "main",
		Types: mainTypes, TypesInfo: mainInfo, Syntax: mainFiles,
	}
	aMain := &aPackage{Package: mainPackage, SSA: mainSSA}
	runtimeInit := runtimeSSA.Func("init")
	irqRuntimeRoot := runtimeSSA.Func("irqRuntimeRoot")
	var publicRuntimeInit *ssa.Function
	if publicRuntimeSSA != nil {
		publicRuntimeInit = publicRuntimeSSA.Func("init")
	}
	mainInit := mainSSA.Func("init")
	mainMain := mainSSA.Func("main")
	suspending := map[*ssa.Function]bool{
		runtimeInit:                     true,
		runtimeSSA.Func("aRuntimeRoot"): true,
		runtimeSSA.Func("zRuntimeRoot"): true,
		mainInit:                        true,
		mainSSA.Func("aMainRoot"):       true,
		mainSSA.Func("zMainRoot"):       true,
	}
	if publicRuntimeInit != nil {
		suspending[publicRuntimeInit] = true
	}
	conf := &Config{
		BuildMode: BuildModeExe,
		Goos:      "linux",
		Goarch:    "amd64", CoroProfile: CoroProfileStackless,
	}
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		// Deliberately provide roots in reverse name order. Descriptor Aux must
		// follow the canonical same-package FunctionID order, never this input
		// order or whole-program package order.
		roots := coro.Roots{
			{Function: mainSSA.Func("zMainRoot"), Demand: coro.AsyncDemand},
			{Function: mainSSA.Func("aMainRoot"), Demand: coro.AsyncDemand},
			{Function: runtimeSSA.Func("zRuntimeRoot"), Demand: coro.AsyncDemand},
			{Function: irqRuntimeRoot, Demand: coro.AsyncDemand},
			{Function: runtimeInit, Demand: coro.AsyncDemand},
			{Function: runtimeSSA.Func("aRuntimeRoot"), Demand: coro.AsyncDemand},
		}
		return input.Analyze(roots, coro.SSAConfig{
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == irqRuntimeRoot {
					return coro.SSAFunctionPolicy{Exec: coro.IRQUnsafe}, nil
				}
				if suspending[fn] {
					return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		})
	}
	ctx := &context{
		progSSA:          ssaProg,
		prog:             prog,
		patches:          make(cl.Patches),
		initial:          []*packages.Package{mainPackage},
		pkgs:             map[*packages.Package]Package{mainPackage: aMain},
		pkgByID:          map[string]Package{mainPackage.ID: aMain},
		mode:             ModeBuild,
		buildConf:        conf,
		coroEmission:     emission,
		coroSSAEmission:  ssaEmission,
		coroPlanMetadata: coro.PlanDigestMetadata{},
	}
	if err := buildCoroPlan(ctx); err != nil {
		t.Fatalf("build v2 coroutine bootstrap test plan: %v", err)
	}
	return coroBootstrapV2TestFixture{
		ctx: ctx, mainPackage: mainPackage,
		runtimeInit: runtimeInit, irqRuntimeRoot: irqRuntimeRoot, publicRuntimeInit: publicRuntimeInit,
		mainInit: mainInit, mainMain: mainMain,
	}
}

func createCoroBootstrapV2SSAPackage(
	t *testing.T, prog *ssa.Program, fset *token.FileSet, pkgPath, source string,
) (*ssa.Package, []*ast.File, *types.Package, *types.Info) {
	t.Helper()
	file, err := parser.ParseFile(fset, pkgPath+".go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{file}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:     make(map[ast.Node]*types.Scope),
	}
	typesPkg, err := (&types.Config{}).Check(pkgPath, fset, files, info)
	if err != nil {
		t.Fatal(err)
	}
	return prog.CreatePackage(typesPkg, files, info, true), files, typesPkg, info
}

func expectedCoroBootstrapV2DescriptorIndex(t *testing.T, plan *coro.SSAPlan, target *ssa.Function) uint64 {
	t.Helper()
	var ids []coro.FunctionID
	for _, root := range plan.Roots() {
		fnPlan, ok := plan.FunctionPlan(root.Function)
		if ok && root.Function.Pkg == target.Pkg && fnPlan.Emission == coro.EmitCoroutine {
			ids = append(ids, root.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok {
		t.Fatalf("descriptor target %q has no function plan", target.Name())
	}
	for index, id := range ids {
		if id == targetPlan.ID {
			got, err := coroProgramRootDescriptorIndexV2(plan, target)
			if err != nil {
				t.Fatal(err)
			}
			if got != uint64(index) {
				t.Fatalf("descriptor index for %q = %d, want FunctionID-sorted index %d in %q", target.Name(), got, index, ids)
			}
			return uint64(index)
		}
	}
	t.Fatalf("descriptor target %q ID %q is absent from sorted coroutine roots %q", target.Name(), targetPlan.ID, ids)
	return 0
}

func coroBootstrapV2LinkedPackage(pkgPath, anchor string) Package {
	return &aPackage{
		Package:          &packages.Package{ID: pkgPath, PkgPath: pkgPath},
		CoroRootAnchorV1: anchor,
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
		BuildMode: BuildModeExe, CoroProfile: CoroProfileStackless,
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
