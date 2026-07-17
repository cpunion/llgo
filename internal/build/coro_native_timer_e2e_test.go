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
	coroNativeTimerE2EPackage                   = "example.com/llgo-coro-native-timer-e2e"
	coroNativeTimerE2EEntry                     = "__llgo_coro_native_timer_e2e_entry"
	coroNativeTimerE2EMarker                    = "LLGO_CORO_NATIVE_TIMER_E2E_OK\n"
	coroNativeTimerPrepareAfterOrAbortE2ESymbol = "__llgo_coro_timer_prepare_after_or_abort_v1"
	coroNativeTimerRetireOrAbortE2ESymbol       = "__llgo_coro_timer_retire_completed_or_abort_v1"
)

// The fixture deliberately has one synchronous-style main body. It neither
// creates a goroutine nor supplies an async duplicate. The llgo.coroPark
// intrinsic makes this exact body a physical LLVM coroutine, while the timer
// owner ABI retains only the token generation and fixed-table identity.
const coroNativeTimerE2ESource = `package main

import "unsafe"

type WaitToken struct { word uint32 }

const delayNanos int64 = 30000000

var Stage uint32
var Result int32
var ElapsedNanos int64

//llgo:coro noblock
//llgo:link monotonicNow C.__llgo_coro_native_timer_e2e_now_v1
func monotonicNow() int64

//llgo:coro noblock
//llgo:link prepareAfterOrAbort C.__llgo_coro_timer_prepare_after_or_abort_v1
func prepareAfterOrAbort(unsafe.Pointer, int64, *uint32, *uint32, *uint32)

//llgo:link park llgo.coroPark
func park(*WaitToken, uint32)

//llgo:coro noblock
//llgo:link retireCompletedOrAbort C.__llgo_coro_timer_retire_completed_or_abort_v1
func retireCompletedOrAbort(unsafe.Pointer, uint32, uint32, uint32)

func main() {
	var token WaitToken
	var ticket uint32
	var timerSlot uint32
	var timerGeneration uint32

	beginNanos := monotonicNow()
	if beginNanos < 0 {
		Result = 21
		return
	}
	prepareAfterOrAbort(unsafe.Pointer(&token), delayNanos, &ticket, &timerSlot, &timerGeneration)
	park(&token, ticket)
	retireCompletedOrAbort(unsafe.Pointer(&token), ticket, timerSlot, timerGeneration)
	Stage = 3
	endNanos := monotonicNow()
	if endNanos < beginNanos {
		Result = 24
		return
	}
	ElapsedNanos = endNanos - beginNanos
	if ElapsedNanos < delayNanos {
		Result = 25
		return
	}
}

func Check() int32 {
	if Result != 0 { return Result }
	if Stage != 3 { return 31 }
	if ElapsedNanos < delayNanos { return 32 }
	return 0
}
`

// This C boundary supplies only a monotonic observation for the black-box
// elapsed-time assertion, an inert before-poll test hook, and the final marker.
// It has no pthread, callback, producer, timer, or scheduler responsibility.
const coroNativeTimerE2ECSource = `
#if !defined(__APPLE__) && !defined(_POSIX_C_SOURCE)
#define _POSIX_C_SOURCE 200809L
#endif

#include <stdint.h>
#include <stddef.h>
#include <time.h>
#include <unistd.h>

#if defined(__APPLE__)
#include <limits.h>
#define LLGO_CLOCK_UPTIME_RAW 8
#endif

static const char timer_e2e_marker[] = "LLGO_CORO_NATIVE_TIMER_E2E_OK\n";

int64_t __llgo_coro_native_timer_e2e_now_v1(void) {
#if defined(__APPLE__)
	uint64_t now = clock_gettime_nsec_np(LLGO_CLOCK_UPTIME_RAW);
	return now > INT64_MAX ? -1 : (int64_t)now;
#else
	struct timespec value;
	if (clock_gettime(CLOCK_MONOTONIC, &value) != 0 ||
		value.tv_sec < 0 || value.tv_nsec < 0 || value.tv_nsec >= 1000000000L) {
		return -1;
	}
	if ((uint64_t)value.tv_sec > (uint64_t)INT64_MAX / 1000000000ULL) {
		return -1;
	}
	uint64_t now = (uint64_t)value.tv_sec * 1000000000ULL + (uint64_t)value.tv_nsec;
	return now > INT64_MAX ? -1 : (int64_t)now;
#endif
}

uint32_t __llgo_coro_native_ingress_before_poll_v1(void) {
	return 1;
}

int32_t __llgo_coro_native_timer_e2e_finish_v1(int32_t check, uint32_t audit) {
	if (check != 0) {
		return check;
	}
	if (audit != 0) {
		return 80;
	}
	if (write(STDOUT_FILENO, timer_e2e_marker, sizeof(timer_e2e_marker) - 1) !=
		(ssize_t)(sizeof(timer_e2e_marker) - 1)) {
		return 81;
	}
	return 0;
}
`

func TestCoroNativeTimerNoGCProductionE2E(t *testing.T) {
	capability := &Config{
		BuildMode:                     BuildModeExe,
		Goos:                          runtime.GOOS,
		Goarch:                        runtime.GOARCH,
		EnableCoroProgramBootstrapRun: true,
	}
	if !nativeCoroTimerRuntimeABI(capability) {
		t.Skipf("native coroutine timer E2E is unavailable on %s/%s", runtime.GOOS, runtime.GOARCH)
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

	userObject, anchor, checkSymbol := buildCoroNativeTimerE2EUser(t, prog, temp)
	entryObject := buildCoroNativeTimerE2EEntry(t, prog, temp, anchor)
	driverObject := buildCoroNativeTimerE2EDriver(t, prog, temp, checkSymbol)
	boundaryObject := buildCoroNativeTimerE2ECBoundary(t, clang, temp)
	runtimeObjects := buildCoroNativeTimerE2ERuntimeIsland(t, temp)
	runtimeArchive := filepath.Join(temp, "libllgo-coro-native-timer.a")
	if output, err := exec.Command(ar, append([]string{"rcs", runtimeArchive}, runtimeObjects...)...).CombinedOutput(); err != nil {
		t.Fatalf("archive native coroutine timer runtime: %v\n%s", err, output)
	}
	assertCoroNativeTimerE2ERuntimeArtifact(t, runtimeArchive)

	executable := filepath.Join(temp, "coro-native-timer-e2e")
	linkArgs := []string{driverObject, entryObject, userObject, boundaryObject, runtimeArchive, "-o", executable}
	if runtime.GOOS == "darwin" {
		linkArgs = append(linkArgs, "-Wl,-dead_strip")
	} else {
		linkArgs = append(linkArgs, "-Wl,--gc-sections")
	}
	assertCoroNativeTimerE2ELinkCommand(t, linkArgs)
	if output, err := exec.Command(clang, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link native coroutine timer E2E: %v\n%s", err, output)
	}
	assertCoroNativeTimerE2ELinkedSymbols(t, executable)

	runCtx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(runCtx, executable).CombinedOutput()
	if runCtx.Err() != nil {
		t.Fatalf("native coroutine timer E2E timed out: %v\n%s", runCtx.Err(), output)
	}
	if err != nil {
		t.Fatalf("native coroutine timer E2E failed: %v\n%s", err, output)
	}
	if string(output) != coroNativeTimerE2EMarker {
		t.Fatalf("native coroutine timer E2E output = %q, want %q", output, coroNativeTimerE2EMarker)
	}
}

func buildCoroNativeTimerE2EUser(t *testing.T, prog llssa.Program, temp string) (object, anchor, checkSymbol string) {
	t.Helper()
	ssaPkg, files := buildCoroPlanTestPackage(t, coroNativeTimerE2EPackage, coroNativeTimerE2ESource, nil)
	universe, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: coroNativeTimerE2EPackage,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	mainFn, checkFn := ssaPkg.Func("main"), ssaPkg.Func("Check")
	prepareFn := ssaPkg.Func("prepareAfterOrAbort")
	parkFn := ssaPkg.Func("park")
	retireFn := ssaPkg.Func("retireCompletedOrAbort")
	prepareCall := findCoroNativeTimerE2EDirectCall(t, mainFn, prepareFn)
	parkCall := findCoroNativeTimerE2EDirectCall(t, mainFn, parkFn)
	retireCall := findCoroNativeTimerE2EDirectCall(t, mainFn, retireFn)
	assertCoroNativeTimerE2ECriticalSpan(t, mainFn, prepareCall, parkCall, retireCall)
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	input := CoroPlanInput{
		Program:                        ssaPkg.Prog,
		EmissionUniverse:               ssaUniverse,
		resolveFunction:                universe.Resolve,
		functionBackground:             universe.FunctionBackground,
		foreignNoBlock:                 universe.CoroForeignNoBlockCertificate,
		intrinsicCallSemantics:         universe.CoroIntrinsicCallSiteSemantics,
		rawFunctionAddressCallArgument: universe.CoroRawFunctionAddressCallArgument,
		demandReferences:               universe.CoroDemandReferences,
		loweredCalls:                   universe.CoroLoweredCalls,
		enableClosedStaticSpawn:        true,
	}
	plan, err := input.Analyze(coro.Roots{
		{Function: mainFn, Demand: coro.AsyncDemand},
		{Function: checkFn, Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	mainPlan, ok := plan.FunctionPlan(mainFn)
	if !ok || mainPlan.Emission != coro.EmitCoroutine || mainPlan.Primary != coro.PrimaryCoroutine ||
		mainPlan.FuncRep != coro.DirectCoro || !mainPlan.DeclaredEffect.Contains(coro.MayPark) ||
		!mainPlan.LocalEffect.Contains(coro.MayPark) || !mainPlan.Effect.Contains(coro.MayPark) {
		t.Fatalf("native timer main plan = %+v, present=%t; want one direct coroutine body", mainPlan, ok)
	}
	semantics, intrinsic, semanticsErr := universe.CoroIntrinsicCallSiteSemantics(parkCall)
	if semanticsErr != nil || !intrinsic || !semantics.SuspendsCurrentFrame() || !plan.ElidesCall(parkCall) {
		t.Fatalf("native timer main park semantics = %v, intrinsic=%t, err=%v, elided=%t; want one production-classified suspend", semantics, intrinsic, semanticsErr, plan.ElidesCall(parkCall))
	}
	checkPlan, ok := plan.FunctionPlan(checkFn)
	if !ok || checkPlan.FuncRep != coro.DirectPlain || checkPlan.Effect.MaySuspend() {
		t.Fatalf("native timer checker plan = %+v, present=%t; want direct plain", checkPlan, ok)
	}
	compilation := &cl.Compilation{
		CoroPlan:                      plan,
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroClosedStaticSpawn:   true,
		EnableCoroProgramBootstrapRun: true,
		CoroFrameRetentionABI:         cl.CoroFrameRetentionTimerABIV1,
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
	mainPhysical := module.NamedFunction(coroNativeTimerE2EPackage + ".main$coro")
	if mainPhysical.IsNil() || mainPhysical.IsDeclaration() {
		t.Fatalf("compiled native timer user module has no physical main coroutine:\n%s", module.String())
	}
	assertCoroTimerRetainedFrameIR(t, "native timer E2E main", mainPhysical.String(), mainPlan.Exec.Contains(coro.NeedsPreempt))
	runCoroSpawnNativeE2EPasses(t, prog, module)
	ir := module.String()
	match := regexp.MustCompile(`@"?(__llgo_coro_root_package_v1\.[0-9a-f]{32})"?\s*=`).FindStringSubmatch(ir)
	if len(match) != 2 {
		t.Fatalf("compiled native timer user module has no root package anchor:\n%s", ir)
	}
	checkSymbol = coroNativeTimerE2EPackage + ".Check"
	if module.NamedFunction(checkSymbol).IsNil() {
		t.Fatalf("compiled native timer user module has no checker %q:\n%s", checkSymbol, ir)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, module, filepath.Join(temp, "timer-user.o")), match[1], checkSymbol
}

func buildCoroNativeTimerE2EEntry(t *testing.T, prog llssa.Program, temp, anchor string) string {
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
	if !nativeCoroTimerRuntimeABI(conf) {
		t.Fatal("native timer entry unexpectedly lacks its runtime capability")
	}
	ctx := &context{prog: prog, buildConf: conf}
	bootstrap := &coroProgramBootstrapV1{
		Version: coroProgramBootstrapVersionV2,
		Steps: []coroProgramBootstrapStepV1{
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleRuntimeInitV2, FunctionID: "timer-e2e-runtime-init", Target: "__llgo_coro_timer_e2e_runtime_init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2, FunctionID: "timer-e2e-abi-init", Target: "init$abitypes"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2, FunctionID: coroProgramPublicRuntimeNoopIDV2, Target: coroProgramPublicRuntimeNoopSymbolV2},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePackageInitV2, FunctionID: "timer-e2e-package-init", Target: "__llgo_coro_timer_e2e_package_init"},
			{
				Kind: coroProgramStepCoroRootV1, Role: coroProgramStepRoleMainV2,
				FunctionID: "timer-e2e-main", Target: coroNativeTimerE2EPackage + ".main$coro",
				Owner: coroNativeTimerE2EPackage, CatalogTarget: anchor,
			},
		},
	}
	var programHash [16]byte
	for index := range programHash {
		programHash[index] = byte(index + 49)
	}
	entry := genMainModule(ctx, llssa.PkgRuntime, &packages.Package{
		ID: coroNativeTimerE2EPackage, PkgPath: coroNativeTimerE2EPackage, ExportFile: "coro-native-timer-e2e.a",
	}, &genConfig{
		coroRootAnchors: []string{anchor}, coroManifestHash: programHash, coroBootstrap: bootstrap,
	})
	for _, name := range []string{"__llgo_coro_timer_e2e_runtime_init", "__llgo_coro_timer_e2e_package_init"} {
		fn := entry.LPkg.FuncOf(name)
		if fn == nil {
			t.Fatalf("entry module has no bounded native timer init %q", name)
		}
		if !fn.HasBody() {
			fn.MakeBody(1).Return()
		}
	}
	entryMain := entry.LPkg.Module().NamedFunction("main")
	if entryMain.IsNil() {
		t.Fatalf("entry module has no native timer main:\n%s", entry.LPkg.String())
	}
	entryMain.SetName(coroNativeTimerE2EEntry)
	if err := lowerCoroControlWrappers(ctx, entry.LPkg); err != nil {
		t.Fatal(err)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, entry.LPkg.Module(), filepath.Join(temp, "timer-entry.o"))
}

func buildCoroNativeTimerE2EDriver(t *testing.T, prog llssa.Program, temp, checkSymbol string) string {
	t.Helper()
	pkg := prog.NewPackage("coro-native-timer-e2e-driver", "coro-native-timer-e2e-driver")
	defer pkg.Module().Dispose()
	pointer := types.Typ[types.UnsafePointer]
	entry := pkg.NewFunc(coroNativeTimerE2EEntry, newSignature(
		[]types.Type{types.Typ[types.Int32], pointer}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	check := pkg.NewFunc(checkSymbol, newSignature(nil, []types.Type{types.Typ[types.Int32]}), llssa.InGo)
	audit := pkg.NewFunc("__llgo_coro_native_ingress_audit_closed_v1", newSignature(nil, []types.Type{types.Typ[types.Uint32]}), llssa.InC)
	finish := pkg.NewFunc("__llgo_coro_native_timer_e2e_finish_v1", newSignature(
		[]types.Type{types.Typ[types.Int32], types.Typ[types.Uint32]}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	abort := pkg.NewFunc("abort", newSignature(nil, nil), llssa.InC)
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
	main := pkg.NewFunc("main", newSignature(
		[]types.Type{types.Typ[types.Int32], pointer}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	body := main.MakeBody(1)
	body.Call(entry.Expr, main.Param(0), main.Param(1))
	checkResult := body.Call(check.Expr)
	auditResult := body.Call(audit.Expr)
	body.Return(body.Call(finish.Expr, checkResult, auditResult))
	pkg.MaterializePreserveSyms()
	return emitCoroSpawnNativeE2EObject(t, prog, pkg.Module(), filepath.Join(temp, "timer-driver.o"))
}

func buildCoroNativeTimerE2ECBoundary(t *testing.T, clang, temp string) string {
	t.Helper()
	source := filepath.Join(temp, "native-timer-boundary.c")
	object := filepath.Join(temp, "native-timer-boundary.o")
	if err := os.WriteFile(source, []byte(coroNativeTimerE2ECSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(clang, "-std=c11", "-O2", "-c", source, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile native timer boundary: %v\n%s", err, output)
	}
	return object
}

func buildCoroNativeTimerE2ERuntimeIsland(t *testing.T, temp string) []string {
	t.Helper()
	files := []string{
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_allocator.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_frame.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_program.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_run_decision.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_sched.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_executor.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_executor_driver_timer_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_spawn.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_native_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_wait_timer_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_timer_owner_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_native_ingress_test_llgo.go"),
	}
	requireCoroRuntimeIslandProductionSource(t, files, "coro_run_decision.go")
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.Tags = "nogc"
	conf.compilerBuildTags = []string{
		"llgo_coro",
		coroNativePipeBuildTag,
		coroNativeTimerBuildTag,
		coroNativeIngressTestBuildTag,
	}
	allowed := map[string]bool{
		"command-line-arguments":                               true,
		"github.com/goplus/llgo/runtime/internal/coro":         true,
		"github.com/goplus/llgo/runtime/internal/coroalloc":    true,
		"github.com/goplus/llgo/runtime/internal/coroclock":    true,
		"github.com/goplus/llgo/runtime/internal/corodoorbell": true,
		"github.com/goplus/llgo/runtime/internal/corotimer":    true,
	}
	seen := make(map[string]bool, len(allowed))
	var objects []string
	conf.ModuleHook = func(pkg Package) {
		if pkg.LPkg == nil || pkg.LPkg.Prog == nil || !allowed[pkg.ID] {
			return
		}
		if seen[pkg.ID] {
			t.Fatalf("native timer runtime emitted duplicate module %q", pkg.ID)
		}
		seen[pkg.ID] = true
		module := pkg.LPkg.Module()
		if module.IsNil() {
			return
		}
		name := fmt.Sprintf("timer-runtime-%03d-%s.o", len(objects), sanitizeCoroSpawnNativeE2EObjectName(pkg.ID))
		objects = append(objects, emitCoroSpawnNativeE2EObject(t, pkg.LPkg.Prog, module, filepath.Join(temp, name)))
	}
	pkgs, err := Do(files, conf)
	if err != nil {
		t.Fatalf("compile native timer production runtime island: %v", err)
	}
	if len(pkgs) == 0 || pkgs[0].LPkg == nil {
		t.Fatal("native timer production runtime island produced no root package")
	}
	pkgs[0].LPkg.Prog.Dispose()
	for id := range allowed {
		if !seen[id] {
			t.Fatalf("native timer runtime did not emit required module %q", id)
		}
	}
	if len(objects) != len(allowed) {
		t.Fatalf("native timer runtime objects = %d, want exactly %d", len(objects), len(allowed))
	}
	return objects
}

func assertCoroNativeTimerE2ELinkCommand(t *testing.T, args []string) {
	t.Helper()
	for _, argument := range args {
		if argument == "-pthread" || strings.Contains(argument, "libuv") || strings.Contains(argument, "bdwgc") {
			t.Fatalf("native timer E2E link command has forbidden dependency %q: %q", argument, args)
		}
	}
}

func assertCoroNativeTimerE2ERuntimeArtifact(t *testing.T, archive string) {
	t.Helper()
	symbols := readCoroNativeTimerE2ENMSymbols(t, archive)
	requiredClock := "clock_gettime"
	if runtime.GOOS == "darwin" {
		requiredClock = "clock_gettime_nsec_np"
	}
	for _, required := range []string{requiredClock, "pipe", "poll", "fcntl", coroNativeTimerPrepareAfterOrAbortE2ESymbol, coroNativeTimerRetireOrAbortE2ESymbol} {
		if !coroNativeTimerE2ENMHasSymbol(symbols, required) {
			t.Fatalf("native timer runtime archive is missing %q:\n%s", required, symbols)
		}
	}
	assertCoroNativeTimerE2ENoLegacyDependencies(t, "runtime archive", symbols)
}

func assertCoroNativeTimerE2ELinkedSymbols(t *testing.T, executable string) {
	t.Helper()
	symbols := readCoroNativeTimerE2ENMSymbols(t, executable)
	requiredClock := "clock_gettime"
	if runtime.GOOS == "darwin" {
		requiredClock = "clock_gettime_nsec_np"
	}
	for _, required := range []string{
		requiredClock,
		"pipe",
		"poll",
		"fcntl",
		coroProgramContinueSymbolV1,
		coroNativeTimerPrepareAfterOrAbortE2ESymbol,
		coroNativeTimerRetireOrAbortE2ESymbol,
		"github.com/goplus/llgo/runtime/internal/coro.RetireCompletedExecutorTimer",
		"github.com/goplus/llgo/runtime/internal/corodoorbell.(*Pipe).WaitDeadline",
	} {
		if !coroNativeTimerE2ENMHasSymbol(symbols, required) {
			t.Fatalf("native timer executable is missing %q:\n%s", required, symbols)
		}
	}
	assertCoroNativeTimerE2ENoLegacyDependencies(t, "executable", symbols)
}

func readCoroNativeTimerE2ENMSymbols(t *testing.T, artifact string) string {
	t.Helper()
	nm, err := exec.LookPath("nm")
	if err != nil {
		t.Skip("nm is unavailable for native timer artifact audit")
	}
	output, err := exec.Command(nm, artifact).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect native timer artifact %s: %v\n%s", filepath.Base(artifact), err, output)
	}
	return string(output)
}

func coroNativeTimerE2ENMHasSymbol(output, want string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		symbol := fields[len(fields)-1]
		if version := strings.IndexByte(symbol, '@'); version >= 0 {
			symbol = symbol[:version]
		}
		if symbol == want || strings.TrimPrefix(symbol, "_") == want {
			return true
		}
	}
	return false
}

func assertCoroNativeTimerE2ENoLegacyDependencies(t *testing.T, label, symbols string) {
	t.Helper()
	for _, forbidden := range []string{"uv_", "GC_", "pthread_"} {
		if strings.Contains(symbols, forbidden) {
			t.Fatalf("native timer %s unexpectedly depends on %q:\n%s", label, forbidden, symbols)
		}
	}
}

func findCoroNativeTimerE2EDirectCall(t *testing.T, owner, target *ssa.Function) *ssa.Call {
	t.Helper()
	if owner == nil || target == nil {
		t.Fatal("native timer critical-span call requires exact owner and target")
	}
	var found *ssa.Call
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil || call.Common().StaticCallee() != target {
				continue
			}
			if found != nil {
				t.Fatalf("native timer main has duplicate direct calls to %s", target.Name())
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("native timer main has no direct call to %s", target.Name())
	}
	return found
}

func assertCoroNativeTimerE2ECriticalSpan(t *testing.T, owner *ssa.Function, prepare, park, retire *ssa.Call) {
	t.Helper()
	if owner == nil || prepare == nil || park == nil || retire == nil ||
		prepare.Parent() != owner || park.Parent() != owner || retire.Parent() != owner ||
		prepare.Block() == nil || prepare.Block() != park.Block() || prepare.Block() != retire.Block() {
		t.Fatalf("native timer critical span is not one exact owner/basic block: prepare=%v park=%v retire=%v", prepare, park, retire)
	}
	block := prepare.Block()
	indexOf := func(want ssa.Instruction) int {
		for index, instruction := range block.Instrs {
			if instruction == want {
				return index
			}
		}
		return -1
	}
	prepareIndex, parkIndex, retireIndex := indexOf(prepare), indexOf(park), indexOf(retire)
	if prepareIndex < 0 || parkIndex <= prepareIndex || retireIndex <= parkIndex {
		t.Fatalf("native timer critical-span instruction order = prepare:%d park:%d retire:%d", prepareIndex, parkIndex, retireIndex)
	}
	for index := prepareIndex + 1; index < retireIndex; index++ {
		instruction := block.Instrs[index]
		if instruction == park {
			continue
		}
		if _, call := instruction.(ssa.CallInstruction); call {
			t.Fatalf("native timer critical span has extra call %T %q at instruction %d", instruction, instruction, index)
		}
		switch instruction.(type) {
		case *ssa.If, *ssa.Jump, *ssa.Return, *ssa.Panic:
			t.Fatalf("native timer critical span has control transfer %T at instruction %d", instruction, index)
		}
	}
}
