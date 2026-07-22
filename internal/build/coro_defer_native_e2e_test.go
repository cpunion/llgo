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
	coroStaticDeferNativeE2ESpawnBegin = "__llgo_coro_static_defer_e2e_spawn_begin_v1"
	coroStaticDeferNativeE2ERun        = "__llgo_coro_static_defer_e2e_run_slice_v2"
	coroStaticDeferNativeE2EContinue   = "__llgo_coro_static_defer_e2e_continue_slice_v2"
	coroStaticDeferNativeE2EChild      = "__llgo_coro_static_defer_e2e_child_v1"
	coroStaticDeferNativeE2ECancel     = "__llgo_coro_static_defer_e2e_cancel_v1"
	coroStaticDeferNativeE2EAccepted   = "__llgo_coro_static_defer_e2e_cancel_accepted_v1"
)

const coroStaticDeferNativeE2ESource = `package main

var NormalLog uint32
var NormalCount uint32
var NormalLocalAfter uint32
var CancelLog uint32
var CancelCount uint32
var CancelLocalAfter uint32
var ChildRegistered uint32
var ChildAfterLoop uint32
var StopChild uint32
var MainSpins uint32
var ChildSpins uint32

func recordNormal(value uint32) {
	NormalLog = NormalLog*10 + value
	NormalCount++
}

func recordCancel(value uint32) {
	CancelLog = CancelLog*10 + value
	CancelCount++
}

func canceledChild() {
	value := uint32(3)
	defer recordCancel(value)
	value++
	defer recordCancel(value)
	value = 9
	CancelLocalAfter = value
	ChildRegistered = 1
	for StopChild == 0 {
		ChildSpins++
	}
	ChildAfterLoop = 1
}

func main() {
	value := ChildRegistered + 1
	defer recordNormal(value)
	value++
	defer recordNormal(value)
	value = 9
	NormalLocalAfter = value
	go canceledChild()
	for CancelCount == 0 {
		MainSpins++
	}
}
`

// TestCoroStaticPlainDeferNativeNoStdlibRuntimeE2E executes both terminal
// paths through production PhysicalABIV1 frames. Main drains two static plain
// cleanup records on normal return. Its spawned child first proves both records
// were registered, then remains in a scheduler-polled loop. A test-only host
// boundary asks the production owner P to RequestTaskCancellation; the next
// production scheduler slice resumes the compiler cancellation gate. Main does
// not return until that child has drained both records, so command-main return
// retains its normal direct-destroy semantics for unrelated background Gs.
func TestCoroStaticPlainDeferNativeNoStdlibRuntimeE2E(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native coroutine static-defer E2E requires Darwin or Linux")
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

	userObject, anchor := buildCoroStaticDeferNativeE2EUser(t, prog, temp)
	entryObject := buildCoroStaticDeferNativeE2EEntry(t, prog, temp, anchor)
	checksObject, setupSymbol, checkSymbol := buildCoroStaticDeferNativeE2EChecks(t, prog, temp)
	driverObject := buildCoroSpawnNativeE2EDriver(t, prog, temp, setupSymbol, checkSymbol)
	runtimeObjects := buildCoroSpawnNativeE2ERuntimeIsland(t, temp)
	runtimeArchive := filepath.Join(temp, "libllgo-coro-static-defer-runtime-island.a")
	arArgs := append([]string{"rcs", runtimeArchive}, runtimeObjects...)
	if output, err := exec.Command(ar, arArgs...).CombinedOutput(); err != nil {
		t.Fatalf("archive coroutine static-defer runtime island: %v\n%s", err, output)
	}

	executable := filepath.Join(temp, "coro-static-defer-e2e")
	linkArgs := []string{driverObject, entryObject, checksObject, userObject, runtimeArchive, "-o", executable}
	if runtime.GOOS == "darwin" {
		linkArgs = append(linkArgs, "-Wl,-dead_strip")
	} else {
		linkArgs = append(linkArgs, "-Wl,--gc-sections")
	}
	if output, err := exec.Command(clang, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link native coroutine static-defer E2E: %v\n%s", err, output)
	}
	assertCoroStaticDeferNativeE2ELinkedSymbols(t, executable)

	runCtx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(runCtx, executable).CombinedOutput()
	if runCtx.Err() != nil {
		t.Fatalf("native coroutine static-defer E2E timed out: %v\n%s", runCtx.Err(), output)
	}
	if err != nil {
		t.Fatalf("native coroutine static-defer E2E failed: %v\n%s", err, output)
	}
}

func buildCoroStaticDeferNativeE2EUser(
	t *testing.T,
	prog llssa.Program,
	temp string,
) (object, anchor string) {
	t.Helper()
	ssaPkg, files := buildCoroPlanTestPackage(t, coroSpawnNativeE2EPackage, coroStaticDeferNativeE2ESource, nil)
	universe, err := cl.PrepareEmissionUniverseWithOptions(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: coroSpawnNativeE2EPackage,
	}}, cl.EmissionUniverseOptions{CoroProfile: cl.CoroProfileStackless})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	mainFn, childFn := ssaPkg.Func("main"), ssaPkg.Func("canceledChild")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{
		Function: mainFn, Demand: coro.AsyncDemand,
	}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
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
	for _, name := range []string{"recordNormal", "recordCancel"} {
		target := ssaPkg.Func(name)
		certified, err := universe.CoroStaticCleanupPlainTarget(plan, target, "")
		if err != nil || !certified {
			t.Fatalf("plain static cleanup target %s certified=%t, err=%v", name, certified, err)
		}
	}
	compilation := &cl.Compilation{
		CoroPlan: plan,

		CoroABI:          coro.PhysicalABIV1,
		SchedulerABI:     coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI:         coro.PanicExplicitStatusABIV0,
		FuncRepABI:       coro.FuncRepABIV1,
		EmissionUniverse: universe, CoroProfile: cl.CoroProfileStackless,
	}
	pkg, _, err := cl.NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		cl.PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	spawnBegin := module.NamedFunction("__llgo_coro_spawn_begin_v1")
	if spawnBegin.IsNil() || !spawnBegin.IsDeclaration() {
		t.Fatalf("compiled static-defer E2E module has no spawn-begin declaration:\n%s", module.String())
	}
	spawnBegin.SetName(coroStaticDeferNativeE2ESpawnBegin)
	runCoroSpawnNativeE2EPasses(t, prog, module)
	ir := module.String()
	match := regexp.MustCompile(`@"?(__llgo_coro_root_package_v1\.[0-9a-f]{32})"?\s*=`).FindStringSubmatch(ir)
	if len(match) != 2 {
		t.Fatalf("compiled static-defer E2E module has no root package anchor:\n%s", ir)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, module, filepath.Join(temp, "static-defer-user.o")), match[1]
}

func buildCoroStaticDeferNativeE2EEntry(t *testing.T, prog llssa.Program, temp, anchor string) string {
	t.Helper()
	conf := &Config{
		BuildMode: BuildModeExe,
		Goos:      runtime.GOOS,
		Goarch:    runtime.GOARCH, CoroProfile: CoroProfileStackless,
	}
	ctx := &context{prog: prog, buildConf: conf}
	bootstrap := &coroProgramBootstrapV1{
		Version: coroProgramBootstrapVersionV2,
		Steps: []coroProgramBootstrapStepV1{
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleRuntimeInitV2, FunctionID: "static-defer-e2e-runtime-init", Target: "__llgo_coro_static_defer_e2e_runtime_init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2, FunctionID: "static-defer-e2e-abi-init", Target: "init$abitypes"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2, FunctionID: coroProgramPublicRuntimeNoopIDV2, Target: coroProgramPublicRuntimeNoopSymbolV2},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePackageInitV2, FunctionID: "static-defer-e2e-package-init", Target: "__llgo_coro_static_defer_e2e_package_init"},
			{
				Kind: coroProgramStepCoroRootV1, Role: coroProgramStepRoleMainV2,
				FunctionID: "static-defer-e2e-main", Target: coroSpawnNativeE2EPackage + ".main$coro",
				Owner: coroSpawnNativeE2EPackage, CatalogTarget: anchor,
			},
		},
	}
	var programHash [16]byte
	for index := range programHash {
		programHash[index] = byte(0x70 + index)
	}
	entry := genMainModule(ctx, llssa.PkgRuntime, &packages.Package{
		ID: coroSpawnNativeE2EPackage, PkgPath: coroSpawnNativeE2EPackage, ExportFile: "coro-static-defer-e2e.a",
	}, &genConfig{
		coroRootAnchors:  []string{anchor},
		coroManifestHash: programHash,
		coroBootstrap:    bootstrap,
	})
	for _, name := range []string{"__llgo_coro_static_defer_e2e_runtime_init", "__llgo_coro_static_defer_e2e_package_init"} {
		fn := entry.LPkg.FuncOf(name)
		if fn == nil {
			t.Fatalf("entry module has no bounded static-defer E2E init declaration %q", name)
		}
		if !fn.HasBody() {
			fn.MakeBody(1).Return()
		}
	}
	module := entry.LPkg.Module()
	entryMain := module.NamedFunction("main")
	if entryMain.IsNil() {
		t.Fatalf("entry module has no native main:\n%s", entry.LPkg.String())
	}
	entryMain.SetName(coroSpawnNativeE2EEntry)
	for original, replacement := range map[string]string{
		coroProgramRunSliceSymbolV2:      coroStaticDeferNativeE2ERun,
		coroProgramContinueSliceSymbolV2: coroStaticDeferNativeE2EContinue,
	} {
		function := module.NamedFunction(original)
		if function.IsNil() || !function.IsDeclaration() {
			t.Fatalf("entry module has no program driver declaration %q:\n%s", original, entry.LPkg.String())
		}
		function.SetName(replacement)
	}
	if err := lowerCoroControlWrappers(ctx, entry.LPkg); err != nil {
		t.Fatal(err)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, module, filepath.Join(temp, "static-defer-entry.o"))
}

// buildCoroStaticDeferNativeE2EChecks keeps test-only Setup/Check functions
// outside the explicit-status source plan. They read the source module's Go
// globals after the production entry loop has completed explicit task
// cancellation and normal main return. It also owns the narrow host-boundary
// observer/wrappers used to issue that cancellation between scheduler slices.
func buildCoroStaticDeferNativeE2EChecks(
	t *testing.T,
	prog llssa.Program,
	temp string,
) (object, setupSymbol, checkSymbol string) {
	t.Helper()
	pkg := prog.NewPackage("coro-static-defer-e2e-checks", "coro-static-defer-e2e-checks")
	defer pkg.Module().Dispose()
	pointer := types.Typ[types.UnsafePointer]
	uint32Type := types.Typ[types.Uint32]
	int32Type := types.Typ[types.Int32]
	global := func(name string) llssa.Expr {
		return pkg.NewVar(
			coroSpawnNativeE2EPackage+"."+name,
			types.NewPointer(uint32Type),
			llssa.InGo,
		).Expr
	}
	normalLog := global("NormalLog")
	normalCount := global("NormalCount")
	normalLocalAfter := global("NormalLocalAfter")
	cancelLog := global("CancelLog")
	cancelCount := global("CancelCount")
	cancelLocalAfter := global("CancelLocalAfter")
	childRegistered := global("ChildRegistered")
	childAfterLoop := global("ChildAfterLoop")
	childSpins := global("ChildSpins")

	observedChild := pkg.NewVar(coroStaticDeferNativeE2EChild, types.NewPointer(pointer), llssa.InC)
	observedChild.InitNil()
	cancelIssued := pkg.NewVar(coroStaticDeferNativeE2ECancel, types.NewPointer(uint32Type), llssa.InC)
	cancelIssued.InitNil()
	cancelAccepted := pkg.NewVar(coroStaticDeferNativeE2EAccepted, types.NewPointer(uint32Type), llssa.InC)
	cancelAccepted.InitNil()

	spawnBeginSignature := newSignature([]types.Type{pointer}, []types.Type{pointer})
	productionSpawnBegin := pkg.NewFunc("__llgo_coro_spawn_begin_v1", spawnBeginSignature, llssa.InC)
	observeSpawnBegin := pkg.NewFunc(coroStaticDeferNativeE2ESpawnBegin, spawnBeginSignature, llssa.InC)
	observeBody := observeSpawnBegin.MakeBody(1)
	child := observeBody.Call(productionSpawnBegin.Expr, observeSpawnBegin.Param(0))
	observeBody.Store(observedChild.Expr, child)
	observeBody.Return(child)

	programP := pkg.NewVar(
		"command-line-arguments.coroProgramPV1State",
		types.NewPointer(pointer),
		llssa.InGo,
	)
	requestCancel := pkg.NewFunc(
		"github.com/goplus/llgo/runtime/internal/coro.RequestTaskCancellation",
		newSignature([]types.Type{pointer, pointer, types.Typ[types.Uint8]}, []types.Type{types.Typ[types.Bool]}),
		llssa.InGo,
	)
	exit := pkg.NewFunc("exit", newSignature([]types.Type{int32Type}, nil), llssa.InC)
	maybeCancel := pkg.NewFunc("__llgo_coro_static_defer_e2e_maybe_cancel_v1", newSignature(nil, nil), llssa.InC)
	maybeBody := maybeCancel.MakeBody(7)
	checkIssued := maybeCancel.Block(1)
	checkChild := maybeCancel.Block(2)
	request := maybeCancel.Block(3)
	accepted := maybeCancel.Block(4)
	failed := maybeCancel.Block(5)
	done := maybeCancel.Block(6)
	zero32 := prog.IntVal(0, prog.Uint32())
	one32 := prog.IntVal(1, prog.Uint32())
	maybeBody.If(maybeBody.BinOp(token.NEQ, maybeBody.Load(childRegistered), zero32), checkIssued, done)
	maybeBody.SetBlock(checkIssued).If(
		maybeBody.BinOp(token.EQL, maybeBody.Load(cancelIssued.Expr), zero32), checkChild, done,
	)
	loadedChild := maybeBody.SetBlock(checkChild).Load(observedChild.Expr)
	maybeBody.If(
		maybeBody.BinOp(token.NEQ, loadedChild, prog.Nil(prog.VoidPtr())), request, failed,
	)
	requested := maybeBody.SetBlock(request).Call(
		requestCancel.Expr,
		maybeBody.Convert(prog.VoidPtr(), programP.Expr),
		loadedChild,
		prog.IntVal(1, prog.Byte()),
	)
	maybeBody.If(requested, accepted, failed)
	maybeBody.SetBlock(accepted).Store(cancelIssued.Expr, one32)
	maybeBody.Store(cancelAccepted.Expr, one32)
	maybeBody.Return()
	maybeBody.SetBlock(failed).Call(exit.Expr, prog.IntVal(71, prog.Int32()))
	maybeBody.Return()
	maybeBody.SetBlock(done).Return()

	runResultFields := make([]*types.Var, 8)
	for index := range runResultFields {
		runResultFields[index] = types.NewField(token.NoPos, nil, fmt.Sprintf("Word%d", index), uint32Type, false)
	}
	runResultType := types.NewStruct(runResultFields, nil)
	runResultPointer := types.NewPointer(runResultType)
	runSignature := newSignature(
		[]types.Type{pointer, pointer, uint32Type, runResultPointer},
		[]types.Type{uint32Type},
	)
	productionRun := pkg.NewFunc(coroProgramRunSliceSymbolV2, runSignature, llssa.InC)
	run := pkg.NewFunc(coroStaticDeferNativeE2ERun, runSignature, llssa.InC)
	runBody := run.MakeBody(1)
	runStatus := runBody.Call(
		productionRun.Expr, run.Param(0), run.Param(1), run.Param(2), run.Param(3),
	)
	runBody.Call(maybeCancel.Expr)
	runBody.Return(runStatus)

	continueSignature := newSignature(
		[]types.Type{uint32Type, uint32Type, uint32Type, uint32Type, runResultPointer},
		[]types.Type{uint32Type},
	)
	productionContinue := pkg.NewFunc(coroProgramContinueSliceSymbolV2, continueSignature, llssa.InC)
	continueRun := pkg.NewFunc(coroStaticDeferNativeE2EContinue, continueSignature, llssa.InC)
	continueBody := continueRun.MakeBody(1)
	continueStatus := continueBody.Call(
		productionContinue.Expr,
		continueRun.Param(0),
		continueRun.Param(1),
		continueRun.Param(2),
		continueRun.Param(3),
		continueRun.Param(4),
	)
	continueBody.Call(maybeCancel.Expr)
	continueBody.Return(continueStatus)

	setupSymbol = coroSpawnNativeE2EPackage + ".Setup"
	setup := pkg.NewFunc(setupSymbol, newSignature(nil, nil), llssa.InGo)
	setup.MakeBody(1).Return()
	checkSymbol = coroSpawnNativeE2EPackage + ".Check"
	check := pkg.NewFunc(checkSymbol, newSignature(nil, []types.Type{int32Type}), llssa.InGo)
	body := check.MakeBody(16)
	normalCountBlock, registeredBlock := check.Block(1), check.Block(2)
	normalLocalBlock, spinsBlock := check.Block(3), check.Block(4)
	cancelIssuedBlock, cancelAcceptedBlock := check.Block(5), check.Block(6)
	cancelLogBlock, cancelCountBlock := check.Block(7), check.Block(8)
	cancelLocalBlock, afterBlock := check.Block(9), check.Block(10)
	successBlock := check.Block(11)
	failNormal, failRegistered := check.Block(12), check.Block(13)
	failRequest, failCancel := check.Block(14), check.Block(15)
	uint32Value := func(value uint64) llssa.Expr { return prog.IntVal(value, prog.Uint32()) }
	int32Value := func(value uint64) llssa.Expr { return prog.IntVal(value, prog.Int32()) }

	body.If(body.BinOp(token.EQL, body.Load(normalLog), uint32Value(21)), normalCountBlock, failNormal)
	body.SetBlock(normalCountBlock).If(
		body.BinOp(token.EQL, body.Load(normalCount), uint32Value(2)), normalLocalBlock, failNormal,
	)
	body.SetBlock(normalLocalBlock).If(
		body.BinOp(token.EQL, body.Load(normalLocalAfter), uint32Value(9)), registeredBlock, failNormal,
	)
	body.SetBlock(registeredBlock).If(
		body.BinOp(token.EQL, body.Load(childRegistered), uint32Value(1)), spinsBlock, failRegistered,
	)
	body.SetBlock(spinsBlock).If(
		body.BinOp(token.NEQ, body.Load(childSpins), uint32Value(0)), cancelIssuedBlock, failRegistered,
	)
	body.SetBlock(cancelIssuedBlock).If(
		body.BinOp(token.EQL, body.Load(cancelIssued.Expr), uint32Value(1)), cancelAcceptedBlock, failRequest,
	)
	body.SetBlock(cancelAcceptedBlock).If(
		body.BinOp(token.EQL, body.Load(cancelAccepted.Expr), uint32Value(1)), cancelLogBlock, failRequest,
	)
	body.SetBlock(cancelLogBlock).If(
		body.BinOp(token.EQL, body.Load(cancelLog), uint32Value(43)), cancelCountBlock, failCancel,
	)
	body.SetBlock(cancelCountBlock).If(
		body.BinOp(token.EQL, body.Load(cancelCount), uint32Value(2)), cancelLocalBlock, failCancel,
	)
	body.SetBlock(cancelLocalBlock).If(
		body.BinOp(token.EQL, body.Load(cancelLocalAfter), uint32Value(9)), afterBlock, failCancel,
	)
	body.SetBlock(afterBlock).If(
		body.BinOp(token.EQL, body.Load(childAfterLoop), uint32Value(0)), successBlock, failCancel,
	)
	body.SetBlock(successBlock).Return(int32Value(0))
	body.SetBlock(failNormal).Return(int32Value(11))
	body.SetBlock(failRegistered).Return(int32Value(12))
	body.SetBlock(failRequest).Return(int32Value(13))
	body.SetBlock(failCancel).Return(int32Value(14))
	pkg.MaterializePreserveSyms()
	return emitCoroSpawnNativeE2EObject(
		t, prog, pkg.Module(), filepath.Join(temp, "static-defer-checks.o"),
	), setupSymbol, checkSymbol
}

func assertCoroStaticDeferNativeE2ELinkedSymbols(t *testing.T, executable string) {
	t.Helper()
	nm, err := exec.LookPath("nm")
	if err != nil {
		t.Log("nm is unavailable; continuing without the linked static-defer symbol audit")
		return
	}
	output, err := exec.Command(nm, executable).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect linked coroutine static-defer E2E: %v\n%s", err, output)
	}
	symbols := string(output)
	for _, required := range []string{
		coroStaticDeferNativeE2ESpawnBegin,
		coroStaticDeferNativeE2ERun,
		coroStaticDeferNativeE2EContinue,
		"__llgo_coro_spawn_begin_v1",
		"github.com/goplus/llgo/runtime/internal/coro.RequestTaskCancellation",
		coroSpawnNativeE2EPackage + ".main$coro",
		coroSpawnNativeE2EPackage + ".canceledChild$coro",
	} {
		if !strings.Contains(symbols, required) {
			t.Fatalf("linked coroutine static-defer E2E is missing %q:\n%s", required, symbols)
		}
	}
	for _, forbidden := range []string{
		"github.com/goplus/llgo/runtime/internal/runtime.Rethrow",
		"github.com/goplus/llgo/runtime/internal/runtime.TracePanic",
		"github.com/goplus/llgo/runtime/internal/runtime.printany",
	} {
		if strings.Contains(symbols, forbidden) {
			t.Fatalf("static-defer E2E unexpectedly linked legacy PanicABI symbol %q", forbidden)
		}
	}
}
