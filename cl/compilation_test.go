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
	"golang.org/x/tools/go/ssa"
)

func TestCompilationCoroPlanObservationAndCacheRegistration(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `
package foo

func F() int { return 42 }
`)
	plan := new(coro.SSAPlan)
	observerCalls := 0
	compilation := &Compilation{
		CoroPlan: plan,
		CoroPlanObserver: func(pkg *ssa.Package, got *coro.SSAPlan) {
			observerCalls++
			if pkg != ssaPkg {
				t.Errorf("observer package = %p, want %p", pkg, ssaPkg)
			}
			if got != plan {
				t.Errorf("observer plan = %p, want %p", got, plan)
			}
		},
	}

	compile := func(cacheHit bool) string {
		t.Helper()
		prog := newLLSSAProg(t)
		defer prog.Dispose()
		pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
			Compilation: compilation,
			CacheHit:    cacheHit,
		})
		if err != nil {
			t.Fatalf("NewPackageExWithEmbedOptions(cache hit %v): %v", cacheHit, err)
		}
		return pkg.String()
	}

	sourceIR := compile(false)
	if observerCalls != 1 {
		t.Fatalf("source observer calls = %d, want 1", observerCalls)
	}
	cachedIR := compile(true)
	if observerCalls != 1 {
		t.Fatalf("cache registration observer calls = %d, want unchanged 1", observerCalls)
	}
	if cachedIR != sourceIR {
		t.Fatal("cache registration option changed frontend LLVM IR")
	}
}

func TestCompilationCoroABIIdentityValidation(t *testing.T) {
	current := func() *Compilation {
		return &Compilation{
			CoroProfile:           CoroProfileStackless,
			CoroABI:               coro.PhysicalABIV1,
			SchedulerABI:          coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
			PanicABI:              coro.PanicExplicitStatusABIV0,
			FuncRepABI:            coro.FuncRepABIV1,
			CoroFrameRetentionABI: CoroFrameRetentionParkABIV2,
		}
	}
	if err := current().validateCoroABIIdentity(false); err != nil {
		t.Fatalf("current stackless ABI identity: %v", err)
	}
	if err := (&Compilation{CoroProfile: CoroProfileStackless}).validateCoroABIIdentity(false); err != nil {
		t.Fatalf("omitted source ABI identity should use current defaults: %v", err)
	}

	for _, test := range []struct {
		name string
		edit func(*Compilation)
		want string
	}{
		{name: "physical", edit: func(c *Compilation) { c.CoroABI = "invalid" }, want: "coroutine ABI"},
		{name: "scheduler", edit: func(c *Compilation) { c.SchedulerABI = "invalid" }, want: "scheduler ABI"},
		{name: "panic", edit: func(c *Compilation) { c.PanicABI = "invalid" }, want: "panic ABI"},
		{name: "function representation", edit: func(c *Compilation) { c.FuncRepABI = "invalid" }, want: "function representation ABI"},
	} {
		t.Run(test.name, func(t *testing.T) {
			compilation := current()
			test.edit(compilation)
			if err := compilation.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ABI mismatch error = %v, want substring %q", err, test.want)
			}
		})
	}

	inactive := current()
	inactive.CoroProfile = CoroProfileNone
	if err := inactive.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "requires the stackless runtime profile") {
		t.Fatalf("inactive ABI identity error = %v", err)
	}
	if err := (&Compilation{}).validateCoroABIIdentity(false); err != nil {
		t.Fatalf("inactive empty identity: %v", err)
	}

	for _, retention := range []string{"", CoroFrameRetentionParkABIV2} {
		compilation := current()
		compilation.CoroFrameRetentionABI = retention
		if err := compilation.validateCoroABIIdentity(false); err != nil {
			t.Fatalf("frame-retention identity %q: %v", retention, err)
		}
	}
	unknownRetention := current()
	unknownRetention.CoroFrameRetentionABI = "invalid"
	if err := unknownRetention.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "unknown coroutine frame-retention ABI") {
		t.Fatalf("unknown frame-retention error = %v", err)
	}

	worker := current()
	worker.CoroTargetCapabilities = CoroNativeTargetCapabilities()
	worker.SchedulerABI = coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	if err := worker.validateCoroABIIdentity(false); err != nil {
		t.Fatalf("native worker ABI identity: %v", err)
	}
	worker.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	if err := worker.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "scheduler ABI") {
		t.Fatalf("native worker scheduler mismatch = %v", err)
	}

	if err := (&Compilation{CoroProfile: CoroProfileStackless}).validateCoroABIIdentity(true); err == nil || !strings.Contains(err.Error(), "coroutine ABI") {
		t.Fatalf("missing cache ABI identity error = %v", err)
	}
	if err := current().preflightCoroPlan(); err == nil || !strings.Contains(err.Error(), "requires a compilation CoroPlan") {
		t.Fatalf("active source preflight error = %v", err)
	}
}

func TestCoroEntryResolutionCacheRegistrationWithDigest(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `
package foo

func F() int { return 42 }
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
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: ssaPkg.Func("F"), Demand: coro.SyncDemand},
	}, coro.SSAConfig{EmissionUniverse: ssaUniverse, FunctionIDs: functionIDs})
	if err != nil {
		t.Fatal(err)
	}
	observerCalls := 0
	compilation := &Compilation{
		CoroPlan:         plan,
		CoroPlanObserver: func(*ssa.Package, *coro.SSAPlan) { observerCalls++ },

		CoroPlanDigest:   strings.Repeat("0", 64),
		CoroABI:          coro.PhysicalABIV1,
		SchedulerABI:     coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI:         coro.PanicExplicitStatusABIV0,
		FuncRepABI:       coro.FuncRepABIV1,
		EmissionUniverse: universe, CoroProfile: CoroProfileStackless,
	}
	installCoroLoweringFactsForTest(t, compilation)
	mismatchedFacts := &Compilation{
		CoroPlanDigest:          compilation.CoroPlanDigest,
		CoroLoweringFacts:       compilation.CoroLoweringFacts,
		CoroLoweringFactsDigest: strings.Repeat("f", 64),
	}
	if err := mismatchedFacts.validateCoroCacheIdentity(); err == nil || !strings.Contains(err.Error(), "lowering-facts digest mismatch") {
		t.Fatalf("mismatched lowering-facts cache identity error = %v", err)
	}
	pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
		Compilation: compilation,
		CacheHit:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pkg == nil {
		t.Fatal("cache registration returned a nil package")
	}
	if observerCalls != 0 {
		t.Fatalf("cache registration observer calls = %d, want 0", observerCalls)
	}
}

func installCoroLoweringFactsForTest(t *testing.T, compilation *Compilation) {
	t.Helper()
	if compilation == nil || compilation.CoroPlan == nil || compilation.EmissionUniverse == nil {
		t.Fatal("test lowering facts require a complete compilation plan and emission universe")
	}
	report, err := compilation.EmissionUniverse.BuildCoroLoweringFactsReport(compilation.CoroPlan)
	if err != nil {
		t.Fatalf("build test lowering facts: %v", err)
	}
	compilation.CoroLoweringFacts = report.Facts
	compilation.CoroLoweringFactsDigest = report.Digest
}

func TestCoroEntryResolutionPlainPrimaryPreservesIR(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `
package foo

func F(value int) int { return value + 1 }
`)
	compile := func(active bool) string {
		t.Helper()
		prog := newLLSSAProg(t)
		defer prog.Dispose()
		var compilation *Compilation
		if active {
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
				{Function: ssaPkg.Func("F"), Demand: coro.SyncDemand},
				{Function: ssaPkg.Func("init"), Demand: coro.SyncDemand},
			}, coro.SSAConfig{
				EmissionUniverse: ssaUniverse,
				FunctionIDs:      universe.FunctionIDConfig(),
			})
			if err != nil {
				t.Fatal(err)
			}
			compilation = &Compilation{
				CoroPlan:         plan,
				EmissionUniverse: universe, CoroProfile: CoroProfileStackless,
			}
		}
		pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
			Compilation: compilation,
		})
		if err != nil {
			t.Fatal(err)
		}
		return pkg.String()
	}

	baseline := compile(false)
	resolved := compile(true)
	if resolved != baseline {
		t.Fatal("plain-primary entry resolution changed emitted LLVM IR")
	}
}

func TestReportOnlyCoroPlanDoesNotSelectSafeArrayEmission(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `
package foo

var values = [...]int{1, 2, 3, 4}
func Sum() int {
	total := 0
	for index := range values { total += values[index] }
	return total
}
`)
	compile := func(reportOnly bool) string {
		t.Helper()
		prog := newLLSSAProg(t)
		defer prog.Dispose()
		var compilation *Compilation
		if reportOnly {
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			// An intentionally empty report-only plan must not participate in
			// physical lowering. In particular, recomputing a safe site outside
			// this plan cannot trigger the active-plan consistency assertion.
			compilation = &Compilation{CoroPlan: new(coro.SSAPlan), EmissionUniverse: universe}
		}
		pkg, _, err := NewPackageExWithEmbedOptions(
			prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
			PackageOptions{Compilation: compilation},
		)
		if err != nil {
			t.Fatal(err)
		}
		return pkg.String()
	}

	baseline := compile(false)
	reportOnly := compile(true)
	if reportOnly != baseline {
		t.Fatal("report-only CoroPlan changed fixed-array LLVM emission")
	}
}
