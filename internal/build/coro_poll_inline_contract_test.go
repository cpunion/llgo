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
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/env"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

type coroPollProductionFixture struct {
	program *ssa.Program

	runtimeSSA   *ssa.Package
	runtimeFiles []*ast.File
}

func TestProductionCoroPollInlineAttemptDefaultPlanAndPhysicalIR(t *testing.T) {
	fixture := buildCoroPollProductionFixture(t)

	program := llssa.NewProgram(nil)
	defer program.Dispose()
	program.SetRuntime(func() *types.Package {
		runtimePackage, err := importer.For("source", nil).Import(llssa.PkgRuntime)
		if err != nil {
			t.Fatal("load runtime failed:", err)
		}
		if runtimePackage.Scope().Lookup("AllocZ") == nil {
			signature := types.NewSignatureType(
				nil, nil, nil,
				types.NewTuple(types.NewVar(token.NoPos, runtimePackage, "size", types.Typ[types.Uintptr])),
				types.NewTuple(types.NewVar(token.NoPos, runtimePackage, "", types.Unsafe.Scope().Lookup("Pointer").Type())),
				false,
			)
			if previous := runtimePackage.Scope().Insert(types.NewFunc(token.NoPos, runtimePackage, "AllocZ", signature)); previous != nil {
				t.Fatalf("install focused runtime.AllocZ declaration: duplicate %v", previous)
			}
		}
		return runtimePackage
	})
	emission, err := cl.PrepareEmissionUniverse(program, nil, []cl.EmissionPackage{
		{SSA: fixture.runtimeSSA, Files: fixture.runtimeFiles, Identity: llssa.PkgRuntime},
	})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(fixture.program, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	input := CoroPlanInput{
		Program:                        fixture.program,
		EmissionUniverse:               ssaEmission,
		resolveFunction:                emission.Resolve,
		functionBackground:             emission.FunctionBackground,
		foreignNoBlock:                 emission.CoroForeignNoBlockCertificate,
		foreignSync:                    emission.CoroForeignSyncCertificate,
		foreignSchedulerWait:           emission.CoroForeignSchedulerWaitCertificate,
		foreignWorker:                  emission.CoroForeignWorkerCertificate,
		callSitePlan:                   emission.CoroCallSitePlan,
		rawFunctionAddressCallArgument: emission.CoroRawFunctionAddressCallArgument,
		staticCodeAddressCallArgument:  emission.CoroStaticCodeAddressCallArgument,
		callableIdentity:               emission.CoroCallableIdentityCertificate,
		callableContract:               emission.CoroCallableContractCertificate,
		trustedInlineCall:              emission.CoroTrustedInlineCallCertificate,
		demandReferences:               emission.CoroDemandReferences,
		syncDemandReferences:           emission.CoroSyncDemandReferences,
		loweredCalls:                   emission.CoroLoweredCalls,
	}
	read := fixture.runtimeSSA.Func("pollCoroReadAttemptV1")
	write := fixture.runtimeSSA.Func("pollCoroWriteAttemptV1")
	fdProbe := fixture.runtimeSSA.Func("pollCoroFDStreamLeafV1")
	if read == nil || write == nil || fdProbe == nil {
		t.Fatal("production poll source has no stream read/write and descriptor-proof wrappers")
	}
	roots := coro.Roots{
		{Function: read, Demand: coro.SyncDemand},
		{Function: write, Demand: coro.SyncDemand},
	}
	// Deliberately leave MaxPlainInstructions at its production default. This
	// proves the real wrappers fit the normal executor-safe budget rather than
	// relying on the unlimited setting used by broad compiler fixtures.
	plan, err := input.Analyze(roots, coro.SSAConfig{FunctionIDs: functionIDs})
	if err != nil {
		t.Fatal(err)
	}
	assertProductionCoroPollInlinePlan(t, plan, read, "llgoCoroPollReadAttemptPackedV1")
	assertProductionCoroPollInlinePlan(t, plan, write, "llgoCoroPollWriteAttemptPackedV1")

	// Production enables the explicit-status implicit-fault profile. Under that
	// architecture a merely conservative MayUnwind body becomes structured outcome
	// work, so this second analysis is the exact regression that the legacy
	// focused fixture previously missed.
	explicitRoots := coro.Roots{
		{Function: read, Demand: coro.AsyncDemand},
		{Function: write, Demand: coro.AsyncDemand},
		{Function: fdProbe, Demand: coro.AsyncDemand},
	}
	explicitInput := input
	explicitPlan, err := explicitInput.Analyze(explicitRoots, coro.SSAConfig{
		FunctionIDs: functionIDs,
		OutcomeMode: coro.OutcomeExplicitStatus,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProductionCoroPollInlinePlan(t, explicitPlan, read, "llgoCoroPollReadAttemptPackedV1")
	assertProductionCoroPollInlinePlan(t, explicitPlan, write, "llgoCoroPollWriteAttemptPackedV1")
	assertProductionCoroPollSelectedLeafPlan(t, explicitPlan, fdProbe, "llgoCoroPollFDStreamV1")

	compilation := &cl.Compilation{CoroPlan: explicitPlan, EmissionUniverse: emission}
	runtimePkg, _, err := cl.NewPackageExWithEmbedOptions(
		program, nil, nil, nil, fixture.runtimeSSA, fixture.runtimeFiles, goembed.VarMap{},
		cl.PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile production poll inline wrappers: %v", err)
	}
	module := runtimePkg.Module()
	for _, test := range []struct {
		wrapper string
		target  string
	}{
		{"pollCoroReadAttemptV1", "__llgo_runtime_poll_read_attempt_v1"},
		{"pollCoroWriteAttemptV1", "__llgo_runtime_poll_write_attempt_v1"},
	} {
		symbol := llssa.FullName(fixture.runtimeSSA.Pkg, test.wrapper)
		function := module.NamedFunction(symbol)
		if function.IsNil() {
			t.Fatalf("compiled production poll module has no %s:\n%s", symbol, module.String())
		}
		body := function.String()
		if strings.Count(body, "@"+test.target) != 1 {
			t.Fatalf("%s direct packed C attempts = %d, want 1:\n%s", test.wrapper, strings.Count(body, "@"+test.target), body)
		}
		for _, forbidden := range []string{
			"runtime.AllocZ",
			"llvm.coro.suspend",
			"__llgo_coro_preempt_poll_v1",
			"__llgo_coro_native_worker_submit_v1",
			"__llgo_coro_native_worker_park_v1",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s physical inline wrapper contains %q:\n%s", test.wrapper, forbidden, body)
			}
		}
	}
}

func assertProductionCoroPollInlinePlan(t *testing.T, plan *coro.SSAPlan, wrapper *ssa.Function, targetName string) {
	t.Helper()
	wrapperPlan, ok := plan.FunctionPlan(wrapper)
	if !ok || wrapperPlan.Effect != coro.NoSuspend || wrapperPlan.Emission != coro.EmitPlain ||
		wrapperPlan.Exec.Contains(coro.BlockForeign|coro.NeedsPreempt|coro.ThreadAffine|coro.OpaqueExec|coro.MayUnwind) {
		t.Fatalf("production %s plan = %+v, %t", wrapper.Name(), wrapperPlan, ok)
	}
	call := onlyCallableContractStaticCall(t, wrapper, targetName)
	callPlan, ok := plan.CallPlan(call)
	if !ok || callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.DirectPlain ||
		callPlan.InvocationPolicy != "" {
		t.Fatalf("production %s executor-safe C call plan = %+v, %t", wrapper.Name(), callPlan, ok)
	}
}

func assertProductionCoroPollSelectedLeafPlan(t *testing.T, plan *coro.SSAPlan, wrapper *ssa.Function, targetName string) {
	t.Helper()
	wrapperPlan, ok := plan.FunctionPlan(wrapper)
	if !ok || wrapperPlan.Effect != coro.NoSuspend || wrapperPlan.Exec.Contains(coro.MayUnwind) ||
		wrapperPlan.Emission != coro.EmitPlain ||
		wrapperPlan.Exec.Contains(coro.BlockForeign|coro.NeedsPreempt|coro.ThreadAffine|coro.OpaqueExec) {
		t.Fatalf("production explicit-status %s plan = %+v, %t", wrapper.Name(), wrapperPlan, ok)
	}
	call := onlyCallableContractStaticCall(t, wrapper, targetName)
	callPlan, ok := plan.CallPlan(call)
	if !ok || callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.DirectPlain ||
		callPlan.InvocationPolicy != "" {
		t.Fatalf("production explicit-status %s executor-safe C call plan = %+v, %t", wrapper.Name(), callPlan, ok)
	}
}

func buildCoroPollProductionFixture(t *testing.T) coroPollProductionFixture {
	t.Helper()
	fset := token.NewFileSet()
	parse := func(path string) *ast.File {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, path, source, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		return file
	}
	runtimeDir := env.LLGoRuntimeDir()
	pollSource := parse(filepath.Join(runtimeDir, "internal", "lib", "runtime", "poll_linkname_coro_llgo.go"))
	runtimeFiles := []*ast.File{selectCoroPollProductionDecls(t, pollSource)}

	newInfo := func() *types.Info {
		return &types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Defs:       make(map[*ast.Ident]types.Object),
			Uses:       make(map[*ast.Ident]types.Object),
			Implicits:  make(map[ast.Node]types.Object),
			Scopes:     make(map[ast.Node]*types.Scope),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
			Instances:  make(map[*ast.Ident]types.Instance),
		}
	}
	fallback := importer.Default()
	runtimeTypes := types.NewPackage(llssa.PkgRuntime, "runtime")
	runtimeInfo := newInfo()
	if err := types.NewChecker(&types.Config{Importer: fallback}, fset, runtimeTypes, runtimeInfo).Files(runtimeFiles); err != nil {
		t.Fatalf("type-check production poll wrapper source: %v", err)
	}

	ssaProgram := ssa.NewProgram(fset, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	created := make(map[*types.Package]bool)
	var createImports func(*types.Package)
	createImports = func(pkg *types.Package) {
		if pkg == nil || created[pkg] {
			return
		}
		created[pkg] = true
		for _, imported := range pkg.Imports() {
			createImports(imported)
		}
		ssaProgram.CreatePackage(pkg, nil, nil, true)
	}
	for _, imported := range runtimeTypes.Imports() {
		createImports(imported)
	}
	runtimeSSA := ssaProgram.CreatePackage(runtimeTypes, runtimeFiles, runtimeInfo, true)
	created[runtimeTypes] = true
	ssaProgram.Build()
	return coroPollProductionFixture{
		program:    ssaProgram,
		runtimeSSA: runtimeSSA, runtimeFiles: runtimeFiles,
	}
}

func selectCoroPollProductionDecls(t *testing.T, source *ast.File) *ast.File {
	t.Helper()
	wantedFunctions := map[string]bool{
		"llgoCoroPollFDStreamV1":           false,
		"llgoCoroPollReadAttemptPackedV1":  false,
		"llgoCoroPollWriteAttemptPackedV1": false,
		"pollCoroFDStreamLeafV1":           false,
		"pollCoroReadAttemptV1":            false,
		"pollCoroWriteAttemptV1":           false,
	}
	selected := &ast.File{Name: ast.NewIdent("runtime")}
	var importSpecs []ast.Spec
	for _, declaration := range source.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			if declaration.Tok != token.IMPORT {
				continue
			}
			for _, spec := range declaration.Specs {
				importSpec, ok := spec.(*ast.ImportSpec)
				if !ok {
					continue
				}
				path, err := strconv.Unquote(importSpec.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if path == "unsafe" {
					importSpecs = append(importSpecs, importSpec)
					selected.Imports = append(selected.Imports, importSpec)
				}
			}
		case *ast.FuncDecl:
			if _, ok := wantedFunctions[declaration.Name.Name]; ok {
				wantedFunctions[declaration.Name.Name] = true
				selected.Decls = append(selected.Decls, declaration)
			}
		}
	}
	if len(importSpecs) != 0 {
		selected.Decls = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT, Specs: importSpecs}}, selected.Decls...)
	}
	for name, found := range wantedFunctions {
		if !found {
			t.Fatalf("production poll source has no %s declaration", name)
		}
	}
	return selected
}

func TestCoroPollInlineAttemptUsesOnlyExactSelectedContract(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/pollattempt", `package pollattempt
import "unsafe"

//llgo:coro contract foreign.v1 progress=unknown affinity=unknown reentry=unknown memory=unknown inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none inline-memory=borrow-until-return
//go:linkname ReadPacked C.__llgo_runtime_poll_read_attempt_v1
func ReadPacked(int32, unsafe.Pointer, uintptr) uint64

type Gate struct {
	state uint64
	resource uintptr
}
type Lease struct {
	gate *Gate
	state uint64
}

func (gate *Gate) Acquire(resource uintptr) (Lease, bool) {
	if gate == nil || gate.state & 1 != 0 || gate.resource != resource {
		return Lease{}, false
	}
	next := gate.state | 1
	gate.state = next
	return Lease{gate: gate, state: next}, true
}

func (lease Lease) Release() bool {
	if lease.gate == nil || lease.gate.state != lease.state {
		return false
	}
	lease.gate.state = lease.state &^ 1
	return true
}

//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return
func ReadLeased(gate *Gate, resource uintptr, fd int32, address unsafe.Pointer, size uintptr) (result int, errno int, attempted bool, released bool) {
	lease, acquired := gate.Acquire(resource)
	if !acquired {
		return 0, 0, false, false
	}
	packed := ReadPacked(fd, address, size)
	result = int(int32(uint32(packed)))
	errno = int(uint32(packed >> 32))
	return result, errno, true, lease.Release()
}

func Inline(gate *Gate, resource uintptr, fd int32, address unsafe.Pointer, size uintptr) (int, int, bool, bool) {
	return ReadLeased(gate, resource, fd, address, size)
}

func Auto(fd int32, address unsafe.Pointer, size uintptr) uint64 {
	return ReadPacked(fd, address, size)
}
`, nil)

	program := llssa.NewProgram(nil)
	defer program.Dispose()
	emission, err := cl.PrepareEmissionUniverse(program, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: []*ast.File{files[0]}, Identity: "example.com/pollattempt",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	input := CoroPlanInput{
		Program:            ssaPkg.Prog,
		EmissionUniverse:   ssaEmission,
		resolveFunction:    emission.Resolve,
		functionBackground: emission.FunctionBackground,
		callableIdentity:   emission.CoroCallableIdentityCertificate,
		callableContract:   emission.CoroCallableContractCertificate,
		trustedInlineCall:  emission.CoroTrustedInlineCallCertificate,
	}
	roots := coro.Roots{
		{Function: ssaPkg.Func("Inline"), Demand: coro.SyncDemand},
		{Function: ssaPkg.Func("Auto"), Demand: coro.SyncDemand},
	}
	plan, err := input.Analyze(roots, coro.SSAConfig{FunctionIDs: functionIDs})
	if err != nil {
		t.Fatal(err)
	}

	target := ssaPkg.Func("ReadPacked")
	leaf := ssaPkg.Func("ReadLeased")
	auto := ssaPkg.Func("Auto")
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.External != coro.ExternalUnknownForeign ||
		!targetPlan.Exec.Contains(coro.BlockForeign|coro.ThreadAffine|coro.OpaqueExec) {
		t.Fatalf("conservative read target plan = %+v, %t", targetPlan, ok)
	}
	leafPlan, ok := plan.FunctionPlan(leaf)
	if !ok || leafPlan.Effect != coro.NoSuspend ||
		leafPlan.Exec.Contains(coro.BlockForeign|coro.NeedsPreempt|coro.ThreadAffine|coro.OpaqueExec) {
		t.Fatalf("selected read leaf plan = %+v, %t", leafPlan, ok)
	}
	primitivePlans := map[string]bool{"Acquire": false, "Release": false}
	for _, block := range leaf.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil || call.Common().StaticCallee() == nil {
				continue
			}
			callee := call.Common().StaticCallee()
			if _, tracked := primitivePlans[callee.Name()]; !tracked {
				continue
			}
			primitivePlan, planned := plan.FunctionPlan(callee)
			if !planned || primitivePlan.Effect != coro.NoSuspend ||
				primitivePlan.Exec.Contains(coro.BlockForeign|coro.NeedsPreempt|coro.ThreadAffine|coro.OpaqueExec) {
				t.Fatalf("lease primitive %s plan = %+v, %t", callee.Name(), primitivePlan, planned)
			}
			primitivePlans[callee.Name()] = true
		}
	}
	for name, found := range primitivePlans {
		if !found {
			t.Fatalf("selected read lease wrapper has no static %s call", name)
		}
	}
	call := onlyCallableContractStaticCall(t, leaf, "ReadPacked")
	callPlan, ok := plan.CallPlan(call)
	if !ok || callPlan.Kind != coro.CallTrustedInline || callPlan.Rep != coro.DirectPlain ||
		callPlan.InvocationPolicy != coro.InvocationTrustedInline || callPlan.InvocationContract == "" ||
		callPlan.InvocationABI == "" || callPlan.InvocationCertificate == "" {
		t.Fatalf("selected read invocation plan = %+v, %t", callPlan, ok)
	}
	autoPlan, ok := plan.FunctionPlan(auto)
	if !ok || !autoPlan.Effect.Contains(coro.WaitForeign) {
		t.Fatalf("ordinary read target invocation escaped conservative policy: %+v, %t", autoPlan, ok)
	}

	_, err = input.Analyze(roots, coro.SSAConfig{MaxPlainInstructions: 1, FunctionIDs: functionIDs})
	if err == nil || !strings.Contains(err.Error(), "claims executor-safe") {
		t.Fatalf("over-budget leased wrapper error = %v; want executor-safe rejection", err)
	}
}

func TestCoroPollInlineAttemptRejectsLoopingLeasePrimitive(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/pollattemptloop", `package pollattemptloop
type Gate struct{ state uint64 }

func (gate *Gate) Acquire() bool {
	for gate.state == 0 {
	}
	return true
}

//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return
func ReadLeased(gate *Gate) bool { return gate.Acquire() }
`, nil)

	program := llssa.NewProgram(nil)
	defer program.Dispose()
	emission, err := cl.PrepareEmissionUniverse(program, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: []*ast.File{files[0]}, Identity: "example.com/pollattemptloop",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	input := CoroPlanInput{
		Program:            ssaPkg.Prog,
		EmissionUniverse:   ssaEmission,
		resolveFunction:    emission.Resolve,
		functionBackground: emission.FunctionBackground,
		callableIdentity:   emission.CoroCallableIdentityCertificate,
		callableContract:   emission.CoroCallableContractCertificate,
		trustedInlineCall:  emission.CoroTrustedInlineCallCertificate,
	}
	_, err = input.Analyze(
		coro.Roots{{Function: ssaPkg.Func("ReadLeased"), Demand: coro.SyncDemand}},
		coro.SSAConfig{FunctionIDs: functionIDs},
	)
	if err == nil || !strings.Contains(err.Error(), "claims executor-safe") {
		t.Fatalf("looping lease primitive error = %v; want executor-safe rejection", err)
	}
}
