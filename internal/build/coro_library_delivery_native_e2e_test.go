//go:build !llgo

package build

import (
	stdcontext "context"
	"go/ast"
	goimporter "go/importer"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/xgo-dev/llgo/cl"
	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

// Real producer records, archive extraction and the production scheduler must
// agree on normal delivery, discarded defer output and canceled child custody.
// The consumer's imported type package has only exported declarations: neither
// private helper gets an invented SSA function or a substitute runtime body.
func TestCoroArchiveDeliveryNativeNoStdlibRuntimeE2E(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native archive delivery requires POSIX")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	llssa.Initialize(llssa.InitAll)
	temp := t.TempDir()
	const libraryPath = "example.com/coro-resource-library"
	const librarySource = `package library
import "unsafe"
type Result = struct { R0 uint32 }
var acquired, released, steps, attempts, lastDrop uint32
//go:linkname yield llgo.coroYield
func yield()
func initialize(out *Result) { out.R0 = 0 }
func drop(out *Result, _ unsafe.Pointer, _ uint32) {
 attempts++
 if out.R0 != 0 { lastDrop = out.R0; released++; yield(); steps++; yield(); steps++; out.R0 = 0 }
}
//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*Result), func(*Result, unsafe.Pointer, uint32)) *Result
func Acquire(value uint32) uint32 {
 out := borrow(initialize, drop); acquired++; out.R0 = value
 if value == 99 { for { yield() } }
 yield(); return out.R0
}
func Stats() uint32 { return acquired*100 + released*10 + steps }
func DropInfo() uint32 { return attempts*1000 + lastDrop }
`
	archive := filepath.Join(temp, "resource.a")
	importcfg := filepath.Join(temp, "importcfg")
	if err := os.WriteFile(importcfg, []byte("packagefile "+libraryPath+"="+archive+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	newProgram := func() llssa.Program {
		prog := llssa.NewProgram(nil)
		prog.SetRuntime(func() *types.Package {
			pkg, err := goimporter.For("source", nil).Import(llssa.PkgRuntime)
			if err != nil {
				t.Fatal(err)
			}
			return pkg
		})
		prog.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
		t.Cleanup(prog.Dispose)
		return prog
	}
	var spawnSignature *types.Signature
	const observedSpawn = "__llgo_archive_delivery_spawn"
	build := func(prog llssa.Program, source *ssa.Package, files []*ast.File, producer bool) (string, string, coro.ProgramCapabilities) {
		t.Helper()
		conf := &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH}
		if !producer {
			conf.ImportCfg = importcfg
		}
		ctx := &context{buildConf: conf, prog: prog}
		index, metadata, err := loadCoroLibraryEffectIndex(ctx)
		if err != nil {
			t.Fatal(err)
		}
		inputs := []cl.EmissionPackage{{SSA: source, Files: files, Identity: source.Pkg.Path()}}
		if !producer {
			library := source.Prog.ImportedPackage(libraryPath)
			if library == nil || library.Func("initialize") != nil || library.Func("drop") != nil {
				t.Fatal("consumer did not isolate private helper declarations")
			}
			inputs = append(inputs, cl.EmissionPackage{SSA: library, Identity: libraryPath})
		}
		for _, input := range inputs {
			cl.ParsePkgSyntax(prog, source.Prog.Fset, input.SSA.Pkg, input.Files)
		}
		u, err := cl.PrepareEmissionUniverseWithOptions(prog, nil, inputs, cl.EmissionUniverseOptions{
			CoroLibraryEffects: index, CoroTargetCapabilities: metadata.TargetCapabilities,
			CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask()},
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx.coroEmission = u
		imported, err := prepareCoroImportedLibraryEffects(ctx, index, metadata)
		if err != nil {
			t.Fatal(err)
		}
		imports, err := u.CoroPlanningMetadata().ImportedFunctions()
		if err != nil {
			t.Fatal(err)
		}
		if !producer && len(imports) != 2 {
			t.Fatalf("archive-only helpers = %d, want 2", len(imports))
		}
		universe, err := coro.NewSSAEmissionUniverse(source.Prog, u.Functions())
		if err != nil {
			t.Fatal(err)
		}
		ids := u.FunctionIDConfig()
		ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = metadata.CoroABI, metadata.SchedulerABI, true
		roots := coro.Roots{{Function: source.Func("main"), Demand: coro.AsyncDemand}}
		if producer {
			roots = coro.Roots{{Function: source.Func("Acquire"), ManagedDemand: coro.AsyncDemand, EmissionEntry: true}, {Function: source.Func("Stats"), ManagedDemand: coro.AsyncDemand, EmissionEntry: true}, {Function: source.Func("DropInfo"), ManagedDemand: coro.AsyncDemand, EmissionEntry: true}}
		}
		input := CoroPlanInput{Program: source.Prog, EmissionUniverse: universe,
			resolveFunction: u.Resolve, localBodyFacts: u.CoroLocalBodyFacts, functionBackground: u.FunctionBackground,
			callSitePlan: u.CoroCallSitePlan, loweredCalls: u.CoroLoweredCalls,
			staticFunctionReferenceCallArgument: u.CoroStaticFunctionReferenceCallArgument,
			importedLibraryEffects:              imported, importedFunctions: imports,
		}
		plan, err := input.Analyze(roots, coro.SSAConfig{FunctionIDs: ids, MaxPlainInstructions: -1, OutcomeMode: coro.OutcomeExplicitStatus})
		if err != nil {
			t.Fatal(err)
		}
		if err := u.ValidateCoroPlan(plan); err != nil {
			t.Fatal(err)
		}
		report, err := u.BuildCoroLoweringFactsReport(plan)
		if err != nil {
			t.Fatal(err)
		}
		planMetadata := coro.PlanDigestMetadata{CoroABI: metadata.CoroABI, SchedulerABI: metadata.SchedulerABI,
			ProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask()},
			PanicABI:          metadata.PanicABI, FuncRepABI: metadata.FuncRepABI, TargetTriple: metadata.TargetTriple,
			TargetCPU: metadata.TargetCPU, TargetFeatures: metadata.TargetFeatures, TargetABI: metadata.TargetABI,
			PointerBits: metadata.PointerBits, Endianness: metadata.Endianness, DataLayout: metadata.DataLayout,
			LoweringFactsSchema: report.Facts.Schema, LoweringFactsDigest: report.Digest}
		digest, err := plan.CoroPlanDigest(planMetadata)
		if err != nil {
			t.Fatal(err)
		}
		compilation := &cl.Compilation{CoroPlan: plan, EmissionUniverse: u, CoroPlanMetadata: planMetadata, CoroPlanDigest: digest,
			CoroLoweringFacts: report.Facts, CoroLoweringFactsDigest: report.Digest,
			CoroABI: metadata.CoroABI, SchedulerABI: metadata.SchedulerABI, PanicABI: metadata.PanicABI, FuncRepABI: metadata.FuncRepABI,
			CoroTargetCapabilities: metadata.TargetCapabilities, CoroLibraryEffectMetadata: metadata, CoroLibraryEffects: imported}
		capabilities := freezeCoroNativeE2EProgramCapabilities(t, compilation)
		if err := compilation.ValidateCoroLibraryProgramPredicates(); err != nil {
			t.Fatal(err)
		}
		pkg, _, err := cl.NewPackageExWithEmbedOptions(prog, nil, nil, nil, source, files, goembed.VarMap{}, cl.PackageOptions{Compilation: compilation})
		if err != nil {
			t.Fatal(err)
		}
		module := pkg.Module()
		defer module.Dispose()
		if dir := os.Getenv("LLGO_CORO_ARCHIVE_DEBUG_DIR"); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, source.Pkg.Name()+"-presplit.ll"), []byte(module.String()), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		records, err := cl.CoroLibraryEffectSummaryRecords(pkg)
		if err != nil || len(records) == 0 {
			t.Fatalf("actual producer records: %v (%d bytes)", err, len(records))
		}
		runCoroSpawnNativeE2EPasses(t, prog, module)
		if dir := os.Getenv("LLGO_CORO_ARCHIVE_DEBUG_DIR"); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, source.Pkg.Name()+"-postsplit.ll"), []byte(module.String()), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if producer {
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatal(err)
			}
			defer object.Dispose()
			if err := ctx.createPackageArchiveFile(archive, &aPackage{ObjBuffers: []packageArchiveBuffer{{name: "resource.o", buffer: object}}, CoroLibraryEffectRecords: records}, false); err != nil {
				t.Fatal(err)
			}
			return "", "", capabilities
		}
		spawn := module.NamedFunction("__llgo_coro_spawn_begin_v1")
		if spawn.IsNil() {
			t.Fatal("consumer has no spawn boundary")
		}
		spawnSignature = pkg.FuncOf("__llgo_coro_spawn_begin_v1").RawType().(*types.Signature)
		spawn.SetName(observedSpawn)
		ir := module.String()
		match := regexp.MustCompile(`@"?(__llgo_coro_root_package_v1\.[0-9a-f]{32})"?\s*=`).FindStringSubmatch(ir)
		if len(match) != 2 {
			t.Fatalf("consumer has no root package anchor:\n%s", ir)
		}
		return emitCoroSpawnNativeE2EObject(t, prog, module, filepath.Join(temp, "consumer.o")), match[1], capabilities
	}
	producer, files := buildCoroPlanTestPackage(t, libraryPath, librarySource, nil)
	build(newProgram(), producer, files, true)
	declarations, _ := buildCoroPlanTestPackage(t, libraryPath, "package library\nfunc Acquire(uint32) uint32\nfunc Stats() uint32\nfunc DropInfo() uint32\n", nil)
	consumer, files := buildCoroPlanTestPackage(t, coroSpawnNativeE2EPackage, `package main
import lib "`+libraryPath+`"
import _ "unsafe"
//go:linkname yield llgo.coroYield
func yield()
var Normal, CancelIssued uint32
func child() { _ = lib.Acquire(99); Normal = 1000 }
func main() {
 Normal = lib.Acquire(7)
 defer lib.Acquire(9)
 go child()
 for CancelIssued == 0 { yield() }
 for lib.Stats() != 212 || lib.DropInfo() != 1099 { yield() }
}
`, coroPlanTestImporter{libraryPath: declarations.Pkg, "unsafe": types.Unsafe})
	consumerProg := newProgram()
	consumerObject, anchor, capabilities := build(consumerProg, consumer, files, false)
	observerSymbols := map[string]string{coroProgramRunSliceSymbolV2: "__llgo_archive_delivery_run", coroProgramContinueSliceSymbolV2: "__llgo_archive_delivery_continue"}
	observerSignatures := make(map[string]*types.Signature)
	entryObject := buildCoroSpawnNativeE2EEntry(t, consumerProg, temp, anchor, capabilities, func(pkg llssa.Package) {
		for original, replacement := range observerSymbols {
			fn := pkg.FuncOf(original)
			if fn == nil {
				t.Fatalf("entry lost slice boundary %q", original)
			}
			observerSignatures[original] = fn.RawType().(*types.Signature)
			pkg.Module().NamedFunction(original).SetName(replacement)
		}
	})
	checks := consumerProg.NewPackage("archive-delivery-checks", "archive-delivery-checks")
	defer checks.Module().Dispose()
	const setupSymbol, checkSymbol = "__llgo_archive_delivery_setup", "__llgo_archive_delivery_check"
	checks.NewFunc(setupSymbol, newSignature(nil, nil), llssa.InGo).MakeBody(1).Return()
	check := checks.NewFunc(checkSymbol, newSignature(nil, []types.Type{types.Typ[types.Int32]}), llssa.InGo)
	stats := checks.NewFunc(libraryPath+".Stats", newSignature(nil, []types.Type{types.Typ[types.Uint32]}), llssa.InGo)
	dropInfo := checks.NewFunc(libraryPath+".DropInfo", newSignature(nil, []types.Type{types.Typ[types.Uint32]}), llssa.InGo)
	pointer, uint32Type := types.Typ[types.UnsafePointer], types.Typ[types.Uint32]
	observed := checks.NewVar("__llgo_archive_delivery_child", types.NewPointer(pointer), llssa.InC)
	observed.InitNil()
	issued := checks.NewVar("main.CancelIssued", types.NewPointer(uint32Type), llssa.InGo)
	spawn := checks.NewFunc(observedSpawn, spawnSignature, llssa.InC)
	productionSpawn := checks.NewFunc("__llgo_coro_spawn_begin_v1", spawnSignature, llssa.InC)
	spawnBody := spawn.MakeBody(1)
	spawned := spawnBody.Call(productionSpawn.Expr, spawn.Param(0))
	spawnBody.Store(observed.Expr, spawned)
	spawnBody.Return(spawned)
	maybeCancel := checks.NewFunc("__llgo_archive_delivery_maybe_cancel", newSignature(nil, nil), llssa.InC)
	maybe := maybeCancel.MakeBody(6)
	maybe.If(maybe.BinOp(token.EQL, maybe.Load(issued.Expr), consumerProg.IntVal(0, consumerProg.Uint32())), maybeCancel.Block(1), maybeCancel.Block(5))
	maybe.SetBlock(maybeCancel.Block(1)).If(maybe.BinOp(token.GEQ, maybe.Call(stats.Expr), consumerProg.IntVal(200, consumerProg.Uint32())), maybeCancel.Block(2), maybeCancel.Block(5))
	programP := checks.NewVar("command-line-arguments.coroProgramPV1State", types.NewPointer(pointer), llssa.InGo)
	request := checks.NewFunc("github.com/xgo-dev/llgo/runtime/internal/coro.RequestTaskCancellation", newSignature([]types.Type{pointer, pointer, types.Typ[types.Uint8]}, []types.Type{types.Typ[types.Bool]}), llssa.InGo)
	accepted := maybe.SetBlock(maybeCancel.Block(2)).Call(request.Expr, maybe.Convert(consumerProg.VoidPtr(), programP.Expr), maybe.Load(observed.Expr), consumerProg.IntVal(1, consumerProg.Byte()))
	maybe.If(accepted, maybeCancel.Block(3), maybeCancel.Block(4))
	maybe.SetBlock(maybeCancel.Block(3)).Store(issued.Expr, consumerProg.IntVal(1, consumerProg.Uint32()))
	maybe.Return()
	exit := checks.NewFunc("exit", newSignature([]types.Type{types.Typ[types.Int32]}, nil), llssa.InC)
	maybe.SetBlock(maybeCancel.Block(4)).Call(exit.Expr, consumerProg.IntVal(71, consumerProg.Int32()))
	maybe.Return()
	maybe.SetBlock(maybeCancel.Block(5)).Return()
	for original, replacement := range observerSymbols {
		signature := observerSignatures[original]
		production := checks.NewFunc(original, signature, llssa.InC)
		observer := checks.NewFunc(replacement, signature, llssa.InC)
		body := observer.MakeBody(1)
		arguments := make([]llssa.Expr, signature.Params().Len())
		for index := range arguments {
			arguments[index] = observer.Param(index)
		}
		status := body.Call(production.Expr, arguments...)
		body.Call(maybeCancel.Expr)
		body.Return(status)
	}
	normal := checks.NewVar("main.Normal", types.NewPointer(types.Typ[types.Uint32]), llssa.InGo)
	body := check.MakeBody(6)
	body.If(body.BinOp(token.EQL, body.Load(normal.Expr), consumerProg.IntVal(7, consumerProg.Uint32())), check.Block(1), check.Block(3))
	actualStats := body.SetBlock(check.Block(1)).Call(stats.Expr)
	actualDrop := body.Call(dropInfo.Expr)
	// Normal delivery, including an ignored defer result, transfers ownership.
	// Only the undelivered canceled value99 must drop: acquired=3, released=1,
	// both asynchronous drop steps completed. This is not automatic Go RAII.
	body.If(body.BinOp(token.EQL, actualStats, consumerProg.IntVal(312, consumerProg.Uint32())), check.Block(5), check.Block(4))
	body.SetBlock(check.Block(5)).If(body.BinOp(token.EQL, actualDrop, consumerProg.IntVal(1099, consumerProg.Uint32())), check.Block(2), check.Block(4))
	body.SetBlock(check.Block(2)).Return(consumerProg.IntVal(0, consumerProg.Int32()))
	body.SetBlock(check.Block(3)).Return(consumerProg.IntVal(11, consumerProg.Int32()))
	report := checks.NewFunc("__llgo_archive_delivery_report", newSignature([]types.Type{types.Typ[types.Uint32], types.Typ[types.Uint32]}, nil), llssa.InC)
	body.SetBlock(check.Block(4)).Call(report.Expr, actualStats, actualDrop)
	body.Return(consumerProg.IntVal(12, consumerProg.Int32()))
	checksObject := emitCoroSpawnNativeE2EObject(t, consumerProg, checks.Module(), filepath.Join(temp, "checks.o"))
	driverObject := buildCoroSpawnNativeE2EDriver(t, consumerProg, temp, setupSymbol, checkSymbol)
	runtimeObjects := buildCoroSpawnNativeE2ERuntimeIsland(t, temp)
	ar, err := exec.LookPath("llvm-ar")
	if err != nil {
		t.Fatal("LLVM 22 llvm-ar required:", err)
	}
	runtimeArchive := filepath.Join(temp, "runtime.a")
	if output, err := exec.Command(ar, append([]string{"rcs", runtimeArchive}, runtimeObjects...)...).CombinedOutput(); err != nil {
		t.Fatalf("runtime archive: %v\n%s", err, output)
	}
	executable := filepath.Join(temp, "archive-delivery")
	reportSource, reportObject := filepath.Join(temp, "report.c"), filepath.Join(temp, "report.o")
	if err := os.WriteFile(reportSource, []byte(`#include <stdint.h>
#include <stdio.h>
void __llgo_archive_delivery_report(uint32_t stats, uint32_t drop) { fprintf(stderr, "archive delivery stats=%u (want312) drop=%u (want1099)\n", stats, drop); }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(clang, "-c", reportSource, "-o", reportObject).CombinedOutput(); err != nil {
		t.Fatalf("compile test diagnostic: %v\n%s", err, output)
	}
	link := []string{driverObject, entryObject, checksObject, consumerObject, reportObject, archive, runtimeArchive, "-o", executable}
	if runtime.GOOS == "darwin" {
		link = append(link, "-Wl,-dead_strip")
	} else {
		link = append(link, "-Wl,--gc-sections")
	}
	if output, err := exec.Command(clang, link...).CombinedOutput(); err != nil {
		t.Fatalf("link real resource archive: %v\n%s", err, output)
	}
	if dir := os.Getenv("LLGO_CORO_ARCHIVE_DEBUG_DIR"); dir != "" {
		image, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "archive-delivery"), image, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Second)
		output, err := exec.CommandContext(ctx, executable).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("archive ownership execution %d: %v\n%s", attempt, err, output)
		}
	}
}
