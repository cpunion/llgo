//go:build !llgo

package cl

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

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
