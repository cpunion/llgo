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
	newPhysical := func() *Compilation {
		return &Compilation{
			EnableCoroEntryResolution: true,
			EnableCoroPhysicalABI:     true,
			CoroABI:                   coro.PhysicalABIV0,
			SchedulerABI:              coro.SchedulerNoneABIV0,
			PanicABI:                  coro.PanicLegacyABIV0,
			FuncRepABI:                coro.FuncRepABIV0,
		}
	}
	physical := newPhysical()
	if err := physical.validateCoroABIIdentity(false); err != nil {
		t.Fatalf("complete source ABI identity: %v", err)
	}
	if err := (&Compilation{EnableCoroEntryResolution: true, EnableCoroPhysicalABI: true}).validateCoroABIIdentity(false); err != nil {
		t.Fatalf("omitted source ABI identity should use current defaults: %v", err)
	}
	newPlainDispatch := func() *Compilation {
		return &Compilation{
			EnableCoroEntryResolution: true,
			EnableCoroPlainDispatch:   true,
			CoroABI:                   coro.EntryResolutionABIV0,
			SchedulerABI:              coro.SchedulerNoneABIV0,
			PanicABI:                  coro.PanicLegacyABIV0,
			FuncRepABI:                coro.FuncRepABIV1,
		}
	}
	plainDispatch := newPlainDispatch()
	if err := plainDispatch.validateCoroABIIdentity(false); err != nil {
		t.Fatalf("complete plain-dispatch ABI identity: %v", err)
	}
	wrongPlainDispatch := newPlainDispatch()
	wrongPlainDispatch.FuncRepABI = coro.FuncRepABIV0
	if err := wrongPlainDispatch.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "function representation ABI") {
		t.Fatalf("plain-dispatch function representation mismatch = %v", err)
	}
	withoutEntry := newPlainDispatch()
	withoutEntry.EnableCoroEntryResolution = false
	if err := withoutEntry.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "requires coroutine entry resolution") {
		t.Fatalf("plain-dispatch dependency error = %v", err)
	}
	if err := withoutEntry.preflightCoroPlan(); err == nil || !strings.Contains(err.Error(), "requires coroutine entry resolution") {
		t.Fatalf("plain-dispatch preflight dependency error = %v", err)
	}
	newExplicitStatus := func() *Compilation {
		compilation := newPhysical()
		compilation.EnableCoroChildAwait = true
		compilation.EnableCoroExplicitStatusPanicABI = true
		compilation.CoroABI = coro.PhysicalABIV1
		compilation.SchedulerABI = coro.SchedulerChildAwaitABIV0
		compilation.PanicABI = coro.PanicExplicitStatusABIV0
		return compilation
	}
	explicitStatus := newExplicitStatus()
	if err := explicitStatus.validateCoroABIIdentity(false); err != nil {
		t.Fatalf("complete explicit-status panic ABI identity: %v", err)
	}
	legacyIdentity := newExplicitStatus()
	legacyIdentity.PanicABI = coro.PanicLegacyABIV0
	if err := legacyIdentity.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "panic ABI") {
		t.Fatalf("explicit-status panic ABI mismatch = %v", err)
	}
	withoutExplicitStatusEntry := newExplicitStatus()
	withoutExplicitStatusEntry.EnableCoroEntryResolution = false
	if err := withoutExplicitStatusEntry.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "requires coroutine entry resolution") {
		t.Fatalf("explicit-status panic ABI dependency error = %v", err)
	}
	if err := withoutExplicitStatusEntry.preflightCoroPlan(); err == nil || !strings.Contains(err.Error(), "requires coroutine entry resolution") {
		t.Fatalf("explicit-status panic ABI preflight dependency error = %v", err)
	}
	if err := explicitStatus.preflightCoroPlan(); err == nil || !strings.Contains(err.Error(), "requires a compilation CoroPlan") {
		t.Fatalf("explicit-status panic ABI active preflight error = %v", err)
	}
	newChildAwait := func() *Compilation {
		return &Compilation{
			EnableCoroEntryResolution: true,
			EnableCoroPhysicalABI:     true,
			EnableCoroChildAwait:      true,
			CoroABI:                   coro.PhysicalABIV1,
			SchedulerABI:              coro.SchedulerChildAwaitABIV0,
			PanicABI:                  coro.PanicLegacyABIV0,
			FuncRepABI:                coro.FuncRepABIV0,
		}
	}
	childAwait := newChildAwait()
	if err := childAwait.validateCoroABIIdentity(false); err != nil {
		t.Fatalf("complete child-await ABI identity: %v", err)
	}
	programBootstrap := newChildAwait()
	programBootstrap.EnableCoroProgramBootstrapRun = true
	programBootstrap.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	if err := programBootstrap.validateCoroABIIdentity(false); err != nil {
		t.Fatalf("complete program-bootstrap ABI identity: %v", err)
	}
	frameRetention := newChildAwait()
	frameRetention.EnableCoroProgramBootstrapRun = true
	frameRetention.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	frameRetention.CoroFrameRetentionABI = CoroFrameRetentionTimerABIV1
	if err := frameRetention.validateCoroABIIdentity(false); err != nil {
		t.Fatalf("complete frame-retention ABI identity: %v", err)
	}
	withoutFrameBootstrap := *frameRetention
	withoutFrameBootstrap.EnableCoroProgramBootstrapRun = false
	if err := withoutFrameBootstrap.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "requires runnable PhysicalABIV1 program-bootstrap lowering") {
		t.Fatalf("frame-retention bootstrap dependency error = %v", err)
	}
	unknownFrameRetention := *frameRetention
	unknownFrameRetention.CoroFrameRetentionABI += ".unknown"
	if err := unknownFrameRetention.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "unknown coroutine frame-retention ABI") {
		t.Fatalf("unknown frame-retention ABI error = %v", err)
	}
	closedStaticSpawn := newChildAwait()
	closedStaticSpawn.EnableCoroProgramBootstrapRun = true
	closedStaticSpawn.EnableCoroClosedStaticSpawn = true
	closedStaticSpawn.SchedulerABI = coro.SchedulerProgramBootstrapClosedStaticSpawnABIV0
	if err := closedStaticSpawn.validateCoroABIIdentity(false); err != nil {
		t.Fatalf("complete closed-static-spawn ABI identity: %v", err)
	}
	closedStaticSpawn.EnableCoroProgramBootstrapRun = false
	if err := closedStaticSpawn.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "runnable program-bootstrap v2") {
		t.Fatalf("closed-static-spawn bootstrap dependency error = %v", err)
	}
	programBootstrap.EnableCoroChildAwait = false
	if err := programBootstrap.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "requires child-await") {
		t.Fatalf("program-bootstrap dependency error = %v", err)
	}
	wrongChildAwait := newChildAwait()
	wrongChildAwait.CoroABI = coro.PhysicalABIV0
	if err := wrongChildAwait.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "coroutine ABI") {
		t.Fatalf("child-await physical ABI mismatch = %v", err)
	}
	wrongChildAwait = newChildAwait()
	wrongChildAwait.SchedulerABI = coro.SchedulerNoneABIV0
	if err := wrongChildAwait.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "scheduler ABI") {
		t.Fatalf("child-await scheduler ABI mismatch = %v", err)
	}
	partial := newPhysical()
	partial.SchedulerABI = ""
	if err := partial.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "scheduler ABI") {
		t.Fatalf("partial source ABI identity error = %v", err)
	}
	mismatch := newPhysical()
	mismatch.SchedulerABI = "llgo.coro.scheduler.other.v0"
	if err := mismatch.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "scheduler ABI") {
		t.Fatalf("mismatched source ABI identity error = %v", err)
	}
	if err := mismatch.preflightCoroPlan(); err == nil || !strings.Contains(err.Error(), "scheduler ABI") {
		t.Fatalf("active source preflight ABI identity error = %v", err)
	}
	if err := (&Compilation{EnableCoroEntryResolution: true}).validateCoroABIIdentity(true); err == nil || !strings.Contains(err.Error(), "coroutine ABI") {
		t.Fatalf("missing cache ABI identity error = %v", err)
	}
}

func TestCoroEntryResolutionCacheRegistrationWithDigest(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `
package foo

func F() int { return 42 }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.EntryResolutionABIV0
	functionIDs.SchedulerABI = coro.SchedulerNoneABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: ssaPkg.Func("F"), Demand: coro.SyncDemand},
	}, coro.SSAConfig{EmissionUniverse: ssaUniverse, FunctionIDs: functionIDs})
	if err != nil {
		t.Fatal(err)
	}
	observerCalls := 0
	compilation := &Compilation{
		CoroPlan:                  plan,
		CoroPlanObserver:          func(*ssa.Package, *coro.SSAPlan) { observerCalls++ },
		EnableCoroEntryResolution: true,
		CoroPlanDigest:            strings.Repeat("0", 64),
		CoroABI:                   coro.EntryResolutionABIV0,
		SchedulerABI:              coro.SchedulerNoneABIV0,
		PanicABI:                  coro.PanicLegacyABIV0,
		FuncRepABI:                coro.FuncRepABIV0,
		EmissionUniverse:          universe,
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
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
				CoroPlan:                  plan,
				EmissionUniverse:          universe,
				EnableCoroEntryResolution: true,
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
