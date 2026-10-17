//go:build !llgo

package cl

import (
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroDeliveryForwardedArchiveKeepsPrivateHelperOpaque(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{
		{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			producerPkg, _, producerFiles := buildGoSSAPkg(t, coroDeliverySourceFixture+`func Wrapped() (int,error) { return Acquire("fixed") }`)
			prog := newLLSSAProgForTarget(t, target.arch)
			defer prog.Dispose()
			producer, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: producerPkg, Files: producerFiles}})
			if err != nil {
				t.Fatal(err)
			}
			metadata := coro.LibraryEffectMetadata{FunctionIDSchema: coro.FunctionIDSchema, CoroABI: coro.PhysicalABIV1,
				SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, PanicABI: coro.PanicExplicitStatusABIV0, FuncRepABI: coro.FuncRepABIV2,
				TargetTriple: prog.TargetSpec().Triple, TargetCPU: prog.TargetSpec().CPU, TargetFeatures: prog.TargetSpec().Features, TargetABI: prog.TargetSpec().TargetABI,
				PointerBits: prog.PointerSize() * 8, Endianness: "little", DataLayout: prog.DataLayout()}
			record, present, err := producer.CoroLibraryEffects().SourceDeliveryRecord(producerPkg.Func("Wrapped"), metadata)
			if err != nil || !present || record.DropView.IsZero() {
				t.Fatalf("forwarded producer record=%+v: %v", record, err)
			}
			facts := make([]coro.LibraryEffectFunction, 3)
			for index, name := range []string{"Wrapped", "initFD", "dropFD"} {
				fn := producerPkg.Func(name)
				hash, err := producer.CoroLibraryEffects().FunctionABIHash(fn, metadata)
				if err != nil {
					t.Fatal(err)
				}
				symbol, err := producer.CoroLibraryEffects().FunctionBaseSymbol(fn)
				if err != nil {
					t.Fatal(err)
				}
				facts[index] = coro.LibraryEffectFunction{ID: []coro.FunctionID{record.Producer, record.Initializer, record.Drop}[index], ABIHash: hash,
					FuncRep: coro.DirectPlain, Primary: coro.PrimaryPlain, ManagedEntry: coro.ManagedEntryPlain, PrimarySymbol: symbol}
				if index != 1 {
					facts[index].Effect, facts[index].Exec = coro.MayPark, coro.MayUnwind
					facts[index].FuncRep, facts[index].Primary, facts[index].ManagedEntry = coro.DirectCoro, coro.PrimaryCoroutine, coro.ManagedEntryCoroutine
					facts[index].PrimarySymbol += coroPrimarySuffix
				}
			}
			facts[0].Delivery = record
			index, err := coro.NewLibraryEffectIndex([]coro.LibraryEffectSummary{{Schema: coro.LibraryEffectSummarySchema, Package: "foo", Metadata: metadata, Functions: facts}}, metadata)
			if err != nil {
				t.Fatal(err)
			}
			pkg, _, files := buildGoSSAPkg(t, `package foo
func Wrapped() (int,error)
func Root() { Wrapped() }
func Deferred() { defer Wrapped() }
`)
			consumerProg := newLLSSAProgForTarget(t, target.arch)
			defer consumerProg.Dispose()
			u, err := prepareStacklessEmissionUniverseWithOptions(consumerProg, nil, []EmissionPackage{{SSA: pkg, Files: files}}, EmissionUniverseOptions{CoroLibraryEffects: index})
			if err != nil {
				t.Fatal(err)
			}
			imports, err := u.CoroPlanningMetadata().ImportedFunctions()
			if err != nil || len(imports) != 2 {
				t.Fatalf("opaque logical helper inventory=%+v: %v", imports, err)
			}
			inventory, err := coro.NewSSAEmissionUniverse(pkg.Prog, u.Functions())
			if err != nil {
				t.Fatal(err)
			}
			ids := u.FunctionIDConfig()
			ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = metadata.CoroABI, metadata.SchedulerABI, true
			plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: pkg.Func("Root"), Demand: coro.AsyncDemand}, {Function: pkg.Func("Deferred"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
				FunctionIDs: ids, EmissionUniverse: inventory, ImportedFunctions: imports, OutcomeMode: coro.OutcomeExplicitStatus, MaxPlainInstructions: -1,
				ClassifyLocalBody: u.CoroLocalBodyFacts, ClassifyLoweredCalls: u.CoroLoweredCalls,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == pkg.Func("Wrapped") {
						return facts[0].ImportedPolicy()
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
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: u, CoroLibraryEffectMetadata: metadata, CoroLibraryEffects: map[*ssa.Function]coro.LibraryEffectFunction{pkg.Func("Wrapped"): facts[0]},
				CoroPlanMetadata: coro.PlanDigestMetadata{CoroABI: metadata.CoroABI, SchedulerABI: metadata.SchedulerABI, PanicABI: metadata.PanicABI, FuncRepABI: metadata.FuncRepABI,
					TargetTriple: metadata.TargetTriple, TargetCPU: metadata.TargetCPU, TargetFeatures: metadata.TargetFeatures, TargetABI: metadata.TargetABI,
					PointerBits: metadata.PointerBits, Endianness: metadata.Endianness, DataLayout: metadata.DataLayout},
			}
			enableCoroChildAwaitCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			compiled, _, err := NewPackageExWithEmbedOptions(consumerProg, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation})
			if err != nil {
				t.Fatal(err)
			}
			module := compiled.Module()
			defer module.Dispose()
			view := module.NamedFunction(record.DropView.Symbol)
			if view.IsNil() || view.BasicBlocksCount() != 0 {
				t.Fatal("private archive helper view was missing or fabricated a local body")
			}
			if raw := module.NamedFunction(facts[2].PrimarySymbol); !raw.IsNil() {
				t.Fatal("consumer redeclared the private original helper with its public view signature")
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			runCoroABITestPipeline(t, consumerProg, module)
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
}
