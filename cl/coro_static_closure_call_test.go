//go:build !llgo

package cl

import (
	"github.com/xgo-dev/llgo/internal/goembed"
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroStaticPlainCallReusesDescriptorClosureEnvironment(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{"native", nil}, {"wasm32", &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		for _, capture := range []bool{false, true} {
			owners := []bool{false, true}
			if capture {
				owners = []bool{false}
			}
			for _, suspend := range owners {
				name := target.name
				fields, result := "", "v+11"
				if capture {
					name += "/receiver"
					fields, result = "N int", "v+c.N"
				} else {
					name += "/zero"
				}
				if suspend {
					name += "/coroutine"
				} else {
					name += "/sync"
				}
				t.Run(name, func(t *testing.T) {
					source := "package foo\ntype Counter struct{" + fields + "}\nfunc(c Counter) Add(v int)int{return " + result + "}\nfunc Root(c Counter)(int,func(int)int){f:=c.Add;return f(3),f}"
					prog, compiled, plan, pkg := compileStaticPlainDescriptorClosureFixture(t, target.arch, source, suspend)
					defer prog.Dispose()
					mod := compiled.Module()
					defer mod.Dispose()
					root := pkg.Func("Root")
					var call *ssa.Call
					for _, block := range root.Blocks {
						for _, instruction := range block.Instrs {
							if c, ok := instruction.(*ssa.Call); ok {
								if _, closure := c.Common().Value.(*ssa.MakeClosure); closure {
									call = c
								}
							}
						}
					}
					if call == nil {
						t.Fatal("fixture lost static bound closure call")
					}
					cp, ok := plan.CallPlan(call)
					vp, value := plan.ValuePlan(call.Common().Value)
					if !ok || cp.Rep != coro.DirectPlain || !value || len(vp.Funcs) != 1 || vp.Funcs[0].Rep != coro.Dispatch {
						t.Fatalf("fixture needs a direct plain call of a descriptor-backed closure: call=%+v value=%+v", cp, vp)
					}
					rp, _ := plan.FunctionPlan(root)
					body := mod.NamedFunction("foo.Root")
					if rp.Emission == coro.EmitCoroutine {
						body = requireCoroPhysicalFunction(t, mod, "foo.Root")
					}
					if rp.Emission == coro.EmitOutcomePlain {
						body = mod.NamedFunction("foo.Root" + coroOutcomePlainPrimarySuffix)
					}
					assertStaticBoundMethodBodyCall(t, body)
					if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
						t.Fatal(err)
					}
					runCoroABITestPipeline(t, prog, mod)
					if rp.Emission == coro.EmitCoroutine {
						assertStaticBoundMethodBodyCall(t, mod.NamedFunction("foo.Root$coro.resume"))
					}
					if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func assertStaticBoundMethodBodyCall(t *testing.T, body llvm.Value) {
	t.Helper()
	if body.IsNil() {
		t.Fatal("missing static closure caller")
	}
	for block := body.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
		for inst := block.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
			if inst.IsACallInst().IsNil() {
				continue
			}
			fn := inst.CalledValue()
			if !fn.IsAFunction().IsNil() && strings.Contains(fn.Name(), "bound") && strings.Contains(fn.Name(), "Add") {
				return
			}
		}
	}
	t.Fatalf("static plain closure call did not select its exact body entry:\n%s", body.String())
}

func compileStaticPlainDescriptorClosureFixture(t *testing.T, target *llssa.Target, source string, suspend bool) (llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Package) {
	t.Helper()
	testProgram := newEmissionTestProgram()
	testProgram.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	rt := testProgram.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocU(size uintptr)unsafe.Pointer{if size==0{return nil};return nil}
func AllocZ(size uintptr)unsafe.Pointer{if size==0{return nil};return nil}
func AssertNilDeref(value bool){}
func PanicWrapNilPointer(value bool,receiver,method string){}
`)
	foo := testProgram.addPackage(t, "foo", source)
	testProgram.ssa.Build()
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{SSA: rt.ssa, Files: []*ast.File{rt.file}}, {SSA: foo.ssa, Files: []*ast.File{foo.file}}}, EmissionUniverseOptions{})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProgram.ssa, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ids := universe.FunctionIDConfig()
	ids.CoroABI = coro.PhysicalABIV1
	ids.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	ids.ArchiveReady = true
	root := foo.ssa.Func("Root")
	demand := coro.SyncDemand
	if suspend {
		demand = coro.AsyncDemand
	}
	plan, err := coro.AnalyzeSSA(testProgram.ssa, coro.Roots{{Function: root, Demand: demand}}, coro.SSAConfig{EmissionUniverse: ssaUniverse, FunctionIDs: ids, DynamicResolution: coro.DynamicCHAClosed, MaxPlainInstructions: -1, OutcomeMode: coro.OutcomeExplicitStatus, ClassifyLoweredCalls: universe.CoroLoweredCalls, ClassifyRawPlainDemandReferences: universe.CoroSyncDemandReferences, ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if suspend && fn == root {
			return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	compiled, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, foo.ssa, []*ast.File{foo.file}, goembed.VarMap{}, PackageOptions{Compilation: compilation})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, compiled, plan, foo.ssa
}
