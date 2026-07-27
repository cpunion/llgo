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
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCompilationCoroABIIdentityValidation(t *testing.T) {
	current := func() *Compilation {
		return &Compilation{
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
	if err := (&Compilation{}).validateCoroABIIdentity(false); err != nil {
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

	if err := (&Compilation{}).validateCoroABIIdentity(true); err == nil || !strings.Contains(err.Error(), "coroutine ABI") {
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
		EmissionUniverse: universe}
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

func TestCoroSourcePackageEmbedsLibraryEffectSummary(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `
package foo

func F(value int) int { return value + 1 }
`)
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
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: ssaPkg.Func("F"), Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	endianness := ""
	switch prog.TargetData().ByteOrder() {
	case llvm.LittleEndian:
		endianness = "little"
	case llvm.BigEndian:
		endianness = "big"
	default:
		t.Fatal("unknown target byte order")
	}
	metadata := coro.PlanDigestMetadata{
		CoroABI:        coro.PhysicalABIV1,
		SchedulerABI:   coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI:       coro.PanicExplicitStatusABIV0,
		FuncRepABI:     coro.FuncRepABIV1,
		TargetTriple:   prog.TargetSpec().Triple,
		TargetCPU:      prog.TargetSpec().CPU,
		TargetFeatures: prog.TargetSpec().Features,
		TargetABI:      prog.TargetSpec().TargetABI,
		PointerBits:    prog.PointerSize() * 8,
		Endianness:     endianness,
		DataLayout:     prog.DataLayout(),
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		CoroPlanDigest:   strings.Repeat("0", 64),
		CoroPlanMetadata: metadata,
		CoroABI:          metadata.CoroABI,
		SchedulerABI:     metadata.SchedulerABI,
		PanicABI:         metadata.PanicABI,
		FuncRepABI:       metadata.FuncRepABI,
		EmissionUniverse: universe,
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	ir := pkg.String()
	hasSummarySection := strings.Contains(ir, coro.LibraryEffectSummarySection) ||
		strings.Contains(ir, "__LLVM,__llgo_coro")
	if !strings.Contains(ir, coroLibrarySummarySymbolPrefix) ||
		!hasSummarySection || !strings.Contains(ir, "@llvm.compiler.used") {
		t.Fatalf("source package omitted compiler-retained library effect summary:\n%s", ir)
	}
	if strings.Contains(ir, "@llvm.used") {
		t.Fatalf("library effect summary unexpectedly requires final-link retention:\n%s", ir)
	}
	producerRecords, err := CoroLibraryEffectSummaryRecords(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(producerRecords) == 0 {
		t.Fatal("source package did not expose byte-exact library effect records to the archiver")
	}
	object, err := prog.TargetMachine().EmitToMemoryBuffer(pkg.Module(), llvm.ObjectFile)
	if err != nil {
		t.Fatalf("emit package object with library effect summary: %v\n%s", err, ir)
	}
	defer object.Dispose()
	var sectionData []byte
	switch triple := strings.ToLower(prog.TargetSpec().Triple); {
	case strings.Contains(triple, "darwin") || strings.Contains(triple, "apple"):
		file, openErr := macho.NewFile(bytes.NewReader(object.Bytes()))
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer file.Close()
		section := file.Section("__llgo_coro")
		if section == nil {
			t.Fatal("Mach-O object omitted __llgo_coro")
		}
		sectionData, err = section.Data()
	case strings.Contains(triple, "windows"):
		file, openErr := pe.NewFile(bytes.NewReader(object.Bytes()))
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer file.Close()
		section := file.Section(coro.LibraryEffectSummarySection)
		if section == nil {
			t.Fatalf("COFF object omitted %s", coro.LibraryEffectSummarySection)
		}
		sectionData, err = section.Data()
	default:
		file, openErr := elf.NewFile(bytes.NewReader(object.Bytes()))
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer file.Close()
		section := file.Section(coro.LibraryEffectSummarySection)
		if section == nil {
			t.Fatalf("ELF object omitted %s", coro.LibraryEffectSummarySection)
		}
		sectionData, err = section.Data()
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sectionData, producerRecords) {
		t.Fatal("object section and archiver-facing library effect records disagree")
	}
	summaries, err := coro.ParseLibraryEffectSummaryRecords(sectionData)
	if err != nil {
		t.Fatalf("parse object library effect section: %v", err)
	}
	if len(summaries) != 1 || len(summaries[0].Functions) != 1 ||
		summaries[0].Functions[0].PrimarySymbol != "foo.F" {
		t.Fatalf("object library effect summaries = %+v", summaries)
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
				EmissionUniverse: universe}
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
