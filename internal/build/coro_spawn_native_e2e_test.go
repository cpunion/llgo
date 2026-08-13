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

func coroNativeE2EMainPhysicalSymbol(name string) string {
	// A package named main keeps its source import path for ownership metadata,
	// but its final Go ABI symbols always use the canonical main prefix.
	return "main." + name
}

const coroSpawnNativeE2ESource = `package main

var Data chan uint32
var Ack chan uint32
var Done chan uint32
var Buffered chan uint32
var SelectSend chan uint32
var SelectRecv chan uint32

var Got uint32
var After uint32
var BufferedGot uint32
var SelectGot uint32
var MainStage uint32
var ChildStage uint32

func child() {
	ChildStage = 1
	Data <- 0x1234abcd
	ChildStage = 2
	<-Ack
	ChildStage = 3
	After = 1
	SelectRecv <- 0x0badcafe
	ChildStage = 4
	Done <- 1
	ChildStage = 5
}

func Setup() {
	Data = make(chan uint32)
	Ack = make(chan uint32)
	Done = make(chan uint32)
	Buffered = make(chan uint32, 1)
	SelectSend = make(chan uint32)
	SelectRecv = make(chan uint32)
}

func main() {
	MainStage = 1
	go child()
	Got = <-Data
	MainStage = 2
	Ack <- 1
	MainStage = 3
	select {
	case SelectSend <- 0xfeedface:
	case SelectGot = <-SelectRecv:
	}
	MainStage = 4
	<-Done
	MainStage = 5
	Buffered <- 0xdecafbad
	BufferedGot = <-Buffered
	MainStage = 6
}

func Check() int32 {
	if Got != 0x1234abcd {
		return 11
	}
	if After != 1 {
		return 12
	}
	if BufferedGot != 0xdecafbad {
		return 13
	}
	if SelectGot != 0x0badcafe {
		return 14
	}
	// An unbuffered rendezvous orders the sender's work before the send against
	// the receiver, but Go does not require the sender's continuation to run
	// again before a main receiver returns. Stage 4 is therefore sufficient;
	// Stage 5 is also legal when the scheduler selects the child first.
	if MainStage != 6 || ChildStage < 4 || ChildStage > 5 {
		return 15
	}
	return 0
}
`

const coroChannelNativeE2ERuntimeShim = `package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

const maxAlloc = ^uintptr(0) >> 1

type errorString string

func (e errorString) Error() string { return string(e) }
func (e errorString) RuntimeError() {}

type plainError string

func (e plainError) Error() string { return string(e) }
func (e plainError) RuntimeError() {}

type boundsErrorCode uint8

// Keep the source-island model numerically identical to the production
// runtime. coro_nil_fault.go transports this code through its compact V2 fault
// kind, so a fixture-local shortened enum would test a different ABI.
const (
	boundsIndex boundsErrorCode = iota
	boundsSliceAlen
	boundsSliceAcap
	boundsSliceB
	boundsSlice3Alen
	boundsSlice3Acap
	boundsSlice3B
	boundsSlice3C
	boundsConvert
)

type boundsError struct {
	x      int64
	y      int
	signed bool
	code   boundsErrorCode
}

func (boundsError) Error() string { return "slice conversion bounds error" }
func (boundsError) RuntimeError() {}

type _type struct{}
type interfacetype struct{}
type itab struct {
	inter *interfacetype
	_type *_type
}
type iface struct {
	tab  *itab
	data unsafe.Pointer
}

type eface struct {
	_type *_type
	data  unsafe.Pointer
}

type Defer struct{}
type mOS struct{}
// The closed runtime island never calls runtime.Caller. Keep only the opaque
// per-G field target required by the production runtime2 layout.
type callerLocationStore struct{}

type PanicNilError struct {
	_ [0]*PanicNilError
}

func (*PanicNilError) Error() string { return "panic called with nil argument" }
func (*PanicNilError) RuntimeError() {}

// The compiler emits this guard in the unused pointer wrappers for the value
// receiver methods above. This closed island never invokes those wrappers, so
// keep only the exact runtime-helper signature instead of importing z_error's
// full legacy panic/string dependency graph.
//go:linkname coroChannelNativeE2EPanicWrapNilPointer github.com/goplus/llgo/runtime/internal/runtime.PanicWrapNilPointer
func coroChannelNativeE2EPanicWrapNilPointer(bool, string, string) {}

//go:linkname AllocU C.malloc
func AllocU(uintptr) unsafe.Pointer

func AllocZ(size uintptr) unsafe.Pointer { return AllocU(size) }
func AllocRoot(size uintptr) unsafe.Pointer { return AllocU(size) }

//go:linkname FreeRoot C.free
func FreeRoot(unsafe.Pointer)

func fatal(message string) { coroRuntimeAbort(message) }

// The closed scheduler islands deliberately omit the caller/symbolization
// closure. A foreign hardware fault therefore remains fail-closed here; full
// runtime acceptance tests exercise StoreCoroWorkerFaultPCs itself.
func StoreCoroWorkerFaultPCs(*coro.G, uintptr, uintptr) bool { return false }

// libc rand returns C int. Keep this fixture ABI identical to every other
// declaration of the shared physical symbol, then perform the Go uint32
// conversion in a bodyful wrapper.
//
//llgo:coro sync
//go:linkname libcRand C.rand
func libcRand() int32

func fastrand() uint32 { return uint32(libcRand()) }
`

// TestCoroChannelAndClosedStaticSpawnNativeNoStdlibRuntimeE2E is deliberately a
// scheduler-island smoke test, not a claim that the complete standard-library
// runtime startup or its legacy PanicABI is coroutine-safe. The compiler emits
// the real closed-static-go and typed channel lowering plus the real V2
// entry/factory/control wrappers. The first four V2 init stages are bounded
// no-ops, while the linked production coroutine adapter/core uses its native
// nogc allocator backend.
//
// Three direct unbuffered rendezvous force main and its child through both send
// and receive slow paths. A two-case select adds one multi-event rendezvous and
// loser cleanup, then a capacity-one channel verifies the same lowering's
// nonblocking buffer fast path before main returns and command shutdown runs.
func TestCoroChannelAndClosedStaticSpawnNativeNoStdlibRuntimeE2E(t *testing.T) {
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

	userObject, anchor, setupSymbol, checkSymbol := buildCoroSpawnNativeE2EUser(t, prog, temp)
	entryObject := buildCoroSpawnNativeE2EEntry(t, prog, temp, anchor)
	driverObject := buildCoroSpawnNativeE2EDriver(t, prog, temp, setupSymbol, checkSymbol)
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

func buildCoroSpawnNativeE2EUser(t *testing.T, prog llssa.Program, temp string) (object, anchor, setupSymbol, checkSymbol string) {
	return buildCoroSpawnNativeE2EUserSource(
		t, prog, temp, coroSpawnNativeE2ESource, true, coro.TargetCapabilities(0),
	)
}

func buildCoroSpawnNativeE2EUserSource(
	t *testing.T,
	prog llssa.Program,
	temp, source string,
	enableChannel bool,
	targetCapabilities coro.TargetCapabilities,
) (object, anchor, setupSymbol, checkSymbol string) {
	t.Helper()
	ssaPkg, files := buildCoroPlanTestPackage(t, coroSpawnNativeE2EPackage, source, nil)
	universe, err := cl.PrepareEmissionUniverseWithOptions(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: coroSpawnNativeE2EPackage,
	}}, cl.EmissionUniverseOptions{CoroTargetCapabilities: targetCapabilities})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	mainFn, childFn := ssaPkg.Func("main"), ssaPkg.Func("child")
	grandchildFn := ssaPkg.Func("grandchild")
	runPhaseFn := ssaPkg.Func("runPhase")
	setupFn, checkFn := ssaPkg.Func("Setup"), ssaPkg.Func("Check")
	spawnSeeded := make(map[*ssa.Function]struct{})
	for _, fn := range universe.Functions() {
		if fn == nil {
			continue
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				spawn, ok := instruction.(*ssa.Go)
				if !ok || spawn.Common() == nil {
					continue
				}
				spawnSeeded[fn] = struct{}{}
				if target := spawn.Common().StaticCallee(); target != nil {
					if canonical, exact := universe.Resolve(target); exact {
						spawnSeeded[canonical] = struct{}{}
					}
				}
			}
		}
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	schedulerABI := coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	if enableChannel {
		schedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	}
	if targetCapabilities.Worker() {
		schedulerABI = coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	}
	functionIDs.SchedulerABI = schedulerABI
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: mainFn, Demand: coro.AsyncDemand},
		{Function: setupFn, Demand: coro.SyncDemand},
		{Function: checkFn, Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			effect := coro.NoSuspend
			if _, required := spawnSeeded[fn]; required {
				effect = effect.Join(coro.YieldOnly)
			}
			switch fn {
			case mainFn, childFn, grandchildFn, runPhaseFn:
				effect = effect.Join(coro.YieldOnly)
			}
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(ssa.CallInstruction)
					if !ok || call.Common() == nil {
						continue
					}
					semantics, intrinsic, err := coroIntrinsicCallSiteSemanticsForTest(
						universe,
						call,
					)
					if err != nil {
						return coro.SSAFunctionPolicy{}, err
					}
					if intrinsic && semantics.SuspendsCurrentFrame() {
						effect = effect.Join(semantics.CurrentFrameEffect())
					}
				}
			}
			if effect != coro.NoSuspend {
				return coro.SSAFunctionPolicy{Effect: effect}, nil
			}
			if fn.Name() == "gomaxprocs" {
				return coro.SSAFunctionPolicy{
					Effect: coro.NoSuspend, IgnoreBody: true,
					External: coro.ExternalKnown, OverrideExternal: true,
				}, nil
			}
			callable, certified, err := universe.CoroCallableContractCertificate(fn)
			if err != nil {
				return coro.SSAFunctionPolicy{}, err
			}
			if certified {
				background, classified, err := universe.FunctionBackground(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, err
				}
				return applyFrozenCallableContractPolicy(
					fn,
					classified && background == llssa.InC,
					coro.SSAFunctionPolicy{},
					callable,
				)
			}
			noblock, certified, err := universe.CoroForeignNoBlockCertificate(fn)
			if err != nil {
				return coro.SSAFunctionPolicy{}, err
			}
			if certified {
				return coro.SSAFunctionPolicy{
					Effect: coro.NoSuspend, Exec: coro.IRQUnsafe,
					IgnoreBody: true, External: coro.ExternalKnown, OverrideExternal: true,
					ForeignNoBlockCertificate: noblock.ID,
				}, nil
			}
			synchronous, certified, err := universe.CoroForeignSyncCertificate(fn)
			if err != nil {
				return coro.SSAFunctionPolicy{}, err
			}
			if certified {
				return coro.SSAFunctionPolicy{
					Effect: coro.NoSuspend, Exec: coro.IRQUnsafe,
					IgnoreBody: true, External: coro.ExternalKnown, OverrideExternal: true,
					ForeignSyncCertificate: synchronous.ID,
				}, nil
			}
			worker, certified, err := universe.CoroForeignWorkerCertificate(fn)
			if err != nil {
				return coro.SSAFunctionPolicy{}, err
			}
			if certified {
				return coro.SSAFunctionPolicy{
					Effect: coro.NoSuspend, Exec: coro.BlockForeign | coro.IRQUnsafe,
					IgnoreBody: true, External: coro.ExternalUnknownForeign, OverrideExternal: true,
					ForeignWorkerCertificate: worker.ID,
				}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Pkg != nil &&
				callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
			semantics, intrinsic, err := coroIntrinsicCallSiteSemanticsForTest(universe, call)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compilation := &cl.Compilation{
		CoroPlan: plan,

		CoroFrameRetentionABI:  cl.CoroFrameRetentionParkABIV2,
		CoroABI:                coro.PhysicalABIV1,
		SchedulerABI:           schedulerABI,
		PanicABI:               coro.PanicExplicitStatusABIV0,
		FuncRepABI:             coro.FuncRepABIV1,
		CoroTargetCapabilities: targetCapabilities,
		EmissionUniverse:       universe}
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
	checkSymbol = coroNativeE2EMainPhysicalSymbol("Check")
	if module.NamedFunction(checkSymbol).IsNil() {
		t.Fatalf("compiled E2E user module has no plain checker %q:\n%s", checkSymbol, ir)
	}
	setupSymbol = coroNativeE2EMainPhysicalSymbol("Setup")
	if module.NamedFunction(setupSymbol).IsNil() {
		t.Fatalf("compiled E2E user module has no plain setup %q:\n%s", setupSymbol, ir)
	}
	return emitCoroSpawnNativeE2EObject(t, prog, module, filepath.Join(temp, "user.o")), match[1], setupSymbol, checkSymbol
}

func buildCoroSpawnNativeE2EEntry(t *testing.T, prog llssa.Program, temp, anchor string) string {
	t.Helper()
	conf := &Config{
		BuildMode: BuildModeExe,
		Goos:      runtime.GOOS,
		Goarch:    runtime.GOARCH}
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
				FunctionID: "e2e-main", Target: coroNativeE2EMainPhysicalSymbol("main$coro"),
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

func buildCoroSpawnNativeE2EDriver(t *testing.T, prog llssa.Program, temp, setupSymbol, checkSymbol string) string {
	t.Helper()
	pkg := prog.NewPackage("coro-spawn-e2e-driver", "coro-spawn-e2e-driver")
	defer pkg.Module().Dispose()
	pointer := types.Typ[types.UnsafePointer]
	entry := pkg.NewFunc(coroSpawnNativeE2EEntry, newSignature(
		[]types.Type{types.Typ[types.Int32], pointer}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	setup := pkg.NewFunc(setupSymbol, newSignature(nil, nil), llssa.InGo)
	check := pkg.NewFunc(checkSymbol, newSignature(nil, []types.Type{types.Typ[types.Int32]}), llssa.InGo)
	// The production scheduler core is intentionally compiled without the full
	// standard-library runtime package in its coroutine plan. LLGo's ordinary
	// pointer checks name this legacy helper even though every valid scheduler
	// path passes false. Keep the test island fail-stop without pulling the
	// legacy panic/printing closure into the final executable.
	exit := pkg.NewFunc("exit", newSignature([]types.Type{types.Typ[types.Int32]}, nil), llssa.InC)
	// These closed scheduler islands verify successful spawn/fleet behavior,
	// not terminal panic presentation. Keep the production entry relocation
	// exact and fail-stop; full-runtime caller acceptance separately exercises
	// the production logical traceback reporter.
	panicReporter := pkg.NewFunc(coroProgramReportPanicSymbolV1, newSignature(
		[]types.Type{pointer}, nil,
	), llssa.InC)
	panicReporterBody := panicReporter.MakeBody(1)
	panicReporterBody.Call(exit.Expr, prog.IntVal(71, prog.Int32()))
	panicReporterBody.Return()
	abort := pkg.NewFunc("__llgo_coro_channel_e2e_fail", newSignature(nil, nil), llssa.InC)
	abortBody := abort.MakeBody(1)
	abortBody.Call(exit.Expr, prog.IntVal(70, prog.Int32()))
	abortBody.Return()
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
	// The production channel sources are compiled as a closed named-file
	// island, so their ordinary Go helper symbols carry the temporary
	// command-line package owner. Exact wrappers expose the runtime helper names
	// frozen into the user module; the exported coroutine hooks already use
	// their production C ABI names directly.
	intType := types.Typ[types.Int]
	boolType := types.Typ[types.Bool]
	rawNewChan := pkg.NewFunc("command-line-arguments.NewChan", newSignature(
		[]types.Type{intType, intType}, []types.Type{pointer},
	), llssa.InGo)
	newChan := pkg.NewFunc(llssa.PkgRuntime+".NewChan", newSignature(
		[]types.Type{intType, intType}, []types.Type{pointer},
	), llssa.InGo)
	newChanBody := newChan.MakeBody(1)
	newChanBody.Return(newChanBody.Call(rawNewChan.Expr, newChan.Param(0), newChan.Param(1)))
	rawTrySend := pkg.NewFunc("command-line-arguments.CoroChanTrySend", newSignature(
		[]types.Type{pointer, pointer, pointer, intType}, []types.Type{boolType},
	), llssa.InGo)
	trySend := pkg.NewFunc(llssa.PkgRuntime+".CoroChanTrySend", newSignature(
		[]types.Type{pointer, pointer, pointer, intType}, []types.Type{boolType},
	), llssa.InGo)
	trySendBody := trySend.MakeBody(1)
	trySendBody.Return(trySendBody.Call(
		rawTrySend.Expr, trySend.Param(0), trySend.Param(1), trySend.Param(2), trySend.Param(3),
	))
	rawTryRecv := pkg.NewFunc("command-line-arguments.CoroChanTryRecv", newSignature(
		[]types.Type{pointer, pointer, pointer, intType}, []types.Type{boolType, boolType},
	), llssa.InGo)
	tryRecv := pkg.NewFunc(llssa.PkgRuntime+".CoroChanTryRecv", newSignature(
		[]types.Type{pointer, pointer, pointer, intType}, []types.Type{boolType, boolType},
	), llssa.InGo)
	tryRecvBody := tryRecv.MakeBody(1)
	tryRecvResult := tryRecvBody.Call(
		rawTryRecv.Expr, tryRecv.Param(0), tryRecv.Param(1), tryRecv.Param(2), tryRecv.Param(3),
	)
	tryRecvBody.Return(tryRecvBody.Extract(tryRecvResult, 0), tryRecvBody.Extract(tryRecvResult, 1))
	chanOpSliceType := types.NewSlice(prog.RuntimeType("ChanOp").RawType())
	uint32Type := types.Typ[types.Uint32]
	rawSelectTry := pkg.NewFunc("command-line-arguments.CoroChanSelectTry", newSignature(
		[]types.Type{chanOpSliceType}, []types.Type{intType, boolType, boolType, boolType},
	), llssa.InGo)
	selectTry := pkg.NewFunc(llssa.PkgRuntime+".CoroChanSelectTry", newSignature(
		[]types.Type{chanOpSliceType}, []types.Type{intType, boolType, boolType, boolType},
	), llssa.InGo)
	selectTryBody := selectTry.MakeBody(1)
	selectTryResult := selectTryBody.Call(rawSelectTry.Expr, selectTry.Param(0))
	selectTryBody.Return(
		selectTryBody.Extract(selectTryResult, 0),
		selectTryBody.Extract(selectTryResult, 1),
		selectTryBody.Extract(selectTryResult, 2),
		selectTryBody.Extract(selectTryResult, 3),
	)
	selectParkParams := []types.Type{pointer, pointer, pointer, pointer, pointer, chanOpSliceType}
	rawSelectPark := pkg.NewFunc("command-line-arguments.CoroChanSelectPark", newSignature(
		selectParkParams, nil,
	), llssa.InGo)
	selectPark := pkg.NewFunc(llssa.PkgRuntime+".CoroChanSelectPark", newSignature(
		selectParkParams, nil,
	), llssa.InGo)
	selectParkBody := selectPark.MakeBody(1)
	selectParkBody.Call(
		rawSelectPark.Expr,
		selectPark.Param(0),
		selectPark.Param(1),
		selectPark.Param(2),
		selectPark.Param(3),
		selectPark.Param(4),
		selectPark.Param(5),
	)
	selectParkBody.Return()
	selectResumeParams := []types.Type{pointer, pointer, pointer, chanOpSliceType}
	rawSelectResume := pkg.NewFunc("command-line-arguments.CoroChanSelectResume", newSignature(
		selectResumeParams, []types.Type{intType, boolType, uint32Type},
	), llssa.InGo)
	selectResume := pkg.NewFunc(llssa.PkgRuntime+".CoroChanSelectResume", newSignature(
		selectResumeParams, []types.Type{intType, boolType, uint32Type},
	), llssa.InGo)
	selectResumeBody := selectResume.MakeBody(1)
	selectResumeResult := selectResumeBody.Call(
		rawSelectResume.Expr,
		selectResume.Param(0),
		selectResume.Param(1),
		selectResume.Param(2),
		selectResume.Param(3),
	)
	selectResumeBody.Return(
		selectResumeBody.Extract(selectResumeResult, 0),
		selectResumeBody.Extract(selectResumeResult, 1),
		selectResumeBody.Extract(selectResumeResult, 2),
	)
	anyType := types.NewInterfaceType(nil, nil)
	anyType.Complete()
	panicStub := pkg.NewFunc(llssa.PkgRuntime+".Panic", newSignature(
		[]types.Type{anyType}, nil,
	), llssa.InGo)
	panicBody := panicStub.MakeBody(1)
	panicBody.Call(abort.Expr)
	panicBody.Return()
	for _, name := range []string{"memequalptr", "strequal"} {
		equal := pkg.NewFunc(llssa.PkgRuntime+"."+name, newSignature(
			[]types.Type{pointer, pointer}, []types.Type{boolType},
		), llssa.InGo)
		equalBody := equal.MakeBody(1)
		equalBody.Return(prog.BoolVal(false))
	}
	assertDivide := pkg.NewFunc(llssa.PkgRuntime+".AssertDivideByZero", newSignature(
		[]types.Type{boolType}, nil,
	), llssa.InGo)
	assertDivideBody := assertDivide.MakeBody(3)
	divideFail, divideValid := assertDivide.Block(1), assertDivide.Block(2)
	assertDivideBody.If(assertDivide.Param(0), divideFail, divideValid)
	assertDivideBody.SetBlock(divideFail).Call(abort.Expr)
	assertDivideBody.Return()
	assertDivideBody.SetBlock(divideValid).Return()
	main := pkg.NewFunc("main", newSignature(
		[]types.Type{types.Typ[types.Int32], pointer}, []types.Type{types.Typ[types.Int32]},
	), llssa.InC)
	body := main.MakeBody(1)
	body.Call(setup.Expr)
	body.Call(entry.Expr, main.Param(0), main.Param(1))
	body.Return(body.Call(check.Expr))
	pkg.MaterializePreserveSyms()
	return emitCoroSpawnNativeE2EObject(t, prog, pkg.Module(), filepath.Join(temp, "driver.o"))
}

func buildCoroSpawnNativeE2ERuntimeIsland(t *testing.T, temp string) []string {
	t.Helper()
	files := append(coroNativeTaskContextRuntimeSources(), []string{
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_allocator.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_abort_libc.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_frame.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_program.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_run_decision.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_run_slice.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_execution_quota_default.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_sched.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_executor.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_channel_request_default.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_current_task_route_default.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_ready_distribution_default.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_executor_retired_default.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_nil_fault.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_panic_payload.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_panic_trace_release.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_executor_driver_worker_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_operation_capacity.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_spawn.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_physical_thread_capacity_native_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_native_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_worker_native_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_worker_result_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_worker_completion_program_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_target_wait_pipe_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_keyed_registry_atomic_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "coro_resume_materialize.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_coro.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_lock_coro.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_lock_coro_atomic_llgo.go"),
		filepath.Join("..", "..", "runtime", "internal", "runtime", "z_chan_wait_coro.go"),
	}...)
	requireCoroRuntimeIslandProductionSource(t, files, "coro_run_decision.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_run_slice.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_execution_quota_default.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_physical_thread_capacity_native_llgo.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_channel_request_default.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_current_task_route_default.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_ready_distribution_default.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_target_executor_retired_default.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_worker_completion_program_llgo.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_worker_result_llgo.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_nil_fault.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_panic_payload.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_panic_trace_release.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_operation_capacity.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_keyed_registry_atomic_llgo.go")
	requireCoroRuntimeIslandProductionSource(t, files, "coro_resume_materialize.go")
	requireCoroRuntimeIslandProductionSource(t, files, "z_chan.go")
	requireCoroRuntimeIslandProductionSource(t, files, "z_chan_coro.go")
	requireCoroRuntimeIslandProductionSource(t, files, "z_chan_lock_coro.go")
	requireCoroRuntimeIslandProductionSource(t, files, "z_chan_lock_coro_atomic_llgo.go")
	requireCoroRuntimeIslandProductionSource(t, files, "z_chan_wait_coro.go")
	files = materializeCoroChannelNativeE2ERuntimeIsland(t, files)
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.Tags = "nogc"
	// This source-island compile intentionally does not enable the complete
	// program bootstrap and its whole-program planner. Select its production
	// runtime files through the private compiler channel; the public Config.Tags
	// path must reject this capability as forged.
	conf.compilerBuildTags = []string{"llgo_coro", coroNativePipeBuildTag}
	configureCoroRuntimeIslandPlan(conf, "NewChan")
	allowed := map[string]bool{
		"command-line-arguments":                               true,
		"github.com/goplus/llgo/runtime/internal/coro":         true,
		"github.com/goplus/llgo/runtime/internal/coroalloc":    true,
		"github.com/goplus/llgo/runtime/internal/corodoorbell": true,
		"github.com/goplus/llgo/runtime/internal/coroworker":   true,
		"github.com/goplus/llgo/runtime/internal/runtime/math": true,
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
	prog := pkgs[0].LPkg.Prog
	for id := range allowed {
		if !seen[id] {
			t.Fatalf("production coroutine runtime island did not emit required module %q", id)
		}
	}
	objects = append(objects,
		buildCoroRuntimeIslandFaultStringStubs(t, prog, temp),
		buildCoroNativeWorkerCallObject(t, temp),
		buildCoroNativeDoorbellObject(t, temp),
	)
	prog.Dispose()
	if len(objects) != len(allowed)+3 {
		t.Fatalf("production coroutine runtime island objects = %d, want exactly %d package objects plus fault-string, worker, and doorbell leaves", len(objects), len(allowed))
	}
	return objects
}

func buildCoroRuntimeIslandFaultStringStubs(
	t *testing.T,
	prog llssa.Program,
	temp string,
) string {
	t.Helper()
	pkg := prog.NewPackage("coro-runtime-island-fault-string-stubs", "coro-runtime-island-fault-string-stubs")
	defer pkg.Module().Dispose()

	// The V2 fault ABI source remains present for exported-hook link auditing,
	// but these closed scheduler islands never execute its private
	// parameterized nil-wrapper string paths. Supply only their final physical
	// symbols instead of importing z_string/z_error or forging source-level
	// metadata for the named internal/runtime.String type.
	boolType := types.Typ[types.Bool]
	stringType := types.Typ[types.String]
	internalRuntime := llssa.PkgRuntime + "."
	symbols := []string{
		internalRuntime + "AssertRuntimeError",
		internalRuntime + "StringCat",
		internalRuntime + "StringEqual",
		internalRuntime + "StringSlice2",
	}
	for _, symbol := range symbols {
		pkg.SetExport(symbol, symbol)
	}

	assertRuntimeError := pkg.NewFunc(symbols[0], newSignature(
		[]types.Type{boolType, stringType}, nil,
	), llssa.InGo)
	assertRuntimeError.MakeBody(1).Return()
	stringCat := pkg.NewFunc(symbols[1], newSignature(
		[]types.Type{stringType, stringType}, []types.Type{stringType},
	), llssa.InGo)
	stringCat.MakeBody(1).Return(pkg.ConstString(""))
	stringEqual := pkg.NewFunc(symbols[2], newSignature(
		[]types.Type{stringType, stringType}, []types.Type{boolType},
	), llssa.InGo)
	stringEqual.MakeBody(1).Return(prog.BoolVal(false))
	stringSlice2 := pkg.NewFunc(symbols[3], newSignature(
		[]types.Type{
			stringType,
			types.Typ[types.Int64],
			types.Typ[types.Int64],
			boolType,
			boolType,
		},
		[]types.Type{stringType},
	), llssa.InGo)
	stringSlice2.MakeBody(1).Return(pkg.ConstString(""))
	pkg.MaterializePreserveSyms()
	for _, symbol := range symbols {
		function := pkg.Module().NamedFunction(symbol)
		if function.IsNil() || function.IsDeclaration() {
			t.Fatalf("runtime island fault-string stub %q is not defined", symbol)
		}
	}
	return emitCoroSpawnNativeE2EObject(
		t,
		prog,
		pkg.Module(),
		filepath.Join(temp, "runtime-fault-string-stubs.o"),
	)
}

// configureCoroRuntimeIslandPlan gives source-list E2E fixtures the exact raw
// runtime boundary that an ordinary build derives from the canonical runtime
// package identity. Source lists are deliberately loaded as
// command-line-arguments, so this is fixture provenance rather than a runtime
// profile or a production feature switch.
func configureCoroRuntimeIslandPlan(conf *Config, linkedRuntimeEntries ...string) {
	linkedRuntimeEntry := make(map[string]struct{}, len(linkedRuntimeEntries))
	for _, name := range linkedRuntimeEntries {
		linkedRuntimeEntry[name] = struct{}{}
	}
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		if input.requiredPlain == nil {
			input.requiredPlain = make(map[*ssa.Function]struct{})
		}
		if input.requiredHostPlain == nil {
			input.requiredHostPlain = make(map[*ssa.Function]struct{})
		}
		rootCount := 0
		for _, fn := range input.EmissionUniverse.Functions() {
			if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil {
				continue
			}
			pkgPath := fn.Pkg.Pkg.Path()
			if pkgPath != "command-line-arguments" && pkgPath != "github.com/goplus/llgo/runtime/internal/coro" {
				continue
			}
			if len(fn.Blocks) == 0 && input.functionBackground != nil {
				background, classified, err := input.functionBackground(fn)
				if err != nil {
					return nil, err
				}
				if classified && background == llssa.InC {
					input.requiredPlain[fn] = struct{}{}
					input.requiredHostPlain[fn] = struct{}{}
				}
				continue
			}
			_, linkedRuntimeRoot := linkedRuntimeEntry[fn.Name()]
			externalRuntimeEntry := pkgPath == "command-line-arguments" &&
				(strings.HasPrefix(fn.Name(), "__llgo_coro_") ||
					strings.HasPrefix(fn.Name(), "Coro") ||
					linkedRuntimeRoot)
			externalCoreEntry := pkgPath == "github.com/goplus/llgo/runtime/internal/coro" &&
				token.IsExported(fn.Name())
			if fn.Parent() != nil || fn.Signature == nil || fn.Signature.Recv() != nil ||
				(!externalRuntimeEntry && !externalCoreEntry) {
				continue
			}
			input.requiredRoots = append(input.requiredRoots, coro.Root{Function: fn, Demand: coro.SyncDemand})
			input.requiredPlain[fn] = struct{}{}
			input.requiredHostPlain[fn] = struct{}{}
			rootCount++
		}
		if rootCount == 0 {
			return nil, fmt.Errorf("runtime island has no exported coroutine ABI roots")
		}
		return input.Analyze(nil, coro.SSAConfig{DynamicResolution: coro.DynamicCHAClosed})
	}
}

func materializeCoroChannelNativeE2ERuntimeIsland(t *testing.T, production []string) []string {
	t.Helper()
	dir, err := os.MkdirTemp(filepath.Join("..", "..", "runtime"), ".coro-channel-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	files := make([]string, 0, len(production)+1)
	for _, source := range production {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("read production coroutine runtime source %q: %v", source, err)
		}
		destination := filepath.Join(dir, filepath.Base(source))
		if err := os.WriteFile(destination, data, 0o644); err != nil {
			t.Fatalf("materialize production coroutine runtime source %q: %v", source, err)
		}
		files = append(files, destination)
	}
	shim := filepath.Join(dir, "coro_channel_e2e_shim.go")
	if err := os.WriteFile(shim, []byte(coroChannelNativeE2ERuntimeShim), 0o644); err != nil {
		t.Fatal(err)
	}
	return append(files, shim)
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
		coroProgramRunSliceSymbolV2,
		coroProgramContinueSliceSymbolV2,
		"__llgo_coro_doorbell_open_v1",
		"__llgo_coro_doorbell_read_v1",
		"__llgo_coro_doorbell_write_v1",
		"__llgo_coro_doorbell_close_v1",
		"__llgo_coro_doorbell_poll_one_v1",
		"__llgo_coro_spawn_begin_v1",
		"__llgo_coro_spawn_commit_v1",
		"__llgo_coro_chan_send_park_v1",
		"__llgo_coro_chan_recv_park_v1",
		"__llgo_coro_chan_resume_v1",
		"__llgo_coro_fault_prepare_v1",
		"__llgo_coro_fault_prepare_v2",
		"__llgo_coro_fault_payload_v2",
		"github.com/goplus/llgo/runtime/internal/coro.CommitSpawn",
		"github.com/goplus/llgo/runtime/internal/coro.BeginChannelExternalCommit",
		"github.com/goplus/llgo/runtime/internal/coro.BeginChannelExternalCommitPair",
		"github.com/goplus/llgo/runtime/internal/coro.RequestCommandShutdownDrain",
		"github.com/goplus/llgo/runtime/internal/coro.BeginCommandShutdown",
	} {
		if !strings.Contains(symbols, required) {
			t.Fatalf("linked coroutine island is missing production symbol %q:\n%s", required, symbols)
		}
	}
	for _, forbidden := range []string{
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
	llssa.RemoveKeepAliveCallsAfterCoroSplit(module)
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify E2E coroutine module after CoroSplit: %v\n%s", err, module.String())
	}
}

func emitCoroSpawnNativeE2EObject(t *testing.T, prog llssa.Program, module llvm.Module, path string) string {
	t.Helper()
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		if strings.HasPrefix(function.Name(), "llvm.coro.") && !function.FirstUse().IsNil() {
			// ModuleHook-based fixtures intercept frontend IR before buildPkg's
			// mandatory backend boundary. Lower exactly once here before asking
			// TargetMachine for native code; already-lowered handcrafted modules
			// retain only unused intrinsic declarations and skip this path.
			runCoroSpawnNativeE2EPasses(t, prog, module)
			break
		}
	}
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
