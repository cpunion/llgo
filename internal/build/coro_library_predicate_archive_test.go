package build

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/cl"
	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	llvm "github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

// This is deliberately not a hand-built metadata record or tool-compile
// frontend-only test: the producer lowers Go to a native object, publishes its
// real compiler-owned records in an indexed archive, and the consumer reads
// that archive through the production importcfg path before native emission.
func TestCoroLibraryProgramPredicateProducedArchiveConsumer(t *testing.T) {
	t.Run("ExecutionDomain", func(t *testing.T) {
		for _, enabled := range []bool{false, true} {
			testCoroLibraryProgramPredicateProducedArchiveConsumer(t, "coroExecutionDomainEnabled", coro.ExecutionDomainProgramCapability(), enabled)
		}
	})
	t.Run("GCObservation", func(t *testing.T) {
		for _, enabled := range []bool{false, true} {
			testCoroLibraryProgramPredicateProducedArchiveConsumer(t, "coroGCObservationEnabled", coro.GCObservationProgramCapability(), enabled)
		}
	})
}

func testCoroLibraryProgramPredicateProducedArchiveConsumer(t *testing.T, intrinsic string, bit coro.ProgramCapabilities, enabled bool) {
	llssa.Initialize(llssa.InitAll)
	packagePath, seed, suffix := "example/predicate_archive", "", "false"
	var producerValues coro.ProgramCapabilities
	if enabled {
		producerValues, suffix = bit, "true"
		if bit.GCObservation() {
			packagePath, seed = "github.com/xgo-dev/llgo/runtime/internal/runtime", "EnableGCObserver"
		} else {
			packagePath, seed = "internal/synctest", "Run"
		}
	}
	registration := ""
	if seed != "" {
		registration = seed + "(); "
	}
	producerSource := `package library
import _ "unsafe"
//go:linkname Enabled llgo.` + intrinsic + `
func Enabled() bool
func guarded(value uint32) uint32 {
	if Enabled() { return value + 100 }
	return value
}
func Imported(value uint32) uint32 { ` + registration + `return guarded(value) + 1 }
func Independent(value uint32) uint32 { return value + 2 }
`
	if seed != "" {
		producerSource += "func " + seed + "() {}\n"
	}
	work := t.TempDir()
	archive := filepath.Join(work, "library.a")
	importcfg := filepath.Join(work, "importcfg")
	if err := os.WriteFile(importcfg, []byte("packagefile "+packagePath+"="+archive+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	build := func(t *testing.T, source string, values coro.ProgramCapabilities, root string, producer bool) {
		t.Helper()
		ssaPkg, files := buildCoroPlanTestPackage(t, packagePath, source, nil)
		prog := llssa.NewProgram(nil)
		defer prog.Dispose()
		conf := &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH}
		if !producer {
			conf.ImportCfg = importcfg
		}
		ctx := &context{buildConf: conf, prog: prog}
		metadata, err := buildCoroLibraryEffectMetadata(ctx)
		if err != nil {
			t.Fatal(err)
		}
		binding := coro.ProgramPredicateRequirements{Mask: bit, Values: values}
		cl.ParsePkgSyntax(prog, ssaPkg.Prog.Fset, ssaPkg.Pkg, files)
		universe, err := cl.PrepareEmissionUniverseWithOptions(prog, nil, []cl.EmissionPackage{{SSA: ssaPkg, Files: files, Identity: packagePath}}, cl.EmissionUniverseOptions{
			CoroTargetCapabilities: metadata.TargetCapabilities,
			CoroProgramPredicates:  binding,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx.coroEmission = universe
		index, importedMetadata, err := loadCoroLibraryEffectIndex(ctx)
		if err != nil {
			t.Fatal(err)
		}
		imported, err := prepareCoroImportedLibraryEffects(ctx, index, importedMetadata)
		if err != nil {
			t.Fatal(err)
		}
		if !producer {
			fact, ok := imported[ssaPkg.Func("Imported")]
			if !ok || fact.ProgramPredicates.Mask != bit || fact.ProgramPredicates.Values != producerValues || fact.ProgramCapabilities&bit != producerValues {
				t.Fatalf("archive lost consumed predicate or transitive registration demand: %+v, found=%v", fact, ok)
			}
			if independent, ok := imported[ssaPkg.Func("Independent")]; !ok || independent.ProgramPredicates.Mask != 0 {
				t.Fatalf("unrelated archived entry missing or constrained: %+v, found=%v", independent, ok)
			}
		}
		ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		ids := universe.FunctionIDConfig()
		ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = metadata.CoroABI, metadata.SchedulerABI, true
		roots := coro.Roots{{Function: ssaPkg.Func(root), Demand: coro.AsyncDemand}}
		if producer {
			roots = append(roots, coro.Root{Function: ssaPkg.Func("Independent"), Demand: coro.AsyncDemand})
		}
		plan, err := coro.AnalyzeSSA(ssaPkg.Prog, roots, coro.SSAConfig{
			EmissionUniverse: ssaUniverse, FunctionIDs: ids,
			MaxPlainInstructions: -1, OutcomeMode: coro.OutcomeExplicitStatus,
			ClassifyLocalBody: universe.CoroLocalBodyFacts,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fact, found := imported[function]; found {
					return fact.ImportedPolicy()
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyElidedCall: func(_ *ssa.Function, site ssa.CallInstruction) (bool, error) {
				frozen, _, err := universe.CoroCallSitePlan(site)
				return frozen.Elision != cl.CoroCallElidedFrontendUnevaluated && frozen.ElidesCall(), err
			},
			ClassifyUnevaluatedCall: func(_ *ssa.Function, site ssa.CallInstruction) (bool, error) {
				frozen, _, err := universe.CoroCallSitePlan(site)
				return frozen.Elision == cl.CoroCallElidedFrontendUnevaluated, err
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		discovered, err := cl.DiscoverCoroProgramPredicates(plan, imported)
		if err != nil || discovered&bit != producerValues && root != "UnrelatedConsumer" || discovered&bit != 0 && root == "UnrelatedConsumer" {
			t.Fatalf("predicate discovery from real archive = %#x, %v; producer=%#x, root=%s", discovered, err, producerValues, root)
		}
		report, err := universe.BuildCoroLoweringFactsReport(plan)
		if err != nil {
			t.Fatal(err)
		}
		planMetadata := coro.PlanDigestMetadata{
			LoweringFactsSchema: coro.LoweringFactsSchema,
			LoweringFactsDigest: report.Digest,
			ProgramPredicates:   binding,
			CoroABI:             metadata.CoroABI, SchedulerABI: metadata.SchedulerABI,
			PanicABI: metadata.PanicABI, FuncRepABI: metadata.FuncRepABI,
			TargetTriple: metadata.TargetTriple, TargetCPU: metadata.TargetCPU,
			TargetFeatures: metadata.TargetFeatures, TargetABI: metadata.TargetABI,
			PointerBits: metadata.PointerBits, Endianness: metadata.Endianness, DataLayout: metadata.DataLayout,
		}
		digest, err := plan.CoroPlanDigest(planMetadata)
		if err != nil {
			t.Fatal(err)
		}
		compilation := &cl.Compilation{
			CoroPlan: plan, CoroPlanDigest: digest, CoroPlanMetadata: planMetadata,
			CoroLoweringFacts: report.Facts, CoroLoweringFactsDigest: report.Digest,
			CoroTargetCapabilities: metadata.TargetCapabilities,
			CoroABI:                metadata.CoroABI, SchedulerABI: metadata.SchedulerABI,
			PanicABI: metadata.PanicABI, FuncRepABI: metadata.FuncRepABI,
			EmissionUniverse:          universe,
			CoroLibraryEffectMetadata: metadata, CoroLibraryEffects: imported,
		}
		if err := compilation.ValidateCoroLibraryProgramPredicates(); err != nil {
			if !producer && values != producerValues && root == "Consumer" && strings.Contains(err.Error(), "recompile the library") {
				return
			}
			t.Fatal(err)
		}
		if !producer && values != producerValues && root == "Consumer" {
			t.Fatal("consumer accepted a reachable entry specialized to a different program predicate")
		}
		pkg, _, err := cl.NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, cl.PackageOptions{Compilation: compilation})
		if err != nil {
			t.Fatal(err)
		}
		records, err := cl.CoroLibraryEffectSummaryRecords(pkg)
		if err != nil || len(records) == 0 {
			t.Fatalf("real frontend producer record: %v, bytes=%d", err, len(records))
		}
		module := pkg.Module()
		module.SetDataLayout(prog.DataLayout())
		module.SetTarget(prog.TargetSpec().Triple)
		if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
			t.Fatal(err)
		}
		object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
		if err != nil {
			t.Fatal(err)
		}
		defer object.Dispose()
		if len(object.Bytes()) == 0 {
			t.Fatal("native object emission produced no code")
		}
		if producer {
			if err := ctx.createPackageArchiveFile(archive, &aPackage{
				ObjBuffers:               []packageArchiveBuffer{{name: "library.o", buffer: object}},
				CoroLibraryEffectRecords: records,
			}, false); err != nil {
				t.Fatal(err)
			}
			summaries, found, err := ReadCoroLibraryEffectArchive(archive)
			if err != nil || !found || len(summaries) != 1 || summaries[0].Schema != coro.LibraryEffectSummarySchema {
				t.Fatalf("actual indexed archive metadata: %+v, %v, %v", summaries, found, err)
			}
		}
	}
	if !t.Run("producer-"+suffix, func(t *testing.T) { build(t, producerSource, producerValues, "Imported", true) }) {
		return
	}
	const consumerSource = `package library
func Imported(value uint32) uint32
func Independent(value uint32) uint32
func Consumer(value uint32) uint32 { return Imported(value) + 1 }
func UnrelatedConsumer(value uint32) uint32 { return Independent(value) }
`
	t.Run("matching-"+suffix, func(t *testing.T) { build(t, consumerSource, producerValues, "Consumer", false) })
	t.Run("mismatched-"+suffix+"-requires-recompile", func(t *testing.T) { build(t, consumerSource, bit^producerValues, "Consumer", false) })
	t.Run("unreachable-"+suffix+"-specialization-does-not-poison-consumer", func(t *testing.T) {
		build(t, consumerSource, bit^producerValues, "UnrelatedConsumer", false)
	})
}
