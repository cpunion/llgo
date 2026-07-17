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
	"golang.org/x/tools/go/ssa"
)

const (
	coroNativeIngressE2EPackage = "example.com/llgo-coro-native-ingress-e2e"
	coroNativeIngressE2EEntry   = "__llgo_coro_native_ingress_e2e_entry"
)

const coroNativeIngressE2ESource = `package main

type WaitToken struct { word uint32 }

var Before uint32
var Published uint32
var After uint32
var Retired uint32
var Failure uint32
var CallbackResult uint32
var PollObserved uint32

var token WaitToken
var ticket uint32
var waitSlot uint32
var waitGeneration uint32
var executorSlot uint32
var executorGeneration uint32

//llgo:link prepare C.__llgo_coro_wait_prepare_v1
func prepare(*WaitToken, *uint32, *uint32, *uint32, *uint32, *uint32) bool

//llgo:link publish C.__llgo_coro_native_ingress_publish_wait_v1
func publish(uint32, uint32, uint32, uint32)

//llgo:link join C.__llgo_coro_native_ingress_join_v1
func join() uint32

//llgo:link pollObserved C.__llgo_coro_native_ingress_poll_observed_v1
func pollObserved() uint32

//llgo:link retire C.__llgo_coro_wait_retire_completed_v1
func retire(*WaitToken, uint32, uint32, uint32) bool

//llgo:link park llgo.coroPark
func park(*WaitToken, uint32)

func main() {
	Before = 1
	if !prepare(&token, &ticket, &waitSlot, &waitGeneration, &executorSlot, &executorGeneration) {
		Failure = 21
		return
	}
	publish(waitSlot, waitGeneration, executorSlot, executorGeneration)
	Published = 1
	park(&token, ticket)
	After = 1
	CallbackResult = join()
	if CallbackResult != 0x0201 {
		Failure = 22
		return
	}
	PollObserved = pollObserved()
	if PollObserved != 1 {
		Failure = 23
		return
	}
	if !retire(&token, ticket, waitSlot, waitGeneration) {
		Failure = 24
		return
	}
	Retired = 1
}

func Check() int32 {
	if Failure != 0 { return int32(Failure) }
	if Before != 1 { return 31 }
	if Published != 1 { return 32 }
	if After != 1 { return 33 }
	if Retired != 1 { return 34 }
	if CallbackResult != 0x0201 { return 35 }
	if PollObserved != 1 { return 36 }
	return 0
}
`

const coroNativeIngressE2ECSource = `
#include <pthread.h>
#include <sched.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>

extern uint32_t __llgo_coro_native_post_wait_v1(uint32_t, uint32_t, uint32_t, uint32_t);
extern uint32_t __llgo_coro_native_ingress_audit_closed_v1(void);

struct wait_pod_v1 {
	uint32_t wait_slot;
	uint32_t wait_generation;
	uint32_t executor_slot;
	uint32_t executor_generation;
};

static struct wait_pod_v1 wait_pod;
static pthread_t producer_thread;
static _Atomic uint32_t started;
static _Atomic uint32_t published;
static _Atomic uint32_t poll_release;
static _Atomic uint32_t poll_observed;
static _Atomic uint32_t callback_done;
static _Atomic uint32_t callback_result;
static _Atomic uint32_t joined;

static void fail_stop(void) {
	abort();
}

static void *producer_main(void *unused) {
	(void)unused;
	while (atomic_load_explicit(&published, memory_order_acquire) == 0) {
		sched_yield();
	}
	while (atomic_load_explicit(&poll_release, memory_order_acquire) == 0) {
		sched_yield();
	}
	uint32_t result = __llgo_coro_native_post_wait_v1(
		wait_pod.wait_slot,
		wait_pod.wait_generation,
		wait_pod.executor_slot,
		wait_pod.executor_generation
	);
	atomic_store_explicit(&callback_result, result, memory_order_release);
	atomic_store_explicit(&callback_done, 1, memory_order_release);
	return 0;
}

void __llgo_coro_native_ingress_start_v1(void) {
	uint32_t expected = 0;
	if (!atomic_compare_exchange_strong_explicit(
			&started, &expected, 1, memory_order_acq_rel, memory_order_acquire) ||
		pthread_create(&producer_thread, 0, producer_main, 0) != 0) {
		fail_stop();
	}
}

void __llgo_coro_native_ingress_publish_wait_v1(
	uint32_t wait_slot,
	uint32_t wait_generation,
	uint32_t executor_slot,
	uint32_t executor_generation
) {
	if (atomic_load_explicit(&started, memory_order_acquire) != 1 ||
		atomic_load_explicit(&published, memory_order_acquire) != 0 ||
		wait_slot == 0 || wait_generation == 0 || executor_slot == 0 || executor_generation == 0) {
		fail_stop();
	}
	wait_pod.wait_slot = wait_slot;
	wait_pod.wait_generation = wait_generation;
	wait_pod.executor_slot = executor_slot;
	wait_pod.executor_generation = executor_generation;
	atomic_store_explicit(&published, 1, memory_order_release);
}

uint32_t __llgo_coro_native_ingress_before_poll_v1(void) {
	if (atomic_load_explicit(&poll_observed, memory_order_acquire) == 1) {
		return atomic_load_explicit(&callback_done, memory_order_acquire) == 1 ? 1 : 0;
	}
	uint32_t expected = 0;
	if (atomic_load_explicit(&published, memory_order_acquire) != 1 ||
		!atomic_compare_exchange_strong_explicit(
			&poll_observed, &expected, 1, memory_order_acq_rel, memory_order_acquire)) {
		return 0;
	}
	atomic_store_explicit(&poll_release, 1, memory_order_release);
	while (atomic_load_explicit(&callback_done, memory_order_acquire) == 0) {
		sched_yield();
	}
	return 1;
}

uint32_t __llgo_coro_native_ingress_join_v1(void) {
	if (atomic_load_explicit(&callback_done, memory_order_acquire) != 1 ||
		pthread_join(producer_thread, 0) != 0) {
		return UINT32_MAX;
	}
	atomic_store_explicit(&joined, 1, memory_order_release);
	return atomic_load_explicit(&callback_result, memory_order_acquire);
}

uint32_t __llgo_coro_native_ingress_poll_observed_v1(void) {
	return atomic_load_explicit(&poll_observed, memory_order_acquire);
}

void __llgo_coro_native_ingress_verify_closed_v1(void) {
	if (atomic_load_explicit(&joined, memory_order_acquire) != 1 ||
		atomic_load_explicit(&callback_result, memory_order_acquire) != 0x0201u ||
		__llgo_coro_native_post_wait_v1(
			wait_pod.wait_slot,
			wait_pod.wait_generation,
			wait_pod.executor_slot,
			wait_pod.executor_generation
		) != 0x0403u ||
		__llgo_coro_native_ingress_audit_closed_v1() != 0) {
		fail_stop();
	}
}
`

func TestCoroNativeProducerIngressNoGCProductionPollE2E(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native coroutine ingress E2E requires Darwin or Linux")
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

	userObject, anchor, checkSymbol := buildCoroNativeIngressE2EUser(t, prog, temp)
	entryObject := buildCoroNativeIngressE2EEntry(t, prog, temp, anchor)
	driverObject := buildCoroNativeIngressE2EDriver(t, prog, temp, checkSymbol)
	syncObject := buildCoroNativeIngressE2ECDriver(t, clang, temp)
	runtimeObjects := buildCoroNativeIngressE2ERuntimeIsland(t, temp)
	runtimeArchive := filepath.Join(temp, "libllgo-coro-native-ingress.a")
	if output, err := exec.Command(ar, append([]string{"rcs", runtimeArchive}, runtimeObjects...)...).CombinedOutput(); err != nil {
		t.Fatalf("archive coroutine native ingress runtime: %v\n%s", err, output)
	}

	executable := filepath.Join(temp, "coro-native-ingress-e2e")
	linkArgs := []string{driverObject, entryObject, userObject, syncObject, runtimeArchive, "-pthread", "-o", executable}
	if runtime.GOOS == "darwin" {
		linkArgs = append(linkArgs, "-Wl,-dead_strip")
	} else {
		linkArgs = append(linkArgs, "-Wl,--gc-sections")
	}
	if output, err := exec.Command(clang, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link native coroutine ingress E2E: %v\n%s", err, output)
	}
	assertCoroNativeIngressE2ELinkedSymbols(t, executable)

	runCtx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(runCtx, executable).CombinedOutput()
	if runCtx.Err() != nil {
		t.Fatalf("native coroutine ingress E2E timed out: %v\n%s", runCtx.Err(), output)
	}
	if err != nil {
		t.Fatalf("native coroutine ingress E2E failed: %v\n%s", err, output)
	}
}

func buildCoroNativeIngressE2EUser(t *testing.T, prog llssa.Program, temp string) (object, anchor, checkSymbol string) {
	t.Helper()
	ssaPkg, files := buildCoroPlanTestPackage(t, coroNativeIngressE2EPackage, coroNativeIngressE2ESource, nil)
	universe, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: coroNativeIngressE2EPackage,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	mainFn, checkFn := ssaPkg.Func("main"), ssaPkg.Func("Check")
	knownExternal := make(map[*ssa.Function]bool)
	for _, name := range []string{"prepare", "publish", "join", "pollObserved", "retire"} {
		knownExternal[ssaPkg.Func(name)] = true
	}
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
			switch {
			case fn == mainFn:
				return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
			case knownExternal[fn]:
				return coro.SSAFunctionPolicy{
					Effect: coro.NoSuspend, IgnoreBody: true, External: coro.ExternalKnown, OverrideExternal: true,
				}, nil
			default:
				return coro.SSAFunctionPolicy{}, nil
			}
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			return intrinsic && semantics.ElidesManagedCall(), err
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
		t.Fatalf("compiled native ingress user module has no root package anchor:\n%s", ir)
	}
	checkSymbol = coroNativeIngressE2EPackage + ".Check"
	if module.NamedFunction(checkSymbol).IsNil() {
		t.Fatalf("compiled native ingress user module has no checker %q:\n%s", checkSymbol, ir)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, module, filepath.Join(temp, "ingress-user.o")), match[1], checkSymbol
}

func buildCoroNativeIngressE2EEntry(t *testing.T, prog llssa.Program, temp, anchor string) string {
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
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleRuntimeInitV2, FunctionID: "ingress-e2e-runtime-init", Target: "__llgo_coro_ingress_e2e_runtime_init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2, FunctionID: "ingress-e2e-abi-init", Target: "init$abitypes"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2, FunctionID: coroProgramPublicRuntimeNoopIDV2, Target: coroProgramPublicRuntimeNoopSymbolV2},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePackageInitV2, FunctionID: "ingress-e2e-package-init", Target: "__llgo_coro_ingress_e2e_package_init"},
			{
				Kind: coroProgramStepCoroRootV1, Role: coroProgramStepRoleMainV2,
				FunctionID: "ingress-e2e-main", Target: coroNativeIngressE2EPackage + ".main$coro",
				Owner: coroNativeIngressE2EPackage, CatalogTarget: anchor,
			},
		},
	}
	var programHash [16]byte
	for index := range programHash {
		programHash[index] = byte(index + 17)
	}
	entry := genMainModule(ctx, llssa.PkgRuntime, &packages.Package{
		ID: coroNativeIngressE2EPackage, PkgPath: coroNativeIngressE2EPackage, ExportFile: "coro-native-ingress-e2e.a",
	}, &genConfig{
		coroRootAnchors: []string{anchor}, coroManifestHash: programHash, coroBootstrap: bootstrap,
	})
	for _, name := range []string{"__llgo_coro_ingress_e2e_runtime_init", "__llgo_coro_ingress_e2e_package_init"} {
		fn := entry.LPkg.FuncOf(name)
		if fn == nil {
			t.Fatalf("entry module has no bounded native ingress init %q", name)
		}
		if !fn.HasBody() {
			fn.MakeBody(1).Return()
		}
	}
	entryMain := entry.LPkg.Module().NamedFunction("main")
	if entryMain.IsNil() {
		t.Fatalf("entry module has no native main:\n%s", entry.LPkg.String())
	}
	entryMain.SetName(coroNativeIngressE2EEntry)
	if err := lowerCoroControlWrappers(ctx, entry.LPkg); err != nil {
		t.Fatal(err)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, entry.LPkg.Module(), filepath.Join(temp, "ingress-entry.o"))
}

func buildCoroNativeIngressE2EDriver(t *testing.T, prog llssa.Program, temp, checkSymbol string) string {
	t.Helper()
	pkg := prog.NewPackage("coro-native-ingress-e2e-driver", "coro-native-ingress-e2e-driver")
	defer pkg.Module().Dispose()
	pointer := types.Typ[types.UnsafePointer]
	entry := pkg.NewFunc(coroNativeIngressE2EEntry, newSignature(
		[]types.Type{types.Typ[types.Int32], pointer}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	check := pkg.NewFunc(checkSymbol, newSignature(nil, []types.Type{types.Typ[types.Int32]}), llssa.InGo)
	start := pkg.NewFunc("__llgo_coro_native_ingress_start_v1", newSignature(nil, nil), llssa.InC)
	verify := pkg.NewFunc("__llgo_coro_native_ingress_verify_closed_v1", newSignature(nil, nil), llssa.InC)
	abort := pkg.NewFunc("abort", newSignature(nil, nil), llssa.InC)
	assertNil := pkg.NewFunc(llssa.PkgRuntime+".AssertNilDeref", newSignature(
		[]types.Type{types.Typ[types.Bool]}, nil,
	), llssa.InGo)
	assertBody := assertNil.MakeBody(3)
	fail, valid := assertNil.Block(1), assertNil.Block(2)
	assertBody.If(assertNil.Param(0), fail, valid)
	assertBody.SetBlock(fail).Call(abort.Expr)
	assertBody.Return()
	assertBody.SetBlock(valid).Return()
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
	main := pkg.NewFunc("main", newSignature(
		[]types.Type{types.Typ[types.Int32], pointer}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	body := main.MakeBody(1)
	body.Call(start.Expr)
	body.Call(entry.Expr, main.Param(0), main.Param(1))
	body.Call(verify.Expr)
	body.Return(body.Call(check.Expr))
	pkg.MaterializePreserveSyms()
	return emitCoroSpawnNativeE2EObject(t, prog, pkg.Module(), filepath.Join(temp, "ingress-driver.o"))
}

func buildCoroNativeIngressE2ECDriver(t *testing.T, clang, temp string) string {
	t.Helper()
	source := filepath.Join(temp, "native-ingress-sync.c")
	object := filepath.Join(temp, "native-ingress-sync.o")
	if err := os.WriteFile(source, []byte(coroNativeIngressE2ECSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(clang, "-std=c11", "-O2", "-pthread", "-c", source, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile native ingress pthread driver: %v\n%s", err, output)
	}
	return object
}

func buildCoroNativeIngressE2ERuntimeIsland(t *testing.T, temp string) []string {
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
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_ingress_test_llgo.go"),
	}
	requireCoroRuntimeIslandProductionSource(t, files, "coro_run_decision.go")
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.Tags = "nogc"
	conf.compilerBuildTags = []string{"llgo_coro", coroNativePipeBuildTag, coroNativeIngressTestBuildTag}
	allowed := map[string]bool{
		"command-line-arguments":                               true,
		"github.com/goplus/llgo/runtime/internal/coro":         true,
		"github.com/goplus/llgo/runtime/internal/coroalloc":    true,
		"github.com/goplus/llgo/runtime/internal/corodoorbell": true,
	}
	seen := make(map[string]bool, len(allowed))
	var objects []string
	conf.ModuleHook = func(pkg Package) {
		if pkg.LPkg == nil || pkg.LPkg.Prog == nil || !allowed[pkg.ID] {
			return
		}
		if seen[pkg.ID] {
			t.Fatalf("native ingress runtime emitted duplicate module %q", pkg.ID)
		}
		seen[pkg.ID] = true
		module := pkg.LPkg.Module()
		if module.IsNil() {
			return
		}
		name := fmt.Sprintf("ingress-runtime-%03d-%s.o", len(objects), sanitizeCoroSpawnNativeE2EObjectName(pkg.ID))
		objects = append(objects, emitCoroSpawnNativeE2EObject(t, pkg.LPkg.Prog, module, filepath.Join(temp, name)))
	}
	pkgs, err := Do(files, conf)
	if err != nil {
		t.Fatalf("compile native ingress production runtime island: %v", err)
	}
	if len(pkgs) == 0 || pkgs[0].LPkg == nil {
		t.Fatal("native ingress production runtime island produced no root package")
	}
	pkgs[0].LPkg.Prog.Dispose()
	for id := range allowed {
		if !seen[id] {
			t.Fatalf("native ingress runtime did not emit required module %q", id)
		}
	}
	return objects
}

func assertCoroNativeIngressE2ELinkedSymbols(t *testing.T, executable string) {
	t.Helper()
	nm, err := exec.LookPath("nm")
	if err != nil {
		t.Skip("nm is unavailable for native ingress linked audit")
	}
	output, err := exec.Command(nm, executable).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect native ingress executable: %v\n%s", err, output)
	}
	symbols := string(output)
	for _, required := range []string{
		coroNativePostWaitSymbolV1,
		coroWaitPrepareSymbolV1,
		coroWaitRetireCompletedSymbolV1,
		"__llgo_coro_native_ingress_before_poll_v1",
		"__llgo_coro_native_ingress_audit_closed_v1",
		"pthread_create",
		"pthread_join",
	} {
		if !strings.Contains(symbols, required) {
			t.Fatalf("native ingress executable is missing %q:\n%s", required, symbols)
		}
	}
	for _, forbidden := range []string{"uv_", "GC_"} {
		if strings.Contains(symbols, forbidden) {
			t.Fatalf("nogc native ingress executable unexpectedly depends on %q:\n%s", forbidden, symbols)
		}
	}
}
