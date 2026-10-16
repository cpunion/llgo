//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"regexp"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroDeliveryDynamicProductionRecipesFollowFinalValueFlow(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, coroDeliverySourceFixture+`
func Dynamic(f func(string) (int, error)) { f("first"); f("second"); defer f("deferred") }
//llgo:nounwind
func Certified(f func(string) (int, error)) { f("certified") }
func Plain(string) (int, error) { return 1, nil }
func Other(string) (int, error) { return 2, nil }
func Closed(flag bool) { f := Plain; if flag { f = Other }; f("closed") }
func Neutral(string) (int, error) { return 3, nil }
func Resource(flag bool) { f := Acquire; if flag { f = Neutral }; f("resource"); defer f("resource-defer") }
func NilOnly() { var f func(string) (int, error); f("nil") }
func Variadic(f func(...int) int, xs []int) { f(xs...) }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	u, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.Prog, u.Functions())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		active bool
		calls  int
	}{
		{"Dynamic", true, 3}, {"Certified", true, 1}, {"Closed", false, 1},
		{"NilOnly", false, 1}, {"Variadic", true, 1}, {"Resource", true, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			fn := pkg.Func(test.name)
			plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: fn, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse: ssaUniverse, FunctionIDs: u.FunctionIDConfig(), OutcomeMode: coro.OutcomeExplicitStatus,
				ClassifyLocalBody: u.CoroLocalBodyFacts, ClassifyLoweredCalls: u.CoroLoweredCalls,
				ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
					return coro.UnknownManagedDispatch, nil
				},
				ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
					site, present, err := u.CoroCallSitePlan(call)
					return present && site.ElidesCall(), err
				},
				ClassifyStaticFunctionReferenceCallArgument: func(_ *ssa.Function, call ssa.CallInstruction, index int) (bool, error) {
					return u.CoroStaticFunctionReferenceCallArgument(call, index)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if fact, present := plan.FunctionPlan(fn); !present || fact.LocalExec.Contains(coro.NeedsCleanupFrame) != test.active {
				t.Fatalf("conditional cleanup: %+v present=%t", fact, present)
			}
			// This production phase runs before physical publication. A typed
			// descriptor helper has no fabricated SSA target; predicate discovery
			// must consume its source CallPlan rather than reject the nil target.
			if _, err := DiscoverCoroProgramPredicates(plan, nil); err != nil {
				t.Fatalf("production predicate discovery rejected the frozen helper domain: %v", err)
			}
			// Seed only the existing exact initializer/drop entries. A known
			// resource family must inherit both dependencies through its source
			// producer; an open or proven nonresource family must not turn on
			// services merely because its helper domain uses managed dispatch.
			initializeBit, dropBit := coro.ExecutionDomainProgramCapability(), coro.GCObservationProgramCapability()
			local := map[*ssa.Function]coro.ProgramCapabilities{pkg.Func("initFD"): initializeBit, pkg.Func("dropFD"): dropBit}
			masks := map[*ssa.Function]coro.ProgramCapabilities{pkg.Func("initFD"): initializeBit, pkg.Func("dropFD"): dropBit}
			capabilities, err := deriveCoroFunctionProgramCapabilities(plan, local, nil, masks)
			if err != nil {
				t.Fatal(err)
			}
			wantCapability := coro.ProgramCapabilities(0)
			if test.name == "Resource" {
				wantCapability = initializeBit | dropBit
			}
			if capabilities[fn] != wantCapability || masks[fn] != wantCapability ||
				projectCoroProgramCapabilities(plan, capabilities)&coro.ProgramPredicateMask() != wantCapability {
				t.Fatalf("helper dependency closure: capability=%#x mask=%#x projected=%#x, want=%#x",
					capabilities[fn], masks[fn], projectCoroProgramCapabilities(plan, capabilities), wantCapability)
			}
			seen := make(map[string]bool)
			calls := 0
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					call, callable := instruction.(ssa.CallInstruction)
					if !callable || call.Common().StaticCallee() != nil {
						continue
					}
					frozen, present, err := u.coroProgramIR.callSitePlan(call)
					if err != nil || !present || frozen.dynamicDelivery == nil {
						t.Fatalf("production call has no typed source recipe: %+v %v", frozen, err)
					}
					calls++
					recipe := frozen.dynamicDelivery
					if recipe.signature.Variadic() || !types.Identical(recipe.result, coro.ResultSlotType(recipe.signature.Results())) {
						t.Fatal("call recipe did not use the normalized source ABI")
					}
					for _, name := range []string{recipe.initialize, recipe.release} {
						if seen[name] {
							t.Fatalf("different call occurrences share a conditional identity: %s", name)
						}
						seen[name] = true
						active, err := u.coroProgramIR.deliveryHelperActive(plan, instruction, name)
						if err != nil || active != test.active {
							t.Fatalf("helper %s active=%t, want=%t: %v", name, active, test.active, err)
						}
						record, found, err := u.ResolveCoroLoweredCallRecord(fn, name)
						if err != nil || !found || record.NoUnwind || !record.ManagedDispatch || record.Target != nil || record.WhenResultDeliveryOf != call {
							t.Fatalf("typed conditional edge lost source identity or borrowed a caller certificate: %+v %v", record, err)
						}
					}
					for _, owner := range u.sortedUseOwners(fn) {
						site := u.coroProgramIR.sitePlans[emissionFunctionOwnerKey{function: fn, owner: owner}][call]
						if !sameCoroFrozenCallSitePlan(site.callPlan, frozen) {
							t.Fatal("owner SitePlan disagrees with typed call recipe")
						}
						placement := coroRuntimeHelperAtSource
						if _, deferred := call.(*ssa.Defer); deferred {
							placement = coroRuntimeHelperAtCleanup
						}
						if got := site.managedRuntimeHelpersAt(placement); len(got) != 1 || got[0] != recipe.initialize {
							t.Fatalf("initializer placement=%v", got)
						}
						if got := site.managedRuntimeHelpersAt(coroRuntimeHelperAtResultDrop); len(got) != 1 || got[0] != recipe.release {
							t.Fatalf("drop placement=%v", got)
						}
					}
				}
			}
			if calls != test.calls {
				t.Fatalf("recipes=%d, want=%d", calls, test.calls)
			}
			if got := len(plan.LoweredCalls(fn)); got != 2*test.calls && test.active || got != 0 && !test.active {
				t.Fatalf("active frozen helper count=%d", got)
			}
			report, err := u.BuildCoroLoweringFactsReport(plan)
			if err != nil {
				t.Fatal(err)
			}
			id, _ := plan.FunctionID(fn)
			helperCount := 0
			for _, function := range report.Facts.Functions {
				if function.Instance.Function != id {
					continue
				}
				for _, site := range function.Sites {
					for _, helper := range site.Helpers {
						if helper.ManagedDispatch {
							helperCount++
							if helper.Target != "" || helper.NoUnwind {
								t.Fatalf("wire helper invented an exact target or no-unwind capability: %+v", helper)
							}
						}
					}
				}
			}
			wantHelpers := 0
			if test.active {
				wantHelpers = 2 * test.calls
			}
			if helperCount != wantHelpers {
				t.Fatalf("pointer-free conditional helpers=%d, want=%d", helperCount, wantHelpers)
			}
		})
	}
}

func TestCoroDeliveryDynamicPhysicalGuardNativeAndWasm(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			source := newEmissionTestProgram()
			source.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			runtimePkg := source.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocU(size uintptr) unsafe.Pointer { return nil }
func FreeDeferNode(pointer unsafe.Pointer) {}
func AssertNilDeref(value bool) {}
`)
			foo := source.addPackage(t, "foo", coroDeliverySourceFixture+`
func Apply(f func(string) (int, error)) (int, error) { return f("open") }
func DeferredDynamic(f func(string) (int, error)) { defer f("deferred") }
func LoopedDynamic(f func(string) (int, error)) { for i := 0; i < 2; i++ { defer f("loop") } }
func Plain(string) (int, error) { return 1, nil }
func Other(string) (int, error) { return 2, nil }
func Closed(flag bool) (int, error) { f := Plain; if flag { f = Other }; return f("closed") }
`)
			source.ssa.Build()
			pkg, files := foo.ssa, []*ast.File{foo.file}
			prog := newLLSSAProgForTarget(t, target.arch)
			defer prog.Dispose()
			u, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
				{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}}, {SSA: pkg, Files: files},
			}, EmissionUniverseOptions{CompleteRuntimeABI: true})
			if err != nil {
				t.Fatal(err)
			}
			universe, err := coro.NewSSAEmissionUniverse(pkg.Prog, u.Functions())
			if err != nil {
				t.Fatal(err)
			}
			ids := u.FunctionIDConfig()
			ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = coro.PhysicalABIV1, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, true
			plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{
				{Function: pkg.Func("Apply"), Demand: coro.AsyncDemand},
				{Function: pkg.Func("DeferredDynamic"), Demand: coro.AsyncDemand},
				{Function: pkg.Func("LoopedDynamic"), Demand: coro.AsyncDemand},
				{Function: pkg.Func("Closed"), Demand: coro.AsyncDemand},
			}, coro.SSAConfig{
				EmissionUniverse: universe, FunctionIDs: ids, MaxPlainInstructions: 64,
				OutcomeMode: coro.OutcomeExplicitStatus, ClassifyLocalBody: u.CoroLocalBodyFacts,
				ClassifyLoweredCalls: u.CoroLoweredCalls,
				ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
					return coro.UnknownManagedDispatch, nil
				},
				ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
					fact, present, err := u.CoroCallSitePlan(call)
					return present && fact.ElidesCall(), err
				},
				ClassifyStaticFunctionReferenceCallArgument: func(_ *ssa.Function, call ssa.CallInstruction, index int) (bool, error) {
					return u.CoroStaticFunctionReferenceCallArgument(call, index)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: u}
			enableCoroPreemptCompilation(compilation)
			compilation.PanicABI, compilation.FuncRepABI = coro.PanicExplicitStatusABIV0, coro.FuncRepABIV2
			compiled, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation})
			if err != nil {
				t.Fatal(err)
			}
			module := compiled.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify dynamic delivery before split: %v\n%s", err, module.String())
			}
			for _, name := range []string{"foo.Apply", "foo.DeferredDynamic", "foo.LoopedDynamic"} {
				body := requireCoroPhysicalFunction(t, module, name).String()
				if !strings.Contains(body, "coro.dispatch.delivery") || !strings.Contains(body, "store volatile ptr null") ||
					!regexp.MustCompile(`and i32 [^\n]+, 32`).MatchString(body) {
					t.Fatalf("%s lost its capability probe, descriptor clearing or typed helper calls:\n%s", name, body)
				}
			}
			closed := module.NamedFunction("foo.Closed").String()
			if strings.Contains(closed, "coro.dispatch.delivery") {
				t.Fatalf("closed nonresource family retained delivery machinery:\n%s", closed)
			}
			runCoroABITestPipeline(t, prog, module)
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify dynamic delivery after split: %v", err)
			}
		})
	}
}

func TestCoroDeliveryDynamicHelpersUseTheExistingEffectGraph(t *testing.T) {
	source := newEmissionTestProgram()
	pkg := source.addPackage(t, "example.com/delivery", `package delivery
func Owner(f func() int) int { return f() }
func Other(f func() int) int { return f() }
func Target() int { return 1 }
func Zero(f func()) { f() }
func Spawn(f func() int) { go f() }
`)
	source.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	u, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	owner := pkg.ssa.Func("Owner")
	builder := coroProgramIRBuilder{canonical: emissionCanonicalIndex{universe: u}}
	callFor := func(name string) ssa.CallInstruction {
		t.Helper()
		for _, block := range pkg.ssa.Func(name).Blocks {
			for _, instruction := range block.Instrs {
				if call, ok := instruction.(ssa.CallInstruction); ok {
					return call
				}
			}
		}
		t.Fatal("fixture has no call")
		return nil
	}
	call := callFor("Owner")
	for _, name := range []string{"delivery.initialize", "delivery.drop"} {
		if err := builder.freezeDeliveryHelperCall(owner, name, call, nil); err != nil {
			t.Fatal(err)
		}
		if err := builder.freezeDeliveryHelperCall(owner, name, call, nil); err != nil {
			t.Fatalf("idempotent conditional helper: %v", err)
		}
	}
	calls, err := u.CoroLoweredCalls(owner)
	if err != nil || len(calls) != 2 || calls[0].LogicalName != "delivery.drop" {
		t.Fatalf("frozen conditional calls=%+v: %v", calls, err)
	}
	for _, record := range calls {
		if !record.ManagedDispatch || record.Target != nil || record.WhenResultDeliveryOf != call {
			t.Fatalf("dynamic helper invented an exact target: %+v", record)
		}
	}
	calls[0].WhenResultDeliveryOf = nil
	if record, ok, err := u.ResolveCoroLoweredCallRecord(owner, "delivery.drop"); err != nil || !ok || record.WhenResultDeliveryOf != call {
		t.Fatalf("frozen helper exposed mutable backing storage: %+v %t %v", record, ok, err)
	}
	if target, exact, err := u.ResolveCoroLoweredCall(owner, "delivery.drop"); err != nil || exact || target != nil {
		t.Fatalf("dynamic helper resolved as an exact target: %v %t %v", target, exact, err)
	}
	universe, err := coro.NewSSAEmissionUniverse(source.ssa, u.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(source.ssa, coro.Roots{{Function: owner, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse: universe, FunctionIDs: u.FunctionIDConfig(), OutcomeMode: coro.OutcomeExplicitStatus,
		ClassifyLocalBody: u.CoroLocalBodyFacts, ClassifyLoweredCalls: u.CoroLoweredCalls,
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
			return coro.UnknownManagedDispatch, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fact, ok := plan.FunctionPlan(owner); !ok || fact.Emission != coro.EmitCoroutine || !fact.Exec.Contains(coro.NeedsCleanupFrame) {
		t.Fatalf("hidden managed calls did not color their real owner: %+v %t", fact, ok)
	}
	if err := u.recordCoroLoweredCall(owner, "delivery.drop", pkg.ssa.Func("Target")); err == nil || !strings.Contains(err.Error(), "mixes exact") {
		t.Fatalf("mixed invocation domain accepted: %v", err)
	}
	for _, name := range []string{"Other", "Zero", "Spawn"} {
		candidateOwner := pkg.ssa.Func(name)
		if name == "Other" {
			candidateOwner = owner
		}
		if err := builder.freezeDeliveryHelperCall(candidateOwner, "invalid."+name, callFor(name), nil); err == nil {
			t.Fatalf("%s accepted as an exact result-owning occurrence", name)
		}
	}
}

func TestCoroDeliveryConditionalExactHelperRetainsItsRealTarget(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo
func Owner(f func() int) int { return f() }
func Allocate() int { return 0 }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	u, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	owner, target := pkg.Func("Owner"), pkg.Func("Allocate")
	call := onlyCoroManagedDispatchValidationCall(t, owner)
	builder := coroProgramIRBuilder{canonical: emissionCanonicalIndex{universe: u}}
	record := coroLoweredCallTarget{target: target, whenResultDeliveryOf: call}
	if err := builder.recordLoweredCallTarget(owner, "delivery.allocate", record); err != nil {
		t.Fatal(err)
	}
	actual, present, err := u.ResolveCoroLoweredCallRecord(owner, "delivery.allocate")
	if err != nil || !present || actual.ManagedDispatch || actual.Target != target || actual.WhenResultDeliveryOf != call {
		t.Fatalf("conditional exact helper became a fabricated/open target: %+v %t %v", actual, present, err)
	}
	if got, present, err := u.ResolveCoroLoweredCall(owner, "delivery.allocate"); err != nil || !present || got != target {
		t.Fatalf("conditional exact target lost lookup identity: %v %t %v", got, present, err)
	}
	record.noUnwind = true
	u.loweredCalls[owner]["delivery.allocate"] = record
	if _, err := u.CoroLoweredCalls(owner); err == nil {
		t.Fatal("conditional exact helper borrowed a caller no-unwind shortcut")
	}
}

func TestCoroDeferredDiscardedLargeResultNeedsNoSink(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			// No runtime allocator declaration exists. Merely discarding a
			// deferred result must not introduce one. Target need not return
			// for this ABI-only gate; linked runtime semantics are separate.
			pkg, _, files := buildGoSSAPkg(t, `package foo
func Target() [131073]byte { for {} }
func Static() { defer Target() }
func Dynamic(f func() [131073]byte) { defer f() }
`)
			prog := newLLSSAProgForTarget(t, target.arch)
			defer prog.Dispose()
			u, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			universe, err := coro.NewSSAEmissionUniverse(pkg.Prog, u.Functions())
			if err != nil {
				t.Fatal(err)
			}
			ids := u.FunctionIDConfig()
			ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = coro.PhysicalABIV1, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, true
			plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{
				{Function: pkg.Func("Static"), Demand: coro.AsyncDemand},
				{Function: pkg.Func("Dynamic"), Demand: coro.AsyncDemand},
			}, coro.SSAConfig{
				EmissionUniverse: universe, FunctionIDs: ids, OutcomeMode: coro.OutcomeExplicitStatus,
				ClassifyLocalBody: u.CoroLocalBodyFacts, ClassifyLoweredCalls: u.CoroLoweredCalls,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == pkg.Func("Target") {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
				ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
					return coro.UnknownManagedDispatch, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: u}
			enableCoroPreemptCompilation(compilation)
			compilation.PanicABI, compilation.FuncRepABI = coro.PanicExplicitStatusABIV0, coro.FuncRepABIV2
			compiled, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation})
			if err != nil {
				t.Fatal(err)
			}
			module := compiled.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("discarded large defer before split: %v", err)
			}
			for _, name := range []string{"foo.Static", "foo.Dynamic"} {
				body := requireCoroPhysicalFunction(t, module, name).String()
				if strings.Contains(body, "AllocZ") || regexp.MustCompile(`alloca[^\n]*131073`).MatchString(body) {
					t.Fatalf("%s allocated an unused deferred result:\n%s", name, body)
				}
				if !regexp.MustCompile(`call ptr [^\n]*\(ptr [^,\n]+, ptr null`).MatchString(body) {
					t.Fatalf("%s did not use the nil-output child ABI:\n%s", name, body)
				}
			}
			runCoroABITestPipeline(t, prog, module)
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("discarded large defer after split: %v", err)
			}
		})
	}
}
