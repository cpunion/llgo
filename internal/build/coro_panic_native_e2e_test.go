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
	stdcontext "context"
	"fmt"
	goimporter "go/importer"
	"go/token"
	"go/types"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroPanicNativeE2EPackage          = "example.com/llgo-coro-panic-e2e"
	coroPanicNativeE2EEntry            = "__llgo_coro_panic_e2e_entry"
	coroPanicNativeE2ERunReport        = "__llgo_coro_program_run_report_e2e_v2"
	coroPanicNativeE2EDestroyObserve   = "__llgo_coro_destroy_observe_e2e_v1"
	coroPanicNativeE2EDestroyCount     = "__llgo_coro_panic_e2e_destroy_count"
	coroPanicNativeE2EFirstDestroy     = "__llgo_coro_panic_e2e_first_destroy"
	coroPanicNativeE2ESecondDestroy    = "__llgo_coro_panic_e2e_second_destroy"
	coroPanicNativeE2EThirdDestroy     = "__llgo_coro_panic_e2e_third_destroy"
	coroPanicNativeE2EExplicitStatus   = uint64(1)
	coroPanicNativeE2EDrivePanic       = uint64(4)
	coroPanicNativeE2EExpectedDestroys = uint64(2)
)

const coroPanicNativeE2ESource = `package main

var Before uint32
var After uint32
var Result uint32
var GlobalPayload byte

type Cell struct { Value uint32 }
var GlobalCell Cell

func panicLeaf(payload any, cell *Cell, delta uint32, doPanic bool) uint32 {
	if doPanic {
		panic(payload)
	}
	cell.Value = ((cell.Value + delta) << 1) / 2
	return cell.Value
}

func panicMiddle(payload any, cell *Cell, delta uint32, doPanic bool) uint32 {
	return panicLeaf(payload, cell, delta, doPanic)
}

func panicChild(doPanic bool) {
	Before = 1
	panicMiddle(&GlobalPayload, &GlobalCell, 0, doPanic)
}

func main() {
	Result = panicMiddle(nil, &GlobalCell, 37, false)
	panicChild(true)
	After = 1
}
`

// TestCoroExplicitPanicNativeNoStdlibRuntimeE2E is a deliberately closed
// scheduler island. It compiles a real source panic through an outcome-plain
// direct-call DAG, reconciles that payload in its physical-coroutine caller, links the
// production native-nogc scheduler/core and panic prepare hook, and runs
// without the legacy panic printer/runtime closure.
//
// Production ActionPanicComplete returns the explicit V2 drive-panic status
// and the native entry now owns a no-return production reporter edge. This
// closed island retargets only the initial run-slice declaration to a test
// report ABI; the report still calls the production internal V2 runner,
// validates the terminal panic, then returns a canonical Complete POD so the
// compiler-owned loop can exit normally. A fail-stop reporter definition keeps
// the required relocation exact but must remain unreachable here; the
// full-runtime caller acceptance test exercises production presentation. The
// report accepts only the exact drive status, a
// published record on a dead, non-reclaimable G, the original package-global
// payload word, and exactly one destroy of each distinct handle in the main ->
// bootstrap chain. panicChild is now outcome-plain, so it propagates an
// explicit status without allocating a third coroutine frame. The test does
// not turn panic into production success or provide a replacement printer.
func TestCoroExplicitPanicNativeNoStdlibRuntimeE2E(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native coroutine link smoke requires Darwin or Linux")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	ar, err := exec.LookPath("llvm-ar")
	if err != nil {
		ar, err = exec.LookPath("ar")
		if err != nil {
			t.Skip("llvm-ar/ar is unavailable")
		}
	}

	llssa.Initialize(llssa.InitAll)
	temp := t.TempDir()
	prog := llssa.NewProgram(nil)
	prog.SetRuntime(func() *types.Package {
		rt, err := goimporter.For("source", nil).Import(llssa.PkgRuntime)
		if err != nil {
			t.Fatal("load runtime type model:", err)
		}
		return rt
	})
	prog.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
	defer prog.Dispose()

	userObject, anchor := buildCoroPanicNativeE2EUser(t, prog, temp)
	entryObject := buildCoroPanicNativeE2EEntry(t, prog, temp, anchor)
	driverObject := buildCoroPanicNativeE2EDriver(t, prog, temp)
	runtimeObjects := buildCoroSpawnNativeE2ERuntimeIsland(t, temp)
	runtimeArchive := filepath.Join(temp, "libllgo-coro-panic-runtime-island.a")
	arArgs := append([]string{"rcs", runtimeArchive}, runtimeObjects...)
	if output, err := exec.Command(ar, arArgs...).CombinedOutput(); err != nil {
		t.Fatalf("archive coroutine panic runtime island: %v\n%s", err, output)
	}

	executable := filepath.Join(temp, "coro-panic-e2e")
	linkArgs := []string{driverObject, entryObject, userObject, runtimeArchive, "-o", executable}
	if runtime.GOOS == "darwin" {
		linkArgs = append(linkArgs, "-Wl,-dead_strip")
	} else {
		linkArgs = append(linkArgs, "-Wl,--gc-sections")
	}
	if output, err := exec.Command(clang, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link native coroutine explicit-panic smoke: %v\n%s", err, output)
	}
	assertCoroPanicNativeE2ELinkedSymbols(t, executable)

	runCtx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(runCtx, executable).CombinedOutput()
	if runCtx.Err() != nil {
		t.Fatalf("native coroutine explicit-panic smoke timed out: %v\n%s", runCtx.Err(), output)
	}
	if err != nil {
		t.Fatalf("native coroutine explicit-panic smoke failed: %v\n%s", err, output)
	}
}

func buildCoroPanicNativeE2EUser(t *testing.T, prog llssa.Program, temp string) (object, anchor string) {
	t.Helper()
	ssaPkg, files := buildCoroPlanTestPackage(t, coroPanicNativeE2EPackage, coroPanicNativeE2ESource, nil)
	universe, err := cl.PrepareEmissionUniverseWithOptions(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: coroPanicNativeE2EPackage,
	}}, cl.EmissionUniverseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	mainFn, childFn := ssaPkg.Func("main"), ssaPkg.Func("panicChild")
	middleFn, leafFn := ssaPkg.Func("panicMiddle"), ssaPkg.Func("panicLeaf")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: mainFn, Demand: coro.AsyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: 64,
		OutcomeMode:          coro.OutcomeExplicitStatus,
		ClassifyLocalBody:    universe.CoroLocalBodyFacts,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			switch fn {
			case mainFn, childFn:
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			default:
				return coro.SSAFunctionPolicy{}, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	leafPlan, found := plan.FunctionPlan(leafFn)
	if !found || leafPlan.Emission != coro.EmitOutcomePlain ||
		leafPlan.ManagedEntry != coro.ManagedEntryOutcomePlain ||
		leafPlan.AtomicCostProof != coro.AtomicCostLeaf {
		t.Fatalf("panic leaf plan = %+v, present=%t; want outcome-plain", leafPlan, found)
	}
	middlePlan, found := plan.FunctionPlan(middleFn)
	if !found || middlePlan.Emission != coro.EmitOutcomePlain ||
		middlePlan.ManagedEntry != coro.ManagedEntryOutcomePlain ||
		middlePlan.AtomicCostProof != coro.AtomicCostDAG || middlePlan.AtomicCost <= leafPlan.AtomicCost {
		t.Fatalf("panic middle plan = %+v, present=%t; want outcome-plain DAG above leaf cost %d", middlePlan, found, leafPlan.AtomicCost)
	}
	compilation := &cl.Compilation{
		CoroPlan: plan,

		CoroABI:          coro.PhysicalABIV1,
		SchedulerABI:     coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI:         coro.PanicExplicitStatusABIV0,
		FuncRepABI:       coro.FuncRepABIV1,
		EmissionUniverse: universe}
	pkg, _, err := cl.NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		cl.PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	presplit := module.String()
	// Both direct outcome call sites retain a fail-closed panic arm and the
	// awaiting parent republishes the consumed child payload. Together with the
	// declaration this produces four symbol references and exactly three calls.
	if references, calls := strings.Count(presplit, "@__llgo_coro_panic_prepare_v1"),
		strings.Count(presplit, "call void @__llgo_coro_panic_prepare_v1"); references != 4 || calls != 3 {
		t.Fatalf("compiled explicit panic prepare-hook references/calls = %d/%d, want 4/3:\n%s", references, calls, presplit)
	}
	if strings.Contains(presplit, llssa.PkgRuntime+".Panic") {
		t.Fatalf("compiled explicit panic retained the legacy runtime.Panic edge:\n%s", presplit)
	}
	if !strings.Contains(presplit, "panicMiddle$outcome") || !strings.Contains(presplit, "panicLeaf$outcome") ||
		strings.Contains(presplit, "panicMiddle$coro") || strings.Contains(presplit, "panicLeaf$coro") {
		t.Fatalf("compiled explicit panic did not collapse the outcome-plain DAG:\n%s", presplit)
	}
	runCoroSpawnNativeE2EPasses(t, prog, module)
	ir := module.String()
	match := regexp.MustCompile(`@"?(__llgo_coro_root_package_v1\.[0-9a-f]{32})"?\s*=`).FindStringSubmatch(ir)
	if len(match) != 2 {
		t.Fatalf("compiled panic E2E user module has no root package anchor:\n%s", ir)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, module, filepath.Join(temp, "panic-user.o")), match[1]
}

func buildCoroPanicNativeE2EEntry(t *testing.T, prog llssa.Program, temp, anchor string) string {
	t.Helper()
	conf := &Config{
		BuildMode: BuildModeExe,
		Goos:      runtime.GOOS,
		Goarch:    runtime.GOARCH}
	ctx := &context{prog: prog, buildConf: conf}
	bootstrap := &coroProgramBootstrapV1{
		Version: coroProgramBootstrapVersionV2,
		Steps: []coroProgramBootstrapStepV1{
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleRuntimeInitV2, FunctionID: "panic-e2e-runtime-init", Target: "__llgo_coro_panic_e2e_runtime_init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2, FunctionID: "panic-e2e-abi-init", Target: "init$abitypes"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2, FunctionID: coroProgramPublicRuntimeNoopIDV2, Target: coroProgramPublicRuntimeNoopSymbolV2},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePackageInitV2, FunctionID: "panic-e2e-package-init", Target: "__llgo_coro_panic_e2e_package_init"},
			{
				Kind: coroProgramStepCoroRootV1, Role: coroProgramStepRoleMainV2,
				FunctionID: "panic-e2e-main", Target: coroNativeE2EMainPhysicalSymbol("main$coro"),
				Owner: coroPanicNativeE2EPackage, CatalogTarget: anchor, Aux: 0,
			},
		},
	}
	var programHash [16]byte
	for i := range programHash {
		programHash[i] = byte(0x40 + i)
	}
	entry := genMainModule(ctx, llssa.PkgRuntime, &packages.Package{
		ID: coroPanicNativeE2EPackage, PkgPath: coroPanicNativeE2EPackage, ExportFile: "coro-panic-e2e.a",
	}, &genConfig{
		coroRootAnchors:  []string{anchor},
		coroManifestHash: programHash,
		coroBootstrap:    bootstrap,
	})
	for _, name := range []string{"__llgo_coro_panic_e2e_runtime_init", "__llgo_coro_panic_e2e_package_init"} {
		fn := entry.LPkg.FuncOf(name)
		if fn == nil {
			t.Fatalf("entry module has no bounded panic-E2E init declaration %q", name)
		}
		if !fn.HasBody() {
			body := fn.MakeBody(1)
			body.Return()
		}
	}
	module := entry.LPkg.Module()
	entryMain := module.NamedFunction("main")
	if entryMain.IsNil() {
		t.Fatalf("entry module has no native main:\n%s", entry.LPkg.String())
	}
	entryMain.SetName(coroPanicNativeE2EEntry)
	run := module.NamedFunction(coroProgramRunSliceSymbolV2)
	if run.IsNil() || !run.IsDeclaration() {
		t.Fatalf("entry module has no program run-slice declaration %q:\n%s", coroProgramRunSliceSymbolV2, entry.LPkg.String())
	}
	run.SetName(coroPanicNativeE2ERunReport)

	destroy := entry.LPkg.FuncOf("__llgo_coro_destroy_v1")
	if destroy == nil || !destroy.HasBody() {
		t.Fatalf("entry module has no coroutine destroy wrapper:\n%s", entry.LPkg.String())
	}
	observe := entry.LPkg.NewFunc(coroPanicNativeE2EDestroyObserve, newSignature(
		[]types.Type{types.Typ[types.UnsafePointer]}, nil,
	), llssa.InC)
	instrument := destroy.NewBuilder()
	instrument.SetBlockEx(destroy.Block(0), llssa.AtStart, true)
	instrument.Call(observe.Expr, destroy.Param(0))
	instrument.Dispose()

	if err := lowerCoroControlWrappers(ctx, entry.LPkg); err != nil {
		t.Fatal(err)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, module, filepath.Join(temp, "panic-entry.o"))
}

func buildCoroPanicNativeE2EDriver(t *testing.T, prog llssa.Program, temp string) string {
	t.Helper()
	pkg := prog.NewPackage("coro-panic-e2e-driver", "coro-panic-e2e-driver")
	defer pkg.Module().Dispose()
	pointer := types.Typ[types.UnsafePointer]
	uint32Type := types.Typ[types.Uint32]
	runResultFields := make([]*types.Var, 8)
	for index := range runResultFields {
		runResultFields[index] = types.NewField(token.NoPos, nil, fmt.Sprintf("Word%d", index), uint32Type, false)
	}
	runResultType := types.NewStruct(runResultFields, nil)
	runResultPointer := types.NewPointer(runResultType)

	abort := pkg.NewFunc("abort", newSignature(nil, nil), llssa.InC)
	exit := pkg.NewFunc("exit", newSignature([]types.Type{types.Typ[types.Int32]}, nil), llssa.InC)
	// This closed scheduler-island test retargets the production RunSlice to a
	// verifier which converts the observed Panic result into Complete. Keep the
	// newly required terminal-report relocation exact and fail-stop if that
	// conversion ever regresses; the full-runtime acceptance test exercises the
	// production reporter itself.
	panicReporter := pkg.NewFunc(coroProgramReportPanicSymbolV1, newSignature(
		[]types.Type{pointer}, nil,
	), llssa.InC)
	panicReporterBody := panicReporter.MakeBody(1)
	panicReporterBody.Call(exit.Expr, prog.IntVal(20, prog.Int32()))
	panicReporterBody.Return()
	require := pkg.NewFunc("__llgo_coro_panic_e2e_require", newSignature(
		[]types.Type{types.Typ[types.Bool], types.Typ[types.Int32]}, nil,
	), llssa.InC)
	requireBody := require.MakeBody(3)
	requireFail, requireValid := require.Block(1), require.Block(2)
	requireBody.If(require.Param(0), requireValid, requireFail)
	requireBody.SetBlock(requireFail).Call(exit.Expr, require.Param(1))
	requireBody.Return()
	requireBody.SetBlock(requireValid).Return()

	destroyCount := pkg.NewVar(coroPanicNativeE2EDestroyCount, types.NewPointer(uint32Type), llssa.InC)
	destroyCount.InitNil()
	firstDestroy := pkg.NewVar(coroPanicNativeE2EFirstDestroy, types.NewPointer(pointer), llssa.InC)
	firstDestroy.InitNil()
	secondDestroy := pkg.NewVar(coroPanicNativeE2ESecondDestroy, types.NewPointer(pointer), llssa.InC)
	secondDestroy.InitNil()
	thirdDestroy := pkg.NewVar(coroPanicNativeE2EThirdDestroy, types.NewPointer(pointer), llssa.InC)
	thirdDestroy.InitNil()
	observe := pkg.NewFunc(coroPanicNativeE2EDestroyObserve, newSignature([]types.Type{pointer}, nil), llssa.InC)
	observeBody := observe.MakeBody(5)
	firstBlock, laterBlock := observe.Block(1), observe.Block(2)
	secondBlock, thirdBlock := observe.Block(3), observe.Block(4)
	count := observeBody.Load(destroyCount.Expr)
	zero32 := prog.IntVal(0, prog.Uint32())
	one32 := prog.IntVal(1, prog.Uint32())
	observeBody.If(observeBody.BinOp(token.EQL, count, zero32), firstBlock, laterBlock)
	firstBody := observeBody.SetBlock(firstBlock)
	firstBody.Store(firstDestroy.Expr, observe.Param(0))
	firstBody.Store(destroyCount.Expr, firstBody.BinOp(token.ADD, count, one32))
	firstBody.Return()
	laterBody := observeBody.SetBlock(laterBlock)
	laterBody.If(laterBody.BinOp(token.EQL, count, one32), secondBlock, thirdBlock)
	secondBody := observeBody.SetBlock(secondBlock)
	secondBody.Store(secondDestroy.Expr, observe.Param(0))
	secondBody.Store(destroyCount.Expr, secondBody.BinOp(token.ADD, count, one32))
	secondBody.Return()
	thirdBody := observeBody.SetBlock(thirdBlock)
	thirdBody.Store(thirdDestroy.Expr, observe.Param(0))
	thirdBody.Store(destroyCount.Expr, thirdBody.BinOp(token.ADD, count, one32))
	thirdBody.Return()

	// The production adapter island is compiled from an explicit runtime file
	// list, so its private Go symbols belong to command-line-arguments while its
	// exported C ABI remains stable.
	runtimeRun := pkg.NewFunc("command-line-arguments.coroProgramRunSliceV2", newSignature(
		[]types.Type{pointer, pointer, uint32Type, runResultPointer}, []types.Type{uint32Type},
	), llssa.InGo)
	panicRecordType := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "Status", uint32Type, false),
		types.NewField(token.NoPos, nil, "TypeWord", pointer, false),
		types.NewField(token.NoPos, nil, "DataWord", pointer, false),
	}, nil)
	loadPanicRecord := pkg.NewFunc("github.com/goplus/llgo/runtime/internal/coro.LoadPanicRecord", newSignature(
		[]types.Type{pointer}, []types.Type{panicRecordType, types.Typ[types.Bool]},
	), llssa.InGo)
	deadG := pkg.NewFunc("github.com/goplus/llgo/runtime/internal/coro.DeadG", newSignature(
		[]types.Type{pointer}, []types.Type{types.Typ[types.Bool]},
	), llssa.InGo)
	reclaimableG := pkg.NewFunc("github.com/goplus/llgo/runtime/internal/coro.ReclaimableG", newSignature(
		[]types.Type{pointer}, []types.Type{types.Typ[types.Bool]},
	), llssa.InGo)
	payload := pkg.NewVar(coroNativeE2EMainPhysicalSymbol("GlobalPayload"), types.NewPointer(types.Typ[types.Byte]), llssa.InGo)
	before := pkg.NewVar(coroNativeE2EMainPhysicalSymbol("Before"), types.NewPointer(uint32Type), llssa.InGo)
	after := pkg.NewVar(coroNativeE2EMainPhysicalSymbol("After"), types.NewPointer(uint32Type), llssa.InGo)
	result := pkg.NewVar(coroNativeE2EMainPhysicalSymbol("Result"), types.NewPointer(uint32Type), llssa.InGo)

	report := pkg.NewFunc(coroPanicNativeE2ERunReport, newSignature(
		[]types.Type{pointer, pointer, uint32Type, runResultPointer}, []types.Type{uint32Type},
	), llssa.InC)
	reportBody := report.MakeBody(1)
	requireCode := uint64(21)
	requireCondition := func(condition llssa.Expr) {
		reportBody.Call(require.Expr, condition, prog.IntVal(requireCode, prog.Int32()))
		requireCode++
	}
	requireCondition(reportBody.BinOp(
		token.EQL,
		report.Param(2),
		prog.IntVal(uint64(coroProgramNativeRunBudgetV2), prog.Uint32()),
	))
	driveStatus := reportBody.Call(runtimeRun.Expr, report.Param(0), report.Param(1), report.Param(2), report.Param(3))
	requireCondition(reportBody.BinOp(
		token.EQL,
		driveStatus,
		prog.IntVal(coroPanicNativeE2EDrivePanic, prog.Uint32()),
	))
	loaded := reportBody.Call(loadPanicRecord.Expr, report.Param(0))
	record := reportBody.Extract(loaded, 0)
	published := reportBody.Extract(loaded, 1)
	requireCondition(published)
	requireCondition(reportBody.BinOp(
		token.EQL,
		reportBody.Field(record, 0),
		prog.IntVal(coroPanicNativeE2EExplicitStatus, prog.Uint32()),
	))
	nilPointer := prog.Nil(prog.VoidPtr())
	typeWord := reportBody.Field(record, 1)
	dataWord := reportBody.Field(record, 2)
	requireCondition(reportBody.BinOp(token.NEQ, typeWord, nilPointer))
	requireCondition(reportBody.BinOp(token.NEQ, typeWord, dataWord))
	requireCondition(reportBody.BinOp(token.EQL, dataWord, reportBody.Convert(prog.VoidPtr(), payload.Expr)))
	requireCondition(reportBody.Call(deadG.Expr, report.Param(0)))
	requireCondition(reportBody.UnOp(token.NOT, reportBody.Call(reclaimableG.Expr, report.Param(0))))
	destroyCalls := reportBody.Load(destroyCount.Expr)
	requireCondition(reportBody.BinOp(
		token.EQL,
		destroyCalls,
		prog.IntVal(coroPanicNativeE2EExpectedDestroys, prog.Uint32()),
	))
	first := reportBody.Load(firstDestroy.Expr)
	second := reportBody.Load(secondDestroy.Expr)
	third := reportBody.Load(thirdDestroy.Expr)
	requireCondition(reportBody.BinOp(token.NEQ, first, nilPointer))
	requireCondition(reportBody.BinOp(token.NEQ, second, nilPointer))
	requireCondition(reportBody.BinOp(token.EQL, third, nilPointer))
	requireCondition(reportBody.BinOp(token.NEQ, first, second))
	requireCondition(reportBody.BinOp(token.EQL, reportBody.Load(before.Expr), one32))
	requireCondition(reportBody.BinOp(token.EQL, reportBody.Load(after.Expr), zero32))
	requireCondition(reportBody.BinOp(
		token.EQL,
		reportBody.Load(result.Expr),
		prog.IntVal(37, prog.Uint32()),
	))
	for index := range runResultFields {
		reportBody.Store(reportBody.FieldAddr(report.Param(3), index), zero32)
	}
	reportBody.Return(prog.IntVal(uint64(coroProgramDriveCompleteV2), prog.Uint32()))

	// The production scheduler core is intentionally compiled without the full
	// standard-library runtime package. Keep ordinary pointer checks fail-stop
	// and resolve unreachable core allocation edges directly to libc, matching
	// the closed-static-spawn island.
	defineCoroNativeE2ENilDerefStubs(prog, pkg, abort)
	checkIndexRange := pkg.NewFunc(llssa.PkgRuntime+".CheckIndexRange", newSignature(
		[]types.Type{types.Typ[types.Bool], types.Typ[types.Int64], types.Typ[types.Bool], types.Typ[types.Int]}, nil,
	), llssa.InGo)
	rangeBody := checkIndexRange.MakeBody(3)
	rangeFail, rangeValid := checkIndexRange.Block(1), checkIndexRange.Block(2)
	rangeBody.If(checkIndexRange.Param(0), rangeFail, rangeValid)
	rangeBody.SetBlock(rangeFail).Call(abort.Expr)
	rangeBody.Return()
	rangeBody.SetBlock(rangeValid).Return()
	uintptrType := types.Typ[types.Uintptr]
	malloc := pkg.NewFunc("malloc", newSignature([]types.Type{uintptrType}, []types.Type{pointer}), llssa.InC)
	calloc := pkg.NewFunc("calloc", newSignature([]types.Type{uintptrType, uintptrType}, []types.Type{pointer}), llssa.InC)
	allocU := pkg.NewFunc(llssa.PkgRuntime+".AllocU", newSignature([]types.Type{uintptrType}, []types.Type{pointer}), llssa.InGo)
	allocUBody := allocU.MakeBody(1)
	allocUBody.Return(allocUBody.Call(malloc.Expr, allocU.Param(0)))
	allocZ := pkg.NewFunc(llssa.PkgRuntime+".AllocZ", newSignature([]types.Type{uintptrType}, []types.Type{pointer}), llssa.InGo)
	allocZBody := allocZ.MakeBody(1)
	allocZBody.Return(allocZBody.Call(calloc.Expr, prog.IntVal(1, prog.Uintptr()), allocZ.Param(0)))
	// The concrete *byte panic value materializes pointer and byte type
	// descriptors. Their equality callbacks are metadata-only in this fixture;
	// provide exact test-island implementations instead of extracting alg.go and
	// its unrelated legacy runtime closure.
	memequal8 := pkg.NewFunc(llssa.PkgRuntime+".memequal8", newSignature(
		[]types.Type{pointer, pointer}, []types.Type{types.Typ[types.Bool]},
	), llssa.InGo)
	memequal8Body := memequal8.MakeBody(1)
	memequal8Pointer := prog.Pointer(prog.Byte())
	memequal8Body.Return(memequal8Body.BinOp(
		token.EQL,
		memequal8Body.Load(memequal8Body.Convert(memequal8Pointer, memequal8.Param(0))),
		memequal8Body.Load(memequal8Body.Convert(memequal8Pointer, memequal8.Param(1))),
	))
	memequalptr := pkg.NewFunc(llssa.PkgRuntime+".memequalptr", newSignature(
		[]types.Type{pointer, pointer}, []types.Type{types.Typ[types.Bool]},
	), llssa.InGo)
	memequalptrBody := memequalptr.MakeBody(1)
	memequalptrPointer := prog.Pointer(prog.Uintptr())
	memequalptrBody.Return(memequalptrBody.BinOp(
		token.EQL,
		memequalptrBody.Load(memequalptrBody.Convert(memequalptrPointer, memequalptr.Param(0))),
		memequalptrBody.Load(memequalptrBody.Convert(memequalptrPointer, memequalptr.Param(1))),
	))

	entry := pkg.NewFunc(coroPanicNativeE2EEntry, newSignature(
		[]types.Type{types.Typ[types.Int32], pointer}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	main := pkg.NewFunc("main", newSignature(
		[]types.Type{types.Typ[types.Int32], pointer}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	mainBody := main.MakeBody(1)
	mainBody.Call(entry.Expr, main.Param(0), main.Param(1))
	mainBody.Return(prog.IntVal(0, prog.Int32()))
	pkg.MaterializePreserveSyms()
	return emitCoroSpawnNativeE2EObject(t, prog, pkg.Module(), filepath.Join(temp, "panic-driver.o"))
}

func assertCoroPanicNativeE2ELinkedSymbols(t *testing.T, executable string) {
	t.Helper()
	nm, err := exec.LookPath("nm")
	if err != nil {
		t.Log("nm is unavailable; continuing without the linked coroutine panic symbol audit")
		return
	}
	output, err := exec.Command(nm, executable).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect linked coroutine panic island: %v\n%s", err, output)
	}
	symbols := string(output)
	for _, required := range []string{
		"__llgo_coro_panic_prepare_v1",
		coroPanicTraceReplaceSymbolV1,
		coroProgramContinueSliceSymbolV2,
		coroPanicNativeE2ERunReport,
		coroProgramReportPanicSymbolV1,
		"command-line-arguments.coroProgramRunSliceV2",
		coroPanicNativeE2EDestroyObserve,
		"github.com/goplus/llgo/runtime/internal/coro.PreparePanic",
		"github.com/goplus/llgo/runtime/internal/coro.PanicDestroyed",
		"github.com/goplus/llgo/runtime/internal/coro.LoadPanicRecord",
		coroNativeE2EMainPhysicalSymbol("panicChild$outcome"),
		coroNativeE2EMainPhysicalSymbol("panicLeaf$outcome"),
	} {
		if !strings.Contains(symbols, required) {
			t.Fatalf("linked coroutine panic island is missing production/test-boundary symbol %q:\n%s", required, symbols)
		}
	}
	for _, forbidden := range []string{
		"github.com/goplus/llgo/runtime/internal/runtime.Panic",
		"github.com/goplus/llgo/runtime/internal/runtime.Rethrow",
		"github.com/goplus/llgo/runtime/internal/runtime.TracePanic",
		"github.com/goplus/llgo/runtime/internal/runtime.printany",
	} {
		if strings.Contains(symbols, forbidden) {
			t.Fatalf("test-only coroutine panic island unexpectedly extracted legacy PanicABI symbol %q", forbidden)
		}
	}
}
