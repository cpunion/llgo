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

package build

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
)

func TestReadImportCfgPackageFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "importcfg")
	if err := os.WriteFile(path, []byte(`
# generated
importmap old/path=new/path
packagefile example/a=/tmp/a.a
packagefile example/b = /tmp/b.a
packagefile	example/c=/tmp/c.a
`), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := readImportCfgPackageFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0] != "/tmp/a.a" || files[1] != "/tmp/b.a" ||
		files[2] != "/tmp/c.a" {
		t.Fatalf("package files = %q", files)
	}

	malformed := filepath.Join(t.TempDir(), "malformed.importcfg")
	if err := os.WriteFile(malformed, []byte("packagefile missing-equals\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readImportCfgPackageFiles(malformed); err == nil ||
		!strings.Contains(err.Error(), "malformed packagefile") {
		t.Fatalf("malformed importcfg error = %v", err)
	}
	bare := filepath.Join(t.TempDir(), "bare.importcfg")
	if err := os.WriteFile(bare, []byte("packagefile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readImportCfgPackageFiles(bare); err == nil ||
		!strings.Contains(err.Error(), "malformed packagefile") {
		t.Fatalf("bare packagefile error = %v", err)
	}
}

func TestLoadCoroLibraryEffectIndexFromImportCfg(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		buildConf: &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH},
		prog:      prog,
	}
	metadata, err := buildCoroLibraryEffectMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const functionID = coro.FunctionID("llgo.function.v0:example/library.F")
	summary := coro.LibraryEffectSummary{
		Schema:   coro.LibraryEffectSummarySchema,
		Package:  "example/library",
		Metadata: metadata,
		Functions: []coro.LibraryEffectFunction{{
			ID:            functionID,
			ABIHash:       strings.Repeat("a", 64),
			Effect:        coro.NoSuspend,
			FuncRep:       coro.DirectPlain,
			Primary:       coro.PrimaryPlain,
			PrimarySymbol: "example/library.F",
		}},
		ExportBindings: []coro.LibraryEffectExportBinding{{
			Symbol:               "library_F",
			ABIHash:              strings.Repeat("b", 64),
			Function:             functionID,
			ManagedPrimary:       coro.PrimaryPlain,
			ManagedPrimarySymbol: "example/library.F",
		}},
	}
	record, err := summary.MarshalRecord()
	if err != nil {
		t.Fatal(err)
	}
	object := coroArchiveTestObject(t, record)
	archive := writeCoroArchiveTestFile(t,
		coroArchiveTestMember{name: "payload.o", data: []byte("payload")},
		coroArchiveTestMember{name: coro.LibraryEffectArchiveMember, data: object},
	)
	importCfg := filepath.Join(t.TempDir(), "importcfg")
	if err := os.WriteFile(importCfg, []byte(
		"packagefile example/library="+archive+"\n"+
			"packagefile example/library/alias="+archive+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx.buildConf.ImportCfg = importCfg
	index, consumer, err := loadCoroLibraryEffectIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if consumer != metadata {
		t.Fatalf("consumer metadata = %+v, want %+v", consumer, metadata)
	}
	fact, found := index.Lookup(functionID)
	if !found || fact.PrimarySymbol != "example/library.F" {
		t.Fatalf("imported fact = %+v, found=%t", fact, found)
	}
	export, found := index.LookupExport("library_F")
	if !found || export.Function != functionID {
		t.Fatalf("imported export = %+v, found=%t", export, found)
	}

	plain := filepath.Join(t.TempDir(), "export-data")
	if err := os.WriteFile(plain, []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	plainCfg := filepath.Join(t.TempDir(), "plain.importcfg")
	if err := os.WriteFile(plainCfg, []byte("packagefile example/plain="+plain+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx.buildConf.ImportCfg = plainCfg
	if index, _, err := loadCoroLibraryEffectIndex(ctx); err != nil || index != nil {
		t.Fatalf("non-archive import metadata = %v, %v; want conservative absence", index, err)
	}

	corrupt := append([]byte(nil), record...)
	corrupt[len(corrupt)-1] ^= 1
	corruptArchive := writeCoroArchiveTestFile(t,
		coroArchiveTestMember{
			name: coro.LibraryEffectArchiveMember,
			data: coroArchiveTestObject(t, corrupt),
		},
	)
	corruptCfg := filepath.Join(t.TempDir(), "corrupt.importcfg")
	if err := os.WriteFile(corruptCfg, []byte("packagefile example/corrupt="+corruptArchive+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx.buildConf.ImportCfg = corruptCfg
	if _, _, err := loadCoroLibraryEffectIndex(ctx); err == nil {
		t.Fatal("corrupt library effect member was accepted")
	}
}

func TestImportCfgLibraryEffectAutomaticallyColorsExactBodylessDeclaration(t *testing.T) {
	const packagePath = "example/library"
	ssaPkg, files := buildCoroPlanTestPackage(t, packagePath, `package library
func Imported(value uint32) uint32
func Caller(value uint32) uint32 { return Imported(value) + 1 }
`, nil)
	prog := llssa.NewProgram(nil)
	t.Cleanup(prog.Dispose)
	cl.ParsePkgSyntax(prog, ssaPkg.Pkg, files)
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA:      ssaPkg,
		Files:    files,
		Identity: packagePath,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	ctx := &context{
		buildConf:       &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH},
		prog:            prog,
		coroEmission:    emission,
		coroSSAEmission: ssaEmission,
	}
	metadata, err := buildCoroLibraryEffectMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	imported := ssaPkg.Func("Imported")
	caller := ssaPkg.Func("Caller")
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = metadata.CoroABI
	functionIDs.SchedulerABI = metadata.SchedulerABI
	functionIDs.ArchiveReady = true
	importedID, err := coro.StableFunctionID(imported, functionIDs)
	if err != nil {
		t.Fatal(err)
	}
	abiHash, err := emission.CoroLibraryEffects().FunctionABIHash(imported, metadata)
	if err != nil {
		t.Fatal(err)
	}
	baseSymbol, err := emission.CoroLibraryEffects().FunctionBaseSymbol(imported)
	if err != nil {
		t.Fatal(err)
	}
	summary := coro.LibraryEffectSummary{
		Schema:   coro.LibraryEffectSummarySchema,
		Package:  packagePath,
		Metadata: metadata,
		Functions: []coro.LibraryEffectFunction{{
			ID:            importedID,
			ABIHash:       abiHash,
			Effect:        coro.MayPark,
			Exec:          coro.MayUnwind,
			FuncRep:       coro.DirectCoro,
			Primary:       coro.PrimaryCoroutine,
			PrimarySymbol: baseSymbol + "$coro",
		}},
	}
	record, err := summary.MarshalRecord()
	if err != nil {
		t.Fatal(err)
	}
	archive := writeCoroArchiveTestFile(t, coroArchiveTestMember{
		name: coro.LibraryEffectArchiveMember,
		data: coroArchiveTestObject(t, record),
	})
	importCfg := filepath.Join(t.TempDir(), "importcfg")
	if err := os.WriteFile(
		importCfg, []byte("packagefile "+packagePath+"="+archive+"\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	ctx.buildConf.ImportCfg = importCfg
	index, consumer, err := loadCoroLibraryEffectIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := prepareCoroImportedLibraryEffects(ctx, index, consumer)
	if err != nil {
		t.Fatal(err)
	}
	fact, found := effects[imported]
	if !found || fact.ID != importedID || len(effects) != 1 {
		t.Fatalf("prepared imported effects = %+v", effects)
	}
	input := CoroPlanInput{
		Program:                ssaPkg.Prog,
		EmissionUniverse:       ssaEmission,
		resolveFunction:        emission.Resolve,
		functionBackground:     emission.FunctionBackground,
		importedLibraryEffects: effects,
	}
	plan, err := input.Analyze(
		coro.Roots{{Function: caller, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	importedPlan := functionPlanForBuildTest(t, plan, imported)
	if importedPlan.External != coro.ExternalKnown ||
		importedPlan.Emission != coro.EmitExternal ||
		importedPlan.FuncRep != coro.DirectCoro ||
		importedPlan.Effect != coro.MayPark ||
		!plan.IgnoresBody(imported) {
		t.Fatalf("imported library plan = %+v", importedPlan)
	}
	callerPlan := functionPlanForBuildTest(t, plan, caller)
	if callerPlan.Emission != coro.EmitCoroutine ||
		!callerPlan.Effect.Contains(coro.MayPark|coro.AwaitStructured) ||
		!callerPlan.Exec.Contains(coro.MayUnwind) {
		t.Fatalf("archive metadata did not automatically color caller: %+v", callerPlan)
	}
}
