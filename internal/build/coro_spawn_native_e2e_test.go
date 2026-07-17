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
	"go/types"
	"os"
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
	llvm "github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const (
	coroSpawnNativeE2EPackage = "example.com/llgo-coro-spawn-e2e"
	coroSpawnNativeE2EEntry   = "__llgo_coro_spawn_e2e_entry"
)

const coroSpawnNativeE2ESource = `package main

var Before uint32
var After uint32
var Leaf uint32

func leaf() { Leaf = 1 }

func child() {
	Before = 1
	go leaf()
	After = 1
}

func main() { go child() }

func Check() int32 {
	if Before != 1 {
		return 11
	}
	if After != 0 {
		return 12
	}
	if Leaf != 0 {
		return 13
	}
	return 0
}
`

// TestCoroClosedStaticSpawnNativeNoStdlibRuntimeE2E is deliberately a
// scheduler-island smoke test, not a claim that the complete standard-library
// runtime startup or its legacy PanicABI is coroutine-safe. The compiler emits
// the real closed-static-go lowering and the real V2 entry/factory/control
// wrappers. The first four V2 init stages are bounded no-ops, while the linked
// production coroutine adapter/core uses its native nogc allocator backend.
//
// The two nested spawns make the result deterministic without a timer source:
// main yields to child, child publishes leaf and yields back behind main, and
// main then returns with leaf initial-suspended and child yield-suspended.
// Command shutdown must destroy both instead of resuming either one.
func TestCoroClosedStaticSpawnNativeNoStdlibRuntimeE2E(t *testing.T) {
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
	defer prog.Dispose()

	userObject, anchor, checkSymbol := buildCoroSpawnNativeE2EUser(t, prog, temp)
	entryObject := buildCoroSpawnNativeE2EEntry(t, prog, temp, anchor)
	driverObject := buildCoroSpawnNativeE2EDriver(t, prog, temp, checkSymbol)
	runtimeObjects := buildCoroSpawnNativeE2ERuntimeIsland(t, temp)
	runtimeArchive := filepath.Join(temp, "libllgo-coro-runtime-island.a")
	arArgs := append([]string{"rcs", runtimeArchive}, runtimeObjects...)
	if output, err := exec.Command(ar, arArgs...).CombinedOutput(); err != nil {
		t.Fatalf("archive coroutine runtime island: %v\n%s", err, output)
	}

	executable := filepath.Join(temp, "coro-spawn-e2e")
	linkArgs := []string{driverObject, entryObject, userObject, runtimeArchive, "-o", executable}
	if runtime.GOOS == "darwin" {
		linkArgs = append(linkArgs, "-Wl,-dead_strip")
	} else {
		linkArgs = append(linkArgs, "-Wl,--gc-sections")
	}
	if output, err := exec.Command(clang, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link native coroutine spawn/shutdown smoke: %v\n%s", err, output)
	}
	assertCoroSpawnNativeE2ELinkedSymbols(t, executable)

	runCtx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(runCtx, executable).CombinedOutput()
	if runCtx.Err() != nil {
		t.Fatalf("native coroutine spawn/shutdown smoke timed out: %v\n%s", runCtx.Err(), output)
	}
	if err != nil {
		t.Fatalf("native coroutine spawn/shutdown smoke failed: %v\n%s", err, output)
	}
}

func buildCoroSpawnNativeE2EUser(t *testing.T, prog llssa.Program, temp string) (object, anchor, checkSymbol string) {
	t.Helper()
	ssaPkg, files := buildCoroPlanTestPackage(t, coroSpawnNativeE2EPackage, coroSpawnNativeE2ESource, nil)
	universe, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: coroSpawnNativeE2EPackage,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	mainFn, childFn, leafFn, checkFn := ssaPkg.Func("main"), ssaPkg.Func("child"), ssaPkg.Func("leaf"), ssaPkg.Func("Check")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: mainFn, Demand: coro.AsyncDemand},
		{Function: checkFn, Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			switch fn {
			case mainFn, childFn, leafFn:
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			default:
				return coro.SSAFunctionPolicy{}, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compilation := &cl.Compilation{
		CoroPlan:                      plan,
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroClosedStaticSpawn:   true,
		EnableCoroProgramBootstrapRun: true,
		CoroABI:                       coro.PhysicalABIV1,
		SchedulerABI:                  coro.SchedulerProgramBootstrapClosedStaticSpawnABIV0,
		PanicABI:                      coro.PanicLegacyABIV0,
		FuncRepABI:                    coro.FuncRepABIV0,
		EmissionUniverse:              universe,
	}
	pkg, _, err := cl.NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		cl.PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	runCoroSpawnNativeE2EPasses(t, prog, module)
	ir := module.String()
	match := regexp.MustCompile(`@"?(__llgo_coro_root_package_v1\.[0-9a-f]{32})"?\s*=`).FindStringSubmatch(ir)
	if len(match) != 2 {
		t.Fatalf("compiled E2E user module has no root package anchor:\n%s", ir)
	}
	checkSymbol = coroSpawnNativeE2EPackage + ".Check"
	if module.NamedFunction(checkSymbol).IsNil() {
		t.Fatalf("compiled E2E user module has no plain checker %q:\n%s", checkSymbol, ir)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, module, filepath.Join(temp, "user.o")), match[1], checkSymbol
}

func buildCoroSpawnNativeE2EEntry(t *testing.T, prog llssa.Program, temp, anchor string) string {
	t.Helper()
	conf := &Config{
		BuildMode:                     BuildModeExe,
		Goos:                          runtime.GOOS,
		Goarch:                        runtime.GOARCH,
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroClosedStaticSpawn:   true,
		EnableCoroProgramBootstrapABI: true,
		EnableCoroProgramBootstrapRun: true,
	}
	ctx := &context{prog: prog, buildConf: conf}
	bootstrap := &coroProgramBootstrapV1{
		Version: coroProgramBootstrapVersionV2,
		Steps: []coroProgramBootstrapStepV1{
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleRuntimeInitV2, FunctionID: "e2e-runtime-init", Target: "__llgo_coro_e2e_runtime_init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2, FunctionID: "e2e-abi-init", Target: "init$abitypes"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2, FunctionID: coroProgramPublicRuntimeNoopIDV2, Target: coroProgramPublicRuntimeNoopSymbolV2},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePackageInitV2, FunctionID: "e2e-package-init", Target: "__llgo_coro_e2e_package_init"},
			{
				Kind: coroProgramStepCoroRootV1, Role: coroProgramStepRoleMainV2,
				FunctionID: "e2e-main", Target: coroSpawnNativeE2EPackage + ".main$coro",
				Owner: coroSpawnNativeE2EPackage, CatalogTarget: anchor, Aux: 0,
			},
		},
	}
	var programHash [16]byte
	for i := range programHash {
		programHash[i] = byte(i + 1)
	}
	entry := genMainModule(ctx, llssa.PkgRuntime, &packages.Package{
		ID: coroSpawnNativeE2EPackage, PkgPath: coroSpawnNativeE2EPackage, ExportFile: "coro-spawn-e2e.a",
	}, &genConfig{
		coroRootAnchors:  []string{anchor},
		coroManifestHash: programHash,
		coroBootstrap:    bootstrap,
	})
	for _, name := range []string{"__llgo_coro_e2e_runtime_init", "__llgo_coro_e2e_package_init"} {
		fn := entry.LPkg.FuncOf(name)
		if fn == nil {
			t.Fatalf("entry module has no bounded E2E init declaration %q", name)
		}
		if !fn.HasBody() {
			body := fn.MakeBody(1)
			body.Return()
		}
	}
	entryMain := entry.LPkg.Module().NamedFunction("main")
	if entryMain.IsNil() {
		t.Fatalf("entry module has no native main:\n%s", entry.LPkg.String())
	}
	entryMain.SetName(coroSpawnNativeE2EEntry)
	if err := lowerCoroControlWrappers(ctx, entry.LPkg); err != nil {
		t.Fatal(err)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, entry.LPkg.Module(), filepath.Join(temp, "entry.o"))
}

func buildCoroSpawnNativeE2EDriver(t *testing.T, prog llssa.Program, temp, checkSymbol string) string {
	t.Helper()
	pkg := prog.NewPackage("coro-spawn-e2e-driver", "coro-spawn-e2e-driver")
	defer pkg.Module().Dispose()
	pointer := types.Typ[types.UnsafePointer]
	entry := pkg.NewFunc(coroSpawnNativeE2EEntry, newSignature(
		[]types.Type{types.Typ[types.Int32], pointer}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	check := pkg.NewFunc(checkSymbol, newSignature(nil, []types.Type{types.Typ[types.Int32]}), llssa.InGo)
	// The production scheduler core is intentionally compiled without the full
	// standard-library runtime package in its coroutine plan. LLGo's ordinary
	// pointer checks name this legacy helper even though every valid scheduler
	// path passes false. Keep the test island fail-stop without pulling the
	// legacy panic/printing closure into the final executable.
	abort := pkg.NewFunc("abort", newSignature(nil, nil), llssa.InC)
	defineCoroNativeE2ENilDerefStubs(prog, pkg, abort)
	// Fixed-capacity executor/wait registries intentionally keep explicit Go
	// bounds checks. The complete runtime would report those through the normal
	// panic path; this closed island instead aborts on the impossible invalid
	// branch without linking that unrelated runtime closure.
	checkIndexRange := pkg.NewFunc(llssa.PkgRuntime+".CheckIndexRange", newSignature(
		[]types.Type{types.Typ[types.Bool], types.Typ[types.Int64], types.Typ[types.Bool], types.Typ[types.Int]}, nil,
	), llssa.InGo)
	rangeBody := checkIndexRange.MakeBody(3)
	rangeFail, rangeValid := checkIndexRange.Block(1), checkIndexRange.Block(2)
	rangeBody.If(checkIndexRange.Param(0), rangeFail, rangeValid)
	rangeBody.SetBlock(rangeFail).Call(abort.Expr)
	rangeBody.Return()
	rangeBody.SetBlock(rangeValid).Return()
	// Compiling the complete production core object also leaves relocations for
	// ordinary runtime allocation helpers in currently unreachable panic-status
	// code. Resolve those helpers directly to libc so archive extraction cannot
	// pull the unrelated legacy runtime/Panic/printing object into this island.
	// Frame and task storage still go through the production coroalloc backend.
	uintptrType := types.Typ[types.Uintptr]
	malloc := pkg.NewFunc("malloc", newSignature(
		[]types.Type{uintptrType}, []types.Type{pointer},
	), llssa.InC)
	calloc := pkg.NewFunc("calloc", newSignature(
		[]types.Type{uintptrType, uintptrType}, []types.Type{pointer},
	), llssa.InC)
	allocU := pkg.NewFunc(llssa.PkgRuntime+".AllocU", newSignature(
		[]types.Type{uintptrType}, []types.Type{pointer},
	), llssa.InGo)
	allocUBody := allocU.MakeBody(1)
	allocUBody.Return(allocUBody.Call(malloc.Expr, allocU.Param(0)))
	allocZ := pkg.NewFunc(llssa.PkgRuntime+".AllocZ", newSignature(
		[]types.Type{uintptrType}, []types.Type{pointer},
	), llssa.InGo)
	allocZBody := allocZ.MakeBody(1)
	allocZBody.Return(allocZBody.Call(calloc.Expr, prog.IntVal(1, prog.Uintptr()), allocZ.Param(0)))
	main := pkg.NewFunc("main", newSignature(
		[]types.Type{types.Typ[types.Int32], pointer}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	body := main.MakeBody(1)
	body.Call(entry.Expr, main.Param(0), main.Param(1))
	body.Return(body.Call(check.Expr))
	pkg.MaterializePreserveSyms()
	return emitCoroSpawnNativeE2EObject(t, prog, pkg.Module(), filepath.Join(temp, "driver.o"))
}

func buildCoroSpawnNativeE2ERuntimeIsland(t *testing.T, temp string) []string {
	t.Helper()
	files := []string{
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_allocator.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_frame.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_program.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_run_decision.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_sched.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_executor.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_executor_driver_legacy.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_spawn.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_native_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_wait_pipe_llgo.go"),
	}
	requireCoroRuntimeIslandProductionSource(t, files, "coro_run_decision.go")
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.Tags = "nogc"
	// This source-island compile intentionally does not enable the complete
	// program bootstrap and its whole-program planner. Select its production
	// runtime files through the private compiler channel; the public Config.Tags
	// path must reject this capability as forged.
	conf.compilerBuildTags = []string{"llgo_coro", coroNativePipeBuildTag}
	allowed := map[string]bool{
		"command-line-arguments":                               true,
		"github.com/goplus/llgo/runtime/internal/coro":         true,
		"github.com/goplus/llgo/runtime/internal/coroalloc":    true,
		"github.com/goplus/llgo/runtime/internal/corodoorbell": true,
	}
	seen := make(map[string]bool, len(allowed))
	var objects []string
	conf.ModuleHook = func(pkg Package) {
		if pkg.LPkg == nil || pkg.LPkg.Prog == nil {
			return
		}
		if !allowed[pkg.ID] {
			return
		}
		if seen[pkg.ID] {
			t.Fatalf("production coroutine runtime island emitted duplicate module %q", pkg.ID)
		}
		seen[pkg.ID] = true
		module := pkg.LPkg.Module()
		if module.IsNil() {
			return
		}
		name := fmt.Sprintf("runtime-%03d-%s.o", len(objects), sanitizeCoroSpawnNativeE2EObjectName(pkg.ID))
		objects = append(objects, emitCoroSpawnNativeE2EObject(
			t, pkg.LPkg.Prog, module, filepath.Join(temp, name),
		))
	}
	pkgs, err := Do(files, conf)
	if err != nil {
		t.Fatalf("compile production coroutine runtime island in nogc mode: %v", err)
	}
	if len(pkgs) == 0 || pkgs[0].LPkg == nil {
		t.Fatal("production coroutine runtime island produced no root package")
	}
	pkgs[0].LPkg.Prog.Dispose()
	for id := range allowed {
		if !seen[id] {
			t.Fatalf("production coroutine runtime island did not emit required module %q", id)
		}
	}
	if len(objects) != len(allowed) {
		t.Fatalf("production coroutine runtime island objects = %d, want exactly %d", len(objects), len(allowed))
	}
	return objects
}

func requireCoroRuntimeIslandProductionSource(t *testing.T, files []string, name string) {
	t.Helper()
	want := filepath.Join("..", "..", "runtime", "internal", "runtime", name)
	count := 0
	for _, file := range files {
		if file == want {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("production coroutine runtime island source %q occurs %d times, want exactly one", want, count)
	}
}

func sanitizeCoroSpawnNativeE2EObjectName(name string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_").Replace(name)
}

func assertCoroSpawnNativeE2ELinkedSymbols(t *testing.T, executable string) {
	t.Helper()
	nm, err := exec.LookPath("nm")
	if err != nil {
		t.Skip("nm is unavailable for linked coroutine island audit")
	}
	output, err := exec.Command(nm, executable).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect linked coroutine island: %v\n%s", err, output)
	}
	symbols := string(output)
	for _, required := range []string{
		coroProgramContinueSymbolV1,
		coroNativePostWaitSymbolV1,
		"__llgo_coro_spawn_begin_v1",
		"__llgo_coro_spawn_commit_v1",
		"github.com/goplus/llgo/runtime/internal/coro.CommitSpawn",
		"github.com/goplus/llgo/runtime/internal/coro.BeginCommandShutdown",
	} {
		if !strings.Contains(symbols, required) {
			t.Fatalf("linked coroutine island is missing production symbol %q:\n%s", required, symbols)
		}
	}
	for _, forbidden := range []string{
		"github.com/goplus/llgo/runtime/internal/runtime.Panic",
		"github.com/goplus/llgo/runtime/internal/runtime.Rethrow",
		"github.com/goplus/llgo/runtime/internal/runtime.TracePanic",
		"github.com/goplus/llgo/runtime/internal/runtime.printany",
	} {
		if strings.Contains(symbols, forbidden) {
			t.Fatalf("test-only coroutine island unexpectedly extracted legacy PanicABI symbol %q", forbidden)
		}
	}
}

func runCoroSpawnNativeE2EPasses(t *testing.T, prog llssa.Program, module llvm.Module) {
	t.Helper()
	module.SetDataLayout(prog.DataLayout())
	module.SetTarget(prog.TargetSpec().Triple)
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify E2E coroutine module before CoroSplit: %v\n%s", err, module.String())
	}
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	const pipeline = "coro-early,cgscc(coro-split),coro-cleanup"
	if err := module.RunPasses(pipeline, prog.TargetMachine(), options); err != nil {
		t.Fatalf("run E2E %s: %v\n%s", pipeline, err, module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify E2E coroutine module after CoroSplit: %v\n%s", err, module.String())
	}
}

func emitCoroSpawnNativeE2EObject(t *testing.T, prog llssa.Program, module llvm.Module, path string) string {
	t.Helper()
	module.SetDataLayout(prog.DataLayout())
	module.SetTarget(prog.TargetSpec().Triple)
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify %s before object emission: %v\n%s", filepath.Base(path), err, module.String())
	}
	object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		t.Fatalf("emit %s: %v\n%s", filepath.Base(path), err, module.String())
	}
	defer object.Dispose()
	if err := os.WriteFile(path, object.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
