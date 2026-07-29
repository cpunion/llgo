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
	"golang.org/x/tools/go/ssa"
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
	cl.ParsePkgSyntax(prog, ssaPkg.Prog.Fset, ssaPkg.Pkg, files)
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

type coroLibraryForeignImportFixture struct {
	pkg         *ssa.Package
	emission    *cl.EmissionUniverse
	ssaEmission *coro.SSAEmissionUniverse
	ctx         *context
	metadata    coro.LibraryEffectMetadata
	functionIDs coro.FunctionIDConfig
}

func newCoroLibraryForeignImportFixture(
	t *testing.T,
	source string,
) coroLibraryForeignImportFixture {
	t.Helper()
	const packagePath = "example/foreignlibrary"
	ssaPkg, files := buildCoroPlanTestPackage(t, packagePath, source, nil)
	prog := llssa.NewProgram(nil)
	t.Cleanup(prog.Dispose)
	cl.ParsePkgSyntax(prog, ssaPkg.Prog.Fset, ssaPkg.Pkg, files)
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
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = metadata.CoroABI
	functionIDs.SchedulerABI = metadata.SchedulerABI
	functionIDs.ArchiveReady = true
	return coroLibraryForeignImportFixture{
		pkg:         ssaPkg,
		emission:    emission,
		ssaEmission: ssaEmission,
		ctx:         ctx,
		metadata:    metadata,
		functionIDs: functionIDs,
	}
}

func (fixture coroLibraryForeignImportFixture) analyzeForeign(
	t *testing.T,
	imported map[*ssa.Function]coro.LibraryEffectForeignCallable,
) *coro.SSAPlan {
	t.Helper()
	plan, err := (CoroPlanInput{
		Program:            fixture.pkg.Prog,
		EmissionUniverse:   fixture.ssaEmission,
		resolveFunction:    fixture.emission.Resolve,
		functionBackground: fixture.emission.FunctionBackground,
		callableIdentity:   fixture.emission.CoroCallableIdentityCertificate,
		callableContract:   fixture.emission.CoroCallableContractCertificate,
		libraryForeign:     imported,
	}).Analyze(
		coro.Roots{{Function: fixture.pkg.Func("Caller"), Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			FunctionIDs:          fixture.functionIDs,
			MaxPlainInstructions: -1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestImportCfgLibraryForeignCallableReplacesOnlyConsumerDefault(t *testing.T) {
	const producerSource = `package foreignlibrary
//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=caller-thread reentry=none memory=borrow-until-complete
//go:linkname Foreign C.foreign_library_probe
func Foreign(value uintptr) uintptr

func Caller(value uintptr) uintptr { return Foreign(value) + 1 }
`
	const consumerSource = `package foreignlibrary
// producer archive owns the exact callable contract
//go:linkname Foreign C.foreign_library_probe
func Foreign(value uintptr) uintptr

func Caller(value uintptr) uintptr { return Foreign(value) + 1 }
`
	producer := newCoroLibraryForeignImportFixture(t, producerSource)
	producerForeign := producer.pkg.Func("Foreign")
	functionID, err := coro.StableFunctionID(producerForeign, producer.functionIDs)
	if err != nil {
		t.Fatal(err)
	}
	identity, identityOK, err := producer.emission.CoroCallableIdentityCertificate(producerForeign)
	if err != nil || !identityOK {
		t.Fatalf("producer identity = %+v, %t, %v", identity, identityOK, err)
	}
	contract, contractOK, err := producer.emission.CoroCallableContractCertificate(producerForeign)
	if err != nil || !contractOK {
		t.Fatalf("producer contract = %+v, %t, %v", contract, contractOK, err)
	}
	if defaulted, err := producer.emission.CoroLibraryEffects().
		CallableContractDefault(producerForeign); err != nil || defaulted {
		t.Fatalf("producer contract defaulted = %t, %v", defaulted, err)
	}
	fact := coro.LibraryEffectForeignCallable{
		Function:    functionID,
		Identity:    identity,
		Contract:    contract,
		HasContract: true,
	}
	summary := coro.LibraryEffectSummary{
		Schema:           coro.LibraryEffectSummarySchema,
		Package:          "example/foreignlibrary",
		Metadata:         producer.metadata,
		ForeignCallables: []coro.LibraryEffectForeignCallable{fact},
	}
	record, err := summary.MarshalRecord()
	if err != nil {
		t.Fatal(err)
	}
	archive := writeCoroArchiveTestFile(t, coroArchiveTestMember{
		name: coro.LibraryEffectArchiveMember,
		data: coroArchiveTestObject(t, record),
	})

	consumer := newCoroLibraryForeignImportFixture(t, consumerSource)
	consumerForeign := consumer.pkg.Func("Foreign")
	localContract, localOK, err := consumer.emission.CoroCallableContractCertificate(consumerForeign)
	if err != nil || !localOK {
		t.Fatalf("consumer default contract = %+v, %t, %v", localContract, localOK, err)
	}
	if defaulted, err := consumer.emission.CoroLibraryEffects().
		CallableContractDefault(consumerForeign); err != nil || !defaulted {
		t.Fatalf("consumer contract defaulted = %t, %v", defaulted, err)
	}
	if localContract == contract {
		t.Fatal("consumer conservative default unexpectedly equals producer contract")
	}
	importCfg := filepath.Join(t.TempDir(), "importcfg")
	if err := os.WriteFile(
		importCfg,
		[]byte("packagefile example/foreignlibrary="+archive+"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	consumer.ctx.buildConf.ImportCfg = importCfg
	index, metadata, err := loadCoroLibraryEffectIndex(consumer.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if indexed, ok := index.LookupForeignFunction(functionID); !ok || indexed != fact {
		t.Fatalf("indexed foreign callable = %+v, %t", indexed, ok)
	}
	imported, err := prepareCoroImportedLibraryForeignCallables(
		consumer.ctx, index, metadata,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := imported[consumerForeign]; !ok || got != fact || len(imported) != 1 {
		t.Fatalf("prepared foreign callables = %+v", imported)
	}

	plan := consumer.analyzeForeign(t, imported)
	plannedIdentity, identityPlanned := plan.CallableIdentityCertificate(consumerForeign)
	plannedContract, contractPlanned := plan.CallableContractCertificate(consumerForeign)
	if !identityPlanned || plannedIdentity != fact.Identity ||
		!contractPlanned || plannedContract != fact.Contract {
		t.Fatalf(
			"planned producer facts = identity:%+v/%t contract:%+v/%t",
			plannedIdentity, identityPlanned, plannedContract, contractPlanned,
		)
	}
	foreignPlan := functionPlanForBuildTest(t, plan, consumerForeign)
	expectedExec := coro.BlockForeign | coro.IRQUnsafe |
		coro.CallableContractExecConstraints(contract.Contract)
	if foreignPlan.External != coro.ExternalUnknownForeign ||
		foreignPlan.Exec != expectedExec ||
		!plan.IgnoresBody(consumerForeign) {
		t.Fatalf("imported foreign plan = %+v, ignored=%t", foreignPlan, plan.IgnoresBody(consumerForeign))
	}
	callerPlan := functionPlanForBuildTest(t, plan, consumer.pkg.Func("Caller"))
	if callerPlan.Emission != coro.EmitCoroutine ||
		!callerPlan.Effect.Contains(coro.WaitForeign) {
		t.Fatalf("producer callable contract did not color consumer caller: %+v", callerPlan)
	}

	t.Run("identity-only grants no operation", func(t *testing.T) {
		identityOnly := fact
		identityOnly.Contract = coro.CallableContractCertificate{}
		identityOnly.HasContract = false
		identitySummary := summary
		identitySummary.ForeignCallables = []coro.LibraryEffectForeignCallable{identityOnly}
		identityIndex, err := coro.NewLibraryEffectIndex(
			[]coro.LibraryEffectSummary{identitySummary}, consumer.metadata,
		)
		if err != nil {
			t.Fatal(err)
		}
		imported, err := prepareCoroImportedLibraryForeignCallables(
			consumer.ctx, identityIndex, consumer.metadata,
		)
		if err != nil {
			t.Fatal(err)
		}
		plan := consumer.analyzeForeign(t, imported)
		if certificate, ok := plan.CallableContractCertificate(consumerForeign); ok ||
			!certificate.IsZero() {
			t.Fatalf("identity-only record gained contract %+v, %t", certificate, ok)
		}
		foreignPlan := functionPlanForBuildTest(t, plan, consumerForeign)
		if foreignPlan.External != coro.ExternalUnknownForeign ||
			foreignPlan.Exec != coro.BlockForeign|coro.IRQUnsafe ||
			!plan.IgnoresBody(consumerForeign) {
			t.Fatalf("identity-only foreign plan = %+v", foreignPlan)
		}
	})

	t.Run("explicit local conflict fails closed", func(t *testing.T) {
		const conflictingSource = `package foreignlibrary
//llgo:coro contract foreign.v1 scope=declaration progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return
//go:linkname Foreign C.foreign_library_probe
func Foreign(value uintptr) uintptr

func Caller(value uintptr) uintptr { return Foreign(value) + 1 }
`
		conflicting := newCoroLibraryForeignImportFixture(t, conflictingSource)
		index, err := coro.NewLibraryEffectIndex(
			[]coro.LibraryEffectSummary{summary}, conflicting.metadata,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = prepareCoroImportedLibraryForeignCallables(
			conflicting.ctx, index, conflicting.metadata,
		)
		if err == nil || !strings.Contains(err.Error(), "conflicts with the explicit local callable contract") {
			t.Fatalf("explicit local conflict error = %v", err)
		}
	})

	t.Run("legacy local conflict fails closed", func(t *testing.T) {
		const legacySource = `package foreignlibrary
//llgo:coro worker
//go:linkname Foreign C.foreign_library_probe
func Foreign(value uintptr) uintptr

func Caller(value uintptr) uintptr { return Foreign(value) + 1 }
`
		legacy := newCoroLibraryForeignImportFixture(t, legacySource)
		index, err := coro.NewLibraryEffectIndex(
			[]coro.LibraryEffectSummary{summary}, legacy.metadata,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = prepareCoroImportedLibraryForeignCallables(
			legacy.ctx, index, legacy.metadata,
		)
		if err == nil || !strings.Contains(err.Error(), "conflicts with explicit local legacy metadata") {
			t.Fatalf("explicit local legacy conflict error = %v", err)
		}
	})

	t.Run("declaration shape mismatch fails validation", func(t *testing.T) {
		const mismatchedSource = `package foreignlibrary
// producer archive owns the exact callable contract
//go:linkname Foreign C.foreign_library_probe
func Foreign(value uint32) uint32

func Caller(value uint32) uint32 { return Foreign(value) + 1 }
`
		mismatched := newCoroLibraryForeignImportFixture(t, mismatchedSource)
		err := mismatched.emission.CoroLibraryEffects().ValidateForeignCallable(
			mismatched.pkg.Func("Foreign"), mismatched.metadata, fact,
		)
		if err == nil ||
			!strings.Contains(err.Error(), "function identity") &&
				!strings.Contains(err.Error(), "declaration shape") {
			t.Fatalf("declaration shape mismatch error = %v", err)
		}
	})
}
