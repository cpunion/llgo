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
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/env"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroTimeSleepPrepareSymbolV1 = "__llgo_coro_timer_prepare_after_or_abort_v1"
	coroTimeSleepRetireSymbolV1  = "__llgo_coro_timer_retire_completed_or_abort_v1"
	coroTimeSleepParkHookV1      = "__llgo_coro_park_prepare_v1"
	coroTimeSleepParkSymbolV2    = "__llgo_coro_timer_park_v2"
	coroTimeSleepResumeSymbolV2  = "__llgo_coro_timer_resume_v2"
	coroTimeSleepPreemptPollV1   = "__llgo_coro_preempt_poll_v1"
	coroTimeSleepAwaitHookV1     = "__llgo_coro_await_prepare_v3"
)

// TestCoroNativeTimeSleepProductionPlanAndCodegen starts from an ordinary
// synchronous Go call to time.Sleep. It obtains the exact injected source from
// the production GOROOT overlay, discovers MayPark from that body's
// llgo.coroTimerSleep intrinsic, taints both callers without a test-supplied effect
// seed, and emits one stackless DirectCoro body at every level.
//
// A complete Do build currently stops before the plan builder because
// sync.Pool passes a captured destructor through an exact synchronous C
// callback ABI that has no closure-context slot. The focused frozen universe
// below deliberately excludes that unrelated callback limitation; it does not
// copy, rewrite, or weaken the production Sleep body or its frame-retention
// checks.
func TestCoroNativeTimeSleepProductionPlanAndCodegen(t *testing.T) {
	capability := &Config{
		BuildMode:                     BuildModeExe,
		Goos:                          runtime.GOOS,
		Goarch:                        runtime.GOARCH,
		EnableCoroProgramBootstrapRun: true,
	}
	if !nativeCoroTimerRuntimeABI(capability) {
		t.Skipf("native coroutine time.Sleep compilation is unavailable on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	overlay, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
		buildFlags: []string{"-tags=llgo,llgo_coro,llgo_coro_native_pipe,llgo_coro_native_timer,nogc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	injectedPath := filepath.Join(runtime.GOROOT(), "src", "time", "z_llgo_patch_sleep_coro_native_llgo.go")
	injected, ok := overlay[injectedPath]
	if !ok {
		t.Fatalf("production source overlay has no native coroutine time.Sleep body %s", injectedPath)
	}
	callerPath := filepath.Join("_testgo", "coro_time_sleep", "main.go")
	caller, err := os.ReadFile(callerPath)
	if err != nil {
		t.Fatal(err)
	}
	ssaProg, timeSSA, timeFiles, mainSSA, mainFiles := buildCoroTimeSleepOverlaySSA(
		t, injectedPath, injected, callerPath, caller,
	)

	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	prog.SetRuntime(func() *types.Package {
		runtimePackage, err := importer.For("source", nil).Import(llssa.PkgRuntime)
		if err != nil {
			t.Fatal("load runtime failed:", err)
		}
		if runtimePackage.Scope().Lookup("CoroTimerParkV2") == nil {
			name := types.NewTypeName(token.NoPos, runtimePackage, "CoroTimerParkV2", nil)
			types.NewNamed(name, types.NewArray(types.Typ[types.Uintptr], 32), nil)
			if previous := runtimePackage.Scope().Insert(name); previous != nil {
				t.Fatalf("install Timer V2 test runtime type: duplicate %v", previous)
			}
		}
		return runtimePackage
	})
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{
		{SSA: timeSSA, Files: timeFiles, Identity: "time"},
		{SSA: mainSSA, Files: mainFiles, Identity: "example.com/llgo-coro-time-sleep"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaProg, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	functionIDs.ArchiveReady = true
	input := CoroPlanInput{
		Program:                        ssaProg,
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
		demandReferences:               emission.CoroDemandReferences,
		syncDemandReferences:           emission.CoroSyncDemandReferences,
		loweredCalls:                   emission.CoroLoweredCalls,
	}
	main := mainSSA.Func("main")
	plan, err := input.Analyze(coro.Roots{{Function: main, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}

	sleep, err := findCoroTimeSleepFunction(input.Program, "time", "Sleep")
	if err != nil {
		t.Fatal(err)
	}
	position := input.Program.Fset.PositionFor(sleep.Pos(), true)
	wantPatch := filepath.ToSlash(filepath.Join("runtime", "_patch", "time", "sleep_coro_native_llgo.go"))
	if got := filepath.ToSlash(position.Filename); !strings.HasSuffix(got, wantPatch) {
		t.Fatalf("time.Sleep source = %q, want production source patch suffix %q", got, wantPatch)
	}
	sleepOnce := mainSSA.Func("sleepOnce")
	sleepZero := mainSSA.Func("sleepZero")
	sleepNegative := mainSSA.Func("sleepNegative")
	if main == nil || sleepOnce == nil || sleepZero == nil || sleepNegative == nil || len(sleepOnce.Blocks) == 0 {
		t.Fatal("synchronous fixture has no bodyful main/Sleep callers")
	}
	intrinsic, err := findCoroTimeSleepFunction(input.Program, "time", "llgoCoroTimerSleep")
	if err != nil {
		t.Fatal(err)
	}
	intrinsicCall := findCoroNativeTimerE2EDirectCall(t, sleep, intrinsic)
	assertCoroTimeSleepNonpositiveFastPath(t, sleep, intrinsicCall)
	semantics, isIntrinsic, semanticsErr := coroIntrinsicCallSiteSemanticsForTest(emission, intrinsicCall)
	if semanticsErr != nil || !isIntrinsic || !semantics.SuspendsCurrentFrame() || !plan.ElidesCall(intrinsicCall) {
		t.Fatalf("production time.Sleep timer semantics = %v, intrinsic=%t, err=%v, elided=%t; want exact suspending intrinsic", semantics, isIntrinsic, semanticsErr, plan.ElidesCall(intrinsicCall))
	}
	if err := assertCoroTimeSleepFunctionPlan(plan, sleep, true, "time.Sleep"); err != nil {
		t.Fatal(err)
	}
	sleepPlan, _ := plan.FunctionPlan(sleep)
	if err := assertCoroTimeSleepFunctionPlan(plan, sleepOnce, false, "sleepOnce"); err != nil {
		t.Fatal(err)
	}
	if err := assertCoroTimeSleepFunctionPlan(plan, sleepZero, false, "sleepZero"); err != nil {
		t.Fatal(err)
	}
	if err := assertCoroTimeSleepFunctionPlan(plan, sleepNegative, false, "sleepNegative"); err != nil {
		t.Fatal(err)
	}
	if err := assertCoroTimeSleepFunctionPlan(plan, main, false, "main"); err != nil {
		t.Fatal(err)
	}
	sleepCall, err := findCoroTimeSleepDirectCall(sleepOnce, sleep)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertCoroTimeSleepDirectCoroCall(plan, sleepCall, sleep, "sleepOnce -> time.Sleep"); err != nil {
		t.Fatal(err)
	}
	zeroCall, err := findCoroTimeSleepDirectCall(sleepZero, sleep)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertCoroTimeSleepDirectCoroCall(plan, zeroCall, sleep, "sleepZero -> time.Sleep"); err != nil {
		t.Fatal(err)
	}
	negativeCall, err := findCoroTimeSleepDirectCall(sleepNegative, sleep)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertCoroTimeSleepDirectCoroCall(plan, negativeCall, sleep, "sleepNegative -> time.Sleep"); err != nil {
		t.Fatal(err)
	}
	mainCall, err := findCoroTimeSleepDirectCall(main, sleepOnce)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertCoroTimeSleepDirectCoroCall(plan, mainCall, sleepOnce, "main -> sleepOnce"); err != nil {
		t.Fatal(err)
	}
	mainZeroCall, err := findCoroTimeSleepDirectCall(main, sleepZero)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertCoroTimeSleepDirectCoroCall(plan, mainZeroCall, sleepZero, "main -> sleepZero"); err != nil {
		t.Fatal(err)
	}
	mainNegativeCall, err := findCoroTimeSleepDirectCall(main, sleepNegative)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertCoroTimeSleepDirectCoroCall(plan, mainNegativeCall, sleepNegative, "main -> sleepNegative"); err != nil {
		t.Fatal(err)
	}

	compilation := &cl.Compilation{
		CoroPlan:                      plan,
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroProgramBootstrapRun: true,
		CoroFrameRetentionABI:         cl.CoroFrameRetentionParkABIV2,
		CoroABI:                       coro.PhysicalABIV1,
		SchedulerABI:                  coro.SchedulerProgramBootstrapABIV2,
		PanicABI:                      coro.PanicLegacyABIV0,
		FuncRepABI:                    coro.FuncRepABIV0,
		EmissionUniverse:              emission,
	}
	timePkg, _, err := cl.NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, timeSSA, timeFiles, goembed.VarMap{},
		cl.PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile production-overlay time package: %v", err)
	}
	mainPkg, _, err := cl.NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, mainSSA, mainFiles, goembed.VarMap{},
		cl.PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile synchronous time.Sleep caller: %v", err)
	}

	sleepSymbol := llssa.FullName(sleep.Pkg.Pkg, sleep.Name()) + "$coro"
	sleepOnceSymbol := llssa.FullName(sleepOnce.Pkg.Pkg, sleepOnce.Name()) + "$coro"
	sleepZeroSymbol := llssa.FullName(sleepZero.Pkg.Pkg, sleepZero.Name()) + "$coro"
	sleepNegativeSymbol := llssa.FullName(sleepNegative.Pkg.Pkg, sleepNegative.Name()) + "$coro"
	mainSymbol := llssa.FullName(main.Pkg.Pkg, main.Name()) + "$coro"
	sleepPhysical := timePkg.Module().NamedFunction(sleepSymbol)
	sleepOncePhysical := mainPkg.Module().NamedFunction(sleepOnceSymbol)
	sleepZeroPhysical := mainPkg.Module().NamedFunction(sleepZeroSymbol)
	sleepNegativePhysical := mainPkg.Module().NamedFunction(sleepNegativeSymbol)
	mainPhysical := mainPkg.Module().NamedFunction(mainSymbol)
	if sleepPhysical.IsNil() || sleepOncePhysical.IsNil() || sleepZeroPhysical.IsNil() || sleepNegativePhysical.IsNil() || mainPhysical.IsNil() {
		t.Fatalf("production time.Sleep codegen bodies = Sleep:%t once:%t zero:%t negative:%t main:%t; want all physical",
			!sleepPhysical.IsNil(), !sleepOncePhysical.IsNil(), !sleepZeroPhysical.IsNil(), !sleepNegativePhysical.IsNil(), !mainPhysical.IsNil())
	}
	sleepIR := sleepPhysical.String()
	assertCoroTimerSleepV2FrameIR(t, "production time.Sleep", sleepIR, sleepPlan.Exec.Contains(coro.NeedsPreempt))
	assertCoroTimeSleepAwaitsIR(t, "sleepOnce", sleepOncePhysical.String(), sleepSymbol)
	assertCoroTimeSleepAwaitsIR(t, "sleepZero", sleepZeroPhysical.String(), sleepSymbol)
	assertCoroTimeSleepAwaitsIR(t, "sleepNegative", sleepNegativePhysical.String(), sleepSymbol)
	assertCoroTimeSleepAwaitsIR(t, "main", mainPhysical.String(), sleepZeroSymbol, sleepNegativeSymbol, sleepOnceSymbol)
	runCoroSpawnNativeE2EPasses(t, prog, timePkg.Module())
	runCoroSpawnNativeE2EPasses(t, prog, mainPkg.Module())
}

type coroTimeSleepImporter struct {
	local    map[string]*types.Package
	fallback types.Importer
}

func (p coroTimeSleepImporter) Import(path string) (*types.Package, error) {
	if pkg := p.local[path]; pkg != nil {
		return pkg, nil
	}
	return p.fallback.Import(path)
}

func buildCoroTimeSleepOverlaySSA(
	t *testing.T, injectedPath string, injected []byte, callerPath string, caller []byte,
) (*ssa.Program, *ssa.Package, []*ast.File, *ssa.Package, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	parse := func(filename string, source []byte) *ast.File {
		file, err := parser.ParseFile(fset, filename, source, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		return file
	}
	patchFile := parse(injectedPath, injected)
	supportFile := parse("coro_time_sleep_support.go", []byte(`package time
type Duration int64
const Nanosecond Duration = 1
type Timer struct{}
func when(Duration) int64
`))
	timeFiles := []*ast.File{patchFile, supportFile}
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
	base := importer.Default()
	timeTypes := types.NewPackage("time", "time")
	timeInfo := newInfo()
	if err := types.NewChecker(&types.Config{Importer: base}, fset, timeTypes, timeInfo).Files(timeFiles); err != nil {
		t.Fatalf("type-check production-overlay time.Sleep: %v", err)
	}

	mainFile := parse(callerPath, caller)
	mainFiles := []*ast.File{mainFile}
	mainTypes := types.NewPackage("example.com/llgo-coro-time-sleep", "main")
	mainInfo := newInfo()
	localImporter := coroTimeSleepImporter{local: map[string]*types.Package{"time": timeTypes}, fallback: base}
	if err := types.NewChecker(&types.Config{Importer: localImporter}, fset, mainTypes, mainInfo).Files(mainFiles); err != nil {
		t.Fatalf("type-check synchronous time.Sleep caller: %v", err)
	}

	ssaProg := ssa.NewProgram(fset, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	created := make(map[*types.Package]bool)
	var createDependencies func(*types.Package)
	createDependencies = func(pkg *types.Package) {
		if pkg == nil || created[pkg] {
			return
		}
		created[pkg] = true
		for _, imported := range pkg.Imports() {
			createDependencies(imported)
		}
		ssaProg.CreatePackage(pkg, nil, nil, true)
	}
	for _, imported := range timeTypes.Imports() {
		createDependencies(imported)
	}
	timeSSA := ssaProg.CreatePackage(timeTypes, timeFiles, timeInfo, true)
	created[timeTypes] = true
	mainSSA := ssaProg.CreatePackage(mainTypes, mainFiles, mainInfo, true)
	ssaProg.Build()
	return ssaProg, timeSSA, timeFiles, mainSSA, mainFiles
}

func findCoroTimeSleepFunction(prog *ssa.Program, path, name string) (*ssa.Function, error) {
	if prog == nil {
		return nil, fmt.Errorf("find %s.%s: nil SSA program", path, name)
	}
	var found *ssa.Function
	for _, pkg := range prog.AllPackages() {
		if pkg == nil || pkg.Pkg == nil || llssa.PathOf(pkg.Pkg) != path {
			continue
		}
		member, ok := pkg.Members[name].(*ssa.Function)
		if !ok || member == nil {
			continue
		}
		if found != nil && found != member {
			return nil, fmt.Errorf("production source has ambiguous %s.%s SSA bodies", path, name)
		}
		found = member
	}
	if found == nil {
		return nil, fmt.Errorf("production source has no %s.%s SSA function", path, name)
	}
	return found, nil
}

func assertCoroTimeSleepFunctionPlan(plan *coro.SSAPlan, function *ssa.Function, intrinsicOwner bool, label string) error {
	got, ok := plan.FunctionPlan(function)
	if !ok || got.External != coro.Defined || got.Emission != coro.EmitCoroutine ||
		got.Primary != coro.PrimaryCoroutine || got.FuncRep != coro.DirectCoro ||
		!got.Effect.Contains(coro.MayPark) {
		return fmt.Errorf("%s production plan = %+v, present=%t; want one defined DirectCoro MayPark body", label, got, ok)
	}
	if intrinsicOwner {
		if !got.DeclaredEffect.Contains(coro.MayPark) || !got.LocalEffect.Contains(coro.MayPark) {
			return fmt.Errorf("%s production local effect = declared:%s local:%s, want intrinsic MayPark seed", label, got.DeclaredEffect, got.LocalEffect)
		}
		return nil
	}
	if got.DeclaredEffect.MaySuspend() || got.LocalEffect.MaySuspend() || !got.Effect.Contains(coro.AwaitStructured) {
		return fmt.Errorf("%s production effects = declared:%s local:%s total:%s, want unseeded synchronous source automatically tainted by MayPark+AwaitStructured", label, got.DeclaredEffect, got.LocalEffect, got.Effect)
	}
	return nil
}

func findCoroTimeSleepDirectCall(owner, target *ssa.Function) (*ssa.Call, error) {
	if owner == nil || target == nil {
		return nil, fmt.Errorf("find production time.Sleep call: nil owner or target")
	}
	var found *ssa.Call
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil || call.Common().StaticCallee() != target {
				continue
			}
			if found != nil {
				return nil, fmt.Errorf("%s has duplicate direct calls to %s", owner.Name(), target.Name())
			}
			found = call
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%s has no direct call to %s", owner.Name(), target.Name())
	}
	return found, nil
}

// The whole-program plan is intentionally value-insensitive, so Sleep(0) and
// Sleep(-1) callers still use one DirectCoro child await. This CFG proof is
// narrower: the nonpositive body returns before entering the dedicated timer
// intrinsic. Avoiding the caller-side coroutine
// handoff as required by Go's immediate-return behavior still needs a
// conditional-effect or call-site fast-path ABI.
func assertCoroTimeSleepNonpositiveFastPath(t *testing.T, sleep *ssa.Function, intrinsic *ssa.Call) {
	t.Helper()
	if sleep == nil || len(sleep.Params) != 1 || len(sleep.Blocks) == 0 || intrinsic == nil {
		t.Fatal("production time.Sleep fast-path proof requires one parameter, body, and timer intrinsic call")
	}
	entry := sleep.Blocks[0]
	if len(entry.Instrs) == 0 || len(entry.Succs) != 2 {
		t.Fatalf("production time.Sleep entry CFG = instructions:%d successors:%d, want one binary duration guard", len(entry.Instrs), len(entry.Succs))
	}
	branch, ok := entry.Instrs[len(entry.Instrs)-1].(*ssa.If)
	if !ok {
		t.Fatalf("production time.Sleep entry terminator = %T, want duration guard", entry.Instrs[len(entry.Instrs)-1])
	}
	comparison, ok := branch.Cond.(*ssa.BinOp)
	if !ok || comparison.Op != token.LEQ {
		t.Fatalf("production time.Sleep guard = %T %v, want d <= 0", branch.Cond, branch.Cond)
	}
	parameterAndZero := func(parameter, zero ssa.Value) bool {
		value, ok := zero.(*ssa.Const)
		return parameter == sleep.Params[0] && ok && value.Value != nil && constant.Sign(value.Value) == 0
	}
	if !parameterAndZero(comparison.X, comparison.Y) && !parameterAndZero(comparison.Y, comparison.X) {
		t.Fatalf("production time.Sleep guard = %s, want its exact duration parameter and zero", comparison)
	}
	fast, positive := entry.Succs[0], entry.Succs[1]
	if coroTimeSleepBlockReachesInstruction(fast, intrinsic) || !coroTimeSleepBlockReachesInstruction(positive, intrinsic) {
		t.Fatalf("production time.Sleep d<=0/positive intrinsic reachability = %t/%t, want false/true",
			coroTimeSleepBlockReachesInstruction(fast, intrinsic), coroTimeSleepBlockReachesInstruction(positive, intrinsic))
	}
	if len(fast.Instrs) == 0 {
		t.Fatal("production time.Sleep d<=0 branch is empty")
	}
	if _, ok := fast.Instrs[len(fast.Instrs)-1].(*ssa.Return); !ok {
		t.Fatalf("production time.Sleep d<=0 terminator = %T, want immediate return", fast.Instrs[len(fast.Instrs)-1])
	}
	for _, block := range []*ssa.BasicBlock{entry, fast} {
		for _, instruction := range block.Instrs {
			if alloc, ok := instruction.(*ssa.Alloc); ok {
				t.Fatalf("production time.Sleep d<=0 path allocates retained state before return: %s", alloc)
			}
		}
	}
}

// assertCoroTimerSleepV2FrameIR checks the production source patch after its
// single intrinsic has become the compiler-owned source-aware park recipe.
// The opaque state must be a coroutine-frame local, with exactly one park /
// suspend / resume sequence and no ordinary heap allocation or V1 token span.
func assertCoroTimerSleepV2FrameIR(t *testing.T, label, body string, needsPreempt bool) {
	t.Helper()
	for _, forbidden := range []string{
		"runtime.AllocZ",
		coroTimeSleepPrepareSymbolV1,
		coroTimeSleepRetireSymbolV1,
		"llgo.coroTimerSleep",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("%s contains obsolete or escaping Timer storage %q:\n%s", label, forbidden, body)
		}
	}
	park := strings.Index(body, "call void @"+coroTimeSleepParkSymbolV2)
	resume := strings.Index(body, "call i32 @"+coroTimeSleepResumeSymbolV2)
	if park < 0 || resume <= park || strings.Count(body, coroTimeSleepParkSymbolV2) != 1 ||
		strings.Count(body, coroTimeSleepResumeSymbolV2) != 1 {
		t.Fatalf("%s does not contain one ordered Timer V2 park/resume pair:\n%s", label, body)
	}
	span := body[park:resume]
	if got := strings.Count(span, "@llvm.coro.suspend"); got != 1 {
		t.Fatalf("%s Timer V2 park/resume span has %d suspends, want 1:\n%s", label, got, body)
	}
	if !regexp.MustCompile(
		`(?s)store i16 4,.*store i16 3,.*call void @` + regexp.QuoteMeta(coroTimeSleepParkSymbolV2) +
			`\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, i64 [^)]+\)`,
	).MatchString(body) {
		t.Fatalf("%s does not publish SuspendPark before the exact Timer V2 park ABI:\n%s", label, body)
	}
	for _, forbidden := range []string{coroTimeSleepPreemptPollV1, coroTimeSleepAwaitHookV1, "__llgo_coro_yield_prepare_v1"} {
		if strings.Contains(span, forbidden) {
			t.Fatalf("%s Timer V2 park/resume span contains forbidden handoff %q:\n%s", label, forbidden, body)
		}
	}
	if needsPreempt && !strings.Contains(body[:park], coroTimeSleepPreemptPollV1) {
		t.Fatalf("%s has no preemption poll before its Timer V2 park:\n%s", label, body)
	}
}

func coroTimeSleepBlockReachesInstruction(start *ssa.BasicBlock, want ssa.Instruction) bool {
	if start == nil || want == nil {
		return false
	}
	seen := make(map[*ssa.BasicBlock]bool)
	queue := []*ssa.BasicBlock{start}
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		if block == nil || seen[block] {
			continue
		}
		seen[block] = true
		for _, instruction := range block.Instrs {
			if instruction == want {
				return true
			}
		}
		queue = append(queue, block.Succs...)
	}
	return false
}

func assertCoroTimeSleepDirectCoroCall(plan *coro.SSAPlan, call ssa.CallInstruction, target *ssa.Function, label string) error {
	targetID, ok := plan.FunctionID(target)
	if !ok {
		return fmt.Errorf("%s target is absent from production plan", label)
	}
	got, ok := plan.CallPlan(call)
	if !ok || got.Kind != coro.CallDirect || got.Rep != coro.DirectCoro || got.Open || got.MayBeNil ||
		len(got.Targets) != 1 || got.Targets[0] != targetID {
		return fmt.Errorf("%s production CallPlan = %+v, present=%t; want one closed DirectCoro target %q", label, got, ok, targetID)
	}
	return nil
}

// assertCoroTimerRetainedFrameIR checks the exact pre-transform LLVM body. A
// retained timer token must stay in the LLVM coroutine frame, not escape
// through the ordinary Go heap allocator. The only suspension after the
// fail-stop prepare and before its matching retire is the one park handoff.
// A function which independently needs preemption must poll before entering
// the transaction; no function may poll or yield while the token is retained.
func assertCoroTimerRetainedFrameIR(t *testing.T, label, body string, needsPreempt bool) {
	t.Helper()
	if strings.Contains(body, "runtime.AllocZ") {
		t.Fatalf("%s retained token escaped through runtime.AllocZ:\n%s", label, body)
	}
	prepare := strings.Index(body, coroTimeSleepPrepareSymbolV1)
	retire := strings.Index(body, coroTimeSleepRetireSymbolV1)
	if prepare < 0 || retire <= prepare || strings.Count(body, coroTimeSleepPrepareSymbolV1) != 1 || strings.Count(body, coroTimeSleepRetireSymbolV1) != 1 {
		t.Fatalf("%s does not contain one ordered prepare/retire pair:\n%s", label, body)
	}
	prepareBlockStart := strings.LastIndex(body[:prepare], "\n_llgo_")
	if prepareBlockStart < 0 {
		prepareBlockStart = 0
	}
	poll := strings.LastIndex(body[prepareBlockStart:prepare], coroTimeSleepPreemptPollV1)
	if needsPreempt && poll < 0 {
		prepareLabelStart := prepareBlockStart + 1
		prepareLabelEnd := strings.IndexByte(body[prepareLabelStart:], ':')
		if prepareBlockStart == 0 || prepareLabelEnd < 0 {
			t.Fatalf("%s timer prepare has no named LLVM block:\n%s", label, body)
		}
		prepareLabel := body[prepareLabelStart : prepareLabelStart+prepareLabelEnd]
		predecessorPoll := strings.LastIndex(body[:prepareBlockStart], coroTimeSleepPreemptPollV1)
		pollBlockStart := -1
		if predecessorPoll >= 0 {
			pollBlockStart = strings.LastIndex(body[:predecessorPoll], "\n_llgo_")
		}
		if pollBlockStart < 0 {
			t.Fatalf("%s has no preemption poll before retaining its frame token:\n%s", label, body)
		}
		pollBlockEnd := strings.Index(body[predecessorPoll:], "\n_llgo_")
		if pollBlockEnd < 0 {
			t.Fatalf("%s preemption poll block has no successor block boundary:\n%s", label, body)
		}
		pollBlock := body[pollBlockStart : predecessorPoll+pollBlockEnd]
		var branch string
		for _, line := range strings.Split(pollBlock, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "br i1 ") {
				branch = line
			}
		}
		targets := strings.Split(branch, ", label %")
		if len(targets) != 3 {
			t.Fatalf("%s nearest preemption poll does not end in a binary LLVM branch: %q\n%s", label, branch, body)
		}
		trueLabel := strings.Fields(targets[1])
		falseLabel := strings.Fields(targets[2])
		if len(trueLabel) == 0 || len(falseLabel) == 0 || falseLabel[0] != prepareLabel {
			t.Fatalf("%s nearest preemption poll false successor = %q, want timer prepare block %q: %q\n%s", label, falseLabel, prepareLabel, branch, body)
		}
		yieldBlockStart := strings.Index(body, "\n"+trueLabel[0]+":")
		if yieldBlockStart < 0 {
			t.Fatalf("%s preemption true successor %q has no LLVM block:\n%s", label, trueLabel[0], body)
		}
		yieldBlockEnd := strings.Index(body[yieldBlockStart+1:], "\n_llgo_")
		if yieldBlockEnd < 0 {
			yieldBlockEnd = len(body) - yieldBlockStart - 1
		}
		yieldBlock := body[yieldBlockStart : yieldBlockStart+1+yieldBlockEnd]
		if !strings.Contains(yieldBlock, "__llgo_coro_yield_prepare_v1") {
			t.Fatalf("%s preemption true successor %q does not prepare a yield:\n%s", label, trueLabel[0], body)
		}
	}
	parkBlockEnd := strings.Index(body[prepare:], "\n\n")
	if parkBlockEnd < 0 {
		t.Fatalf("%s prepare block has no LLVM block boundary:\n%s", label, body)
	}
	parkBlock := body[prepare : prepare+parkBlockEnd]
	park := strings.Index(parkBlock, coroTimeSleepParkHookV1)
	suspend := strings.Index(parkBlock, "@llvm.coro.suspend")
	parkCount := strings.Count(parkBlock, coroTimeSleepParkHookV1)
	suspendCount := strings.Count(parkBlock, "@llvm.coro.suspend")
	if park < 0 || suspend <= park || parkCount != 1 || suspendCount != 1 {
		t.Fatalf("%s retained-frame span is not exactly one park suspension (park=%d suspend=%d park-count=%d suspend-count=%d):\n%s", label, park, suspend, parkCount, suspendCount, body)
	}
	resumeBlockStart := strings.LastIndex(body[:retire], "\n_llgo_")
	if resumeBlockStart < 0 {
		t.Fatalf("%s retire has no LLVM resume block:\n%s", label, body)
	}
	resumePrefix := body[resumeBlockStart:retire]
	if strings.Contains(resumePrefix, "@llvm.coro.suspend") {
		t.Fatalf("%s resume-to-retire span contains a second suspension:\n%s", label, body)
	}
	for _, forbidden := range []string{coroTimeSleepPreemptPollV1, coroTimeSleepAwaitHookV1, "__llgo_coro_yield_prepare_v1"} {
		if strings.Contains(parkBlock, forbidden) || strings.Contains(resumePrefix, forbidden) {
			t.Fatalf("%s retained-frame span contains forbidden handoff %q:\n%s", label, forbidden, body)
		}
	}
}

func assertCoroTimeSleepAwaitsIR(t *testing.T, label, body string, childSymbols ...string) {
	t.Helper()
	if len(childSymbols) == 0 || strings.Count(body, coroTimeSleepAwaitHookV1) != len(childSymbols) {
		t.Fatalf("%s await handoffs = %d, want %d:\n%s", label, strings.Count(body, coroTimeSleepAwaitHookV1), len(childSymbols), body)
	}
	previous := -1
	for _, childSymbol := range childSymbols {
		child := strings.Index(body, childSymbol)
		if child <= previous || strings.Count(body, childSymbol) != 1 {
			t.Fatalf("%s does not call coroutine child %q exactly once in source order:\n%s", label, childSymbol, body)
		}
		await := strings.Index(body[child:], coroTimeSleepAwaitHookV1)
		if await < 0 {
			t.Fatalf("%s child %q is not followed by a structured await:\n%s", label, childSymbol, body)
		}
		previous = child + await
	}
}
