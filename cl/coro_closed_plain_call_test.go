//go:build !llgo

package cl

import (
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroClosedPlainCallUsesPrimaryDespiteDescriptorPublication(t *testing.T) {
	testCoroClosedPlainDescriptor(t, true)
}

func TestCoroClosedPlainDescriptorAvoidsInventedAwait(t *testing.T) {
	testCoroClosedPlainDescriptor(t, false)
}

func testCoroClosedPlainDescriptor(t *testing.T, parking bool) {
	t.Helper()
	llssa.Initialize(llssa.InitAll)
	source := `package foo
type Callback func(uint32) uint32
var gate chan uint32
func Target(value uint32) uint32 { return value + 1 }
func Park() { <-gate }
func Dynamic(callback Callback, value uint32) uint32 { Park(); return callback(value) }
func PlainCaller(value uint32, _ bool) uint32 {
	callback := Callback(Target)
	return callback(value)
}
func Root(value uint32, external Callback) uint32 {
	callback := Callback(Target)
	return callback(value) + PlainCaller(value, true) + Dynamic(external, value)
}
`
	if !parking {
		source = strings.Replace(source, "Park(); ", "", 1)
	}
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			pkg, _, files := buildGoSSAPkg(t, source)
			prog := newLLSSAProgForTarget(t, target.arch)
			defer prog.Dispose()
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.Prog, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			root, plain := pkg.Func("Root"), pkg.Func("Target")
			ids := universe.FunctionIDConfig()
			ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = coro.PhysicalABIV1, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, true
			plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse: ssaUniverse, FunctionIDs: ids, MaxPlainInstructions: -1, OutcomeMode: coro.OutcomeExplicitStatus,
				DynamicResolution:    coro.DynamicCHAClosed,
				ClassifyLocalBody:    universe.CoroLocalBodyFacts,
				ClassifyLoweredCalls: universe.CoroLoweredCalls,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == root {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			fact, planned := plan.FunctionPlan(plain)
			if !planned || fact.FuncRep != coro.Dispatch || fact.Emission != coro.EmitPlain {
				t.Fatalf("fixture target is not one shared plain/descriptor body: %+v", fact)
			}
			var call *ssa.Call
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					if candidate, ok := instruction.(*ssa.Call); ok && candidate.Common().StaticCallee() == nil {
						call = candidate
					}
				}
			}
			if call == nil || call.Common().StaticCallee() != nil {
				t.Fatal("fixture must call through a named function conversion")
			}
			resolved, _, err := resolveCoroStaticPlainCall(plan, call)
			if err != nil || resolved != plain {
				t.Fatalf("closed plain occurrence rejected: target=%v error=%v", resolved, err)
			}
			compilation := coroClosedInterfacePlainCompilation(plan, universe)
			compiled, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation})
			if err != nil {
				t.Fatal(err)
			}
			module := compiled.Module()
			defer module.Dispose()
			if !parking {
				dynamicPlan, _ := plan.FunctionPlan(pkg.Func("Dynamic"))
				if dynamicPlan.Emission != coro.EmitOutcomePlain || !dynamicPlan.StaticOutcome {
					t.Fatalf("closed plain descriptor owner retained a coroutine frame: %+v", dynamicPlan)
				}
				body := module.NamedFunction("foo.Dynamic" + coroOutcomePlainPrimarySuffix)
				if body.IsNil() {
					t.Fatal("missing synchronous dynamic owner")
				}
				for _, forbidden := range []string{"llvm.coro.", coroFrameAllocHookV1, coroAwaitPrepareInlineHookV4, "coro.dispatch.version.invalid"} {
					if strings.Contains(body.String(), forbidden) {
						t.Fatalf("closed plain call retained %q:\n%s", forbidden, body.String())
					}
				}
			}
			check := func(fn llvm.Value) {
				t.Helper()
				if fn.IsNil() {
					plainCaller, _ := plan.FunctionPlan(pkg.Func("PlainCaller"))
					t.Fatalf("expected call owner missing (PlainCaller=%+v):\n%s", plainCaller, module.String())
				}
				if countCoroIRDirectCalls(fn, "foo.Target") != 1 || strings.Contains(fn.String(), "coro.dispatch.version.invalid") {
					t.Fatalf("closed plain call did not use exactly one existing primary entry:\n%s", fn.String())
				}
			}
			check(requireCoroPhysicalFunction(t, module, "foo.Root"))
			callerPlan, _ := plan.FunctionPlan(pkg.Func("PlainCaller"))
			callerSymbol := "foo.PlainCaller"
			switch callerPlan.Emission {
			case coro.EmitPlain:
			case coro.EmitOutcomePlain:
				callerSymbol += coroOutcomePlainPrimarySuffix
			default:
				t.Fatalf("synchronous caller acquired a coroutine frame: %+v", callerPlan)
			}
			check(module.NamedFunction(callerSymbol))
			if !module.NamedFunction("foo.Target$coro").IsNil() {
				t.Fatal("plain target acquired an unnecessary coroutine body")
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			runCoroABITestPipeline(t, prog, module)
			check(module.NamedFunction("foo.Root$coro.resume"))
		})
	}
}
