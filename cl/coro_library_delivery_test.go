//go:build !llgo

package cl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func TestCoroArchiveBodylessParametersAreNotClosureContext(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	declarations, _, _ := buildGoSSAPkg(t, `package foo
func Imported(a, b, c uint32) uint32
`)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "consumer.go", `package consumer
import library "foo"
func Deferred() { defer library.Imported(1, 2, 3) }
`, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{file}
	pkg, _, err := ssautil.BuildPackage(&types.Config{Importer: &emissionTestImporter{packages: map[string]*types.Package{"foo": declarations.Pkg}}},
		fset, types.NewPackage("consumer", "consumer"), files, ssa.SanityCheckFunctions)
	if err != nil {
		t.Fatal(err)
	}
	library := pkg.Prog.ImportedPackage("foo")
	prog := newLLSSAProgForTarget(t, nil)
	defer prog.Dispose()
	u, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}, {SSA: library}})
	if err != nil {
		t.Fatal(err)
	}
	target := library.Func("Imported")
	if len(target.Params) != 0 {
		t.Fatal("fixture unexpectedly has body parameter values")
	}
	signature, err := u.coroPhysicalEntrySourceSignature(target)
	if err != nil {
		t.Fatal(err)
	}
	context, err := u.functionABIContext(target, u.ownerOf(target))
	if err != nil {
		t.Fatal(err)
	}
	abi := newCoroPhysicalABI(context, plannedFunctionSymbol{function: target,
		plan: coro.FunctionPlan{ID: "bodyless", External: coro.ExternalKnown}}, signature)
	if abi.hasEnv || abi.physicalSig.Params().Len() != 5 {
		t.Fatalf("ordinary declaration acquired closure context: %+v", abi)
	}
	for _, block := range pkg.Func("Deferred").Blocks {
		for _, instruction := range block.Instrs {
			if call, deferred := instruction.(*ssa.Defer); deferred {
				if err := validateCoroStaticCleanupOperands(call.Common(), target); err != nil {
					t.Fatal(err)
				}
				bad := *call.Common()
				bad.Args = append([]ssa.Value(nil), bad.Args...)
				bad.Args[0] = nil
				if err := validateCoroStaticCleanupOperands(&bad, target); err == nil {
					t.Fatal("bodyless defer accepted an incomplete operand")
				}
				return
			}
		}
	}
	t.Fatal("fixture has no deferred invocation")
}

func TestCoroArchiveDeliveryBindsPrivateHelpersFromTypedProducer(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			producerPkg, _, producerFiles := buildGoSSAPkg(t, coroDeliverySourceFixture)
			producerProg := newLLSSAProgForTarget(t, target.arch)
			defer producerProg.Dispose()
			producer, err := prepareStacklessEmissionUniverse(producerProg, nil, []EmissionPackage{{SSA: producerPkg, Files: producerFiles}})
			if err != nil {
				t.Fatal(err)
			}
			metadata := coro.LibraryEffectMetadata{
				FunctionIDSchema: coro.FunctionIDSchema, CoroABI: coro.PhysicalABIV1,
				SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
				PanicABI:     coro.PanicExplicitStatusABIV0, FuncRepABI: coro.FuncRepABIV2,
				TargetTriple: producerProg.TargetSpec().Triple, TargetCPU: producerProg.TargetSpec().CPU,
				TargetFeatures: producerProg.TargetSpec().Features, TargetABI: producerProg.TargetSpec().TargetABI,
				PointerBits: producerProg.PointerSize() * 8, Endianness: "little", DataLayout: producerProg.DataLayout(),
			}
			record, present, err := producer.CoroLibraryEffects().SourceDeliveryRecord(producerPkg.Func("Acquire"), metadata)
			if err != nil || !present {
				t.Fatalf("producer recipe: %t: %v", present, err)
			}
			functions := []*ssa.Function{producerPkg.Func("Acquire"), producerPkg.Func("initFD"), producerPkg.Func("dropFD")}
			facts := make([]coro.LibraryEffectFunction, len(functions))
			ids := []coro.FunctionID{record.Producer, record.Initializer, record.Drop}
			for index, fn := range functions {
				hash, err := producer.CoroLibraryEffects().FunctionABIHash(fn, metadata)
				if err != nil {
					t.Fatal(err)
				}
				base, err := producer.CoroLibraryEffects().FunctionBaseSymbol(fn)
				if err != nil {
					t.Fatal(err)
				}
				facts[index] = coro.LibraryEffectFunction{ID: ids[index], ABIHash: hash,
					FuncRep: coro.DirectPlain, Primary: coro.PrimaryPlain, ManagedEntry: coro.ManagedEntryPlain, PrimarySymbol: base}
				if index != 1 {
					facts[index].Effect, facts[index].Exec = coro.MayPark, coro.MayUnwind
					facts[index].FuncRep, facts[index].Primary, facts[index].ManagedEntry = coro.DirectCoro, coro.PrimaryCoroutine, coro.ManagedEntryCoroutine
					facts[index].PrimarySymbol += coroPrimarySuffix
				}
			}
			facts[0].Delivery = record
			pkg, _, files := buildGoSSAPkg(t, `package foo
func Acquire(path string) (int, error)
func Root() { Acquire("archive") }
func Deferred() { defer Acquire("discard") }
`)
			prog := newLLSSAProgForTarget(t, target.arch)
			defer prog.Dispose()
			consumer, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			view := consumer.CoroLibraryEffects()
			makeIndex := func(entries []coro.LibraryEffectFunction) *coro.LibraryEffectIndex {
				t.Helper()
				index, err := coro.NewLibraryEffectIndex([]coro.LibraryEffectSummary{{Schema: coro.LibraryEffectSummarySchema,
					Package: "foo", Metadata: metadata, Functions: entries}}, metadata)
				if err != nil {
					t.Fatal(err)
				}
				return index
			}
			bound, err := view.bindArchiveDeliveryRecipe(pkg.Func("Acquire"), metadata, facts[0], makeIndex(facts))
			if err != nil || bound.recipe.Record() != record {
				t.Fatalf("private helper binding: %+v: %v", bound, err)
			}
			for ordinal, helper := range bound.helpers {
				if helper.fact != facts[ordinal+1] || helper.signature == nil {
					t.Fatalf("helper %d lost exact archive fact", ordinal)
				}
				if _, hasSource := pkg.Members[functions[ordinal+1].Name()]; hasSource {
					t.Fatal("fixture accidentally exported the private helper SSA")
				}
			}
			// Type binding is not physical custody permission. Until ProgramIR
			// installs the complete consumer guard, the existing gate must reject.
			if err := view.ValidateFunction(pkg.Func("Acquire"), metadata, facts[0]); err == nil || !strings.Contains(err.Error(), "bound physical resource-delivery recipe") {
				t.Fatalf("typed metadata bypassed physical guard: %v", err)
			}
			for _, visibleHelpers := range []bool{false, true} {
				name := "private-helpers"
				if visibleHelpers {
					name = "declared-helpers"
				}
				t.Run("physical-consumer/"+name, func(t *testing.T) {
					pkg, files := pkg, files
					if visibleHelpers {
						pkg, _, files = buildGoSSAPkg(t, `package foo
import "unsafe"
type Result = struct { R0 int; R1 error }
func Acquire(path string) (int, error)
func initFD(*Result)
func dropFD(*Result, unsafe.Pointer, string)
func Root() { Acquire("archive") }
func Deferred() { defer Acquire("discard") }
`)
					}
					libraryEffects := map[*ssa.Function]coro.LibraryEffectFunction{pkg.Func("Acquire"): facts[0]}
					wantImports := 2
					if visibleHelpers {
						wantImports = 0
						libraryEffects[pkg.Func("initFD")] = facts[1]
						libraryEffects[pkg.Func("dropFD")] = facts[2]
					}
					physicalProg := newLLSSAProgForTarget(t, target.arch)
					defer physicalProg.Dispose()
					u, err := prepareStacklessEmissionUniverseWithOptions(physicalProg, nil,
						[]EmissionPackage{{SSA: pkg, Files: files}}, EmissionUniverseOptions{CoroLibraryEffects: makeIndex(facts)})
					if err != nil {
						t.Fatal(err)
					}
					typedImports, err := u.CoroPlanningMetadata().ImportedFunctions()
					if err != nil || len(typedImports) != wantImports {
						t.Fatalf("typed archive inventory: %+v: %v", typedImports, err)
					}
					universe, err := coro.NewSSAEmissionUniverse(pkg.Prog, u.Functions())
					if err != nil {
						t.Fatal(err)
					}
					ids := u.FunctionIDConfig()
					ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = metadata.CoroABI, metadata.SchedulerABI, true
					plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{
						{Function: pkg.Func("Root"), Demand: coro.AsyncDemand},
						{Function: pkg.Func("Deferred"), Demand: coro.AsyncDemand},
					}, coro.SSAConfig{
						FunctionIDs: ids, EmissionUniverse: universe, ImportedFunctions: typedImports,
						OutcomeMode: coro.OutcomeExplicitStatus, MaxPlainInstructions: -1,
						ClassifyLocalBody: u.CoroLocalBodyFacts, ClassifyLoweredCalls: u.CoroLoweredCalls,
						ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
							if fact, ok := libraryEffects[fn]; ok {
								return fact.ImportedPolicy()
							}
							return coro.SSAFunctionPolicy{}, nil
						},
					})
					if err != nil {
						t.Fatal(err)
					}
					if err := u.ValidateCoroPlan(plan); err != nil {
						t.Fatal(err)
					}
					if _, err := u.BuildCoroLoweringFactsReport(plan); err != nil {
						t.Fatal(err)
					}
					compilation := &Compilation{CoroPlan: plan, EmissionUniverse: u,
						CoroLibraryEffectMetadata: metadata, CoroLibraryEffects: libraryEffects,
						CoroPlanMetadata: coro.PlanDigestMetadata{CoroABI: metadata.CoroABI, SchedulerABI: metadata.SchedulerABI,
							PanicABI: metadata.PanicABI, FuncRepABI: metadata.FuncRepABI,
							TargetTriple: metadata.TargetTriple, TargetCPU: metadata.TargetCPU, TargetFeatures: metadata.TargetFeatures, TargetABI: metadata.TargetABI,
							PointerBits: metadata.PointerBits, Endianness: metadata.Endianness, DataLayout: metadata.DataLayout},
					}
					enableCoroChildAwaitCompilation(compilation)
					compilation.PanicABI = coro.PanicExplicitStatusABIV0
					compiled, _, err := NewPackageExWithEmbedOptions(physicalProg, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation})
					if err != nil {
						t.Fatal(err)
					}
					module := compiled.Module()
					defer module.Dispose()
					if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
						t.Fatalf("pre-split archive consumer: %v\n%s", err, module.String())
					}
					for _, fact := range facts[1:] {
						fn := module.NamedFunction(fact.PrimarySymbol)
						if fn.IsNil() || fn.BasicBlocksCount() != 0 {
							t.Fatalf("private helper %q is missing or fabricated a local body", fact.PrimarySymbol)
						}
					}
					runCoroABITestPipeline(t, physicalProg, module)
					if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
						t.Fatalf("post-split archive consumer: %v\n%s", err, module.String())
					}
				})
			}
			for _, test := range []struct {
				name    string
				helper  int
				capture bool
			}{
				{name: "wrong-init-result", helper: 1},
				{name: "wrong-drop-result", helper: 2},
				{name: "captured-init", helper: 1, capture: true},
				{name: "captured-drop", helper: 2, capture: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					entries := append([]coro.LibraryEffectFunction(nil), facts...)
					signature := bound.helpers[test.helper-1].signature
					parameters := make([]*types.Var, 0, signature.Params().Len()+1)
					if test.capture {
						parameters = append(parameters, types.NewParam(token.NoPos, nil, "env", types.Typ[types.UnsafePointer]))
					}
					for index := 0; index < signature.Params().Len(); index++ {
						param := signature.Params().At(index)
						if index == 0 && !test.capture {
							param = types.NewParam(token.NoPos, nil, "out", types.NewPointer(types.Typ[types.Int]))
						}
						parameters = append(parameters, param)
					}
					wrong := types.NewSignatureType(nil, nil, nil, types.NewTuple(parameters...), nil, false)
					entries[test.helper].ABIHash = view.signatureABIHash(wrong, metadata)
					if test.helper == 1 {
						entries[0].Delivery.InitializerABI = entries[1].ABIHash
					} else {
						entries[0].Delivery.DropABI = entries[2].ABIHash
					}
					if _, err := view.bindArchiveDeliveryRecipe(pkg.Func("Acquire"), metadata, entries[0], makeIndex(entries)); err == nil || !strings.Contains(err.Error(), "complete typed no-capture signature") {
						t.Fatalf("accepted incompatible helper physical ABI: %v", err)
					}
				})
			}
			layout := append([]coro.LibraryEffectFunction(nil), facts...)
			layout[0].Delivery.ResultSize += layout[0].Delivery.ResultAlign
			if _, err := view.bindArchiveDeliveryRecipe(pkg.Func("Acquire"), metadata, layout[0], makeIndex(layout)); err == nil || !strings.Contains(err.Error(), "result layout") {
				t.Fatalf("accepted wrong target result layout: %v", err)
			}
			wrongTarget := metadata
			wrongTarget.TargetTriple += "-other"
			if _, err := view.bindArchiveDeliveryRecipe(pkg.Func("Acquire"), wrongTarget, facts[0], makeIndex(facts)); err == nil {
				t.Fatal("accepted wrong target metadata")
			}
		})
	}
}
